package responses

import (
	"context"
	"encoding/json"
	"io"

	"github.com/zatiti/zatiti/internal/contract"
)

// maxContextBytes bounds the persisted request context this adapter will
// load into memory for one model step. It narrows the shared 256 MiB
// artifact limit; a context beyond it is refused before any call.
const maxContextBytes = 32 << 20

// Context part kinds of the frozen ContextPart union.
const (
	partText          = "text"
	partArtifact      = "artifact"
	partToolCall      = "tool_call"
	partToolResult    = "tool_result"
	partMemoryExcerpt = "memory_excerpt"
)

// maxPartBytes bounds one referenced artifact (an artifact part or a tool
// result) loaded for the model step; the whole context, references
// included, stays within maxContextBytes.
const maxPartBytes = 4 << 20

// contextPart is one decoded ContextPart. Exactly one typed field is
// non-nil, matching Kind. Bytes holds the referenced artifact's content
// for artifact and tool_result parts, loaded and digest-verified by
// loadContext, so a wire protocol never performs I/O.
type contextPart struct {
	Kind          string
	Text          *wireContextText
	Artifact      *wireContextArtifactPart
	ToolCall      *wireContextToolCall
	ToolResult    *wireContextToolResult
	MemoryExcerpt *wireContextMemoryExcerpt
	Bytes         []byte
}

// contextMessage is one model-visible message with its parts decoded in
// their exact persisted order.
type contextMessage struct {
	wireContextMessage
	DecodedParts []contextPart
}

// contextDocument is the loaded, verified, decoded request context of one
// model step: what a wire protocol translates into the upstream request.
type contextDocument struct {
	Ref      wireArtifactRef
	Document wireContextArtifact
	Messages []contextMessage
	// Classification is the most restrictive classification the context
	// declares, never below internal ("repository and task content default
	// to internal classification"). Everything the model derives from this
	// context is staged under it.
	Classification string
}

// classificationRank orders the frozen Classification enum.
var classificationRank = map[string]int{"public": 0, "internal": 1, "restricted": 2}

// loadContext reads the action's context_artifact through the BlobStore,
// verifies the bytes hash to the referenced digest (the action, and any
// review bound to it, names these exact bytes), validates them against
// zatiti.context/v1 and decodes every part. It then applies the rules the
// model step depends on: complete capture, tools drawn only from the
// action's pinned tool closure, and disclosure only of classifications the
// profile permits for this provider.
func loadContext(ctx context.Context, blobs contract.BlobStore, profile *responsesProfile, act *wireResponsesParameters) (*contextDocument, error) {
	if blobs == nil {
		return nil, prerequisiteMissing("responses adapter requires a blob store dependency to load context artifact %s", act.ContextArtifact.ID)
	}
	raw, err := readArtifact(ctx, blobs, act.ContextArtifact, maxContextBytes, "the action")
	if err != nil {
		return nil, err
	}
	budget := int64(maxContextBytes - len(raw))

	schema, err := contextSchema()
	if err != nil {
		return nil, internalError("responses context schema composition failed: %v", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, invalidInput("context artifact %s does not match the zatiti.context/v1 schema: %v", act.ContextArtifact.ID, err)
	}
	var w wireContextArtifact
	if err := contract.DecodeStrict(raw, &w); err != nil {
		return nil, invalidInput("context artifact %s decode failed: %v", act.ContextArtifact.ID, err)
	}

	if w.Capture != "complete" {
		return nil, invalidInput("context artifact %s has capture %q; a hosted model step requires complete capture", act.ContextArtifact.ID, w.Capture)
	}

	pinned := make(map[wireVersionRef]bool, len(act.ToolContractVersions))
	for _, ref := range act.ToolContractVersions {
		pinned[ref] = true
	}
	names := make(map[string]bool, len(w.Tools))
	for _, tool := range w.Tools {
		if !pinned[tool.Tool] {
			return nil, invalidInput("context tool %q (%s version %d) is not in the action's tool_contract_versions", tool.Name, tool.Tool.ID, tool.Tool.Version)
		}
		if names[tool.Name] {
			return nil, invalidInput("context declares tool name %q more than once", tool.Name)
		}
		names[tool.Name] = true
	}

	doc := &contextDocument{Ref: act.ContextArtifact, Document: w, Classification: "internal"}
	for _, msg := range w.Messages {
		decoded := contextMessage{wireContextMessage: msg, DecodedParts: make([]contextPart, 0, len(msg.Parts))}
		for _, rawPart := range msg.Parts {
			part, err := decodeContextPart(rawPart)
			if err != nil {
				return nil, err
			}
			var ref *wireArtifactRef
			if part.Artifact != nil {
				c := part.Artifact.Classification
				if !profile.Classifications[c] {
					return nil, permissionDenied("context discloses %s-classified artifact %s, which the profile's enforcement.classifications does not permit for this provider", c, part.Artifact.Artifact.ID)
				}
				if classificationRank[c] > classificationRank[doc.Classification] {
					doc.Classification = c
				}
				ref = &part.Artifact.Artifact
			}
			if part.ToolResult != nil {
				ref = &part.ToolResult.Artifact
			}
			if ref != nil {
				bound := min(int64(maxPartBytes), budget)
				data, err := readArtifact(ctx, blobs, *ref, bound, "the context")
				if err != nil {
					return nil, err
				}
				budget -= int64(len(data))
				part.Bytes = data
			}
			decoded.DecodedParts = append(decoded.DecodedParts, part)
		}
		doc.Messages = append(doc.Messages, decoded)
	}
	return doc, nil
}

// readArtifact reads one referenced artifact through the BlobStore, bounded
// to limit bytes, and verifies the bytes hash to the digest the reference
// binds: the model step discloses exactly the content the action names.
func readArtifact(ctx context.Context, blobs contract.BlobStore, ref wireArtifactRef, limit int64, binder string) ([]byte, error) {
	rc, err := blobs.Open(ctx, ref.Digest, 0, 0)
	if err != nil {
		return nil, blobFault(err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(rc, limit+1))
	_ = rc.Close()
	if readErr != nil {
		return nil, artifactFault("reading artifact %s failed", ref.ID)
	}
	if int64(len(raw)) > limit {
		return nil, invalidInput("artifact %s exceeds the %d byte bound for a model step", ref.ID, limit)
	}
	if got := contract.Hash(raw); got != ref.Digest {
		return nil, artifactFault("artifact %s bytes hash to %s, not the digest %s binds", ref.ID, got, binder)
	}
	return raw, nil
}

// decodeContextPart strict-decodes one schema-validated ContextPart into
// the variant its "kind" discriminator names.
func decodeContextPart(raw json.RawMessage) (contextPart, error) {
	var peek struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return contextPart{}, invalidInput("context part kind could not be read: %v", err)
	}
	part := contextPart{Kind: peek.Kind}
	var target any
	switch peek.Kind {
	case partText:
		part.Text = &wireContextText{}
		target = part.Text
	case partArtifact:
		part.Artifact = &wireContextArtifactPart{}
		target = part.Artifact
	case partToolCall:
		part.ToolCall = &wireContextToolCall{}
		target = part.ToolCall
	case partToolResult:
		part.ToolResult = &wireContextToolResult{}
		target = part.ToolResult
	case partMemoryExcerpt:
		part.MemoryExcerpt = &wireContextMemoryExcerpt{}
		target = part.MemoryExcerpt
	default:
		return contextPart{}, invalidInput("context part kind %q is not part of zatiti.context/v1", peek.Kind)
	}
	if err := contract.DecodeStrict(raw, target); err != nil {
		return contextPart{}, invalidInput("context %s part decode failed: %v", peek.Kind, err)
	}
	return part, nil
}
