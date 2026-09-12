package artifacts

import (
	"encoding/base64"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestReadReturnsBoundedRange(t *testing.T) {
	e := newEnv(t)
	a, data := e.publishArtifact(300)

	payload, err := e.readArtifact(a.ID, 10, 50)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if payload.Status != contract.StatusCompleted {
		t.Fatalf("read status %q: %v", payload.Status, payload.Error)
	}
	var out readOutput
	e.decode(payload.Data, &out)
	got, derr := base64.StdEncoding.DecodeString(out.BytesB64)
	if derr != nil {
		t.Fatalf("decode returned bytes: %v", derr)
	}
	want := data[10:60]
	if string(got) != string(want) {
		t.Fatalf("read returned wrong bytes")
	}
	if out.TotalSize != int64(len(data)) {
		t.Fatalf("total_size = %d, want %d", out.TotalSize, len(data))
	}
	if out.Digest != a.Digest {
		t.Fatalf("digest mismatch")
	}
}

func TestReadClampsLengthPastEndOfArtifact(t *testing.T) {
	e := newEnv(t)
	a, data := e.publishArtifact(20)

	payload, err := e.readArtifact(a.ID, 15, 1000)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	var out readOutput
	e.decode(payload.Data, &out)
	got, _ := base64.StdEncoding.DecodeString(out.BytesB64)
	if len(got) != 5 {
		t.Fatalf("clamped read returned %d bytes, want 5", len(got))
	}
	if string(got) != string(data[15:]) {
		t.Fatalf("clamped read returned wrong bytes")
	}
}

func TestReadOffsetPastEndIsInvalid(t *testing.T) {
	e := newEnv(t)
	a, _ := e.publishArtifact(10)
	payload, err := e.readArtifact(a.ID, 11, 1)
	_ = e.assertFault(opArtifactRead, payload, err, contract.CodeInvalidInput)
}

func TestReadDeniesCrossProjectScope(t *testing.T) {
	e := newEnv(t)
	scoped := e.scope
	scoped.ProjectID = e.ids.New()
	payload := e.mustOK(opPublish, publishInput{
		Scope: scoped, Digest: digestOf([]byte("project scoped")), Size: 14,
		MediaType: "text/plain", Classification: "internal", Encrypted: true,
	})
	var out artifactOutput
	e.decode(payload.Data, &out)

	prev := e.scope
	e.scope.ProjectID = e.ids.New()
	defer func() { e.scope = prev }()

	readPayload, err := e.driveLocalIO(opArtifactRead, readInput{
		Scope: e.scope, ID: out.Resource.ID, Offset: 0, Length: 4,
	}, false)
	_ = e.assertFault(opArtifactRead, readPayload, err, contract.CodePermissionDenied)
}

// TestReadMissingBytesYieldsArtifactFault covers Z14.missing_artifact:
// metadata references a committed content hash whose bytes are missing or
// corrupt. Read must return a visible artifact_fault, never fabricate bytes.
func TestReadMissingBytesYieldsArtifactFault(t *testing.T) {
	e := newEnv(t)
	a, _ := e.publishArtifact(64)
	e.blobs.markCorrupt(a.Digest)

	payload, err := e.readArtifact(a.ID, 0, 10)
	_ = e.assertFault(opArtifactRead, payload, err, contract.CodeArtifactFault)
}

// TestReadTamperedBytesYieldsArtifactFault is the encrypted-tamper local
// proving focus: bytes that were altered underneath committed metadata must
// never be silently disclosed.
func TestReadTamperedBytesYieldsArtifactFault(t *testing.T) {
	e := newEnv(t)
	a := e.uploadFull([]byte("bytes that will be tampered with after commit"), 16)
	e.blobs.markCorrupt(a.Digest)

	payload, err := e.readArtifact(a.ID, 0, 5)
	_ = e.assertFault(opArtifactRead, payload, err, contract.CodeArtifactFault)
}

func TestExportRegistersDurableJob(t *testing.T) {
	e := newEnv(t)
	a, _ := e.publishArtifact(8)

	payload := e.mustLocalOK(opArtifactExport, exportInput{Scope: e.scope, ID: a.ID}, true)
	if payload.Status != contract.StatusAccepted {
		t.Fatalf("export status = %q, want accepted", payload.Status)
	}
	var out jobOutput
	e.decode(payload.Data, &out)
	if out.Resource.Owner != ownerName || out.Resource.Operation != opArtifactExport {
		t.Fatalf("export job = %+v, want owner/operation set to artifacts/artifact.export", out.Resource)
	}
	calls := e.ports.callsOf(peerExecutionJobCreate)
	if len(calls) != 1 {
		t.Fatalf("expected exactly one _execution.job.create call, got %d", len(calls))
	}
}

func TestExportPropagatesPeerFault(t *testing.T) {
	e := newEnv(t)
	a, _ := e.publishArtifact(8)
	e.ports.fail = &contract.Fault{Code: contract.CodeControllerUnavailable, Message: "execution unavailable", Retryable: true}

	payload, err := e.driveLocalIO(opArtifactExport, exportInput{Scope: e.scope, ID: a.ID}, true)
	_ = e.assertFault(opArtifactExport, payload, err, contract.CodeControllerUnavailable)
}

// TestExportDetectsAndRecordsCorruptBytes covers the "encrypted tamper" /
// missing bytes local proving focus from the export side: export
// independently reverifies the source artifact's bytes before letting its
// job stand, and a detected corruption durably transitions the artifact to
// the fault state and records the observation rather than certifying a
// broken export.
func TestExportDetectsAndRecordsCorruptBytes(t *testing.T) {
	e := newEnv(t)
	a, _ := e.publishArtifact(8)
	e.blobs.markCorrupt(a.Digest)

	payload, err := e.driveLocalIO(opArtifactExport, exportInput{Scope: e.scope, ID: a.ID}, true)
	_ = e.assertFault(opArtifactExport, payload, err, contract.CodeArtifactFault)

	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		row, lerr := loadArtifact(e.ctx, unit, a.ID)
		if lerr != nil {
			return lerr
		}
		if row.State != "fault" {
			t.Fatalf("artifact state = %q after a detected corruption, want fault", row.State)
		}
		var count int
		frow := unit.QueryRowContext(e.ctx, `SELECT COUNT(1) FROM artifacts_faults WHERE artifact_id = ?`, a.ID)
		if serr := frow.Scan(&count); serr != nil {
			return serr
		}
		if count == 0 {
			t.Fatalf("no durable fault record for artifact %s", a.ID)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify fault state: %v", err)
	}
}

// TestExportRefusesAlreadyFaultedArtifact covers the case where the
// artifact was already marked fault before export is attempted: Prepare
// authorizes on metadata state and never proceeds to Perform at all.
func TestExportRefusesAlreadyFaultedArtifact(t *testing.T) {
	e := newEnv(t)
	a, _ := e.publishArtifact(8)
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		row, lerr := loadArtifact(e.ctx, unit, a.ID)
		if lerr != nil {
			return lerr
		}
		return e.svc.markArtifactFault(e.ctx, unit, row, contract.CodeArtifactFault, "pre-existing test fault")
	}); err != nil {
		t.Fatalf("mark fault: %v", err)
	}
	payload, err := e.driveLocalIO(opArtifactExport, exportInput{Scope: e.scope, ID: a.ID}, true)
	_ = e.assertFault(opArtifactExport, payload, err, contract.CodeArtifactFault)
}
