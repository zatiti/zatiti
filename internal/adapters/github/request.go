package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/zatiti/zatiti/internal/contract"
)

// maxArtifactTextBytes bounds title/body/commit message text resolved from
// a BlobStore artifact, matching the shared contract's default 1 MiB read
// bound.
const maxArtifactTextBytes = 1 << 20

// ---------- outgoing request bodies (GitHub's documented REST shapes) ----------

type ghCreateRefBody struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

type ghUpdateRefBody struct {
	SHA   string `json:"sha"`
	Force bool   `json:"force"`
}

type ghCreatePullBody struct {
	Title string `json:"title"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Body  string `json:"body"`
	Draft bool   `json:"draft"`
}

type ghMergePullBody struct {
	SHA           string `json:"sha"`
	MergeMethod   string `json:"merge_method"`
	CommitTitle   string `json:"commit_title,omitempty"`
	CommitMessage string `json:"commit_message,omitempty"`
}

// ---------- incoming response shapes (only the fields this adapter uses) ----------

type ghRefResponse struct {
	Ref    string `json:"ref"`
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

type ghPullResponse struct {
	Number  int64  `json:"number"`
	HTMLURL string `json:"html_url"`
	Merged  bool   `json:"merged"`
	State   string `json:"state"`
	Head    struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		SHA string `json:"sha"`
	} `json:"base"`
}

type ghMergeResponse struct {
	SHA     string `json:"sha"`
	Merged  bool   `json:"merged"`
	Message string `json:"message"`
}

type ghErrorResponse struct {
	Message string `json:"message"`
}

// buildReadPath returns the documented GitHub REST path for one
// read_repository resource selector.
func buildReadPath(r *wireReadRepository) string {
	base := fmt.Sprintf("/repos/%s/%s", r.Repository.Owner, r.Repository.Name)
	switch r.Resource {
	case "metadata":
		return base
	case "ref":
		return base + "/git/ref/heads/" + r.Branch
	case "commit":
		return base + "/git/commits/" + r.SHA
	case "tree":
		return base + "/git/trees/" + r.SHA
	case "blob":
		return base + "/git/blobs/" + r.SHA
	case "pull_request":
		return base + "/pulls/" + strconv.FormatInt(r.PullRequestNumber, 10)
	default:
		return base
	}
}

// buildRequest constructs the single documented GitHub REST call for act:
// method, path (relative to the profile's api_base) and an encoded JSON
// body (nil for a GET). Title/body/commit message text is resolved from
// the BlobStore, since GitHub's create/merge endpoints take literal text,
// not artifact references.
func buildRequest(ctx context.Context, blobs contract.BlobStore, act *action) (method, path string, body []byte, err error) {
	switch act.Kind {
	case kindReadRepository:
		return http.MethodGet, buildReadPath(act.ReadRepository), nil, nil

	case kindCreateBranch:
		w := act.CreateBranch
		b, merr := json.Marshal(ghCreateRefBody{Ref: "refs/heads/" + w.Branch, SHA: w.BaseSHA})
		if merr != nil {
			return "", "", nil, internalError("encoding create_branch request body failed: %v", merr)
		}
		return http.MethodPost, fmt.Sprintf("/repos/%s/%s/git/refs", w.Repository.Owner, w.Repository.Name), b, nil

	case kindPushCommit:
		w := act.PushCommit
		b, merr := json.Marshal(ghUpdateRefBody{SHA: w.PreparedCommitSHA, Force: false})
		if merr != nil {
			return "", "", nil, internalError("encoding push_commit request body failed: %v", merr)
		}
		return http.MethodPatch, fmt.Sprintf("/repos/%s/%s/git/refs/heads/%s", w.Repository.Owner, w.Repository.Name, w.Branch), b, nil

	case kindOpenPullRequest:
		w := act.OpenPullRequest
		title, terr := resolveText(ctx, blobs, w.Title)
		if terr != nil {
			return "", "", nil, terr
		}
		prBody, berr := resolveText(ctx, blobs, w.Body)
		if berr != nil {
			return "", "", nil, berr
		}
		b, merr := json.Marshal(ghCreatePullBody{Title: title, Head: w.HeadBranch, Base: w.BaseBranch, Body: prBody, Draft: w.Draft})
		if merr != nil {
			return "", "", nil, internalError("encoding open_pull_request request body failed: %v", merr)
		}
		return http.MethodPost, fmt.Sprintf("/repos/%s/%s/pulls", w.Repository.Owner, w.Repository.Name), b, nil

	case kindMergePullRequest:
		w := act.MergePullRequest
		title, terr := resolveText(ctx, blobs, w.CommitTitle)
		if terr != nil {
			return "", "", nil, terr
		}
		msg, merr2 := resolveText(ctx, blobs, w.CommitBody)
		if merr2 != nil {
			return "", "", nil, merr2
		}
		b, merr := json.Marshal(ghMergePullBody{SHA: w.ExpectedHeadSHA, MergeMethod: w.MergeMethod, CommitTitle: title, CommitMessage: msg})
		if merr != nil {
			return "", "", nil, internalError("encoding merge_pull_request request body failed: %v", merr)
		}
		return http.MethodPut, fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", w.Repository.Owner, w.Repository.Name, w.PullRequestNumber), b, nil

	default:
		return "", "", nil, capabilityUnsupported("github action kind %q is not a qualified v1 capability", act.Kind)
	}
}

// buildReconcileRequest constructs the single bounded read GitHub uses to
// reconcile act's original effect: the same read for read_repository, or
// the state-check GET for a mutation kind. Reconciliation never repeats the
// original mutating call.
func buildReconcileRequest(act *action) (method, path string, err error) {
	switch act.Kind {
	case kindReadRepository:
		return http.MethodGet, buildReadPath(act.ReadRepository), nil
	case kindCreateBranch:
		w := act.CreateBranch
		return http.MethodGet, fmt.Sprintf("/repos/%s/%s/git/ref/heads/%s", w.Repository.Owner, w.Repository.Name, w.Branch), nil
	case kindPushCommit:
		w := act.PushCommit
		return http.MethodGet, fmt.Sprintf("/repos/%s/%s/git/ref/heads/%s", w.Repository.Owner, w.Repository.Name, w.Branch), nil
	case kindOpenPullRequest:
		w := act.OpenPullRequest
		return http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls?head=%s:%s&state=all", w.Repository.Owner, w.Repository.Name, w.Repository.Owner, w.HeadBranch), nil
	case kindMergePullRequest:
		w := act.MergePullRequest
		return http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls/%d", w.Repository.Owner, w.Repository.Name, w.PullRequestNumber), nil
	default:
		return "", "", capabilityUnsupported("github reconcile does not support kind %q", act.Kind)
	}
}

// resolveText reads the full text of an artifact through the BlobStore.
// GitHub's create/merge endpoints take literal title/body/commit-message
// text, not artifact references, so this is the one place the adapter
// dereferences an ArtifactRef into bytes.
func resolveText(ctx context.Context, blobs contract.BlobStore, ref wireArtifactRef) (string, error) {
	if blobs == nil {
		return "", prerequisiteMissing("github adapter requires a blob store dependency to resolve artifact %s", ref.ID)
	}
	rc, err := blobs.Open(ctx, ref.Digest, 0, 0)
	if err != nil {
		return "", blobFault(err)
	}
	defer func() { _ = rc.Close() }()
	data, truncated := readBounded(rc, maxArtifactTextBytes)
	if truncated {
		return "", invalidInput("artifact %s exceeds the %d byte text bound", ref.ID, maxArtifactTextBytes)
	}
	return string(data), nil
}

// readBounded reads at most limit+1 bytes from r, reporting whether the
// data was truncated at limit. It never returns a read error: a partial
// read is still meaningful evidence of what was received.
func readBounded(r io.Reader, limit int64) (data []byte, truncated bool) {
	data, _ = io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(data)) > limit {
		return data[:limit], true
	}
	return data, false
}
