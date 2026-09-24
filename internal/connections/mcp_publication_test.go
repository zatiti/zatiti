package connections

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestMCPPublicationPreservesRecordedEvidence(t *testing.T) {
	scope := wireScope{InstallationID: "install"}
	digest := contract.Hash([]byte("request"))
	staged := []mcpStagedPublication{{StagingRef: "stage", Digest: digest, Size: 7, MediaType: "application/json", Classification: "restricted"}}
	ref := wireArtifactRef{ID: "artifact", Digest: digest}
	metadata := map[contract.ID]mcpPublishedMetadata{ref.ID: {ID: ref.ID, Digest: digest, Size: 7, MediaType: "application/json", Classification: "restricted", Scope: scope, State: "available"}}
	recorded, _ := json.Marshal(map[string]any{"staged_outputs": staged, "output_artifacts": []any{}, "physical_call": map[string]any{"request_context": map[string]any{"kind": "staged", "staging_ref": "stage", "digest": digest}}, "counter": json.Number("9007199254740993")})
	delivered, _ := json.Marshal(map[string]any{"staged_outputs": []any{}, "output_artifacts": []wireArtifactRef{ref}, "physical_call": map[string]any{"request_context": map[string]any{"kind": "artifact", "artifact": ref}}, "counter": json.Number("9007199254740993")})
	if _, err := reconstructMCPPublication(scope, recorded, delivered, staged, []wireArtifactRef{ref}, []wireArtifactRef{ref}, metadata); err != nil {
		t.Fatal(err)
	}
	t.Run("forged recorded outcome", func(t *testing.T) {
		var changed map[string]json.RawMessage
		if err := json.Unmarshal(delivered, &changed); err != nil {
			t.Fatal(err)
		}
		changed["disposition"] = json.RawMessage(`"succeeded"`)
		forged, _ := json.Marshal(changed)
		if _, err := reconstructMCPPublication(scope, recorded, forged, staged, []wireArtifactRef{ref}, []wireArtifactRef{ref}, metadata); err == nil {
			t.Fatal("accepted extra unrecorded evidence")
		}
	})
	tests := map[string]func(*mcpPublishedMetadata){
		"digest":         func(a *mcpPublishedMetadata) { a.Digest = contract.Hash([]byte("other")) },
		"size":           func(a *mcpPublishedMetadata) { a.Size++ },
		"media":          func(a *mcpPublishedMetadata) { a.MediaType = "text/plain" },
		"classification": func(a *mcpPublishedMetadata) { a.Classification = "public" },
		"scope":          func(a *mcpPublishedMetadata) { a.Scope.InstallationID = "other" },
		"state":          func(a *mcpPublishedMetadata) { a.State = "pending" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			a := metadata[ref.ID]
			mutate(&a)
			if _, err := reconstructMCPPublication(scope, recorded, delivered, staged, []wireArtifactRef{ref}, []wireArtifactRef{ref}, map[contract.ID]mcpPublishedMetadata{ref.ID: a}); err == nil {
				t.Fatal("accepted mismatched artifact metadata")
			}
		})
	}
	t.Run("unpublished", func(t *testing.T) {
		if _, err := reconstructMCPPublication(scope, recorded, delivered, staged, []wireArtifactRef{ref}, []wireArtifactRef{ref}, nil); err == nil {
			t.Fatal("accepted missing artifact")
		}
	})
	t.Run("stale staging locator", func(t *testing.T) {
		if _, err := reconstructMCPPublication(scope, recorded, recorded, staged, []wireArtifactRef{ref}, []wireArtifactRef{ref}, metadata); err == nil {
			t.Fatal("accepted original staging locator after publication")
		}
	})
}
