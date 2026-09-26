package connections

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

type mcpStagedPublication struct {
	StagingRef     string          `json:"staging_ref"`
	Digest         contract.Digest `json:"digest"`
	Size           int64           `json:"size"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
}

type mcpPublishedMetadata struct {
	ID             contract.ID     `json:"id"`
	Digest         contract.Digest `json:"digest"`
	Size           int64           `json:"size"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
	Scope          wireScope       `json:"scope"`
	State          string          `json:"state"`
}

// normalizeMCPPublication trusts the recorded observation and artifact owner.
// Delivery supplies only a proposed mapping, never observation truth.
func (s *Service) normalizeMCPPublication(ctx context.Context, u contract.Unit, scope wireScope, recorded, delivered json.RawMessage) (json.RawMessage, error) {
	var original struct {
		Staged    []mcpStagedPublication `json:"staged_outputs"`
		Artifacts []wireArtifactRef      `json:"output_artifacts"`
	}
	if err := json.Unmarshal(recorded, &original); err != nil {
		return nil, err
	}
	if len(original.Staged) == 0 {
		return recorded, nil
	}
	if len(original.Staged) > 20 {
		return nil, permissionDenied("MCP publication exceeds output bound")
	}
	var proposed struct {
		Artifacts []wireArtifactRef `json:"output_artifacts"`
	}
	if err := json.Unmarshal(delivered, &proposed); err != nil {
		return nil, err
	}
	if len(proposed.Artifacts) != len(original.Artifacts)+len(original.Staged) {
		return nil, permissionDenied("MCP publication mapping count mismatch")
	}
	for i, ref := range original.Artifacts {
		if proposed.Artifacts[i] != ref {
			return nil, permissionDenied("MCP publication changed existing artifact")
		}
	}
	refs := proposed.Artifacts[len(original.Artifacts):]
	input, err := json.Marshal(map[string]any{"scope": scope, "artifacts": refs})
	if err != nil {
		return nil, err
	}
	payload, err := s.ports.Call(ctx, u, contract.Invocation{Operation: "_artifacts.metadata", Version: 1, Input: input})
	if err != nil {
		return nil, err
	}
	var out struct {
		Artifacts []mcpPublishedMetadata `json:"artifacts"`
	}
	if err = json.Unmarshal(payload.Data, &out); err != nil {
		return nil, err
	}
	metadata := map[contract.ID]mcpPublishedMetadata{}
	for _, a := range out.Artifacts {
		if _, exists := metadata[a.ID]; exists {
			return nil, permissionDenied("ambiguous MCP artifact metadata")
		}
		metadata[a.ID] = a
	}
	return reconstructMCPPublication(scope, recorded, delivered, original.Staged, proposed.Artifacts, refs, metadata)
}

func reconstructMCPPublication(scope wireScope, recorded, delivered json.RawMessage, staged []mcpStagedPublication, artifacts, refs []wireArtifactRef, metadata map[contract.ID]mcpPublishedMetadata) (json.RawMessage, error) {
	mapping := map[string]wireArtifactRef{}
	for i, st := range staged {
		ref := refs[i]
		a, ok := metadata[ref.ID]
		if !ok || st.StagingRef == "" || ref.Digest != st.Digest || a.Digest != st.Digest || a.Size != st.Size || a.MediaType != st.MediaType || a.Classification != st.Classification || a.Scope != scope || a.State != "available" {
			return nil, permissionDenied("MCP publication artifact metadata mismatch")
		}
		if _, exists := mapping[st.StagingRef]; exists {
			return nil, permissionDenied("duplicate MCP staged output")
		}
		mapping[st.StagingRef] = ref
	}
	dec := json.NewDecoder(bytes.NewReader(recorded))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	var walk func(any) (any, error)
	walk = func(value any) (any, error) {
		switch v := value.(type) {
		case map[string]any:
			if v["kind"] == "staged" {
				ref, ok := mapping[stringValue(v["staging_ref"])]
				if !ok || string(ref.Digest) != stringValue(v["digest"]) {
					return nil, permissionDenied("unmapped MCP staged locator")
				}
				return map[string]any{"kind": "artifact", "artifact": ref}, nil
			}
			for k, x := range v {
				next, err := walk(x)
				if err != nil {
					return nil, err
				}
				v[k] = next
			}
		case []any:
			for i, x := range v {
				next, err := walk(x)
				if err != nil {
					return nil, err
				}
				v[i] = next
			}
		}
		return value, nil
	}
	for key, value := range root {
		if key == "staged_outputs" {
			continue
		}
		next, err := walk(value)
		if err != nil {
			return nil, err
		}
		root[key] = next
	}
	root["staged_outputs"] = []any{}
	root["output_artifacts"] = artifacts
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	canonical, err := contract.Canonicalize(encoded)
	if err != nil {
		return nil, err
	}
	supplied, err := contract.Canonicalize(delivered)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(canonical, supplied) {
		return nil, permissionDenied("MCP delivery altered recorded evidence")
	}
	return encoded, nil
}

func stringValue(v any) string { s, _ := v.(string); return s }
