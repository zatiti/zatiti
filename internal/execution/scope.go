package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Shared handler helpers: scope narrowing, content digests, lease timing
// and the worker-call identity fence shared by heartbeat, checkpoint and
// report.

const (
	// leaseDuration is the default lease window a claim or heartbeat holds.
	leaseDuration = 60 * time.Second
	// maxListLimit bounds the default page size when the caller omits limit.
	defaultListLimit = 100
)

// narrowScope enforces the optional scope dimensions the caller declared:
// any set field must equal the resource's stored dimension, so a scoped
// caller can neither widen its query nor leak across organization, project,
// worker or task boundaries.
func narrowScope(scope contract.Scope, organizationID, projectID, workerID, taskID contract.ID) error {
	if scope.OrganizationID != "" && scope.OrganizationID != organizationID {
		return permissionDenied("scope organization does not match the resource")
	}
	if scope.ProjectID != "" && scope.ProjectID != projectID {
		return permissionDenied("scope project does not match the resource")
	}
	if scope.WorkerID != "" && scope.WorkerID != workerID {
		return permissionDenied("scope worker does not match the resource")
	}
	if scope.TaskID != "" && scope.TaskID != taskID {
		return permissionDenied("scope task does not match the resource")
	}
	return nil
}

// narrowAttemptScope applies narrowScope to an attempt row. Attempts store
// the worker dimension twice: the executing worker (worker_id) and the
// task-scope worker (worker_scope_id); callers address the executing worker.
func narrowAttemptScope(scope contract.Scope, a *attemptRow) error {
	return narrowScope(scope, a.OrganizationID, a.ProjectID, a.WorkerID, a.TaskID)
}

// narrowRunScope applies narrowScope to a run row.
func narrowRunScope(scope contract.Scope, r *runRow) error {
	return narrowScope(scope, r.OrganizationID, r.ProjectID, r.WorkerID, r.TaskID)
}

// sha256Hex computes the canonical content digest of a byte slice.
func sha256Hex(b []byte) contract.Digest {
	sum := sha256.Sum256(b)
	return contract.Digest(hex.EncodeToString(sum[:]))
}

// uuidFromDigest renders a deterministic UUIDv4-shaped identifier from a
// digest: the first 16 bytes with the version and variant bits set. Used for
// the claim envelope, so a replayed claim answers with the same reference.
func uuidFromDigest(d contract.Digest) contract.ID {
	raw, err := hex.DecodeString(string(d))
	if err != nil || len(raw) < 16 {
		return contract.ID("")
	}
	b := append([]byte(nil), raw[:16]...)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return contract.ID(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

// leaseExpiryAt computes the initial lease deadline: the default window,
// never past the task's root deadline when one is pinned.
func leaseExpiryAt(now time.Time, rootDeadline string) time.Time {
	expiry := now.Add(leaseDuration)
	if rootDeadline != "" {
		deadline, err := time.Parse(time.RFC3339Nano, rootDeadline)
		if err == nil && deadline.Before(expiry) {
			expiry = deadline
		}
	}
	return expiry
}

// checkWorkerCall enforces the identity fence shared by every worker-driven
// mutation: the caller must address a live attempt through its current
// lease, own generation and executing worker identity, the expected version
// must match, and the lease window must still be open. An expired lease is
// stale: heartbeat, checkpoint and report cannot revive it, and the tick
// scan fences it independently. Returns nil when the call may proceed.
func checkWorkerCall(a *attemptRow, inScope contract.Scope, leaseID contract.ID, generation int64, now time.Time) error {
	if a.LeaseID != leaseID {
		return conflict("lease %s is not the attempt's current lease", leaseID)
	}
	if a.Generation != generation {
		return conflict("generation %d does not match the attempt's current generation %d", generation, a.Generation)
	}
	if a.State != "claimed" && a.State != "running" && a.State != "waiting" {
		return conflict("attempt is %s and cannot be revived", a.State)
	}
	if !a.LeaseExpiresAt.IsZero() && !a.LeaseExpiresAt.After(now) {
		return conflict("lease expired at %s; request a replacement attempt", formatStamp(a.LeaseExpiresAt))
	}
	if inScope.WorkerID != "" && inScope.WorkerID != a.WorkerID {
		return permissionDenied("worker %s is not the attempt's bound worker", inScope.WorkerID)
	}
	return nil
}

// liveRunStates are the run states from which new attempts may be claimed.
var liveRunStates = map[string]bool{"ready": true, "waiting": true}

// terminalRunStates are the run states no further mutation may leave.
var terminalRunStates = map[string]bool{"succeeded": true, "failed": true, "cancelled": true}

// terminalJobStates are the job states a record call may not leave.
var terminalJobStates = map[string]bool{
	"succeeded": true, "failed": true, "outcome_unknown": true, "cancelled": true,
}
