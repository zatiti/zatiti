package reviews

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Row structs mirror the reviews_-prefixed tables. Scope dimensions are
// stored both as columns (for filtered reads) and as a canonical scope JSON
// snapshot (for exact wire output).

type reviewRow struct {
	ID              contract.ID
	Version         int64
	Scope           contract.Scope
	ActionDigest    string
	PreviewJSON     string // canonical wireAction
	RequirementJSON string // canonical wireRequirement
	ProposerID      contract.ID
	State           string
	DecisionID      *contract.ID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type decisionRow struct {
	ID            contract.ID
	ReviewID      contract.ID
	ReviewVersion int64
	Install       contract.ID
	ActionDigest  string
	ReviewerID    contract.ID
	Decision      string
	DecidedAt     time.Time
	Reason        string
	CreatedAt     time.Time
}

type delegationRow struct {
	ID            contract.ID
	ReviewID      contract.ID
	ReviewVersion int64
	Install       contract.ID
	DelegatorID   contract.ID
	DelegateeID   contract.ID
	CreatedAt     time.Time
}

// encodeScope marshals a scope for the scope_json column.
func encodeScope(s contract.Scope) (string, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("reviews: encode scope: %w", err)
	}
	return string(raw), nil
}

// decodeScope parses a stored scope snapshot.
func decodeScope(raw string) (contract.Scope, error) {
	var s contract.Scope
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return contract.Scope{}, fmt.Errorf("reviews: decode stored scope: %w", err)
	}
	return s, nil
}

// canonicalActionJSON round-trips an action through its DTO and canonical
// form. The digest is taken over exactly these bytes, so the same action
// always yields the same digest regardless of input key order or integer
// spelling.
func canonicalActionJSON(a wireAction) (string, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("reviews: encode action: %w", err)
	}
	canon, err := contract.Canonicalize(raw)
	if err != nil {
		return "", fmt.Errorf("reviews: canonicalize action: %w", err)
	}
	return string(canon), nil
}

// canonicalRequirementJSON canonicalizes a requirement for storage.
func canonicalRequirementJSON(r wireRequirement) (string, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("reviews: encode requirement: %w", err)
	}
	canon, err := contract.Canonicalize(raw)
	if err != nil {
		return "", fmt.Errorf("reviews: canonicalize requirement: %w", err)
	}
	return string(canon), nil
}

// actionDigest binds the exact action: account, destination, content and
// media hashes, timing, preconditions, configuration revision and tool
// versions.
func actionDigest(a wireAction) (string, error) {
	canon, err := canonicalActionJSON(a)
	if err != nil {
		return "", err
	}
	return string(contract.Hash([]byte(canon))), nil
}

// requirement decodes the stored requirement snapshot.
func (r reviewRow) requirement() (wireRequirement, error) {
	var req wireRequirement
	if err := json.Unmarshal([]byte(r.RequirementJSON), &req); err != nil {
		return wireRequirement{}, fmt.Errorf("reviews: decode stored requirement: %w", err)
	}
	return req, nil
}

// preview decodes the stored canonical action preview.
func (r reviewRow) preview() (wireAction, error) {
	var a wireAction
	if err := json.Unmarshal([]byte(r.PreviewJSON), &a); err != nil {
		return wireAction{}, fmt.Errorf("reviews: decode stored preview: %w", err)
	}
	return a, nil
}

// scanReview reads one reviews_requests row.
func scanReview(scan func(dest ...any) error) (reviewRow, error) {
	var (
		r                    reviewRow
		scopeJSON            string
		decision             sql.NullString
		createdAt, updatedAt string
	)
	if err := scan(&r.ID, &r.Version, &scopeJSON, &r.ActionDigest, &r.PreviewJSON,
		&r.RequirementJSON, &r.ProposerID, &r.State, &decision, &createdAt, &updatedAt); err != nil {
		return reviewRow{}, err
	}
	scope, err := decodeScope(scopeJSON)
	if err != nil {
		return reviewRow{}, err
	}
	created, err := parseStamp(createdAt)
	if err != nil {
		return reviewRow{}, err
	}
	updated, err := parseStamp(updatedAt)
	if err != nil {
		return reviewRow{}, err
	}
	r.Scope = scope
	r.CreatedAt, r.UpdatedAt = created, updated
	if decision.Valid {
		id := contract.ID(decision.String)
		r.DecisionID = &id
	}
	return r, nil
}

// scanDecision reads one reviews_decisions row.
func scanDecision(scan func(dest ...any) error) (decisionRow, error) {
	var (
		d                    decisionRow
		decidedAt, createdAt string
	)
	if err := scan(&d.ID, &d.ReviewID, &d.ReviewVersion, &d.Install, &d.ActionDigest, &d.ReviewerID,
		&d.Decision, &decidedAt, &d.Reason, &createdAt); err != nil {
		return decisionRow{}, err
	}
	decided, err := parseStamp(decidedAt)
	if err != nil {
		return decisionRow{}, err
	}
	created, err := parseStamp(createdAt)
	if err != nil {
		return decisionRow{}, err
	}
	d.DecidedAt, d.CreatedAt = decided, created
	return d, nil
}

// scanDelegation reads one reviews_delegations row.
func scanDelegation(scan func(dest ...any) error) (delegationRow, error) {
	var d delegationRow
	var createdAt string
	if err := scan(&d.ID, &d.ReviewID, &d.ReviewVersion, &d.Install, &d.DelegatorID, &d.DelegateeID, &createdAt); err != nil {
		return delegationRow{}, err
	}
	created, err := parseStamp(createdAt)
	if err != nil {
		return delegationRow{}, err
	}
	d.CreatedAt = created
	return d, nil
}

// parseStamp parses a stored UTC RFC3339Nano timestamp.
func parseStamp(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("reviews: decode stored timestamp: %w", err)
	}
	return t, nil
}

func formatStamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// Wire conversions.

func (r reviewRow) wire() (wireReview, error) {
	preview, err := r.preview()
	if err != nil {
		return wireReview{}, err
	}
	requirement, err := r.requirement()
	if err != nil {
		return wireReview{}, err
	}
	return wireReview{
		ID:           r.ID,
		Version:      r.Version,
		Scope:        r.Scope,
		ActionDigest: r.ActionDigest,
		Preview:      preview,
		Requirement:  requirement,
		ProposerID:   r.ProposerID,
		State:        r.State,
		DecisionID:   r.DecisionID,
	}, nil
}

func (d decisionRow) wire() wireDecision {
	return wireDecision{
		ID:            d.ID,
		ReviewID:      d.ReviewID,
		ReviewVersion: d.ReviewVersion,
		ActionDigest:  d.ActionDigest,
		ReviewerID:    d.ReviewerID,
		Decision:      d.Decision,
		At:            d.DecidedAt,
		Reason:        d.Reason,
	}
}
