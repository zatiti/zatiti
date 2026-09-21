package controller

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// The dispatch journal is the controller's own durable state: a write-ahead
// record of every unit of work that leaves a transaction. Each phase is
// appended and synced BEFORE the step it announces, so after a crash the
// last durable phase bounds what may have happened:
//
//	admitting  the admit transaction may have committed; nothing was claimed
//	admitted   an attempt exists; its claim may have committed; the adapter
//	           was never invoked
//	claimed    the claim committed; the adapter may have been invoked
//	observed   the adapter returned this observation; the record transaction
//	           may not have committed
//	recorded   the owner recorded the observation; owner callbacks and output
//	           publication may be outstanding
//	done       nothing is outstanding
//
// The journal doubles as the deduplicated outbox for owner callbacks: an
// entry is keyed by its own identity and is delivered until it reaches done.
// It is recovery state, never a log to be parsed for meaning.

type phase string

const (
	phaseAdmitting phase = "admitting"
	phaseAdmitted  phase = "admitted"
	phaseClaimed   phase = "claimed"
	phaseObserved  phase = "observed"
	phaseRecorded  phase = "recorded"
	phaseDone      phase = "done"
	// phaseRefused retains work an owner durably refused: the evidence stays
	// inspectable and is never retried or resent.
	phaseRefused phase = "refused"
	// phaseStranded retains an admission whose commit is unknowable through
	// the controller's allowed calls.
	phaseStranded phase = "stranded"

	// Turn-work phases (kindTurn). Every owner call these phases bracket
	// (work.claim, context.prepare, context.commit) is a pure database
	// transaction with its own version/generation fence and is safe to
	// retry unconditionally after a crash -- unlike an effect's adapter
	// call, nothing here is a physical, possibly-already-sent invocation.
	// The one physical action in this phase set is staging and publishing
	// the context artifact's bytes outside any transaction, which is why it
	// gets its own write-ahead phase: phaseContextStaging is durable before
	// the stage/publish calls run, and phaseContextStaged (carrying the
	// resulting artifact) is durable before context.commit is attempted, so
	// a crash between them resumes by committing the already-published
	// artifact instead of staging a second one.
	phaseTurnClaiming   phase = "turn_claiming"
	phaseContextStaging phase = "context_staging"
	phaseContextStaged  phase = "context_staged"
)

const (
	kindEffect = "effect"
	kindJob    = "job"
	// kindTurn is one durable worker-turn work-item step: a claim of
	// pending/waiting work, or the stage-then-commit of one context plan.
	kindTurn = "turn"
)

const (
	journalDirName  = "controller"
	journalFileName = "dispatch.journal"
	journalTempName = "dispatch.journal.tmp"

	dirPrivate  fs.FileMode = 0o700
	filePrivate fs.FileMode = 0o600

	// compactAfter bounds finished entries retained between compactions.
	compactAfter = 256
)

// route names the owner callback an effect outcome is delivered to.
type route struct {
	// Owner is "", "execution", "execution_proposal", "memory" or
	// "connections".
	Owner string `json:"owner,omitempty"`
	// AttemptID is the execution attempt a model effect belongs to.
	AttemptID contract.ID `json:"attempt_id,omitempty"`
	// JobID is the network job waiting on the operation.
	JobID contract.ID `json:"job_id,omitempty"`
	// Connection is the connection a probe validated.
	Connection wireRef `json:"connection,omitempty"`
	// ProposalID names the WorkerTurn proposal (owner execution_proposal)
	// this effect's outcome completes.
	ProposalID string `json:"proposal_id,omitempty"`
}

// turnProposalRef is what the controller remembers about one prepared
// external_tool proposal between discovering it (_execution.proposal.
// prepare, immediately after delivering the model step that produced it)
// and its own effect's eventual delivery.
type turnProposalRef struct {
	TurnID     contract.ID
	StepIndex  int64
	ProposalID string
	// ExpectedVersion is the turn's version captured at discovery time. The
	// turn stays proposal_pending -- unchanged -- for exactly as long as
	// this one prepared proposal is outstanding, so the version observed
	// when the proposal was first discovered "prepared" is still current
	// when its effect later completes and this proposal is recorded.
	ExpectedVersion int64
}

// publishedOutput is one staged output whose bytes and metadata are
// published.
type publishedOutput struct {
	StagingRef string          `json:"staging_ref"`
	Digest     contract.Digest `json:"digest"`
	ArtifactID contract.ID     `json:"artifact_id"`
}

// entry is one journaled unit of work. Lines are merged by ID in file order;
// a later line overrides the scalar fields and leaves a nil pointer or empty
// slice field as it was.
type entry struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Phase      phase  `json:"phase"`
	Generation int64  `json:"generation"`

	// Effect fields.
	OperationID contract.ID           `json:"operation_id,omitempty"`
	AttemptID   contract.ID           `json:"attempt_id,omitempty"`
	Scope       *contract.Scope       `json:"scope,omitempty"`
	Bound       *wireMoney            `json:"bound,omitempty"`
	Route       *route                `json:"route,omitempty"`
	Observation *contract.Observation `json:"observation,omitempty"`
	Published   []publishedOutput     `json:"published,omitempty"`
	// Unacked marks a callback whose acknowledgement was lost: the owner may
	// already hold it, so its duplicate refusal then means delivered.
	Unacked bool `json:"unacked,omitempty"`

	// Job fields.
	JobID      contract.ID `json:"job_id,omitempty"`
	JobVersion int64       `json:"job_version,omitempty"`
	JobOwner   string      `json:"job_owner,omitempty"`
	JobOp      string      `json:"job_operation,omitempty"`
	Outcome    *JobOutcome `json:"outcome,omitempty"`

	// Turn-work fields (kindTurn).
	TurnID         contract.ID      `json:"turn_id,omitempty"`
	Plan           *wireContextPlan `json:"plan,omitempty"`
	StagedArtifact *wireArtifact    `json:"staged_artifact,omitempty"`

	// Fault is the owner's refusal or the prerequisite that blocks progress.
	Fault     *contract.Fault `json:"fault,omitempty"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (e entry) finished() bool { return e.Phase == phaseDone }

// open reports whether the controller still owes this entry a step. Refused
// and stranded entries are retained as evidence but are never advanced.
func (e entry) open() bool {
	return e.Phase != phaseDone && e.Phase != phaseRefused && e.Phase != phaseStranded
}

type journal struct {
	mu      sync.Mutex
	dir     string
	file    *os.File
	order   []string
	entries map[string]entry
	done    int
}

// openJournal opens or creates the journal below the state directory. The
// directory is private, never a symlink, and a torn final line — an append
// the dead process never finished — is discarded; any other damage refuses
// to open, because a controller that cannot read its ambiguity must not
// dispatch.
func openJournal(stateDir string) (*journal, error) {
	if stateDir == "" || !filepath.IsAbs(stateDir) {
		return nil, invalidInput("controller state directory must be an absolute path")
	}
	dir := filepath.Join(stateDir, journalDirName)
	if err := os.MkdirAll(dir, dirPrivate); err != nil {
		return nil, unavailable("controller state directory cannot be created")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return nil, unavailable("controller state directory is not a private directory")
	}
	if err := os.Chmod(dir, dirPrivate); err != nil {
		return nil, unavailable("controller state directory cannot be protected")
	}
	path := filepath.Join(dir, journalFileName)
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, unavailable("dispatch journal is not a regular file")
	}
	j := &journal{dir: dir, entries: map[string]entry{}}
	if err := j.load(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, filePrivate)
	if err != nil {
		return nil, unavailable("dispatch journal cannot be opened")
	}
	j.file = f
	if err := syncDir(dir); err != nil {
		_ = f.Close()
		return nil, err
	}
	return j, nil
}

func (j *journal) load(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return unavailable("dispatch journal cannot be read")
	}
	whole := data
	if cut := bytes.LastIndexByte(data, '\n'); cut+1 != len(data) {
		// Torn tail: the append never completed, so the step it announced
		// never started.
		whole = data[:cut+1]
		if err := os.Truncate(path, int64(len(whole))); err != nil {
			return unavailable("dispatch journal cannot be repaired")
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(whole))
	scanner.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil || e.ID == "" || e.Phase == "" {
			return internalFault("dispatch journal is corrupt; refusing to dispatch over unreadable recovery state")
		}
		j.merge(e)
	}
	if err := scanner.Err(); err != nil {
		return internalFault("dispatch journal is corrupt; refusing to dispatch over unreadable recovery state")
	}
	return nil
}

// merge folds one line into the in-memory view.
func (j *journal) merge(e entry) {
	prev, ok := j.entries[e.ID]
	if !ok {
		j.order = append(j.order, e.ID)
	} else {
		if prev.finished() {
			j.done--
		}
		if e.Scope == nil {
			e.Scope = prev.Scope
		}
		if e.Bound == nil {
			e.Bound = prev.Bound
		}
		if e.Route == nil {
			e.Route = prev.Route
		}
		if e.Observation == nil {
			e.Observation = prev.Observation
		}
		if len(e.Published) == 0 {
			e.Published = prev.Published
		}
		e.Unacked = e.Unacked || prev.Unacked
		if e.Outcome == nil {
			e.Outcome = prev.Outcome
		}
		if e.TurnID == "" {
			e.TurnID = prev.TurnID
		}
		if e.Plan == nil {
			e.Plan = prev.Plan
		}
		if e.StagedArtifact == nil {
			e.StagedArtifact = prev.StagedArtifact
		}
	}
	if e.finished() {
		j.done++
	}
	j.entries[e.ID] = e
}

// put durably appends one entry state. It returns only after the line is
// synced: the caller may then take the step the phase announces.
func (j *journal) put(e entry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return internalFault("dispatch journal entry cannot be encoded")
	}
	line = append(line, '\n')
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return unavailable("dispatch journal is closed")
	}
	if _, err := j.file.Write(line); err != nil {
		return unavailable("dispatch journal cannot be written")
	}
	if err := j.file.Sync(); err != nil {
		return unavailable("dispatch journal cannot be made durable")
	}
	j.merge(e)
	return nil
}

// snapshot returns the merged entries in first-seen order.
func (j *journal) snapshot() []entry {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]entry, 0, len(j.order))
	for _, id := range j.order {
		out = append(out, j.entries[id])
	}
	return out
}

func (j *journal) get(id string) (entry, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e, ok := j.entries[id]
	return e, ok
}

// compactIfDue drops finished entries once enough accumulated.
func (j *journal) compactIfDue() error {
	j.mu.Lock()
	due := j.done >= compactAfter
	j.mu.Unlock()
	if !due {
		return nil
	}
	return j.compact()
}

// compact atomically rewrites the journal without finished entries. Refused
// and stranded entries are evidence and survive.
func (j *journal) compact() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return unavailable("dispatch journal is closed")
	}
	var buf bytes.Buffer
	keep := make([]string, 0, len(j.order))
	for _, id := range j.order {
		e := j.entries[id]
		if e.finished() {
			continue
		}
		line, err := json.Marshal(e)
		if err != nil {
			return internalFault("dispatch journal entry cannot be encoded")
		}
		buf.Write(line)
		buf.WriteByte('\n')
		keep = append(keep, id)
	}
	tmp := filepath.Join(j.dir, journalTempName)
	path := filepath.Join(j.dir, journalFileName)
	if err := writeSynced(tmp, buf.Bytes()); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return unavailable("dispatch journal cannot be compacted")
	}
	if err := syncDir(j.dir); err != nil {
		return err
	}
	next, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, filePrivate)
	if err != nil {
		return unavailable("dispatch journal cannot be reopened")
	}
	_ = j.file.Close()
	j.file = next
	for _, id := range j.order {
		if j.entries[id].finished() {
			delete(j.entries, id)
		}
	}
	j.order = keep
	j.done = 0
	return nil
}

func (j *journal) close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return nil
	}
	err := j.file.Close()
	j.file = nil
	if err != nil {
		return unavailable("dispatch journal cannot be closed")
	}
	return nil
}

func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePrivate)
	if err != nil {
		return unavailable("dispatch journal cannot be rewritten")
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return unavailable("dispatch journal cannot be rewritten")
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return unavailable("dispatch journal cannot be made durable")
	}
	if err := f.Close(); err != nil {
		return unavailable("dispatch journal cannot be rewritten")
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return unavailable("controller state directory cannot be synced")
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		return unavailable("controller state directory cannot be synced")
	}
	return nil
}
