package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// Test harness: a real storage database per test, deterministic fakes for
// clock, ids, peer ports and blobs, and helpers that drive every operation
// through Service.Handle inside storage write transactions. skill.import is
// driven through the three local IO phases the way the application drives it.

// fakeClock is a deterministic contract.Clock with a controllable instant.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// seqIDs mints syntactically valid UUIDv4 identities in sequence.
type seqIDs struct {
	mu sync.Mutex
	n  int
}

func (s *seqIDs) New() contract.ID {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return contract.ID(fmt.Sprintf("00000000-0000-4000-8000-%012d", s.n))
}

// fakeArtifact is the artifact metadata the fake artifacts owner serves.
type fakeArtifact struct {
	ID        contract.ID     `json:"id"`
	Version   int64           `json:"version"`
	Scope     contract.Scope  `json:"scope"`
	Digest    contract.Digest `json:"digest"`
	Size      int64           `json:"size"`
	MediaType string          `json:"media_type"`
	State     string          `json:"state"`
	CreatedAt string          `json:"created_at"`
}

// fakePorts serves the peer operations skills calls — artifacts metadata
// and publication, configuration staging, execution job creation and policy
// invalidation — with recorded calls and injectable faults.
type fakePorts struct {
	mu        sync.Mutex
	calls     []contract.Invocation
	artifacts map[contract.ID]fakeArtifact
	jobN      int
	artifactN int
	fail      map[string]*contract.Fault
}

func newFakePorts() *fakePorts {
	return &fakePorts{
		artifacts: map[contract.ID]fakeArtifact{},
		fail:      map[string]*contract.Fault{},
	}
}

// addArtifact registers one artifact the fake artifacts owner knows about.
func (p *fakePorts) addArtifact(a fakeArtifact) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.artifacts[a.ID] = a
}

func (p *fakePorts) Call(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	p.mu.Lock()
	p.calls = append(p.calls, inv)
	injected := p.fail[inv.Operation]
	p.mu.Unlock()
	if injected != nil {
		return contract.Payload{}, injected
	}
	var body any
	switch inv.Operation {
	case "_artifacts.metadata":
		var in struct {
			Artifacts []struct {
				ID     contract.ID     `json:"id"`
				Digest contract.Digest `json:"digest"`
			} `json:"artifacts"`
		}
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: "fake ports: bad metadata input"}
		}
		p.mu.Lock()
		found := make([]fakeArtifact, 0, len(in.Artifacts))
		for _, ref := range in.Artifacts {
			if a, ok := p.artifacts[ref.ID]; ok {
				found = append(found, a)
			}
		}
		p.mu.Unlock()
		body = map[string]any{"artifacts": found}
	case "_artifacts.publish":
		var in struct {
			Digest contract.Digest `json:"digest"`
			Size   int64           `json:"size"`
		}
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: "fake ports: bad publish input"}
		}
		p.mu.Lock()
		p.artifactN++
		id := contract.ID(fmt.Sprintf("00000000-0000-4000-9000-%012d", p.artifactN))
		a := fakeArtifact{ID: id, Version: 1, Digest: in.Digest, Size: in.Size, State: "available"}
		p.artifacts[id] = a
		p.mu.Unlock()
		body = map[string]any{"resource": a}
	case "_configuration.stage":
		p.mu.Lock()
		p.jobN++
		draftID := contract.ID(fmt.Sprintf("00000000-0000-4000-a000-%012d", p.jobN))
		p.mu.Unlock()
		body = map[string]any{"resource": wireDraft{
			ID: draftID, Version: 1, BaseRevision: 0,
			Changes: []json.RawMessage{}, Diagnostics: []wireDiagnostic{},
		}}
	case "_execution.job.create":
		p.mu.Lock()
		p.jobN++
		jobID := contract.ID(fmt.Sprintf("00000000-0000-4000-b000-%012d", p.jobN))
		p.mu.Unlock()
		body = map[string]any{"resource": wireJob{
			ID: jobID, Version: 1, Kind: "skill.evaluate", State: "pending",
			Requirements: []wireRequirement{}, Owner: "skills", Operation: "skill.evaluate",
		}}
	case "_policy.invalidate":
		body = map[string]any{"qualification_ids": []contract.ID{}}
	default:
		return contract.Payload{}, &contract.Fault{
			Code: contract.CodeInternalError, Message: "fake ports: unexpected peer call " + inv.Operation,
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return contract.Payload{}, err
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
}

// callsOf returns a snapshot of the recorded peer invocations.
func (p *fakePorts) callsOf(op string) []contract.Invocation {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []contract.Invocation
	for _, c := range p.calls {
		if c.Operation == op {
			out = append(out, c)
		}
	}
	return out
}

// fakeBlobs is an in-memory content-addressed blob store.
type fakeBlobs struct {
	mu        sync.Mutex
	staged    map[string][]byte
	published map[contract.Digest][]byte
	publishN  int
	next      int
}

func newFakeBlobs() *fakeBlobs {
	return &fakeBlobs{staged: map[string][]byte{}, published: map[contract.Digest][]byte{}}
}

// seed places bytes at a digest so an artifact can be opened.
func (b *fakeBlobs) seed(digest contract.Digest, raw []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published[digest] = raw
}

func (b *fakeBlobs) Stage(ctx context.Context, r io.Reader, size int64) (string, contract.Digest, int64, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", "", 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	ref := fmt.Sprintf("staging-%d", b.next)
	b.staged[ref] = raw
	return ref, contract.Hash(raw), int64(len(raw)), nil
}

func (b *fakeBlobs) Publish(ctx context.Context, stagingRef string, digest contract.Digest) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, ok := b.staged[stagingRef]
	if !ok {
		return errors.New("fake blobs: unknown staging reference " + stagingRef)
	}
	b.published[digest] = raw
	delete(b.staged, stagingRef)
	b.publishN++
	return nil
}

func (b *fakeBlobs) Open(ctx context.Context, digest contract.Digest, offset, length int64) (io.ReadCloser, error) {
	b.mu.Lock()
	raw, ok := b.published[digest]
	b.mu.Unlock()
	if !ok {
		return nil, errors.New("fake blobs: unknown digest " + string(digest))
	}
	data := raw
	if offset > 0 {
		if offset > int64(len(data)) {
			return nil, errors.New("fake blobs: offset past end")
		}
		data = data[offset:]
	}
	if length >= 0 && length < int64(len(data)) {
		data = data[:length]
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (b *fakeBlobs) RemoveStaged(ctx context.Context, stagingRef string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.staged, stagingRef)
	return nil
}

// publishCalls counts Publish invocations, so tests can distinguish new
// publications from content seeded before the operation ran.
func (b *fakeBlobs) publishCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.publishN
}

// testEnv is one installation wired to a real storage database.
type testEnv struct {
	t       *testing.T
	ctx     context.Context
	db      contract.Database
	svc     *Service
	ports   *fakePorts
	blobs   *fakeBlobs
	clock   *fakeClock
	ids     *seqIDs
	actor   contract.Actor
	install contract.ID
	scope   wireScope
}

// newEnv opens a fresh database, migrates the skills owner and wires the
// service with deterministic fakes.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "skills-test.db")})
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	env := &testEnv{
		t: t, ctx: ctx, db: db,
		ports: newFakePorts(), blobs: newFakeBlobs(),
		clock: &fakeClock{now: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)},
		ids:   &seqIDs{},
	}
	svc, err := New(contract.Dependencies{Clock: env.clock, IDs: env.ids, Ports: env.ports, Blobs: env.blobs})
	if err != nil {
		t.Fatalf("skills.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate skills: %v", err)
	}
	owner := env.ids.New()
	env.actor = contract.Actor{PrincipalID: owner, Kind: contract.KindService}
	env.install = env.ids.New()
	env.scope = wireScope{InstallationID: env.install}
	return env
}

// callAs runs one operation inside a write transaction on the given scope.
func (e *testEnv) callAs(scope wireScope, op string, in any) (contract.Payload, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal input for %s: %v", op, err)
	}
	var payload contract.Payload
	err = e.db.Write(e.ctx, e.actor, scope.toContract(), func(unit contract.Unit) error {
		p, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
		payload = p
		return err
	})
	return payload, err
}

func (e *testEnv) call(op string, in any) (contract.Payload, error) {
	return e.callAs(e.scope, op, in)
}

// mustOK runs an operation and requires a completed payload.
func (e *testEnv) mustOK(op string, in any) contract.Payload {
	e.t.Helper()
	payload, err := e.call(op, in)
	if err != nil {
		e.t.Fatalf("%s failed: %v", op, err)
	}
	if payload.Status != contract.StatusCompleted {
		e.t.Fatalf("%s status %q, want completed (error %v)", op, payload.Status, payload.Error)
	}
	if payload.Error != nil {
		e.t.Fatalf("%s returned fault %s: %s", op, payload.Error.Code, payload.Error.Message)
	}
	return payload
}

// expectFault runs an operation and requires a fault with the exact code.
func (e *testEnv) expectFault(op string, in any, code string) {
	e.t.Helper()
	payload, err := e.call(op, in)
	var f *contract.Fault
	if err != nil {
		errors.As(err, &f)
	}
	if f == nil {
		f = payload.Error
	}
	if f == nil {
		e.t.Fatalf("%s: expected %s fault, got completed payload %s", op, code, payload.Status)
	}
	if f.Code != code {
		e.t.Fatalf("%s: fault %s (%s), want %s", op, f.Code, f.Message, code)
	}
}

func (e *testEnv) decode(raw json.RawMessage, out any) {
	e.t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		e.t.Fatalf("decode payload: %v", err)
	}
}

// runImport drives skill.import through its three local IO phases the way
// the application does: Prepare and Finish inside write transactions,
// Perform outside any transaction.
func (e *testEnv) runImport(in importInput) (contract.Payload, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal import input: %v", err)
	}
	inv := contract.Invocation{Operation: "skill.import", Version: 1, Input: raw}
	var plan contract.IOPlan
	if err := e.db.Write(e.ctx, e.actor, in.Scope.toContract(), func(unit contract.Unit) error {
		p, err := e.svc.Prepare(e.ctx, unit, inv)
		plan = p
		return err
	}); err != nil {
		return contract.Payload{}, err
	}
	result, err := e.svc.Perform(e.ctx, plan)
	if err != nil {
		return contract.Payload{}, err
	}
	var payload contract.Payload
	err = e.db.Write(e.ctx, e.actor, in.Scope.toContract(), func(unit contract.Unit) error {
		p, err := e.svc.Finish(e.ctx, unit, plan, result)
		payload = p
		return err
	})
	return payload, err
}

// runImportExpectFault runs the full import flow and requires a fault with
// the exact code, whether it surfaces from a phase or the payload.
func (e *testEnv) runImportExpectFault(in importInput, code string) {
	e.t.Helper()
	payload, err := e.runImport(in)
	var f *contract.Fault
	if err != nil {
		errors.As(err, &f)
	}
	if f == nil {
		f = payload.Error
	}
	if f == nil {
		e.t.Fatalf("skill.import: expected %s fault, got status %q", code, payload.Status)
	}
	if f.Code != code {
		e.t.Fatalf("skill.import: fault %s (%s), want %s", f.Code, f.Message, code)
	}
}

// ---------- skill archive fixtures ----------

// zipEntry is one entry in a fixture archive. Mode 0 means a regular file.
type zipEntry struct {
	Name string
	Data []byte
	Mode fs.FileMode
}

// buildZip writes a fixture archive. It panics on writer errors: fixtures
// are static data, and a failure there is a test-authoring bug.
func buildZip(entries []zipEntry) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, en := range entries {
		h := &zip.FileHeader{Name: en.Name, Method: zip.Deflate}
		mode := en.Mode
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		f, err := w.CreateHeader(h)
		if err != nil {
			panic(fmt.Sprintf("fixture zip header: %v", err))
		}
		if len(en.Data) > 0 {
			if _, err := f.Write(en.Data); err != nil {
				panic(fmt.Sprintf("fixture zip write: %v", err))
			}
		}
	}
	if err := w.Close(); err != nil {
		panic(fmt.Sprintf("fixture zip close: %v", err))
	}
	return buf.Bytes()
}

// validSkillMD is a SKILL.md fixture with frontmatter and instruction body.
// It declares no dependencies: dependency resolution and cycle rejection
// have dedicated tests that build their own fixtures.
const validSkillMD = `---
name: greeter
description: Greets a named subject politely.
license: MIT
allowed-tools: tool:echo, tool:clock
---

# Greeter

Greet the subject by name. This instruction body is retained byte-exact.
`

// validArchive returns the canonical valid fixture archive.
func validArchive() []byte {
	return buildZip([]zipEntry{
		{Name: "SKILL.md", Data: []byte(validSkillMD)},
		{Name: "scripts/run.sh", Data: []byte("#!/bin/sh\necho hi\n")},
	})
}

// minimalSkillMD is a SKILL.md with only the required keys.
const minimalSkillMD = `---
name: minimal
description: A minimal skill.
---

Body.
`

// ---------- direct row seeding for activate/validate ----------

// seedSkill inserts one skill version row and its dependency pins directly,
// returning the row so tests can build the matching candidate definition.
func (e *testEnv) seedSkill(name string, version int64, state string, deps ...dependencyRef) *skillRow {
	e.t.Helper()
	var row *skillRow
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		id := contract.ID("")
		if version == 1 {
			id = e.ids.New()
		} else {
			// Continue the identity of version 1.
			first, err := fetchSkillByNameVersion(e.ctx, unit, e.install, name, 1)
			if err != nil {
				return err
			}
			if first == nil {
				return errors.New("seedSkill: version 1 of " + name + " not seeded")
			}
			id = first.ID
		}
		contentDigest := contract.Digest(fmt.Sprintf("%064d", version))
		instructionDigest := contract.Digest(fmt.Sprintf("%064d", version+1000))
		artifactID := e.ids.New()
		now := e.clock.Now().UTC().Format(timeLayout)
		requirements := []string{"tool:alpha"}
		depsForJSON := make([]wireRef, 0, len(deps))
		for _, d := range deps {
			resolved, err := fetchSkillByName(e.ctx, unit, e.install, d.Name)
			if err != nil {
				return err
			}
			if resolved == nil {
				return errors.New("seedSkill: dependency " + d.Name + " not seeded")
			}
			want := resolved.Version
			if d.Version > 0 {
				want = d.Version
			}
			depsForJSON = append(depsForJSON, wireRef{ID: resolved.ID, Version: contract.Version(want)})
		}
		depsJSON, err := marshalJSON(depsForJSON)
		if err != nil {
			return err
		}
		requirementsJSON, err := marshalJSON(requirements)
		if err != nil {
			return err
		}
		manifest := importManifest{Name: name, Description: "Seeded " + name}
		manifestJSON, err := marshalJSON(manifest)
		if err != nil {
			return err
		}
		if _, err := unit.ExecContext(e.ctx, `
			INSERT INTO skills_versions
				(id, version, installation_id, name, description, state,
				 content_digest, instruction_digest, instruction_artifact_id,
				 manifest_json, requirements_json, dependencies_json,
				 input_schema_json, output_schema_json, source, license,
				 created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '{}', '{}', ?, ?, ?, ?)`,
			string(id), version, string(e.install), name, "Seeded "+name, state,
			string(contentDigest), string(instructionDigest), string(artifactID),
			manifestJSON, requirementsJSON, depsJSON,
			"seed", "MIT", now, now); err != nil {
			return err
		}
		for _, d := range deps {
			resolved, err := fetchSkillByName(e.ctx, unit, e.install, d.Name)
			if err != nil {
				return err
			}
			want := resolved.Version
			if d.Version > 0 {
				want = d.Version
			}
			if err := insertDependency(e.ctx, unit, dependencyRow{
				InstallationID: e.install, SkillID: id, SkillVersion: version,
				DepName: d.Name, DepID: resolved.ID, DepVersion: want,
			}); err != nil {
				return err
			}
		}
		row = &skillRow{
			ID: id, Version: version, InstallationID: e.install, Name: name,
			Description: "Seeded " + name, State: state,
			ContentDigest: contentDigest, InstructionDigest: instructionDigest,
			InstructionArtifactID: artifactID,
			ManifestJSON:          manifestJSON, RequirementsJSON: requirementsJSON,
			DependenciesJSON: depsJSON, Source: "seed", License: "MIT",
		}
		return nil
	}); err != nil {
		e.t.Fatalf("seedSkill %s v%d: %v", name, version, err)
	}
	return row
}

// defOfRow builds the candidate definition that exactly matches a seeded row.
func (e *testEnv) defOfRow(row *skillRow) wireSkill {
	e.t.Helper()
	var def wireSkill
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		built, err := wireSkillOf(e.ctx, unit, row)
		def = built
		return err
	}); err != nil {
		e.t.Fatalf("defOfRow: %v", err)
	}
	return def
}

// changeOf builds one candidate change around a definition.
func changeOf(action string, def wireSkill, expected contract.Version) wireChange {
	return wireChange{
		Kind: "skill", Action: action, ID: def.ID,
		ExpectedVersion: expected,
		Definition:      rawDef(def),
	}
}

// rawDef marshals a definition into the inert change body.
func rawDef(def any) json.RawMessage {
	raw, err := json.Marshal(def)
	if err != nil {
		panic(err)
	}
	return raw
}

// candidateInputOf wraps changes into the _skills.activate input.
func candidateInputOf(changes ...wireChange) candidateInput {
	return candidateInput{Candidate: wireCandidate{
		PlanID: "00000000-0000-4000-c000-000000000001", BaseRevision: 1,
		CandidateDigest: contract.Digest(strings.Repeat("0", 64)),
		Changes:         changes, Dependencies: []wireRef{},
	}}
}

// rowState reads the stored state of one skill version directly.
func (e *testEnv) rowState(id contract.ID, version int64) (string, bool) {
	e.t.Helper()
	var state string
	var found bool
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		row, err := fetchSkillVersion(e.ctx, unit, e.install, id, version)
		if row != nil {
			state, found = row.State, true
		}
		return err
	}); err != nil {
		e.t.Fatalf("rowState: %v", err)
	}
	return state, found
}

// skillsRows returns every skills_versions row in the environment, so tests
// can assert that a rejected operation persisted nothing.
func (e *testEnv) skillsRows() ([]*skillRow, error) {
	var rows []*skillRow
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		rows, err = querySkills(e.ctx, unit, `
			SELECT id, version, installation_id, name, description, state,
			       content_digest, instruction_digest, instruction_artifact_id,
			       manifest_json, requirements_json, dependencies_json,
			       input_schema_json, output_schema_json, source, license,
			       created_at, updated_at
			FROM skills_versions`)
		return err
	}); err != nil {
		return nil, err
	}
	return rows, nil
}
