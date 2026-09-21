package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Document-publish jobs: the durable, testable "perform" mechanism a
// synchronous public mutation (run.claim, run.export) cannot itself finish
// inside its own Unit-bound transaction. No network/model call, secret-store
// access, filesystem streaming or subprocess work is permitted inside a
// Unit (AGENTS.md's transaction rules); context_build.go's stageContext and
// verifier.go's stageRequest already establish the same discipline for
// exactly this reason -- both explicitly run outside any Unit.
//
// The pattern: a Unit-bound step (claimContextRef's fallback branch,
// handleRunExport) assembles deterministic document bytes it already has
// everything in hand to build, seals them by digest, and commits a durable
// execution_jobs row carrying those exact bytes as its Input -- the same
// admit/perform/record shape effects and verification already use. A later
// "perform" phase (stageDocumentArtifact) stages and publishes those bytes
// outside any Unit with no further computation and no peer call; it is
// callable directly (exactly as context_build_test.go calls stageContext
// directly) or, once a controller exists, through the ordinary
// _execution.job.pending/.claim pipeline. A final Unit-bound step
// (recordDocumentJob) registers the real Artifact metadata and marks the
// job succeeded with that artifact as its terminal result -- the terminal
// job callback a caller observes through job.get.
//
// The digest/ID a caller sees synchronously (run.claim's context field,
// computed from these same bytes before staging) is never a reference to
// nothing: it names exactly the document this mechanism durably commits to
// publishing, and a replayed claim recomputes byte-identical content and
// therefore the same reference.

// documentJobInput is the durable execution_jobs Input shape a document
// publish job carries: the exact pre-assembled bytes a Unit-bound step
// already sealed by digest, plus the metadata the eventual Artifact needs.
type documentJobInput struct {
	Document       json.RawMessage `json:"document"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
}

// enqueueDocumentPublish commits (or replays) the durable job that will
// eventually stage and publish raw's exact bytes. sourceID is derived from
// the content's own deterministic reference so a replayed caller building
// byte-identical content always dedups onto the same job, exactly like
// run.export's own job already dedups on the run's identity.
func (s *Service) enqueueDocumentPublish(ctx context.Context, unit contract.Unit, scope contract.Scope, kind string, sourceID contract.ID, raw []byte, mediaType, classification string, now time.Time) (*jobRow, error) {
	input := documentJobInput{Document: json.RawMessage(raw), MediaType: mediaType, Classification: classification}
	inputJSON, err := canonicalJSON(input)
	if err != nil {
		return nil, err
	}
	prior, err := findJobBySourceID(ctx, unit, sourceID)
	if err != nil {
		return nil, err
	}
	if prior != nil {
		if prior.InputHash != string(sha256Hex(inputJSON)) {
			return nil, conflict("document publish job %s already exists with different content", sourceID)
		}
		return prior, nil
	}
	j := &jobRow{
		ID:               s.newID(),
		Version:          1,
		Kind:             kind,
		State:            "pending",
		InstallationID:   scope.InstallationID,
		OrganizationID:   scope.OrganizationID,
		ProjectID:        scope.ProjectID,
		Scope:            scope,
		Owner:            ownerName,
		Operation:        kind,
		Input:            inputJSON,
		InputHash:        string(sha256Hex(inputJSON)),
		SourceID:         sourceID,
		Requirements:     []wireRequirement{},
		CompletionSchema: schemaExportResult,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := insertJob(ctx, unit, j); err != nil {
		return nil, err
	}
	return j, nil
}

// stageDocumentArtifact is the perform phase for a durable document-publish
// job: it runs outside any Unit, stages and publishes the exact bytes a
// prior Unit-bound step already assembled and sealed by digest. It performs
// no further computation, reads no further state and calls no peer
// operation -- a "dumb" IO transform, exactly the shape LocalJobRunner.RunJob
// requires (no Unit in its own signature).
func (s *Service) stageDocumentArtifact(ctx context.Context, raw []byte) (wireArtifactRef, int64, error) {
	if s.deps.Blobs == nil {
		return wireArtifactRef{}, 0, prerequisiteMissing("execution: staging a document requires a blob store")
	}
	stagingRef, stagedDigest, size, err := s.deps.Blobs.Stage(ctx, bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return wireArtifactRef{}, 0, fmt.Errorf("execution: stage document artifact: %w", err)
	}
	if err := s.deps.Blobs.Publish(ctx, stagingRef, stagedDigest); err != nil {
		return wireArtifactRef{}, 0, fmt.Errorf("execution: publish document artifact: %w", err)
	}
	return wireArtifactRef{ID: uuidFromDigest(stagedDigest), Digest: stagedDigest}, size, nil
}

// var _ asserts *Service genuinely satisfies contract.LocalJobRunner for
// its own document-publish job kinds (claim_context, run_export) -- the
// same typed-completion-callback shape any other job owner implements.
var _ contract.LocalJobRunner = (*Service)(nil)

// RunJob implements contract.LocalJobRunner for execution's document-publish
// jobs (claim_context, run_export): the perform phase a controller invokes
// outside any Unit after claiming the job through the ordinary
// _execution.job.pending/.claim boundary. It decodes the exact
// pre-assembled bytes the originating Unit-bound step sealed, stages and
// publishes them, and returns the terminal outcome; the caller still
// records it through _execution.job.record (or recordDocumentJob directly)
// under its own current claim.
func (s *Service) RunJob(ctx context.Context, work contract.JobWork) (contract.JobOutcome, error) {
	var in documentJobInput
	if err := json.Unmarshal(work.Input, &in); err != nil {
		return contract.JobOutcome{}, fmt.Errorf("execution: decode document job input: %w", err)
	}
	ref, size, err := s.stageDocumentArtifact(ctx, in.Document)
	if err != nil {
		return contract.JobOutcome{}, err
	}
	result, err := canonicalJSON(map[string]any{
		"digest": ref.Digest, "size": size, "media_type": in.MediaType, "classification": in.Classification,
	})
	if err != nil {
		return contract.JobOutcome{}, err
	}
	return contract.JobOutcome{State: "succeeded", Result: result}, nil
}

// recordDocumentJob is the Unit-bound record phase: given the exact staged
// reference a prior perform phase already produced outside any Unit, it
// registers the real Artifact metadata (publishArtifact performs no blob
// IO itself -- it only promotes bytes a trusted IO phase already staged)
// and marks the job succeeded with that artifact as its terminal result --
// the terminal job callback a caller inspects via job.get. Reachable
// directly (same package, exactly as a controller would drive it once
// wired end to end) or through the ordinary _execution.job.record boundary.
func (s *Service) recordDocumentJob(ctx context.Context, unit contract.Unit, j *jobRow, staged wireArtifactRef, size int64, mediaType, classification string, now time.Time) (wireArtifact, error) {
	published, err := s.publishArtifact(ctx, unit, j.Scope, staged.Digest, size, mediaType, classification, true)
	if err != nil {
		return wireArtifact{}, err
	}
	resultJSON, err := canonicalJSON(map[string]any{"resource": published})
	if err != nil {
		return wireArtifact{}, err
	}
	j.State = "succeeded"
	j.Result = resultJSON
	ref := wireArtifactRef{ID: published.ID, Digest: published.Digest}
	j.ResultArtifact = &ref
	j.UpdatedAt = now
	if err := updateJob(ctx, unit, j); err != nil {
		return wireArtifact{}, err
	}
	if err := emitTransition(ctx, unit, terminalJobEvent(j.Kind), j.ID, j.Version); err != nil {
		return wireArtifact{}, err
	}
	return published, nil
}

// terminalJobEvent names the kind-specific terminal callback event a job's
// completion emits in addition to the generic execution_jobs ledger, so a
// caller watching run-level or claim-level events (not only job.get polling)
// observes the specific completion it cares about.
func terminalJobEvent(kind string) string {
	switch kind {
	case "run_export":
		return eventRunExportSucceeded
	case "claim_context":
		return eventClaimContextPublished
	default:
		return eventJobRecorded
	}
}

// wireClaimContext is the local zatiti.claim-context/v1 document a
// cooperative claim's synthesized context publishes when no real
// worker-produced context exists yet (no prior checkpoint): the pinned
// task/run identity, the worker's current authorized bindings, the
// executor's required capabilities and an explicit advisory disclaimer.
// Never carries a credential or owner secret (AGENTS.md: "Cooperative
// claims return pinned task/context/bindings/capabilities and advisory
// usage disclaimer, never dispatch credential or owner secrets").
// Deliberately carries no timestamp: the document is a pure function of
// stable task/run/worker/capability/binding identity, so a lost-ack replay
// (claimReplay, hitting the same no-checkpoint-yet fallback again, possibly
// at a later wall-clock time) recomputes byte-identical bytes and therefore
// the exact same digest/reference -- never a second, merely-similar one.
type wireClaimContext struct {
	Schema                string           `json:"schema"`
	AttemptID             contract.ID      `json:"attempt_id"`
	RunID                 contract.ID      `json:"run_id"`
	TaskID                contract.ID      `json:"task_id"`
	TaskVersion           contract.Version `json:"task_version"`
	WorkerID              contract.ID      `json:"worker_id"`
	ConfigurationRevision contract.Version `json:"configuration_revision"`
	InputVersions         []wireRef        `json:"input_versions"`
	DeclaredCapabilities  []string         `json:"declared_capabilities"`
	RequiredCapabilities  []string         `json:"required_capabilities"`
	Bindings              []wireBinding    `json:"bindings"`
	Advisory              string           `json:"advisory"`
	Capture               string           `json:"capture"`
}

// claimContextSchema names the local document schema.
const claimContextSchema = "zatiti.claim-context/v1"

// claimContextAdvisory is the fixed advisory-posture disclaimer every
// cooperative claim context carries (Z13.advisory_worker): lease fencing
// restricts Zatiti-side mutations only and never claims the external
// process is contained, sandboxed or terminated, and usage/cost figures a
// cooperative worker reports are advisory, not independently metered.
const claimContextAdvisory = "This context is advisory. Zatiti's lease fencing restricts further Zatiti-side mutations on this attempt after expiry or replacement; it does not sandbox, contain, meter, or terminate this worker's external process, shell, or network access. Usage and cost figures this worker reports are advisory only and are not independently metered by Zatiti. Only an independent verifier's recorded result -- never this worker's own report or a process exit -- can establish task success."

// buildClaimContext assembles the deterministic zatiti.claim-context/v1
// document bytes for a cooperative claim with no real context yet. Pure
// computation from data the caller already holds inside its own
// transaction -- no IO, no peer call, no wall-clock input.
func buildClaimContext(a *attemptRow, r *runRow, requiredCapabilities []string, bindings []wireBinding) ([]byte, error) {
	doc := wireClaimContext{
		Schema:                claimContextSchema,
		AttemptID:             a.ID,
		RunID:                 r.ID,
		TaskID:                r.TaskID,
		TaskVersion:           r.TaskVersion,
		WorkerID:              a.WorkerID,
		ConfigurationRevision: r.ConfigurationRevision,
		InputVersions:         nonNilRefs(r.InputVersions),
		DeclaredCapabilities:  nonEmptyStrings(a.Capabilities),
		RequiredCapabilities:  nonEmptyStrings(requiredCapabilities),
		Bindings:              nonNilBindings(bindings),
		Advisory:              claimContextAdvisory,
		Capture:               "advisory",
	}
	return canonicalJSON(doc)
}

// nonNilBindings guarantees a JSON array (never null) for the optional
// bindings list.
func nonNilBindings(in []wireBinding) []wireBinding {
	if in == nil {
		return []wireBinding{}
	}
	return in
}

// Run export canonical history (P21 item 3): a durable, bounded,
// full-fidelity export of a run's accepted task, every attempt it admitted,
// their effects/outputs/observations, the run's context/checkpoint lineage
// and any independently recorded verifier evidence with its source digests.
// Assembled entirely inside handleRunExport's own transaction (every field
// below is read from execution's own tables or task's already-pinned
// snapshot -- no new peer dependency), then published through the same
// document-publish job mechanism claim-context uses.

// wireRunExportHistory is the local zatiti.run-export/v1 canonical history
// document.
type wireRunExportHistory struct {
	Schema      string                 `json:"schema"`
	RunID       contract.ID            `json:"run_id"`
	Task        wireTask               `json:"task"`
	Run         wireRun                `json:"run"`
	Attempts    []wireExportAttempt    `json:"attempts"`
	Checkpoints []wireExportCheckpoint `json:"checkpoints"`
	TotalCosts  wireUsage              `json:"total_costs"`
	ExportedAt  string                 `json:"exported_at"`
}

// wireExportAttempt is one attempt's full record inside the export: its
// current wire state, every effect it dispatched, its reported outputs and
// observations, and its independently recorded verification evidence with
// source digests, when one exists.
type wireExportAttempt struct {
	Attempt      wireAttempt             `json:"attempt"`
	Effects      []wireExportEffect      `json:"effects"`
	Outputs      []wireArtifactRef       `json:"outputs"`
	Observations []wireObservation       `json:"observations"`
	Verification *wireExportVerification `json:"verification,omitempty"`
}

// wireExportEffect is one owned operation record: an effect this attempt
// dispatched, with its disposition.
type wireExportEffect struct {
	ID           contract.ID `json:"id"`
	Kind         string      `json:"kind"`
	State        string      `json:"state"`
	OperationRef string      `json:"operation_ref"`
	CreatedAt    string      `json:"created_at"`
}

// wireExportCheckpoint is one persisted checkpoint in the run's context
// lineage.
type wireExportCheckpoint struct {
	ID        contract.ID       `json:"id"`
	AttemptID contract.ID       `json:"attempt_id"`
	Context   wireArtifactRef   `json:"context"`
	Outputs   []wireArtifactRef `json:"outputs"`
	CreatedAt string            `json:"created_at"`
}

// wireExportVerification carries the trusted verifier's own recorded
// evidence -- never a worker-supplied verdict -- with the source digests
// (the sealed request bytes' own digest, and the pinned acceptance digest
// it was sealed against) that let a reader independently confirm this
// evidence traces to the exact accepted contract.
type wireExportVerification struct {
	JobID            contract.ID     `json:"job_id"`
	State            string          `json:"state"`
	AcceptanceDigest contract.Digest `json:"acceptance_digest"`
	RequestDigest    contract.Digest `json:"request_digest"`
	Request          json.RawMessage `json:"request"`
	Result           json.RawMessage `json:"result,omitempty"`
}

// runExportSchema names the local document schema.
const runExportSchema = "zatiti.run-export/v1"

// buildRunExportHistory assembles the full canonical history document for
// one run, entirely inside the caller's own transaction: the accepted task
// snapshot, every attempt (effects, outputs, observations, verification
// evidence) and the run's checkpoint lineage. No partial view -- every
// attempt the run ever admitted is included, not only the current one.
func (s *Service) buildRunExportHistory(ctx context.Context, unit contract.Unit, r *runRow, now time.Time) ([]byte, error) {
	task, err := s.callTaskSnapshot(ctx, unit, r.Scope, r.TaskID)
	if err != nil {
		return nil, err
	}

	attempts, err := listAttempts(ctx, unit, []string{"run_id = ?"}, []any{r.ID}, 4096)
	if err != nil {
		return nil, err
	}
	total := wireUsage{}
	exportAttempts := make([]wireExportAttempt, 0, len(attempts))
	for _, a := range attempts {
		ops, err := listOperationRecords(ctx, unit, a.ID)
		if err != nil {
			return nil, err
		}
		effects := make([]wireExportEffect, 0, len(ops))
		for _, o := range ops {
			effects = append(effects, wireExportEffect{
				ID: o.ID, Kind: o.Kind, State: o.State, OperationRef: o.OperationRef,
				CreatedAt: formatStamp(o.CreatedAt),
			})
		}
		var verification *wireExportVerification
		v, verr := loadVerificationJob(ctx, unit, a.ID)
		if verr != nil {
			var f *contract.Fault
			if !errors.As(verr, &f) || f.Code != contract.CodeNotFound {
				return nil, verr
			}
		}
		if v != nil {
			verification = &wireExportVerification{
				JobID: v.ID, State: v.State, AcceptanceDigest: v.AcceptanceDigest,
				RequestDigest: sha256Hex(v.Request), Request: v.Request, Result: v.Result,
			}
		}
		outputs := a.Outputs
		if outputs == nil {
			outputs = []wireArtifactRef{}
		}
		observations := a.Observations
		if observations == nil {
			observations = []wireObservation{}
		}
		exportAttempts = append(exportAttempts, wireExportAttempt{
			Attempt: attemptOut(a), Effects: effects, Outputs: outputs,
			Observations: observations, Verification: verification,
		})
		attemptUsage := sumUsage(a.Observations)
		total.Spent += attemptUsage.Spent
		total.Reserved += attemptUsage.Reserved
		total.Estimated += attemptUsage.Estimated
		total.Unknown += attemptUsage.Unknown
		total.Advisory = total.Advisory || attemptUsage.Advisory
		if attemptUsage.Currency != "" {
			total.Currency = attemptUsage.Currency
		}
	}

	checkpoints, err := listRunCheckpoints(ctx, unit, r.ID)
	if err != nil {
		return nil, err
	}
	exportCheckpoints := make([]wireExportCheckpoint, 0, len(checkpoints))
	for _, c := range checkpoints {
		exportCheckpoints = append(exportCheckpoints, wireExportCheckpoint{
			ID: c.ID, AttemptID: c.AttemptID, Context: c.Context, Outputs: c.Outputs,
			CreatedAt: formatStamp(c.CreatedAt),
		})
	}

	doc := wireRunExportHistory{
		Schema: runExportSchema, RunID: r.ID, Task: task, Run: runOut(r),
		Attempts: exportAttempts, Checkpoints: exportCheckpoints, TotalCosts: total,
		ExportedAt: formatStamp(now),
	}
	return canonicalJSON(doc)
}
