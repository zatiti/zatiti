package tasks

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// The acceptance fence. At admission the acceptance contract is validated
// and sealed: its canonical digest is computed and stored beside the exact
// contract bytes. At success evaluation the digest is recomputed from the
// stored bytes and every expected observation is checked against the
// evidence tasks holds. Verification of result semantics belongs to the
// verifier runner inside execution; tasks owns the structural fence that a
// worker report or process exit can never satisfy alone.

// sealAcceptance validates the acceptance contract semantically and returns
// its canonical digest. The JSON body has already passed schema validation
// through strict input decoding.
func sealAcceptance(a *wireAcceptance) (contract.Digest, error) {
	if strings.TrimSpace(a.VerifierID) == "" {
		return "", invalidInput("acceptance verifier_id must not be empty")
	}
	if strings.TrimSpace(a.VerifierVersion) == "" {
		return "", invalidInput("acceptance verifier_version must not be empty")
	}
	if a.Mode != acceptanceModeIndependent && a.Mode != acceptanceModeManual {
		return "", invalidInput("acceptance mode %q is not recognized", a.Mode)
	}
	childSeen := map[contract.ID]bool{}
	for _, child := range a.RequiredChildIDs {
		if child == "" {
			return "", invalidInput("acceptance required_child_ids entries must be UUIDs")
		}
		if childSeen[child] {
			return "", invalidInput("acceptance required_child_ids contains duplicate %s", child)
		}
		childSeen[child] = true
	}
	profile, err := profileDecoded(a.Profile)
	if err != nil {
		return "", invalidInput("acceptance profile decoding failed: %v", err)
	}
	if strings.TrimSpace(profile.ID) == "" || strings.TrimSpace(profile.Version) == "" {
		return "", invalidInput("acceptance profile must pin verifier identity and version")
	}
	if profile.CodeDigest == "" {
		return "", invalidInput("acceptance profile must pin the verifier code digest")
	}
	switch {
	case a.Mode == acceptanceModeIndependent && profile.Kind == profileArtifactContract:
		if err := validateObservations(a.ExpectedObservations, map[string]bool{
			obsArtifactPresence: true, obsArtifactDigest: true, obsJSONSchema: true,
		}, map[string]string{
			obsArtifactPresence: "presence", obsArtifactDigest: "digest", obsJSONSchema: "json_schema",
		}, profile.SupportedChecks); err != nil {
			return "", err
		}
	case a.Mode == acceptanceModeIndependent && profile.Kind == profileRepositoryPatch:
		if err := validateObservations(a.ExpectedObservations, map[string]bool{
			obsRepositoryPatchApplies: true, obsRepositoryCommand: true,
		}, nil, nil); err != nil {
			return "", err
		}
	case a.Mode == acceptanceModeManual:
		// Manual contracts still pin the verifier identity they would use,
		// but authorize an eligible principal to decide. Observations may be
		// empty; any present ones stay validated against the profile kind.
		if profile.Kind != profileArtifactContract && profile.Kind != profileRepositoryPatch {
			return "", invalidInput("acceptance profile kind %q is not recognized", profile.Kind)
		}
		if profile.Kind == profileArtifactContract {
			if err := validateObservations(a.ExpectedObservations, map[string]bool{
				obsArtifactPresence: true, obsArtifactDigest: true, obsJSONSchema: true,
			}, map[string]string{
				obsArtifactPresence: "presence", obsArtifactDigest: "digest", obsJSONSchema: "json_schema",
			}, profile.SupportedChecks); err != nil {
				return "", err
			}
		} else {
			if err := validateObservations(a.ExpectedObservations, map[string]bool{
				obsRepositoryPatchApplies: true, obsRepositoryCommand: true,
			}, nil, nil); err != nil {
				return "", err
			}
		}
	default:
		return "", invalidInput("acceptance mode %s does not match verifier profile kind %q",
			a.Mode, profile.Kind)
	}
	raw, err := marshalData(a)
	if err != nil {
		return "", err
	}
	canonical, err := contract.Canonicalize(raw)
	if err != nil {
		return "", invalidInput("acceptance canonicalization failed: %v", err)
	}
	return contract.Hash(canonical), nil
}

// validateObservations checks kind/profile consistency and the structural
// requirements each observation kind carries. supportedCheckNames maps an
// observation kind to the artifact-contract profile capability that covers
// it; profileCapabilities may be nil for repository profiles.
func validateObservations(obs []wireExpectedObservation, allowedKinds map[string]bool, supportedCheckNames map[string]string, profileCapabilities []string) error {
	if len(obs) == 0 {
		return invalidInput("independent acceptance requires expected_observations")
	}
	capabilitySet := map[string]bool{}
	for _, c := range profileCapabilities {
		capabilitySet[c] = true
	}
	seen := map[string]bool{}
	for _, o := range obs {
		if o.CheckID == "" {
			return invalidInput("expected observation check_id must not be empty")
		}
		if seen[o.CheckID] {
			return invalidInput("expected observation check_id %q is duplicated", o.CheckID)
		}
		seen[o.CheckID] = true
		if !allowedKinds[o.Kind] {
			return invalidInput("expected observation %s kind %q does not match the pinned verifier profile", o.CheckID, o.Kind)
		}
		if o.Expected != observationExpectedPass && o.Expected != observationExpectedFail {
			return invalidInput("expected observation %s expected %q is not pass or fail", o.CheckID, o.Expected)
		}
		if name, ok := supportedCheckNames[o.Kind]; ok && !capabilitySet[name] {
			return invalidInput("verifier profile does not declare support for check %q", o.Kind)
		}
		switch o.Kind {
		case obsArtifactPresence:
			if o.ExpectedDigest == "" {
				// Presence of named bytes is only structurally provable when
				// the exact digest is pinned at admission; a contract without
				// it cannot fence success independently.
				return invalidInput("expected observation %s kind %s must pin expected_digest", o.CheckID, o.Kind)
			}
		case obsArtifactDigest:
			if o.ExpectedDigest == "" {
				return invalidInput("expected observation %s kind %s must pin expected_digest", o.CheckID, o.Kind)
			}
		case obsJSONSchema:
			if len(o.Schema) == 0 {
				return invalidInput("expected observation %s kind %s must pin the inert schema", o.CheckID, o.Kind)
			}
		case obsRepositoryCommand:
			if o.CommandID == "" {
				return invalidInput("expected observation %s kind %s must pin command_id", o.CheckID, o.Kind)
			}
		}
	}
	return nil
}

// acceptanceEvaluation is the outcome of the structural success fence.
type acceptanceEvaluation struct {
	// established records how the task may be marked succeeded.
	established string
}

// evaluateSuccess applies the acceptance fence for a transition to
// succeeded. It returns the establishment method (verification or manual)
// or an error naming every check that is not established.
func (s *Service) evaluateSuccess(ctx context.Context, unit contract.Unit, r *taskRow, evidence []evidenceRow, manual bool) (*acceptanceEvaluation, error) {
	// Seal integrity first: the stored contract must hash to the sealed
	// digest. A mismatch means the acceptance was tampered with during the
	// run; dependent qualifications are invalidated immediately.
	canonical, err := contract.Canonicalize([]byte(r.AcceptanceJSON))
	if err != nil {
		return nil, verificationFailed("stored acceptance is not canonicalizable: %v", err)
	}
	if contract.Hash(canonical) != contract.Digest(r.AcceptanceDigest) {
		if ierr := s.invalidateAcceptance(ctx, unit, r, "accepted contract digest mismatch during verification"); ierr != nil {
			return nil, ierr
		}
		return nil, verificationFailed("accepted contract does not match the sealed digest; verification refused")
	}
	acceptance, err := r.decodeAcceptance()
	if err != nil {
		return nil, err
	}

	if acceptance.Mode == acceptanceModeManual {
		// Manual contracts resolve only through the eligibility-fenced
		// task.accept path, which records the durable decision under the
		// accountable reviewer. A transition can never establish manual
		// success, labeled or not.
		return nil, verificationFailed("task contract requires explicitly manual acceptance; manual success applies only through task.accept")
	}

	// Independent mode: a manual label never substitutes for verification.
	if manual {
		return nil, verificationFailed("independent contract needs verifier-established observations; a manual label is not proof")
	}

	// Digest fence: every passing byte-level observation needs an evidence
	// artifact with exactly the pinned digest. Schema and repository checks
	// need a verifier-produced result artifact among the evidence.
	digestPresent := map[string]bool{}
	for _, e := range evidence {
		digestPresent[e.Digest] = true
	}
	verifierRan := false
	for _, e := range evidence {
		if e.MediaType == verificationMediaType {
			verifierRan = true
			break
		}
	}
	var unestablished []string
	for _, o := range acceptance.ExpectedObservations {
		if o.Expected != observationExpectedPass {
			// Negative observations fail when their pinned bytes appear.
			if o.ExpectedDigest != "" && digestPresent[string(o.ExpectedDigest)] {
				unestablished = append(unestablished, fmt.Sprintf("%s (pinned bytes must be absent)", o.CheckID))
			}
			continue
		}
		switch o.Kind {
		case obsArtifactPresence, obsArtifactDigest:
			if !digestPresent[string(o.ExpectedDigest)] {
				unestablished = append(unestablished, fmt.Sprintf("%s (no artifact with pinned digest)", o.CheckID))
			}
		case obsJSONSchema, obsRepositoryPatchApplies, obsRepositoryCommand:
			if !verifierRan {
				unestablished = append(unestablished, fmt.Sprintf("%s (no verifier result artifact)", o.CheckID))
			}
		}
	}
	if len(unestablished) > 0 {
		sort.Strings(unestablished)
		return nil, verificationFailed("acceptance observations not established: %s",
			strings.Join(unestablished, ", "))
	}
	return &acceptanceEvaluation{established: establishedVerification}, nil
}

// checkRequiredChildren enforces parent completion criteria: every required
// child must exist and be succeeded, and an independent parent requires
// verifier-established child success. A manually accepted child stays
// labeled manual and never reads as automated proof.
func (s *Service) checkRequiredChildren(ctx context.Context, unit contract.Unit, r *taskRow) error {
	acceptance, err := r.decodeAcceptance()
	if err != nil {
		return err
	}
	for _, childID := range acceptance.RequiredChildIDs {
		child, err := getTask(ctx, unit, childID)
		if err != nil {
			return err
		}
		if child == nil {
			return prerequisiteMissing("required child %s does not exist", childID)
		}
		if child.InstallationID != r.InstallationID {
			return permissionDenied("required child %s is outside the installation scope", childID)
		}
		if child.State != stateSucceeded {
			return verificationFailed("required child %s is not succeeded (state %s)", childID, child.State)
		}
		if acceptance.Mode == acceptanceModeIndependent && child.EstablishedBy != establishedVerification {
			return verificationFailed("required child %s succeeded only through %s, not independent verification", childID, child.EstablishedBy)
		}
	}
	return nil
}

// invalidateAcceptance calls policy to invalidate qualifications that
// depend on the tampered contract, before any further admission.
func (s *Service) invalidateAcceptance(ctx context.Context, unit contract.Unit, r *taskRow, reason string) error {
	return s.callPeer(ctx, unit, "_policy.invalidate", peerPolicyInvalidateIn{
		ChangedDependencies: []wireRef{{ID: r.ID, Version: contract.Version(r.Version)}},
		Reason:              reason,
	}, nil)
}
