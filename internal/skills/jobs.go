package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/zatiti/zatiti/internal/contract"
)

// The skill.evaluate durable job seam (P19; contract-proposals.md "Job
// registry linkage"; AGENTS.md revision 3 "_skills.evaluation.record").
//
//  1. handleEvaluate (handlers.go) seals the evaluation record and registers
//     a durable job through _execution.job.create -- never inventing a job
//     id locally. No verification happens here.
//  2. RunJob is this owner's contract.LocalJobRunner: the frozen local job
//     executor the controller invokes outside any transaction once it
//     claims the pending job. It performs real verification against the
//     published fixture artifacts the acceptance sealed, under the exact
//     pinned verifier profile, and stages the resulting evidence document.
//     It never stores pending metadata forever and never invents a pass:
//     an unobservable or interrupted check makes the job's own outcome
//     unknown, not a fabricated verdict.
//  3. _skills.evaluation.record (handlers below) is this owner's callback:
//     execution/controller records the established verifier evidence
//     against the exact immutable skill version the evaluation names. A
//     verifier identity that no longer matches what was admitted invalidates
//     the evaluation instead of recording the mismatched submission.

// defaultEvaluationMaxBytes bounds fixture reads when the pinned profile
// declares no read bound.
const defaultEvaluationMaxBytes = 1 << 20

// verifierProfileCore reads the shared identity fields of either verifier
// profile flavor (artifact_contract or repository_patch), decoded
// tolerantly: skills only ever acts on the artifact_contract fields it
// understands, and a repository profile's extra fields are irrelevant to
// the honest "unavailable" observation it always receives here.
type verifierProfileCore struct {
	Schema          string   `json:"schema"`
	Kind            string   `json:"kind"`
	ID              string   `json:"id"`
	Version         string   `json:"version"`
	CodeDigest      string   `json:"code_digest"`
	SupportedChecks []string `json:"supported_checks,omitempty"`
	MaxBytes        int64    `json:"max_bytes"`
	TimeoutSeconds  int64    `json:"timeout_seconds"`
}

// evalObservedCheck is one independently observed expected-check outcome.
type evalObservedCheck struct {
	CheckID        string          `json:"check_id"`
	Kind           string          `json:"kind"`
	Status         string          `json:"status"` // passed | failed | unavailable
	Explanation    string          `json:"explanation"`
	ObservedDigest contract.Digest `json:"observed_digest,omitempty"`
}

// profileSupportsCheck reports whether the pinned artifact_contract profile
// declares the exact check kind supported. A repository_patch profile (or
// any other kind) never supports anything here: skills has no controlled
// runner in this runtime, so dry-run/sandbox containment it cannot actually
// provide is never advertised as available.
func profileSupportsCheck(profile verifierProfileCore, kind string) bool {
	if profile.Kind != "artifact_contract" {
		return false
	}
	declared, ok := map[string]string{
		"artifact_presence": "presence",
		"artifact_digest":   "digest",
		"json_schema":       "json_schema",
	}[kind]
	if !ok {
		return false
	}
	for _, c := range profile.SupportedChecks {
		if c == declared {
			return true
		}
	}
	return false
}

// observeCheck independently establishes one expected check against the
// published fixture artifacts, reading real bytes through the blob store.
// A check kind the pinned profile does not declare supported -- including
// every repository_patch_applies/repository_command check, since skills
// hosts no controlled runner -- is reported unavailable, never a fabricated
// pass: this is the "never advertise unavailable containment" guarantee.
func (s *Service) observeCheck(ctx context.Context, profile verifierProfileCore, want wireExpectedObservation) evalObservedCheck {
	obs := evalObservedCheck{CheckID: want.CheckID, Kind: want.Kind}
	if !profileSupportsCheck(profile, want.Kind) {
		obs.Status = "unavailable"
		if profile.Kind == "repository_patch" {
			obs.Explanation = "repository verifier profile requires the controlled runner, which is unavailable in this runtime"
		} else {
			obs.Explanation = "check kind is not declared supported by the pinned verifier profile"
		}
		return obs
	}
	maxBytes := profile.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultEvaluationMaxBytes
	}
	digest := contract.Digest(want.ExpectedDigest)
	switch want.Kind {
	case "artifact_presence":
		rc, err := s.blobs.Open(ctx, digest, 0, 1)
		if err != nil {
			obs.Status = "unavailable"
			obs.Explanation = fmt.Sprintf("fixture artifact %s is not addressable in the blob store", digest)
			return obs
		}
		_ = rc.Close()
		obs.Status = "passed"
		obs.Explanation = fmt.Sprintf("fixture artifact %s is addressable", digest)
		return obs

	case "artifact_digest":
		data, err := s.readBoundedFixture(ctx, digest, maxBytes)
		if err != nil {
			obs.Status = "unavailable"
			obs.Explanation = fmt.Sprintf("fixture artifact %s could not be read for digest comparison", digest)
			return obs
		}
		obs.ObservedDigest = contract.Hash(data)
		if obs.ObservedDigest == digest {
			obs.Status = "passed"
			obs.Explanation = "observed digest matches the pinned expectation"
		} else {
			obs.Status = "failed"
			obs.Explanation = "observed digest does not match the pinned expectation"
		}
		return obs

	case "json_schema":
		data, err := s.readBoundedFixture(ctx, digest, maxBytes)
		if err != nil {
			obs.Status = "unavailable"
			obs.Explanation = fmt.Sprintf("fixture artifact %s could not be read for schema validation", digest)
			return obs
		}
		obs.ObservedDigest = contract.Hash(data)
		if err := contract.ValidateSchema(want.Schema, data); err != nil {
			obs.Status = "failed"
			obs.Explanation = fmt.Sprintf("fixture does not satisfy the pinned schema: %v", err)
		} else {
			obs.Status = "passed"
			obs.Explanation = "fixture satisfies the pinned schema"
		}
		return obs

	default:
		// repository_patch_applies and repository_command are unreachable
		// here: profileSupportsCheck already refused them above. Kept as an
		// explicit, honest fallback rather than a silent default pass.
		obs.Status = "unavailable"
		obs.Explanation = "check kind requires the controlled runner, which is unavailable in this runtime"
		return obs
	}
}

// readBoundedFixture reads one published fixture artifact's full content
// under the pinned read bound.
func (s *Service) readBoundedFixture(ctx context.Context, digest contract.Digest, limit int64) ([]byte, error) {
	rc, err := s.blobs.Open(ctx, digest, 0, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("fixture artifact %s exceeds the %d byte read bound", digest, limit)
	}
	return data, nil
}

// evaluationVerdict recomputes the overall verdict from the expected checks
// against what was actually observed. definite is false whenever any
// expected check could not be observed at all -- the honest outcome is
// unknown, never an invented pass or an unjustified fail. A pass requires
// every expected check to have been observed exactly as required.
func evaluationVerdict(expected []wireExpectedObservation, observed []evalObservedCheck) (definite, passed bool, reason string) {
	byID := make(map[string]evalObservedCheck, len(observed))
	for _, o := range observed {
		byID[o.CheckID] = o
	}
	unavailable, mismatch := "", ""
	for _, want := range expected {
		obs, ok := byID[want.CheckID]
		if !ok || obs.Status == "unavailable" {
			if unavailable == "" {
				unavailable = want.CheckID
			}
			continue
		}
		required := "passed"
		if want.Expected == "fail" {
			required = "failed"
		}
		if obs.Status != required && mismatch == "" {
			mismatch = want.CheckID
		}
	}
	switch {
	case unavailable != "":
		return false, false, "check " + unavailable + " could not be independently observed"
	case mismatch != "":
		return true, false, "check " + mismatch + " did not observe the required outcome"
	default:
		return true, true, ""
	}
}

// RunJob implements contract.LocalJobRunner: the frozen local job executor
// for skill.evaluate. It preserves the accepted skill/version, fixtures,
// posture, limits and verifier profile exactly as sealed at admission --
// work.Input is decoded strictly, never re-derived -- and performs real
// verification against published fixture artifacts rather than storing
// pending evaluation metadata forever. It runs outside any transaction:
// blob reads and staging are its only physical effects.
func (s *Service) RunJob(ctx context.Context, work contract.JobWork) (contract.JobOutcome, error) {
	if work.Owner != ownerName {
		return contract.JobOutcome{}, invalidInput("skills cannot run a job owned by %q", work.Owner)
	}
	if work.Operation != "skill.evaluate" {
		return contract.JobOutcome{}, invalidInput("skills has no local job runner for operation %q", work.Operation)
	}
	var in evaluationJobInput
	if err := contract.DecodeStrict(work.Input, &in); err != nil {
		return contract.JobOutcome{}, invalidInput("skill evaluation job input is not decodable: %v", err)
	}

	var profile verifierProfileCore
	if err := json.Unmarshal(in.Acceptance.Profile, &profile); err != nil {
		return unknownEvaluationOutcome("pinned verifier profile does not decode"), nil
	}

	observed := make([]evalObservedCheck, 0, len(in.Acceptance.ExpectedObservations))
	for _, want := range in.Acceptance.ExpectedObservations {
		observed = append(observed, s.observeCheck(ctx, profile, want))
		if ctx.Err() != nil {
			break
		}
	}
	definite, passed, reason := evaluationVerdict(in.Acceptance.ExpectedObservations, observed)
	if ctx.Err() != nil {
		return unknownEvaluationOutcome("the evaluation run was interrupted before every expected check could be observed"), nil
	}
	if !definite {
		return unknownEvaluationOutcome(reason), nil
	}

	// Stage the independently observed evidence document. This is the
	// verifier's own record of what it actually saw, distinct from the
	// self-authored acceptance it was checked against.
	doc := map[string]any{
		"schema":           "zatiti.skill-evaluation-result/v1",
		"evaluation_id":    in.EvaluationID,
		"skill":            in.Skill,
		"verifier_id":      profile.ID,
		"verifier_version": profile.Version,
		"passed":           passed,
		"observations":     observed,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return contract.JobOutcome{}, internalError("skill evaluation evidence encoding failed")
	}
	canon, err := contract.Canonicalize(raw)
	if err != nil {
		return contract.JobOutcome{}, invalidInput("skill evaluation evidence is not canonicalizable: %v", err)
	}
	stagingRef, digest, _, err := s.blobs.Stage(ctx, bytes.NewReader(canon), int64(len(canon)))
	if err != nil {
		return contract.JobOutcome{}, err
	}
	if err := s.blobs.Publish(ctx, stagingRef, digest); err != nil {
		return contract.JobOutcome{}, err
	}
	evidenceID := s.ids.New()

	result, err := json.Marshal(map[string]any{
		"evaluation_id": in.EvaluationID,
		"passed":        passed,
		"evidence":      []wireArtifactRef{{ID: evidenceID, Digest: digest}},
	})
	if err != nil {
		return contract.JobOutcome{}, internalError("skill evaluation result encoding failed")
	}
	return contract.JobOutcome{
		State:       "succeeded",
		Result:      result,
		EvidenceIDs: []contract.ID{evidenceID},
	}, nil
}

// unknownEvaluationOutcome builds the outcome_unknown JobOutcome for a run
// that could not establish a definite verdict. Result stays the empty
// object: nothing about pass/fail is asserted, and no evidence is minted.
func unknownEvaluationOutcome(reason string) contract.JobOutcome {
	if reason == "" {
		reason = "the skill evaluation could not be independently established"
	}
	return contract.JobOutcome{
		State:  "outcome_unknown",
		Result: json.RawMessage(`{}`),
		Requirements: []contract.Requirement{{
			Code: contract.CodeOutcomeUnknown, Message: reason,
		}},
	}
}

// handleEvaluationRecord implements _skills.evaluation.record: the sole
// path that records published verifier evidence against the exact
// immutable skill version an evaluation names. It never leaves an
// evaluation pending forever and never lets a later call invent a
// different pass: a call that exactly replays an already-terminal
// disposition returns it unchanged; anything else against a terminal
// evaluation is refused outright.
func handleEvaluationRecord(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[evaluationRecordInput](s, "_skills.evaluation.record", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	installation := unit.Scope().InstallationID
	row, err := fetchEvaluation(ctx, unit, installation, in.EvaluationID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("evaluation %s was not found", in.EvaluationID)
	}
	if row.JobID != in.JobID {
		return contract.Payload{}, conflictFault(
			"evaluation %s is linked to job %s, not %s", in.EvaluationID, row.JobID, in.JobID)
	}
	if in.Passed && len(in.EvidenceIDs) == 0 {
		return contract.Payload{}, invalidInput("a passed evaluation requires at least one evidence reference")
	}
	// Job.result is declared as an object in the shared $defs, so the
	// evidence identity list is wrapped rather than stored as a bare array.
	evidenceJSON, err := marshalJSON(map[string]any{"evidence_ids": in.EvidenceIDs})
	if err != nil {
		return contract.Payload{}, err
	}

	if terminalState(row.State) {
		if sameEvaluationDisposition(row, in, evidenceJSON) {
			return s.completed(jobOutput{Resource: wireJobOf(*row)})
		}
		return contract.Payload{}, staleVersion(
			"evaluation %s already recorded a terminal disposition and cannot record a different one", in.EvaluationID)
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("evaluation %s is at version %d, not %d", in.EvaluationID, row.Version, in.ExpectedVersion)
	}

	now := s.clock.Now().UTC().Format(timeLayout)

	// Revision 3: a verifier identity/version that no longer matches what
	// this evaluation admitted means the evaluator changed since admission.
	// The evaluation is invalidated -- moved to a terminal failed state --
	// instead of recording the mismatched submission as if it qualified.
	if row.VerifierID != in.VerifierID || row.VerifierVersion != in.VerifierVersion {
		observations, err := marshalJSON(map[string]any{
			"invalidated":                true,
			"reason":                     "verifier identity changed since evaluation admission",
			"admitted_verifier_id":       row.VerifierID,
			"admitted_verifier_version":  row.VerifierVersion,
			"submitted_verifier_id":      in.VerifierID,
			"submitted_verifier_version": in.VerifierVersion,
		})
		if err != nil {
			return contract.Payload{}, err
		}
		// No evidence is recorded for an invalidated evaluation: the
		// mismatched submission's evidence never qualified anything.
		if err := recordEvaluationOutcome(ctx, unit, now, installation, in.EvaluationID, in.ExpectedVersion, "failed", observations, ""); err != nil {
			return contract.Payload{}, err
		}
		updated, err := fetchEvaluation(ctx, unit, installation, in.EvaluationID)
		if err != nil {
			return contract.Payload{}, err
		}
		if updated == nil {
			return contract.Payload{}, internalError("evaluation %s vanished after recording", in.EvaluationID)
		}
		if err := s.emitEvaluationRecorded(ctx, unit, in.EvaluationID, updated.Version, row.SkillID, row.SkillVersion, "failed", false); err != nil {
			return contract.Payload{}, err
		}
		return s.completed(jobOutput{Resource: wireJobOf(*updated)})
	}

	state := "failed"
	if in.Passed {
		state = "succeeded"
	}
	observations, err := marshalJSON(map[string]any{
		"verifier_id":      in.VerifierID,
		"verifier_version": in.VerifierVersion,
		"passed":           in.Passed,
	})
	if err != nil {
		return contract.Payload{}, err
	}
	if err := recordEvaluationOutcome(ctx, unit, now, installation, in.EvaluationID, in.ExpectedVersion, state, observations, evidenceJSON); err != nil {
		return contract.Payload{}, err
	}
	updated, err := fetchEvaluation(ctx, unit, installation, in.EvaluationID)
	if err != nil {
		return contract.Payload{}, err
	}
	if updated == nil {
		return contract.Payload{}, internalError("evaluation %s vanished after recording", in.EvaluationID)
	}
	if err := s.emitEvaluationRecorded(ctx, unit, in.EvaluationID, updated.Version, row.SkillID, row.SkillVersion, state, in.Passed); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(jobOutput{Resource: wireJobOf(*updated)})
}

// terminalState reports whether an evaluation state no longer accepts a
// first recording.
func terminalState(state string) bool {
	switch state {
	case "succeeded", "failed", "outcome_unknown", "cancelled":
		return true
	default:
		return false
	}
}

// sameEvaluationDisposition reports whether a record call against an
// already-terminal row is an exact replay of what is already stored: the
// same job, the same admitted verifier, the same pass/fail verdict and the
// same evidence set. Only an exact replay is idempotent; anything else
// against a terminal row is refused.
func sameEvaluationDisposition(row *evaluationRow, in *evaluationRecordInput, evidenceJSON string) bool {
	wantState := "failed"
	if in.Passed {
		wantState = "succeeded"
	}
	return row.State == wantState &&
		row.VerifierID == in.VerifierID &&
		row.VerifierVersion == in.VerifierVersion &&
		row.EvidenceJSON == evidenceJSON
}
