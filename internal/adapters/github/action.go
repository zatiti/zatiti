package github

import (
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Action kinds qualified for v1. These are the only values the frozen
// zatiti.github.action/v1 oneOf schema accepts.
const (
	kindReadRepository   = "read_repository"
	kindCreateBranch     = "create_branch"
	kindPushCommit       = "push_commit"
	kindOpenPullRequest  = "open_pull_request"
	kindMergePullRequest = "merge_pull_request"
)

// action is the decoded, kind-discriminated Dispatch.Action. Exactly one of
// the typed fields is non-nil, matching Kind.
type action struct {
	Kind             string
	Repository       wireRepository
	ReadRepository   *wireReadRepository
	CreateBranch     *wireCreateBranch
	PushCommit       *wirePushCommit
	OpenPullRequest  *wireOpenPullRequest
	MergePullRequest *wireMergePullRequest
}

// decodeAction validates raw against the composed zatiti.github.action/v1
// oneOf schema, strict-decodes it into the variant named by its "kind"
// discriminator, and runs the Go-level validation the frozen JSON Schema
// cannot express: git-aware branch syntax, and read_repository's
// per-resource selector requirements ("branch for ref, sha for
// commit/tree/blob, and pull_request_number for pull_request; irrelevant
// resource selectors are rejected").
func decodeAction(raw json.RawMessage) (*action, error) {
	schema, err := parametersSchema()
	if err != nil {
		return nil, internalError("github parameters schema composition failed: %v", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, invalidInput("github action does not match the zatiti.github.action/v1 schema: %v", err)
	}

	var peek struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return nil, invalidInput("github action kind could not be read: %v", err)
	}

	act := &action{Kind: peek.Kind}
	switch peek.Kind {
	case kindReadRepository:
		var w wireReadRepository
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("github read_repository action decode failed: %v", err)
		}
		if err := validateReadRepository(w); err != nil {
			return nil, invalidInput("github read_repository action invalid: %v", err)
		}
		act.Repository = w.Repository
		act.ReadRepository = &w
	case kindCreateBranch:
		var w wireCreateBranch
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("github create_branch action decode failed: %v", err)
		}
		if err := validGitBranch(w.Branch); err != nil {
			return nil, invalidInput("github create_branch branch invalid: %v", err)
		}
		act.Repository = w.Repository
		act.CreateBranch = &w
	case kindPushCommit:
		var w wirePushCommit
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("github push_commit action decode failed: %v", err)
		}
		if err := validGitBranch(w.Branch); err != nil {
			return nil, invalidInput("github push_commit branch invalid: %v", err)
		}
		act.Repository = w.Repository
		act.PushCommit = &w
	case kindOpenPullRequest:
		var w wireOpenPullRequest
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("github open_pull_request action decode failed: %v", err)
		}
		if err := validGitBranch(w.HeadBranch); err != nil {
			return nil, invalidInput("github open_pull_request head_branch invalid: %v", err)
		}
		if err := validGitBranch(w.BaseBranch); err != nil {
			return nil, invalidInput("github open_pull_request base_branch invalid: %v", err)
		}
		act.Repository = w.Repository
		act.OpenPullRequest = &w
	case kindMergePullRequest:
		var w wireMergePullRequest
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("github merge_pull_request action decode failed: %v", err)
		}
		act.Repository = w.Repository
		act.MergePullRequest = &w
	default:
		// The oneOf schema already rejected any kind outside the five
		// qualified forms above; this is unreachable defense in depth.
		return nil, capabilityUnsupported("github action kind %q is not a qualified v1 capability", peek.Kind)
	}
	return act, nil
}

// validateReadRepository enforces the per-resource selector requirements
// the frozen schema leaves to Go validation: exactly the selector the
// resource needs must be present, path is unused by every v1 resource, and
// every other selector must be absent.
func validateReadRepository(w wireReadRepository) error {
	if w.Path != "" {
		return fmt.Errorf("resource %q does not use the path selector in v1", w.Resource)
	}

	type selector struct {
		name    string
		present bool
	}
	selectors := []selector{
		{"branch", w.Branch != ""},
		{"sha", w.SHA != ""},
		{"pull_request_number", w.PullRequestNumber != 0},
	}

	var need string
	switch w.Resource {
	case "metadata":
		need = ""
	case "ref":
		need = "branch"
	case "commit", "tree", "blob":
		need = "sha"
	case "pull_request":
		need = "pull_request_number"
	default:
		return fmt.Errorf("unsupported resource %q", w.Resource)
	}

	for _, s := range selectors {
		if s.present && s.name != need {
			return fmt.Errorf("resource %q does not use the %s selector", w.Resource, s.name)
		}
	}
	if need != "" {
		var have bool
		for _, s := range selectors {
			if s.name == need {
				have = s.present
			}
		}
		if !have {
			return fmt.Errorf("resource %q requires %s", w.Resource, need)
		}
	}
	if need == "branch" {
		if err := validGitBranch(w.Branch); err != nil {
			return fmt.Errorf("branch invalid: %w", err)
		}
	}
	return nil
}
