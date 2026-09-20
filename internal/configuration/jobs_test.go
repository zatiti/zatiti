package configuration

import (
	"io"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// P05 step 4 coverage: organization/team/project export moves off the query
// transaction into the common durable job ledger, registering both export
// job and artifact metadata rather than returning invented ids.

// TestExportJobSurvivesRestartAndReadsThroughTheJobLedger drives the full
// revision-3 export pipeline -- organization.export registers a durable job
// through _execution.job.create rather than inventing one, RunJob stages and
// publishes the canonical bundle outside any transaction, and
// _configuration.export.record durably finalizes the job/artifact linkage --
// then closes and reopens the database to prove the result is still
// inspectable afterward. This owner's own durable table stands in for
// job.get (execution owns the real one) and the blob store's published
// bytes stand in for artifact.read (artifacts owns the real one); both are
// owner-backed lookups, never an id fabricated inside the query transaction.
func TestExportJobSurvivesRestartAndReadsThroughTheJobLedger(t *testing.T) {
	env := newEnv(t)
	orgID, workerID := env.createOrg(&env.org, "durable")

	job := env.exportJob(kindOrganization, orgID)
	if job.State != "pending" || job.ResultArtifact != nil {
		t.Fatalf("export must register a durable pending job, not invent a completed one: %+v", job)
	}
	artifact := env.runExportJob(job)

	// job.get stand-in: this owner's own durable local record, not an
	// in-memory value the handler happened to return.
	var row *exportJobRow
	if err := env.db.Write(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
		var err error
		row, err = fetchExportJobByID(env.ctx, unit, env.install, job.ID)
		return err
	}); err != nil {
		t.Fatalf("fetch export job: %v", err)
	}
	if row == nil || row.State != "succeeded" || row.ArtifactID != artifact.ID || row.ArtifactDigest != string(artifact.Digest) {
		t.Fatalf("export job record not durably finalized: %+v", row)
	}

	// artifact.read stand-in: the blob store, addressed by the digest
	// _configuration.export.record was actually given.
	rc, err := env.blobs.Open(env.ctx, artifact.Digest, 0, -1)
	if err != nil {
		t.Fatalf("open published artifact: %v", err)
	}
	raw, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("read artifact bytes: %v", err)
	}
	var bundle exportBundle
	if err := contract.DecodeStrict(raw, &bundle); err != nil || bundle.Organization == nil || bundle.Organization.ID != orgID {
		t.Fatalf("artifact bytes do not decode to the exported bundle: %v (%s)", err, raw)
	}
	found := false
	for _, w := range bundle.Workers {
		if w.ID == workerID {
			found = true
		}
	}
	if !found {
		t.Fatalf("exported bundle lost worker %s: %+v", workerID, bundle.Workers)
	}

	// Restart: close and reopen the database. Configuration's own migrated
	// schema and its export-job row must survive independent of this
	// process's lifetime.
	if err := env.db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	reopened, err := storage.Open(env.ctx, storage.Config{Path: env.dbPath})
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Migrate(env.ctx, env.svc.Migrations()); err != nil {
		t.Fatalf("re-migrate after restart: %v", err)
	}
	var afterRestart *exportJobRow
	if err := reopened.Write(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
		var err error
		afterRestart, err = fetchExportJobByID(env.ctx, unit, env.install, job.ID)
		return err
	}); err != nil {
		t.Fatalf("fetch export job after restart: %v", err)
	}
	if afterRestart == nil || afterRestart.State != "succeeded" ||
		afterRestart.ArtifactID != artifact.ID || afterRestart.ArtifactDigest != string(artifact.Digest) {
		t.Fatalf("export job record did not survive restart: %+v", afterRestart)
	}
}

// TestExportPrepareAndRecordRouteThroughTheDurableJobLedger exercises the
// internal _configuration.export.prepare/_configuration.export.record pair
// directly: prepare never mints its own job id (it comes back from
// _execution.job.create, the owner-backed lookup), a replayed record with
// the identical artifact is idempotent, a record naming a different
// artifact for an already-finalized job is refused, and a stale
// expected_version or a generation from a different controller epoch is
// refused before any local state changes.
func TestExportPrepareAndRecordRouteThroughTheDurableJobLedger(t *testing.T) {
	env := newEnv(t)
	orgID, _ := env.createOrg(&env.org, "internal-export")

	payload := env.mustAccepted("_configuration.export.prepare", exportPrepareIn{
		Scope: env.scope, Family: kindOrganization, ResourceID: orgID,
	})
	var prepared struct {
		Resource wireJob `json:"resource"`
	}
	env.decode(payload.Data, &prepared)
	if prepared.Resource.State != "pending" || prepared.Resource.ResultArtifact != nil {
		t.Fatalf("export.prepare must not fabricate a completed job: %+v", prepared.Resource)
	}
	calls := env.ports.callsOf("_execution.job.create")
	if len(calls) == 0 {
		t.Fatalf("export.prepare did not register a job through _execution.job.create")
	}
	var created executionJobCreateIn
	env.decode(calls[len(calls)-1].Input, &created)
	if prepared.Resource.ID == "" {
		t.Fatalf("export.prepare returned no job id")
	}
	// The generic seqIDs source assigns monotonically increasing UUIDs; the
	// prepared job's id must be one execution's fake minted, not one this
	// package derived on its own (for example a submission-key echo).
	found := false
	for _, c := range calls {
		var in executionJobCreateIn
		env.decode(c.Input, &in)
		if in.Operation == created.Operation {
			found = true
		}
	}
	if !found {
		t.Fatalf("export.prepare's job id has no matching _execution.job.create call")
	}

	gen := env.generation()
	artifact := wireArtifactRef{ID: env.ids.New(), Digest: contract.Hash([]byte("export-record-test"))}
	recordPayload := env.mustOK("_configuration.export.record", exportRecordIn{
		JobID: prepared.Resource.ID, ExpectedVersion: 1, Generation: gen, Artifact: artifact,
	})
	var recorded struct {
		Resource wireJob `json:"resource"`
	}
	env.decode(recordPayload.Data, &recorded)
	if recorded.Resource.State != "succeeded" || recorded.Resource.ResultArtifact == nil || *recorded.Resource.ResultArtifact != artifact {
		t.Fatalf("export.record did not finalize with the given artifact: %+v", recorded.Resource)
	}

	// Replay with the identical artifact is idempotent.
	replay := env.mustOK("_configuration.export.record", exportRecordIn{
		JobID: prepared.Resource.ID, ExpectedVersion: 1, Generation: gen, Artifact: artifact,
	})
	var replayed struct {
		Resource wireJob `json:"resource"`
	}
	env.decode(replay.Data, &replayed)
	if replayed.Resource.State != "succeeded" || *replayed.Resource.ResultArtifact != artifact {
		t.Fatalf("replaying export.record with the same artifact must stay idempotent: %+v", replayed.Resource)
	}

	// A different artifact for an already-succeeded job is refused, not
	// silently overwritten.
	other := wireArtifactRef{ID: env.ids.New(), Digest: contract.Hash([]byte("a-different-artifact"))}
	_ = env.expectFault("_configuration.export.record", exportRecordIn{
		JobID: prepared.Resource.ID, ExpectedVersion: 1, Generation: gen, Artifact: other,
	}, contract.CodeStaleVersion)

	// A stale expected_version against a still-pending job is refused.
	otherJob := env.exportJob(kindOrganization, orgID)
	_ = env.expectFault("_configuration.export.record", exportRecordIn{
		JobID: otherJob.ID, ExpectedVersion: 2, Generation: gen, Artifact: other,
	}, contract.CodeStaleVersion)

	// A generation from a different controller epoch is refused before any
	// local state changes.
	_ = env.expectFault("_configuration.export.record", exportRecordIn{
		JobID: otherJob.ID, ExpectedVersion: 1, Generation: gen + 1, Artifact: other,
	}, contract.CodeStaleVersion)
}
