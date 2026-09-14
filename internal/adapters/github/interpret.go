package github

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// evidenceFields carries the optional, kind-specific fields of
// GitHubEvidence, along with the disposition/confirmation a reconcile read
// determined by comparing observed provider state to what the original
// dispatch expected. Invoke's own disposition/confirmation are decided
// directly from the HTTP status of its one physical call and are not
// carried here.
type evidenceFields struct {
	disposition       string
	confirmation      string
	branch            string
	observedHeadSHA   string
	observedBaseSHA   string
	commitSHA         string
	pullRequestNumber int64
	pullRequestURL    string
}

func (f evidenceFields) providerReference() string {
	switch {
	case f.pullRequestURL != "":
		return f.pullRequestURL
	case f.commitSHA != "":
		return f.commitSHA
	case f.observedHeadSHA != "":
		return f.observedHeadSHA
	default:
		return ""
	}
}

// interpretInvokeResponse extracts the observed state GitHub returned for a
// successful (2xx) Invoke response. It never changes disposition: Invoke's
// own synchronous response is already authoritative for the call just made.
func interpretInvokeResponse(act *action, body []byte) evidenceFields {
	var f evidenceFields
	switch act.Kind {
	case kindCreateBranch:
		var r ghRefResponse
		if json.Unmarshal(body, &r) == nil {
			f.observedHeadSHA = r.Object.SHA
		}
		f.branch = act.CreateBranch.Branch
	case kindPushCommit:
		var r ghRefResponse
		if json.Unmarshal(body, &r) == nil {
			f.observedHeadSHA = r.Object.SHA
		}
		f.branch = act.PushCommit.Branch
	case kindOpenPullRequest:
		var r ghPullResponse
		if json.Unmarshal(body, &r) == nil {
			f.pullRequestNumber = r.Number
			f.pullRequestURL = r.HTMLURL
			f.observedHeadSHA = r.Head.SHA
			f.observedBaseSHA = r.Base.SHA
		}
	case kindMergePullRequest:
		var r ghMergeResponse
		if json.Unmarshal(body, &r) == nil {
			f.commitSHA = r.SHA
		}
		f.pullRequestNumber = act.MergePullRequest.PullRequestNumber
	case kindReadRepository:
		f.branch = act.ReadRepository.Branch
		if act.ReadRepository.Resource == "ref" {
			var r ghRefResponse
			if json.Unmarshal(body, &r) == nil {
				f.observedHeadSHA = r.Object.SHA
			}
		}
	}
	return f
}

// interpretReconcileResponse compares a bounded reconcile read's observed
// state to what the original dispatch expected. A GitHub not-found can
// never prove nonexecution ("GitHub eventual not-found cannot prove
// nonexecution"), so only a positive, present observation that
// contradicts the expected outcome is reported as authoritative_nonexecution;
// every other inconclusive case stays unknown.
func interpretReconcileResponse(act *action, body []byte) evidenceFields {
	var f evidenceFields
	switch act.Kind {
	case kindCreateBranch:
		w := act.CreateBranch
		var r ghRefResponse
		_ = json.Unmarshal(body, &r)
		f.branch = w.Branch
		f.observedHeadSHA = r.Object.SHA
		if r.Object.SHA != "" && r.Object.SHA == w.BaseSHA {
			f.disposition = contract.DispositionSucceeded
			f.confirmation = "authoritative_success"
		} else {
			f.disposition = contract.DispositionUnknown
			f.confirmation = "unknown"
		}

	case kindPushCommit:
		w := act.PushCommit
		var r ghRefResponse
		_ = json.Unmarshal(body, &r)
		f.branch = w.Branch
		f.observedHeadSHA = r.Object.SHA
		switch {
		case r.Object.SHA != "" && r.Object.SHA == w.PreparedCommitSHA:
			f.disposition = contract.DispositionSucceeded
			f.confirmation = "authoritative_success"
		case r.Object.SHA != "" && r.Object.SHA == w.ExpectedHeadSHA:
			// A positive read showing the ref unchanged at the pre-push
			// head is direct, present evidence the push never applied --
			// unlike an absence, this is not the not-found ambiguity.
			f.disposition = contract.DispositionFailed
			f.confirmation = "authoritative_nonexecution"
		default:
			f.disposition = contract.DispositionUnknown
			f.confirmation = "unknown"
		}

	case kindOpenPullRequest:
		w := act.OpenPullRequest
		var list []ghPullResponse
		_ = json.Unmarshal(body, &list)
		var match *ghPullResponse
		for i := range list {
			if list[i].Base.SHA == w.BaseSHA || list[i].Head.SHA == w.HeadSHA {
				match = &list[i]
				break
			}
		}
		if match == nil && len(list) > 0 {
			match = &list[0]
		}
		if match != nil {
			f.pullRequestNumber = match.Number
			f.pullRequestURL = match.HTMLURL
			f.observedHeadSHA = match.Head.SHA
			f.observedBaseSHA = match.Base.SHA
			f.disposition = contract.DispositionSucceeded
			f.confirmation = "authoritative_success"
		} else {
			// No pull request is currently visible for this head branch.
			// Absence cannot prove nonexecution.
			f.disposition = contract.DispositionUnknown
			f.confirmation = "unknown"
		}

	case kindMergePullRequest:
		w := act.MergePullRequest
		var r ghPullResponse
		_ = json.Unmarshal(body, &r)
		f.pullRequestNumber = w.PullRequestNumber
		if r.Merged {
			f.disposition = contract.DispositionSucceeded
			f.confirmation = "authoritative_success"
		} else {
			// A positive read of the existing pull request showing
			// merged=false is direct, present evidence, not an absence.
			f.disposition = contract.DispositionFailed
			f.confirmation = "authoritative_nonexecution"
		}

	case kindReadRepository:
		f.disposition = contract.DispositionSucceeded
		f.confirmation = "authoritative_success"
	}
	return f
}

// interpretReconcileFailure handles a non-2xx reconcile read. GitHub
// eventual not-found cannot prove nonexecution, so this is always unknown,
// never a claim that the original effect failed.
func interpretReconcileFailure() evidenceFields {
	return evidenceFields{disposition: contract.DispositionUnknown, confirmation: "unknown"}
}

// actionAutomation returns the automation constraints declared by a
// mutating action, or nil for read_repository (which has none).
func actionAutomation(act *action) *wireAutomationConstraints {
	switch {
	case act.CreateBranch != nil:
		return &act.CreateBranch.AutomationConstraints
	case act.PushCommit != nil:
		return &act.PushCommit.AutomationConstraints
	case act.OpenPullRequest != nil:
		return &act.OpenPullRequest.AutomationConstraints
	case act.MergePullRequest != nil:
		return &act.MergePullRequest.AutomationConstraints
	default:
		return nil
	}
}

// callContext bounds ctx by the earlier of the profile's configured
// timeout (measured from the injected clock) and the dispatch's deadline.
func callContext(ctx context.Context, clock contract.Clock, timeout time.Duration, deadline time.Time) (context.Context, context.CancelFunc) {
	d := clock.Now().Add(timeout)
	if !deadline.IsZero() && deadline.Before(d) {
		d = deadline
	}
	return context.WithDeadline(ctx, d)
}
