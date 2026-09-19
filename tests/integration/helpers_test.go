package integration_test

import (
	"encoding/base64"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// rootOrganization returns the bootstrap organization and its chief.
func (f *fixture) rootOrganization() (org, chief contract.ID) {
	f.t.Helper()
	res := f.must(f.owner, "organization.list", "", map[string]any{"scope": f.scope()})
	var out struct {
		Items []struct {
			ID      contract.ID `json:"id"`
			ChiefID contract.ID `json:"chief_id"`
		} `json:"items"`
	}
	decode(f.t, res.Data, &out)
	if len(out.Items) != 1 {
		f.t.Fatalf("bootstrap created %d organizations, want exactly the root: %s", len(out.Items), res.Data)
	}
	return out.Items[0].ID, out.Items[0].ChiefID
}

// count returns the number of items a public list operation returns.
func (f *fixture) count(op string, input map[string]any) int {
	f.t.Helper()
	res := f.must(f.owner, op, "", input)
	var out struct {
		Items []any `json:"items"`
	}
	decode(f.t, res.Data, &out)
	return len(out.Items)
}

// eventCount returns the number of events visible to the owner.
func (f *fixture) eventCount() int {
	f.t.Helper()
	return f.count("event.list", map[string]any{"scope": f.scope(), "limit": 500})
}

// eventKinds returns the ordered event kinds visible to the owner.
func (f *fixture) eventKinds() []string {
	f.t.Helper()
	res := f.must(f.owner, "event.list", "", map[string]any{"scope": f.scope(), "limit": 500})
	var out struct {
		Items []struct {
			Kind     string `json:"kind"`
			Sequence int64  `json:"sequence"`
		} `json:"items"`
	}
	decode(f.t, res.Data, &out)
	kinds := make([]string, 0, len(out.Items))
	last := int64(0)
	for _, e := range out.Items {
		if e.Sequence <= last {
			f.t.Fatalf("event sequence %d does not increase after %d", e.Sequence, last)
		}
		last = e.Sequence
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

// syntheticDigest is a well-formed SHA-256 hex digest of nothing real.
var syntheticDigest = strings.Repeat("ab", 32)

// artifactRef names a published artifact by identity and digest.
type artifactRef struct {
	ID     contract.ID `json:"id"`
	Digest string      `json:"digest"`
}

// uploadArtifact publishes body through the real artifact path, the way a
// client does: upload.begin, one chunk, upload.finish.
func (f *fixture) uploadArtifact(label string, body []byte, mediaType string) artifactRef {
	f.t.Helper()
	digest := string(contract.Hash(body))
	begun := f.must(f.owner, "artifact.upload.begin", label+"-begin", map[string]any{
		"scope": f.scope(), "size": len(body), "digest": digest, "media_type": mediaType, "classification": "internal",
	})
	var upload struct {
		Resource struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
		} `json:"resource"`
	}
	decode(f.t, begun.Data, &upload)
	chunk := f.must(f.owner, "artifact.upload.chunk", label+"-chunk", map[string]any{
		"scope": f.scope(), "upload_id": upload.Resource.ID, "offset": 0,
		"bytes_base64": base64.StdEncoding.EncodeToString(body), "chunk_digest": digest,
	})
	decode(f.t, chunk.Data, &upload)
	finished := f.must(f.owner, "artifact.upload.finish", label+"-finish", map[string]any{
		"scope": f.scope(), "upload_id": upload.Resource.ID, "expected_version": upload.Resource.Version,
	})
	var out struct {
		Resource artifactRef `json:"resource"`
	}
	decode(f.t, finished.Data, &out)
	if out.Resource.Digest != digest {
		f.t.Fatalf("published artifact digest %s, uploaded %s", out.Resource.Digest, digest)
	}
	return out.Resource
}

// capabilityEvidence is the verifier profile's capability-evidence
// artifact, published once per fixture through the real artifact path so
// the reference resolves through _artifacts.metadata at task admission.
// This is the real first-task sequence: qualification evidence is uploaded
// before any task can name the verifier that relies on it.
func (f *fixture) capabilityEvidence() artifactRef {
	f.t.Helper()
	f.evidenceOnce.Do(func() {
		f.evidence = f.uploadArtifact("capability-evidence", []byte(
			`{"schema":"zatiti.capability-evidence/v1","verifier":"artifact-contract","version":"1",`+
				`"qualified_checks":["presence"],"note":"synthetic qualification record for the integration fixture"}`),
			"application/json")
	})
	return f.evidence
}

// taskDefinition is a complete task.create definition for the bootstrap
// chief: independent artifact acceptance under a pinned verifier profile
// whose capability evidence is a real published artifact, in the given
// currency.
func (f *fixture) taskDefinition(scope contract.Scope, owner, worker contract.ID, currency string) map[string]any {
	f.t.Helper()
	evidence := f.capabilityEvidence()
	profile := map[string]any{
		"schema": "zatiti.verifier-profile/v1", "kind": "artifact_contract",
		"id": "artifact-contract", "version": "1", "code_digest": syntheticDigest,
		"supported_checks": []string{"presence"}, "max_bytes": 1024, "timeout_seconds": 30,
		"capability_evidence": map[string]any{
			"artifact":        map[string]any{"id": evidence.ID, "digest": evidence.Digest},
			"adapter_version": "1", "source_revision": "r1", "protocol_revision": "p1",
			"profile_digest": syntheticDigest, "qualified_at": "2026-01-01T00:00:00Z",
			"capabilities": []string{}, "limitations": []string{},
		},
	}
	return map[string]any{
		"scope": scope, "owner_id": owner, "worker_id": worker, "outcome": "produce a note",
		"inputs": []any{}, "required_outputs": []string{"note"},
		"acceptance": map[string]any{
			"verifier_id": "artifact-contract", "verifier_version": "1", "sealed_inputs": []any{},
			"expected_observations": []any{map[string]any{
				"check_id": "note-present", "kind": "artifact_presence", "expected": "pass",
				"artifact_name": "note", "expected_digest": syntheticDigest,
			}},
			"mode": "independent", "required_child_ids": []string{}, "profile": profile,
		},
		"limits": map[string]any{
			"currency": currency, "spend_micro_units": 0, "concurrency": 1, "model_steps": 1,
			"child_count": 0, "delegation_depth": 0, "attempt_seconds": 60,
			"root_deadline": "2026-01-06T09:00:00Z",
		},
		"dependencies": []string{},
	}
}

// unconfiguredCurrency is the currency an installation reports before any
// budget is configured; a zero-spend task in it needs no paid admission.
const unconfiguredCurrency = "XXX"

// retainedCommand reads the durable command for a submission key.
type retainedCommand struct {
	ID        contract.ID     `json:"id"`
	Status    string          `json:"status"`
	ErrorCode string          `json:"error_code"`
	Result    contract.Result `json:"result"`
}

func (f *fixture) command(op, key string) (retainedCommand, error) {
	f.t.Helper()
	res, err := f.invoke(f.owner, "command.get", "", map[string]any{
		"scope": f.scope(), "submission_key": key, "operation": op, "operation_version": 1,
	})
	if err != nil {
		return retainedCommand{}, err
	}
	var out struct {
		Resource retainedCommand `json:"resource"`
	}
	decode(f.t, res.Data, &out)
	return out.Resource, nil
}

// principalRow is the public projection of a principal.
type principalRow struct {
	ID    contract.ID    `json:"id"`
	Kind  string         `json:"kind"`
	Name  string         `json:"name"`
	Scope contract.Scope `json:"scope"`
}

// principals lists every principal of the installation.
func (f *fixture) principals() []principalRow {
	f.t.Helper()
	res := f.must(f.owner, "principal.list", "", map[string]any{"scope": f.scope(), "limit": 200})
	var out struct {
		Items []principalRow `json:"items"`
	}
	decode(f.t, res.Data, &out)
	return out.Items
}

// expectPrincipals asserts the installation holds exactly the bootstrap
// pair, the human owner and the controller's scoped service principal
// (internal/identity/service_principal.go), plus the named client agents.
func (f *fixture) expectPrincipals(agents ...string) {
	f.t.Helper()
	want := map[string]string{"Integration Owner": contract.KindHuman, "controller": contract.KindService}
	for _, a := range agents {
		want[a] = contract.KindClientAgent
	}
	rows := f.principals()
	got := map[string]string{}
	for _, r := range rows {
		if r.Scope.InstallationID != f.installationID {
			f.t.Errorf("principal %s %q is scoped to installation %s", r.Kind, r.Name, r.Scope.InstallationID)
		}
		if _, dup := got[r.Name]; dup {
			f.t.Errorf("principal name %q appears twice", r.Name)
		}
		got[r.Name] = r.Kind
	}
	if len(got) != len(want) {
		f.t.Errorf("principals %v, want exactly %v", got, want)
		return
	}
	for name, kind := range want {
		if got[name] != kind {
			f.t.Errorf("principal %q kind %q, want %q (all: %v)", name, got[name], kind, got)
		}
	}
}
