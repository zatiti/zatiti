package storage

import (
	"context"
	"database/sql"

	"github.com/zatiti/zatiti/internal/contract"
)

// SchemaVersion is one owner's currently applied migration: the version and
// the SHA-256 pin of its body, in the same shape as a backup manifest's
// database_schema_versions entries (docs/implementation/adapter-schemas.json
// BackupManifest). It is storage's typed contribution to backup inventory
// and to a restore's schema-compatibility check -- the P00-011 six-step
// restore protocol's step 2 -- so a caller never needs raw SQL against
// storage_migrations to build or verify one.
type SchemaVersion struct {
	Owner           string
	Version         int64
	MigrationDigest contract.Digest
}

// SchemaVersions returns every owner's currently applied migration version
// and digest, ordered by owner then version. Installation composes this into
// a backup manifest's database_schema_versions (contract.SnapshotInventory,
// entrypoint-supplied, P00-011) without querying storage_migrations
// directly; PrepareRestore compares an imported image's own schema versions
// against a manifest's claim the same way.
func (d *database) SchemaVersions(ctx context.Context) ([]SchemaVersion, error) {
	if d.closed.Load() {
		return nil, closedFault()
	}
	return querySchemaVersions(ctx, d.db)
}

// schemaVersionReader is the subset of query execution SchemaVersions and
// staged-image inspection need; *sql.DB satisfies it.
type schemaVersionReader interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// querySchemaVersions reads storage_migrations through q, which may be the
// live database or a throwaway connection opened against a staged restore
// image.
func querySchemaVersions(ctx context.Context, q schemaVersionReader) ([]SchemaVersion, error) {
	rows, err := q.QueryContext(ctx, "SELECT owner, version, sha256 FROM storage_migrations ORDER BY owner, version")
	if err != nil {
		return nil, storageFault("query schema versions", err)
	}
	defer func() { _ = rows.Close() }()

	var out []SchemaVersion
	for rows.Next() {
		var v SchemaVersion
		if err := rows.Scan(&v.Owner, &v.Version, &v.MigrationDigest); err != nil {
			return nil, storageFault("scan schema version", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, storageFault("read schema versions", err)
	}
	return out, nil
}

// equalSchemaVersions reports whether two schema version sets contain
// exactly the same (owner, version, digest) triples, order independent. A
// restore refuses on anything but an exact match: a manifest that
// undersells or oversells what a staged image actually contains is treated
// as untrustworthy, not merely incomplete.
func equalSchemaVersions(a, b []SchemaVersion) bool {
	if len(a) != len(b) {
		return false
	}
	count := make(map[SchemaVersion]int, len(a))
	for _, v := range a {
		count[v]++
	}
	for _, v := range b {
		count[v]--
		if count[v] < 0 {
			return false
		}
	}
	return true
}
