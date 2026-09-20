package artifacts

import (
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// This file proves the P09 card's required behaviors end to end against
// real Service/storage/blob-store code (never a fake implementation of the
// behavior itself). Step 1: provenance additions and scope-owned
// validation through the frozen ports.

// TestArtifactProvenanceFieldsRoundTrip proves the revision-3 provenance
// additions (Artifact.source_operation_id/purpose) at the storage/wire
// layer. _artifacts.publish's frozen input schema does not yet carry these
// fields for a caller to supply (see doc.go's noted contract gap), so this
// exercises the same insertArtifact path a future internal caller with a
// coordinated schema addition would use, and confirms an ordinary publish
// with no provenance supplied round-trips with both fields cleanly absent.
func TestArtifactProvenanceFieldsRoundTrip(t *testing.T) {
	e := newEnv(t)
	sourceOp := e.ids.New()
	row := &artifactRow{
		ID: e.ids.New(), Version: 1, InstallationID: e.install,
		Scope: e.scope.toContract(), Digest: digestOf([]byte("provenance fixture bytes")),
		Size: 25, MediaType: "text/plain", Classification: "internal",
		Encrypted: true, State: "available", CreatedAt: e.clock.Now(),
		SourceOperationID: sourceOp, Purpose: "context",
	}
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		return insertArtifact(e.ctx, unit, row)
	}); err != nil {
		t.Fatalf("insert provenance fixture: %v", err)
	}

	payload := e.mustOK(opArtifactGet, artifactIDInput{Scope: e.scope, ID: row.ID})
	var out artifactOutput
	e.decode(payload.Data, &out)
	if out.Resource.SourceOperationID != sourceOp {
		t.Fatalf("source_operation_id = %q, want %q", out.Resource.SourceOperationID, sourceOp)
	}
	if out.Resource.Purpose != "context" {
		t.Fatalf("purpose = %q, want %q", out.Resource.Purpose, "context")
	}

	normal, _ := e.publishArtifact(8)
	if normal.SourceOperationID != "" || normal.Purpose != "" {
		t.Fatalf("ordinary publish unexpectedly set provenance: %+v", normal)
	}
	normalGet := e.mustOK(opArtifactGet, artifactIDInput{Scope: e.scope, ID: normal.ID})
	if strings.Contains(string(normalGet.Data), "source_operation_id") || strings.Contains(string(normalGet.Data), "purpose") {
		t.Fatalf("omitempty failed: provenance keys present for an artifact with none: %s", normalGet.Data)
	}
}

// TestArtifactMetadataDeniesForeignTaskArtifact covers half of the required
// behavior "A task cannot bind a foreign artifact...": _artifacts.metadata
// is the exact primitive a binding caller (tasks._tasks.evidence.record)
// uses to validate an output_bindings artifact reference before recording
// it, and it must never disclose an artifact scoped to a different task.
// artifact.get, the direct-fetch equivalent, must refuse the same way.
func TestArtifactMetadataDeniesForeignTaskArtifact(t *testing.T) {
	e := newEnv(t)
	taskAScope := e.scope
	taskAScope.TaskID = e.ids.New()
	published := e.mustOK(opPublish, publishInput{
		Scope: taskAScope, Digest: digestOf([]byte("task a's own output")), Size: 20,
		MediaType: "text/plain", Classification: "internal", Encrypted: true,
	})
	var out artifactOutput
	e.decode(published.Data, &out)

	taskBScope := e.scope
	taskBScope.TaskID = e.ids.New()

	metaPayload := e.mustOK(opMetadata, metadataInput{
		Scope:     taskBScope,
		Artifacts: []wireArtifactRef{{ID: out.Resource.ID, Digest: out.Resource.Digest}},
	})
	var metaOut metadataOutput
	e.decode(metaPayload.Data, &metaOut)
	if len(metaOut.Artifacts) != 0 {
		t.Fatalf("_artifacts.metadata disclosed a foreign task's artifact to task B: %+v", metaOut.Artifacts)
	}

	_ = e.expectQueryFault(opArtifactGet, artifactIDInput{Scope: taskBScope, ID: out.Resource.ID}, contract.CodePermissionDenied)
}
