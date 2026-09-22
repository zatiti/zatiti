package main

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/controller"
)

// This file is cmd/zatiti's half of the P00-011 six-step offline restore
// protocol: the entrypoint-supplied controller.RestoreLifecycle capability
// (internal/controller/restore.go) that only entrypoint assembly may
// implement, plus the outer per-process loop (serve.go's runServe) that
// reassembles Application/Controller/server over a freshly reopened
// database whenever a lifetime ends with controller.ErrRestoreHandoff.
//
// P33's card frames RestoreLifecycle as reachable because "only entrypoint
// assembly has direct Go access to internal/installation's private
// backup-bundle/key-resolution/overlay code" -- mirroring
// Collaborators.RestoreLifecycle's own doc comment in internal/controller.
// That is not true in the sense Go's package visibility enforces: every
// symbol that actually performs bundle decryption, key resolution and
// backup-artifact lookup (installation.openBackupBundle, .decodeArtifact,
// .resolveBackupKey, the AES-256-GCM sealBundle/openBundle pair in
// internal/installation/crypto.go) is unexported, and internal/installation
// exposes exactly one internal operation for the whole restore protocol:
// _installation.restore.record (the final disposition report this
// package's Collaborators.Identity-authenticated controller already calls
// via failRestore/settleRestore inside internal/controller/restore.go).
// There is no operation, public or internal, that returns a pending
// restore job's original backup-artifact reference (public job.get's own
// wire projection -- internal/execution/dto.go's jobOut -- never carries
// Input; only the controller's own _execution.job.claim call does, and
// that result lives in the controller's private journal entry, never
// passed to this capability) and no owner-defined merge operation exists
// in internal/effects, internal/identity or internal/memory to fold a
// RecoveryOverlay's obligations back into their own tables. Confirmed
// further: internal/installation's own landed obligation capture
// (snapshotObligations, restore.go) records only effects
// (claimed_effect/unknown_effect) and memory (memory_write) obligation
// kinds -- a credential/grant revocation is never captured into the
// overlay at all, exactly as internal/installation/restore_test.go's
// TestRestoreOverlayPreservesPostBackupUnknownEffectAcrossRewind doc
// comment says outright: "a credential/grant revocation cannot be
// exercised here because no owner port installation may call enumerates
// current revocations -- see the P31 handoff."
//
// Duplicating internal/installation's bundle/key-resolution crypto in this
// package (cmd/zatiti has its own direct contract.SecretStore/BlobStore
// access, so it is technically possible) was considered and rejected
// without explicit sign-off: it would create a second, un-synced
// implementation of secret-bearing AEAD framing that can silently drift
// from internal/installation's own (the exact "never invent a seam" this
// plan's own rule warns against), and it still would not solve
// MergeOverlay, which needs to write into OTHER owners' tables
// (effects_*/identity_*/memory_*) that cmd/zatiti has no sibling-SQL
// access to and no internal operation to call instead.
//
// So both methods below fail closed with a specific, machine-readable
// contract.CodePrerequisiteMissing fault naming exactly what is missing,
// exercising precisely the fallback Collaborators.RestoreLifecycle's own
// doc comment already designed for ("a restore job observed without one
// attached is recorded failed with prerequisite_missing, never guessed
// at") rather than fabricating success. StageCandidate failing means
// performSwap (internal/controller/restore.go) never calls
// storage.Restorable.PrepareRestore/CommitRestore, so no database file is
// ever touched by an incomplete restore attempt: the failure is safe, not
// merely honest. See docs/implementation-remediation/assignments/P33.md's
// handoff for the follow-up shape this needs (new merge operations in
// internal/effects/internal/identity/internal/memory, revocation-obligation
// capture in internal/installation's snapshotObligations, and a job-input
// lookup path for StageCandidate) before this capability can do real work.
type restoreLifecycle struct{}

var _ controller.RestoreLifecycle = restoreLifecycle{}

// errNoCandidateLookup is StageCandidate's fault: no operation, public or
// internal, hands entrypoint assembly a pending restore job's original
// backup-artifact reference, and internal/installation's bundle-decrypt
// code that would turn that reference into decrypted bytes is unexported.
func errNoCandidateLookup(restoreJobID contract.ID) *contract.Fault {
	return &contract.Fault{
		Code: contract.CodePrerequisiteMissing,
		Message: "no operation exposes restore job " + string(restoreJobID) +
			"'s backup artifact reference to entrypoint assembly, and internal/installation's " +
			"bundle-decrypt/key-resolution code is unexported; StageCandidate cannot resolve a " +
			"decrypted candidate database image (see cmd/zatiti/restore.go and " +
			"docs/implementation-remediation/assignments/P33.md's handoff)",
	}
}

// errNoOwnerMerge is MergeOverlay's fault: no owner-defined merge
// operation exists in internal/effects, internal/identity or
// internal/memory to fold a RecoveryOverlay's obligations back into their
// own tables, and revocation obligations were never captured into the
// overlay to begin with (internal/installation/restore_test.go documents
// this gap on the landed P31 code itself).
func errNoOwnerMerge(restoreJobID contract.ID) *contract.Fault {
	return &contract.Fault{
		Code: contract.CodePrerequisiteMissing,
		Message: "no owner-defined merge operation exists in internal/effects, internal/identity or " +
			"internal/memory to fold restore job " + string(restoreJobID) + "'s recovery overlay into " +
			"their own tables, and credential/grant revocation obligations are never captured into the " +
			"overlay in the first place; MergeOverlay cannot safely resume this restore (see " +
			"cmd/zatiti/restore.go and docs/implementation-remediation/assignments/P33.md's handoff)",
	}
}

// StageCandidate implements controller.RestoreLifecycle. See this file's
// package-level doc comment for why it fails closed.
func (restoreLifecycle) StageCandidate(_ context.Context, restoreJobID contract.ID, _ string) (controller.RestoreCandidate, error) {
	return controller.RestoreCandidate{}, errNoCandidateLookup(restoreJobID)
}

// MergeOverlay implements controller.RestoreLifecycle. See this file's
// package-level doc comment for why it fails closed. u is unused: there is
// nothing yet to merge into it.
func (restoreLifecycle) MergeOverlay(_ context.Context, _ contract.Unit, restoreJobID contract.ID) error {
	return errNoOwnerMerge(restoreJobID)
}
