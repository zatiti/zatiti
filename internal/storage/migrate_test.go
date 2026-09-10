package storage

import (
	"context"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// migration builds one valid testx migration with a pinned digest.
func migration(version int64, sqlText string) contract.Migration {
	return contract.Migration{
		Owner:   "testx",
		Version: version,
		SQL:     sqlText,
		SHA256:  contract.Hash([]byte(sqlText)),
	}
}

// TestMigrateAppliesAndRecordsMetadata proves a valid migration creates its
// objects and records owner, version and digest.
func TestMigrateAppliesAndRecordsMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)

	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := bumpState(ctx, db, testActor(), testScope(), "m1"); err != nil {
		t.Fatalf("write against migrated schema: %v", err)
	}

	var applied []contract.Migration
	err := db.Read(ctx, testActor(), testScope(), func(u contract.Unit) error {
		rows, err := u.QueryContext(ctx,
			"SELECT owner, version, sha256 FROM storage_migrations ORDER BY owner, version")
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var owner string
			var version int64
			var digest string
			if err := rows.Scan(&owner, &version, &digest); err != nil {
				return err
			}
			applied = append(applied, contract.Migration{Owner: owner, Version: version, SHA256: contract.Digest(digest)})
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("read migration metadata: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("recorded migrations = %d, want 1", len(applied))
	}
	m := counterMigrations()[0]
	if applied[0].Owner != m.Owner || applied[0].Version != m.Version || applied[0].SHA256 != m.SHA256 {
		t.Fatalf("recorded migration = %+v, want owner %s version %d digest %s",
			applied[0], m.Owner, m.Version, m.SHA256)
	}
}

// TestMigrateRerunIsNoop proves re-presenting identical migrations changes
// nothing: no error, no duplicate metadata, schema still usable.
func TestMigrateRerunIsNoop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	migrations := counterMigrations()
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := bumpState(ctx, db, testActor(), testScope(), "seed"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	if got := readStateValue(t, db); got != 1 {
		t.Fatalf("state value after re-migrate = %d, want 1 (data intact)", got)
	}
	if got := countMigrationRows(t, db, "testx"); got != 1 {
		t.Fatalf("metadata rows = %d, want 1", got)
	}
}

// TestMigrateRejectsChangedAppliedMigration proves a migration whose body
// changed after being applied is rejected: the digest on file no longer
// matches the submitted SQL.
func TestMigrateRejectsChangedAppliedMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tampered := counterMigrations()[0]
	tampered.SQL = tampered.SQL + "\n-- editorial change after the fact"
	tampered.SHA256 = contract.Hash([]byte(tampered.SQL))
	err := db.Migrate(ctx, []contract.Migration{tampered})
	if err == nil {
		t.Fatal("expected rejection for a changed applied migration")
	}
	if got := countMigrationRows(t, db, "testx"); got != 1 {
		t.Fatalf("metadata rows = %d, want 1", got)
	}
}

// TestMigrateRejectsDigestMismatch proves a submitted digest that does not
// match its SQL is rejected before any statement runs.
func TestMigrateRejectsDigestMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	m := counterMigrations()[0]
	m.SHA256 = contract.Hash([]byte("different body entirely"))

	err := db.Migrate(ctx, []contract.Migration{m})
	requireFault(t, err, contract.CodeInvalidInput, false)
	if got := countMigrationRows(t, db, "testx"); got != 0 {
		t.Fatalf("metadata rows = %d, want 0", got)
	}
}

// TestMigrateRejectsNonMonotonicVersion proves a version at or below the
// owner's highest applied version is rejected when it is not already applied.
func TestMigrateRejectsNonMonotonicVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	v1 := migration(1, "CREATE TABLE testx_a (id TEXT PRIMARY KEY)")
	v3 := migration(3, "CREATE TABLE testx_b (id TEXT PRIMARY KEY)")
	if err := db.Migrate(ctx, []contract.Migration{v1, v3}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Version 2 is below the owner maximum of 3 and was never applied.
	gap := migration(2, "CREATE TABLE testx_c (id TEXT PRIMARY KEY)")
	err := db.Migrate(ctx, []contract.Migration{gap})
	if err == nil {
		t.Fatal("expected rejection for a non-monotonic version")
	}
	if got := countMigrationRows(t, db, "testx"); got != 2 {
		t.Fatalf("metadata rows = %d, want 2", got)
	}
}

// TestMigrateRejectsReservedAndInvalidOwners proves the owner identifier is
// validated before anything runs.
func TestMigrateRejectsReservedAndInvalidOwners(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)

	cases := []struct {
		name  string
		owner string
	}{
		{"storage prefix", "storage"},
		{"sqlite prefix", "sqlite"},
		{"empty", ""},
		{"uppercase", "Testx"},
		{"digits first", "1testx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := contract.Migration{
				Owner:   tc.owner,
				Version: 1,
				SQL:     "CREATE TABLE " + tc.owner + "_t (id TEXT)",
				SHA256:  contract.Hash([]byte("CREATE TABLE " + tc.owner + "_t (id TEXT)")),
			}
			err := db.Migrate(ctx, []contract.Migration{m})
			if err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	if got := countMigrationRows(t, db, "testx"); got != 0 {
		t.Fatalf("metadata rows = %d, want 0", got)
	}
}

// TestMigrateRejectsForeignNamespaceObjects proves a migration touching
// objects outside its owner namespace is rejected, including qualified
// schema names and storage-owned tables.
func TestMigrateRejectsForeignNamespace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)

	cases := []struct {
		name string
		sql  string
	}{
		{"other owner table", "CREATE TABLE other_state (id TEXT PRIMARY KEY)"},
		{"unprefixed table", "CREATE TABLE loose (id TEXT)"},
		{"storage outbox", "DROP TABLE storage_events"},
		{"migration table", "DELETE FROM storage_migrations"},
		{"qualified name", "CREATE TABLE main.testx_qualified (id TEXT)"},
		{"referenced foreign table", "CREATE TABLE testx_fk (id TEXT REFERENCES other_state(id))"},
		{"index on foreign table", "CREATE INDEX testx_idx ON other_state (id)"},
		{"trigger on foreign table", "CREATE TRIGGER testx_trg AFTER INSERT ON other_state BEGIN SELECT 1; END"},
		{"unsupported statement", "PRAGMA journal_mode = DELETE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := db.Migrate(ctx, []contract.Migration{migration(1, tc.sql)})
			if err == nil {
				t.Fatal("expected namespace rejection")
			}
		})
	}
}

// TestMigrateAllowsNamespaceStatements proves the namespace validator accepts
// the shapes owners legitimately use.
func TestMigrateAllowsNamespaceStatements(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)

	body := `
	CREATE TABLE IF NOT EXISTS testx_items (id TEXT PRIMARY KEY, note TEXT);
	CREATE INDEX IF NOT EXISTS testx_items_note_idx ON testx_items (note);
	CREATE TRIGGER IF NOT EXISTS testx_items_touch
		AFTER UPDATE ON testx_items
		BEGIN UPDATE testx_items SET note = note WHERE id = NEW.id; END;
	INSERT INTO testx_items (id, note) VALUES ('seed', 'first');
	UPDATE testx_items SET note = 'edited' WHERE id = 'seed';
	DELETE FROM testx_items WHERE id = 'missing';
	ALTER TABLE testx_items ADD COLUMN flag INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE testx_items RENAME TO testx_items_renamed;
	DROP TRIGGER IF EXISTS testx_items_touch;
	CREATE TABLE testx_items (id TEXT PRIMARY KEY);
	DROP TABLE IF EXISTS testx_items_renamed;
	`
	if err := db.Migrate(ctx, []contract.Migration{migration(1, body)}); err != nil {
		t.Fatalf("namespace-valid migration rejected: %v", err)
	}
}

// TestMigrateRollsBackFailedApplication proves a migration that fails partway
// leaves no tables, no data and no metadata behind, and the database stays
// usable.
func TestMigrateRollsBackFailedApplication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)
	if err := db.Migrate(ctx, counterMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := bumpState(ctx, db, testActor(), testScope(), "keep"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The second statement in v2 fails: the table from the first statement
	// must not survive the rollback, and neither may the metadata row.
	body := `
	CREATE TABLE testx_good (id TEXT PRIMARY KEY);
	CREATE TABLE testx_state (id INTEGER PRIMARY KEY);
	`
	err := db.Migrate(ctx, []contract.Migration{migration(2, body)})
	if err == nil {
		t.Fatal("expected the duplicate table creation to fail")
	}
	if got := countMigrationRows(t, db, "testx"); got != 1 {
		t.Fatalf("metadata rows = %d, want 1 (only v1)", got)
	}
	if err := db.Read(ctx, testActor(), testScope(), func(u contract.Unit) error {
		var n int
		if err := u.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'testx_good'").Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("testx_good survived a failed migration")
		}
		return nil
	}); err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if got := readStateValue(t, db); got != 1 {
		t.Fatalf("state value = %d, want 1 (existing data intact)", got)
	}

	// The database is still migratable after the failed attempt.
	fixed := migration(2, "CREATE TABLE testx_good (id TEXT PRIMARY KEY)")
	if err := db.Migrate(ctx, []contract.Migration{fixed}); err != nil {
		t.Fatalf("migrate after failure: %v", err)
	}
}

// TestMigrateSeparateOwnersAreIndependent proves per-owner version rules do
// not interfere and both namespaces coexist.
func TestMigrateSeparateOwnersAreIndependent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, 0)

	testxV1 := migration(1, "CREATE TABLE testx_t (id TEXT PRIMARY KEY)")
	alphaV1 := contract.Migration{
		Owner:   "alpha",
		Version: 1,
		SQL:     "CREATE TABLE alpha_t (id TEXT PRIMARY KEY)",
		SHA256:  contract.Hash([]byte("CREATE TABLE alpha_t (id TEXT PRIMARY KEY)")),
	}
	if err := db.Migrate(ctx, []contract.Migration{testxV1, alphaV1}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// The same version number for a different owner is fine.
	if err := db.Migrate(ctx, []contract.Migration{alphaV1, testxV1}); err != nil {
		t.Fatalf("re-migrate both owners: %v", err)
	}
	if got := countMigrationRows(t, db, "testx"); got != 1 {
		t.Fatalf("testx metadata rows = %d, want 1", got)
	}
	if got := countMigrationRows(t, db, "alpha"); got != 1 {
		t.Fatalf("alpha metadata rows = %d, want 1", got)
	}
}

// countMigrationRows returns the number of applied migrations recorded for
// one owner.
func countMigrationRows(t *testing.T, db *database, owner string) int {
	t.Helper()
	var n int
	err := db.Read(context.Background(), testActor(), testScope(), func(u contract.Unit) error {
		return u.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM storage_migrations WHERE owner = ?", owner).Scan(&n)
	})
	if err != nil {
		t.Fatalf("count migration rows: %v", err)
	}
	return n
}
