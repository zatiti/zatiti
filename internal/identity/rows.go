package identity

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Row structs mirror the identity_-prefixed tables. Scope dimensions are
// stored both as columns (for filtered reads and containment checks) and as
// a canonical scope JSON snapshot (for exact wire output).

type principalRow struct {
	ID        contract.ID
	Version   int64
	Kind      string
	Name      string
	Scope     contract.Scope
	Revoked   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type grantRow struct {
	ID            contract.ID
	Version       int64
	PrincipalID   contract.ID
	Scope         contract.Scope
	Capabilities  []string
	Destinations  []string
	Denied        bool
	ExpiresAt     *time.Time
	ParentGrantID *contract.ID
	Revoked       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type credentialRow struct {
	ID          contract.ID
	Version     int64
	PrincipalID contract.ID
	StoreRef    string
	ExpiresAt   *time.Time
	Revoked     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type restrictionRow struct {
	ID          contract.ID
	Version     int64
	PrincipalID contract.ID
	Capability  string
	Reason      string
	Active      bool
	CreatedAt   time.Time
}

// encodeScope marshals a scope for the scope_json column.
func encodeScope(s contract.Scope) (string, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("identity: encode scope: %w", err)
	}
	return string(raw), nil
}

// decodeScope parses a stored scope snapshot.
func decodeScope(raw string) (contract.Scope, error) {
	var s contract.Scope
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return contract.Scope{}, fmt.Errorf("identity: decode stored scope: %w", err)
	}
	return s, nil
}

func encodeStrings(v []string) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("identity: encode string list: %w", err)
	}
	return string(raw), nil
}

func decodeStrings(raw string) ([]string, error) {
	var v []string
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("identity: decode stored string list: %w", err)
	}
	if v == nil {
		v = []string{}
	}
	return v, nil
}

// scanPrincipal reads one identity_principals row.
func scanPrincipal(scan func(dest ...any) error) (principalRow, error) {
	var (
		r                    principalRow
		scopeJSON            string
		revokedInt           int64
		createdAt, updatedAt string
	)
	if err := scan(&r.ID, &r.Version, &r.Kind, &r.Name, &scopeJSON, &revokedInt, &createdAt, &updatedAt); err != nil {
		return principalRow{}, err
	}
	scope, err := decodeScope(scopeJSON)
	if err != nil {
		return principalRow{}, err
	}
	created, err := parseStamp(createdAt)
	if err != nil {
		return principalRow{}, err
	}
	updated, err := parseStamp(updatedAt)
	if err != nil {
		return principalRow{}, err
	}
	r.Scope = scope
	r.Revoked = revokedInt == 1
	r.CreatedAt, r.UpdatedAt = created, updated
	return r, nil
}

// scanGrant reads one identity_grants row.
func scanGrant(scan func(dest ...any) error) (grantRow, error) {
	var (
		r                             grantRow
		scopeJSON, capsJSON, destJSON string
		revokedInt                    int64
		deniedInt                     int64
		expires                       sql.NullString
		parent                        sql.NullString
		createdAt, updatedAt          string
	)
	if err := scan(&r.ID, &r.Version, &r.PrincipalID, &scopeJSON, &capsJSON, &destJSON,
		&deniedInt, &expires, &parent, &revokedInt, &createdAt, &updatedAt); err != nil {
		return grantRow{}, err
	}
	scope, err := decodeScope(scopeJSON)
	if err != nil {
		return grantRow{}, err
	}
	caps, err := decodeStrings(capsJSON)
	if err != nil {
		return grantRow{}, err
	}
	dests, err := decodeStrings(destJSON)
	if err != nil {
		return grantRow{}, err
	}
	created, err := parseStamp(createdAt)
	if err != nil {
		return grantRow{}, err
	}
	updated, err := parseStamp(updatedAt)
	if err != nil {
		return grantRow{}, err
	}
	r.Scope = scope
	r.Capabilities = caps
	r.Destinations = dests
	r.Denied = deniedInt == 1
	r.Revoked = revokedInt == 1
	r.CreatedAt, r.UpdatedAt = created, updated
	if expires.Valid {
		t, err := parseStamp(expires.String)
		if err != nil {
			return grantRow{}, err
		}
		r.ExpiresAt = &t
	}
	if parent.Valid {
		id := contract.ID(parent.String)
		r.ParentGrantID = &id
	}
	return r, nil
}

// scanCredential reads one identity_credentials row.
func scanCredential(scan func(dest ...any) error) (credentialRow, error) {
	var (
		r                    credentialRow
		revokedInt           int64
		expires              sql.NullString
		createdAt, updatedAt string
	)
	if err := scan(&r.ID, &r.Version, &r.PrincipalID, &r.StoreRef, &expires, &revokedInt, &createdAt, &updatedAt); err != nil {
		return credentialRow{}, err
	}
	created, err := parseStamp(createdAt)
	if err != nil {
		return credentialRow{}, err
	}
	updated, err := parseStamp(updatedAt)
	if err != nil {
		return credentialRow{}, err
	}
	r.Revoked = revokedInt == 1
	r.CreatedAt, r.UpdatedAt = created, updated
	if expires.Valid {
		t, err := parseStamp(expires.String)
		if err != nil {
			return credentialRow{}, err
		}
		r.ExpiresAt = &t
	}
	return r, nil
}

// scanRestriction reads one identity_restrictions row.
func scanRestriction(scan func(dest ...any) error) (restrictionRow, error) {
	var (
		r         restrictionRow
		activeInt int64
		createdAt string
	)
	if err := scan(&r.ID, &r.Version, &r.PrincipalID, &r.Capability, &r.Reason, &activeInt, &createdAt); err != nil {
		return restrictionRow{}, err
	}
	created, err := parseStamp(createdAt)
	if err != nil {
		return restrictionRow{}, err
	}
	r.Active = activeInt == 1
	r.CreatedAt = created
	return r, nil
}

// parseStamp parses a stored UTC RFC3339Nano timestamp.
func parseStamp(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("identity: decode stored timestamp: %w", err)
	}
	return t, nil
}

func formatStamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// Wire conversions.

func (r principalRow) wire() principalOut {
	return principalOut{
		ID:      r.ID,
		Version: contract.Version(r.Version),
		Kind:    r.Kind,
		Name:    r.Name,
		Scope:   r.Scope,
		Revoked: r.Revoked,
	}
}

func (r grantRow) wire() grantOut {
	return grantOut{
		ID:            r.ID,
		Version:       contract.Version(r.Version),
		PrincipalID:   r.PrincipalID,
		Scope:         r.Scope,
		Capabilities:  r.Capabilities,
		Destinations:  r.Destinations,
		Denied:        r.Denied,
		ExpiresAt:     r.ExpiresAt,
		ParentGrantID: r.ParentGrantID,
	}
}

func (r credentialRow) wire() credentialOut {
	return credentialOut{
		ID:          r.ID,
		Version:     contract.Version(r.Version),
		PrincipalID: r.PrincipalID,
		StoreRef:    r.StoreRef,
		Revoked:     r.Revoked,
		ExpiresAt:   r.ExpiresAt,
	}
}
