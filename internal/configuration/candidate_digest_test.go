package configuration

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// candidateDigestPattern is the frozen $defs/Candidate candidate_digest
// pattern every peer schema-validates before its handler runs.
var candidateDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// peerCandidates decodes the candidate envelopes one peer operation
// received, in call order.
func peerCandidates(t *testing.T, env *testEnv, op string) []candidate {
	t.Helper()
	var out []candidate
	for _, inv := range env.ports.callsOf(op) {
		var in candidateEnvelope
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			t.Fatalf("%s input does not decode as a candidate envelope: %v", op, err)
		}
		out = append(out, in.Candidate)
	}
	return out
}

// TestPeerValidateReceivesSealedCandidateDigest: the digest a peer sees at
// plan-time validation is the digest of the complete candidate the plan
// seals — the same value the operator reviews on the plan, passes to
// configuration.apply, the peer sees again at the apply-time recheck and at
// activation, and the revision records. It is never empty.
func TestPeerValidateReceivesSealedCandidateDigest(t *testing.T) {
	env := newEnv(t)
	workerID, teamID := env.ids.New(), env.ids.New()
	draft := env.stage(workerChange(workerID, env.org, "digest-worker"))
	draft = env.stageInto(draft, createChange(kindTeam, teamID, newTeamDef(teamID, env.org, "digest-team", nil)))

	plan := env.planDraft(draft)
	if !candidateDigestPattern.MatchString(plan.CandidateDigest) {
		t.Fatalf("plan candidate_digest = %q, want 64 lowercase hex", plan.CandidateDigest)
	}
	planned := peerCandidates(t, env, "_identity.validate")
	if len(planned) != 1 {
		t.Fatalf("_identity.validate calls at plan = %d, want 1", len(planned))
	}
	if got := planned[0].CandidateDigest; got != plan.CandidateDigest {
		t.Fatalf("_identity.validate candidate_digest = %q, want the sealed plan digest %q", got, plan.CandidateDigest)
	}
	if planned[0].PlanID != plan.ID || planned[0].BaseRevision != plan.BaseRevision {
		t.Fatalf("_identity.validate candidate = plan %s base %d, want plan %s base %d",
			planned[0].PlanID, planned[0].BaseRevision, plan.ID, plan.BaseRevision)
	}
	// The peer sees only its slice, while the digest covers the complete
	// candidate: it is the whole-plan digest, not a per-owner one.
	if len(planned[0].Changes) != 1 || planned[0].Changes[0].Kind != kindWorker {
		t.Fatalf("_identity.validate slice = %+v, want only the worker change", planned[0].Changes)
	}
	staged, err := json.Marshal(draft.Changes)
	if err != nil {
		t.Fatalf("draft changes: %v", err)
	}
	changes, err := draftChanges(string(staged))
	if err != nil {
		t.Fatalf("draft changes: %v", err)
	}
	whole, err := computeCandidateDigest(changes)
	if err != nil {
		t.Fatalf("computeCandidateDigest: %v", err)
	}
	if plan.CandidateDigest != whole {
		t.Fatalf("plan digest %q is not the digest of the complete candidate %q", plan.CandidateDigest, whole)
	}

	rev := env.apply(plan)
	if rev.CandidateDigest != plan.CandidateDigest {
		t.Fatalf("revision candidate_digest = %q, want %q", rev.CandidateDigest, plan.CandidateDigest)
	}
	rechecked := peerCandidates(t, env, "_identity.validate")
	if len(rechecked) != 2 || rechecked[1].CandidateDigest != plan.CandidateDigest {
		t.Fatalf("apply-time _identity.validate candidates = %+v, want a recheck bound to %q", rechecked, plan.CandidateDigest)
	}
	activated := peerCandidates(t, env, "_identity.activate")
	if len(activated) != 1 || activated[0].CandidateDigest != plan.CandidateDigest {
		t.Fatalf("_identity.activate candidates = %+v, want one bound to %q", activated, plan.CandidateDigest)
	}
}

// TestCandidateDigestTracksTheCandidate: a different complete candidate
// yields a different digest at the peer, and an identical candidate yields
// the identical one, so the value binds what was reviewed.
func TestCandidateDigestTracksTheCandidate(t *testing.T) {
	env := newEnv(t)
	first := env.planDraft(env.stage(workerChange(env.ids.New(), env.org, "digest-a")))
	second := env.planDraft(env.stage(workerChange(env.ids.New(), env.org, "digest-b")))
	seen := peerCandidates(t, env, "_identity.validate")
	if len(seen) != 2 {
		t.Fatalf("_identity.validate calls = %d, want 2", len(seen))
	}
	if seen[0].CandidateDigest != first.CandidateDigest || seen[1].CandidateDigest != second.CandidateDigest {
		t.Fatalf("peer digests %q, %q do not match the sealed plans %q, %q",
			seen[0].CandidateDigest, seen[1].CandidateDigest, first.CandidateDigest, second.CandidateDigest)
	}
	if seen[0].CandidateDigest == seen[1].CandidateDigest {
		t.Fatalf("two different candidates share digest %q", seen[0].CandidateDigest)
	}
}
