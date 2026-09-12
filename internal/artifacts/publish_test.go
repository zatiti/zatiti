package artifacts

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestPublishAndGet(t *testing.T) {
	e := newEnv(t)
	a, _ := e.publishArtifact(128)
	if a.State != "available" {
		t.Fatalf("published artifact state = %q, want available", a.State)
	}
	if !a.Encrypted {
		t.Fatalf("published artifact must be encrypted")
	}

	payload := e.mustOK(opArtifactGet, artifactIDInput{Scope: e.scope, ID: a.ID})
	var out artifactOutput
	e.decode(payload.Data, &out)
	if out.Resource.Digest != a.Digest {
		t.Fatalf("get digest = %q, want %q", out.Resource.Digest, a.Digest)
	}
}

func TestPublishRejectsUnencrypted(t *testing.T) {
	e := newEnv(t)
	_ = e.expectFault(opPublish, publishInput{
		Scope: e.scope, Digest: digestOf([]byte("x")), Size: 1,
		MediaType: "text/plain", Classification: "internal", Encrypted: false,
	}, contract.CodeInvalidInput)
}

func TestPublishCreatesRetentionPin(t *testing.T) {
	e := newEnv(t)
	a, _ := e.publishArtifact(64)
	var count int
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		row := unit.QueryRowContext(e.ctx, `SELECT COUNT(1) FROM artifacts_pins WHERE artifact_id = ?`, a.ID)
		return row.Scan(&count)
	}); err != nil {
		t.Fatalf("read pins: %v", err)
	}
	if count == 0 {
		t.Fatalf("published artifact %s has no retention pin", a.ID)
	}
}

func TestArtifactGetDeniesCrossOrganizationScope(t *testing.T) {
	e := newEnv(t)
	orgScope := e.scope
	orgScope.OrganizationID = e.ids.New()
	payload := e.mustOK(opPublish, publishInput{
		Scope: orgScope, Digest: digestOf([]byte("scoped")), Size: 6,
		MediaType: "text/plain", Classification: "internal", Encrypted: true,
	})
	var out artifactOutput
	e.decode(payload.Data, &out)

	otherOrgScope := e.scope
	otherOrgScope.OrganizationID = e.ids.New()
	f := e.expectQueryFault(opArtifactGet, artifactIDInput{Scope: otherOrgScope, ID: out.Resource.ID}, contract.CodePermissionDenied)
	if f.Code != contract.CodePermissionDenied {
		t.Fatalf("unexpected fault code %s", f.Code)
	}
}

func TestArtifactMetadataFiltersUnresolvedAndMismatchedDigest(t *testing.T) {
	e := newEnv(t)
	a, _ := e.publishArtifact(32)

	payload := e.mustOK(opMetadata, metadataInput{
		Scope: e.scope,
		Artifacts: []wireArtifactRef{
			{ID: a.ID, Digest: a.Digest},
			{ID: a.ID, Digest: digestOf([]byte("wrong"))}, // mismatched digest, deduped by ID anyway
			{ID: e.ids.New(), Digest: a.Digest},           // unknown id
		},
	})
	var out metadataOutput
	e.decode(payload.Data, &out)
	if len(out.Artifacts) != 1 {
		t.Fatalf("metadata returned %d artifacts, want 1", len(out.Artifacts))
	}
}

func TestArtifactListPaginatesAndRejectsUnsupportedFilter(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 3; i++ {
		e.clock.advance(time.Second)
		e.publishArtifact(16)
	}

	payload := e.mustOK(opArtifactList, artifactListInput{Scope: e.scope, Limit: ptrInt64(2)})
	var page1 artifactListOutput
	e.decode(payload.Data, &page1)
	if len(page1.Items) != 2 {
		t.Fatalf("page1 items = %d, want 2", len(page1.Items))
	}
	if payload.NextCursor == nil {
		t.Fatalf("expected a next cursor")
	}

	payload2 := e.mustOK(opArtifactList, artifactListInput{Scope: e.scope, Limit: ptrInt64(2), Cursor: payload.NextCursor})
	var page2 artifactListOutput
	e.decode(payload2.Data, &page2)
	if len(page2.Items) != 1 {
		t.Fatalf("page2 items = %d, want 1", len(page2.Items))
	}

	key := "x"
	_ = e.expectFault(opArtifactList, artifactListInput{
		Scope: e.scope, Filter: &artifactListFilter{Key: &key},
	}, contract.CodeInvalidInput)
}

func ptrInt64(v int64) *int64 { return &v }
