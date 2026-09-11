package connections

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Public connection.* handlers. Staging operations (create, update, archive)
// never activate a definition: they stage a typed change in configuration
// through the compiler boundary and return the draft plus the projected
// resource identity. Activation happens only under configuration apply.

// connDefIn is the create/update definition body from the operation schema.
type connDefIn struct {
	Scope           wireScope `json:"scope"`
	Provider        string    `json:"provider"`
	AccountIdentity string    `json:"account_identity"`
	CredentialRef   string    `json:"credential_ref"`
	Destinations    []string  `json:"destinations"`
	AllowedScopes   []string  `json:"allowed_scopes"`
}

type connCreateIn struct {
	Scope      wireScope    `json:"scope"`
	Definition connDefIn    `json:"definition"`
	DraftID    *contract.ID `json:"draft_id,omitempty"`
}

type connUpdateIn struct {
	Scope           wireScope    `json:"scope"`
	ID              contract.ID  `json:"id"`
	ExpectedVersion int64        `json:"expected_version"`
	Definition      connDefIn    `json:"definition"`
	DraftID         *contract.ID `json:"draft_id,omitempty"`
}

type connArchiveIn struct {
	Scope           wireScope    `json:"scope"`
	ID              contract.ID  `json:"id"`
	ExpectedVersion int64        `json:"expected_version"`
	DraftID         *contract.ID `json:"draft_id,omitempty"`
}

type connGetIn struct {
	Scope wireScope   `json:"scope"`
	ID    contract.ID `json:"id"`
}

type connListIn struct {
	Scope  wireScope   `json:"scope"`
	Cursor string      `json:"cursor,omitempty"`
	Limit  int         `json:"limit,omitempty"`
	Filter *listFilter `json:"filter,omitempty"`
}

type connRevokeIn struct {
	Scope           wireScope   `json:"scope"`
	ID              contract.ID `json:"id"`
	ExpectedVersion int64       `json:"expected_version"`
}

type createOut struct {
	Draft    wireDraft      `json:"draft"`
	Resource wireConnection `json:"resource"`
}

type resourceOut struct {
	Resource wireConnection `json:"resource"`
}

type dispositionOut struct {
	Resource wireDisposition `json:"resource"`
}

type listOut struct {
	Items []wireConnection `json:"items"`
}

// listFilter mirrors the shared structured filter object. Each resource
// decides which fields it supports; unsupported fields refuse invalid_input.
type listFilter struct {
	State          string      `json:"state,omitempty"`
	Key            string      `json:"key,omitempty"`
	ParentID       contract.ID `json:"parent_id,omitempty"`
	WorkerID       contract.ID `json:"worker_id,omitempty"`
	TaskID         contract.ID `json:"task_id,omitempty"`
	OrganizationID contract.ID `json:"organization_id,omitempty"`
	Descendants    *bool       `json:"descendants,omitempty"`
	NeedsYou       *bool       `json:"needs_you,omitempty"`
}

// rawConnectionDef builds the change definition body for a projected
// connection, matching the Connection $def exactly.
func rawConnectionDef(w wireConnection) (json.RawMessage, error) {
	return marshalData(w)
}

// stageChange calls _configuration.stage with one typed change and returns
// the resulting draft.
func (s *Service) stageChange(ctx context.Context, unit contract.Unit, scope wireScope, ch wireChange, draftID *contract.ID) (wireDraft, error) {
	inRaw, err := marshalData(struct {
		Scope   wireScope    `json:"scope"`
		Change  wireChange   `json:"change"`
		DraftID *contract.ID `json:"draft_id,omitempty"`
	}{Scope: scope, Change: ch, DraftID: draftID})
	if err != nil {
		return wireDraft{}, err
	}
	payload, err := s.ports.Call(ctx, unit, contract.Invocation{
		Operation: "_configuration.stage",
		Version:   1,
		Input:     inRaw,
	})
	if err != nil {
		return wireDraft{}, err
	}
	var out struct {
		Resource wireDraft `json:"resource"`
	}
	if err := contract.DecodeStrict(payload.Data, &out); err != nil {
		return wireDraft{}, internalError("stage result decoding failed: %v", err)
	}
	return out.Resource, nil
}

// handleCreate stages a typed connection create. The identity is allocated
// once here; submission replay at the application boundary makes retries
// return the same resource.
func handleCreate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[connCreateIn](s, "connection.create", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	if in.Definition.Scope != in.Scope {
		return contract.Payload{}, invalidInput("definition scope does not match the request scope")
	}
	w := wireConnection{
		ID:              s.ids.New(),
		Version:         1,
		Scope:           in.Definition.Scope,
		Provider:        in.Definition.Provider,
		AccountIdentity: in.Definition.AccountIdentity,
		CredentialRef:   in.Definition.CredentialRef,
		Destinations:    in.Definition.Destinations,
		AllowedScopes:   in.Definition.AllowedScopes,
		ValidationState: connStateUnverified,
	}
	def, err := rawConnectionDef(w)
	if err != nil {
		return contract.Payload{}, err
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindConnection,
		Action:          actionCreate,
		ID:              w.ID,
		ExpectedVersion: 0,
		Definition:      def,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(createOut{Draft: draft, Resource: w})
}

// handleUpdate stages a typed connection update. The staged definition
// carries the projected post-apply version; account or provider changes are
// flagged at candidate validation as review requirements, never silently
// applied.
func handleUpdate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[connUpdateIn](s, "connection.update", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	row, err := s.loadConnectionChecked(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row.LifecycleState != connLifecycleActive {
		return contract.Payload{}, invalidInput("connection %s is archived and cannot be updated", in.ID)
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("connection %s version %d does not match expected version %d",
			in.ID, row.Version, in.ExpectedVersion)
	}
	if in.Definition.Scope != in.Scope || in.Definition.Scope.toContract() != row.Scope {
		return contract.Payload{}, invalidInput("connection scope cannot move; archive and recreate instead")
	}
	w := wireConnection{
		ID:              row.ID,
		Version:         row.Version + 1,
		Scope:           in.Definition.Scope,
		Provider:        in.Definition.Provider,
		AccountIdentity: in.Definition.AccountIdentity,
		CredentialRef:   in.Definition.CredentialRef,
		Destinations:    in.Definition.Destinations,
		AllowedScopes:   in.Definition.AllowedScopes,
		ValidationState: row.ValidationState,
	}
	def, err := rawConnectionDef(w)
	if err != nil {
		return contract.Payload{}, err
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindConnection,
		Action:          actionUpdate,
		ID:              row.ID,
		ExpectedVersion: row.Version,
		Definition:      def,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(createOut{Draft: draft, Resource: w})
}

// handleArchive stages a typed archive. The definition stays unchanged;
// activation flips the private lifecycle state so retained work and
// obligations remain visible.
func handleArchive(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[connArchiveIn](s, "connection.archive", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	row, err := s.loadConnectionChecked(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("connection %s version %d does not match expected version %d",
			in.ID, row.Version, in.ExpectedVersion)
	}
	w := row.wire()
	def, err := rawConnectionDef(w)
	if err != nil {
		return contract.Payload{}, err
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindConnection,
		Action:          actionArchive,
		ID:              row.ID,
		ExpectedVersion: row.Version,
		Definition:      def,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(createOut{Draft: draft, Resource: w})
}

// handleGet resolves one connection exactly. Unknown or out-of-scope rows
// return not_found without disclosing cross-scope existence.
func handleGet(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[connGetIn](s, "connection.get", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	row, err := s.loadConnectionChecked(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !scopeCovers(in.Scope.toContract(), row.Scope) {
		return contract.Payload{}, notFound("connection %s is unknown in this scope", in.ID)
	}
	return s.completed(resourceOut{Resource: row.wire()})
}

// supportedConnectionFilters lists the structured filter fields the
// connection resource supports; every other field refuses invalid_input.
var supportedConnectionFilters = map[string]bool{
	"state":           true,
	"organization_id": true,
	"worker_id":       true,
	"task_id":         true,
}

// connectionFilterWhere builds the SQL where clause and arguments for the
// structured filter. Filter values bind as parameters; filter field names
// come from the supported set only and never interpolate user strings.
func connectionFilterWhere(unit contract.Unit, filter listFilter) (string, []any) {
	where := "installation_id = ?"
	args := []any{string(unit.Scope().InstallationID)}
	if filter.State != "" {
		where += " AND validation_state = ?"
		args = append(args, filter.State)
	}
	if filter.OrganizationID != "" {
		where += ` AND json_extract(scope_json, '$.organization_id') = ?`
		args = append(args, string(filter.OrganizationID))
	}
	if filter.WorkerID != "" {
		where += ` AND json_extract(scope_json, '$.worker_id') = ?`
		args = append(args, string(filter.WorkerID))
	}
	if filter.TaskID != "" {
		where += ` AND json_extract(scope_json, '$.task_id') = ?`
		args = append(args, string(filter.TaskID))
	}
	return where, args
}

// handleList reads an authorized consistent snapshot page. Scope and
// structured filters apply before pagination; the cursor binds the
// requesting principal and the exact query.
func handleList(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[connListIn](s, "connection.list", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	filter := listFilter{}
	if in.Filter != nil {
		filter = *in.Filter
		raw, rerr := json.Marshal(in.Filter)
		if rerr != nil {
			return contract.Payload{}, internalError("filter encoding failed")
		}
		var present map[string]json.RawMessage
		if err := json.Unmarshal(raw, &present); err != nil {
			return contract.Payload{}, internalError("filter decoding failed")
		}
		for field := range present {
			if !supportedConnectionFilters[field] {
				return contract.Payload{}, invalidInput("filter field %q is not supported for this resource", field)
			}
		}
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	qid := queryID(filter, limit)
	offset := 0
	if in.Cursor != "" {
		cur, err := decodeCursor(in.Cursor)
		if err != nil {
			return contract.Payload{}, err
		}
		if cur.Actor != unit.Actor().PrincipalID {
			return contract.Payload{}, cursorExpired("cursor is bound to the requesting principal")
		}
		if cur.QueryID != qid {
			return contract.Payload{}, cursorExpired("cursor is bound to a different query")
		}
		offset = cur.Offset
	}
	rows, err := s.listConnectionsPage(ctx, unit, filter, offset, limit+1)
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]wireConnection, 0, len(rows))
	for _, r := range rows {
		if !scopeCovers(in.Scope.toContract(), r.Scope) {
			continue
		}
		items = append(items, r.wire())
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c, cerr := encodeCursor(cursorPayload{Offset: offset + limit, QueryID: qid, Actor: unit.Actor().PrincipalID})
		if cerr != nil {
			return contract.Payload{}, cerr
		}
		next = &c
	}
	return s.completedWithCursor(listOut{Items: items}, next)
}

// handleRevoke immediately blocks credential use: the row flips to revoked
// in the same transaction, so _connections.resolve refuses from now on.
// Retained effects stay visible; the disposition records the cleanup
// obligation. Re-revoking the current version succeeds idempotently.
func handleRevoke(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[connRevokeIn](s, "connection.revoke", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	row, err := s.loadConnectionChecked(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !scopeCovers(in.Scope.toContract(), row.Scope) {
		return contract.Payload{}, notFound("connection %s is unknown in this scope", in.ID)
	}
	if row.ValidationState == connStateRevoked {
		return s.completed(dispositionOut{Resource: wireDisposition{
			ID: row.ID, Version: row.Version, State: connStateRevoked,
		}})
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("connection %s version %d does not match expected version %d",
			in.ID, row.Version, in.ExpectedVersion)
	}
	if err := s.updateConnectionState(ctx, unit, row, connStateRevoked, nil, nil); err != nil {
		return contract.Payload{}, err
	}
	if err := s.emit(ctx, unit, "connections.connection.revoked", row.ID, row.Version+1, map[string]any{
		"id": row.ID, "version": row.Version + 1, "state": connStateRevoked,
	}); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(dispositionOut{Resource: wireDisposition{
		ID:      row.ID,
		Version: row.Version + 1,
		State:   connStateRevoked,
	}})
}

// loadConnectionChecked loads a connection and refuses rows that belong to a
// different installation.
func (s *Service) loadConnectionChecked(ctx context.Context, unit contract.Unit, id contract.ID) (connectionRow, error) {
	row, found, err := s.loadConnection(ctx, unit, id)
	if err != nil {
		return connectionRow{}, err
	}
	if !found || row.Scope.InstallationID != unit.Scope().InstallationID {
		return connectionRow{}, notFound("connection %s is unknown in this installation", id)
	}
	return row, nil
}

// scopeCovers reports whether the request scope reaches the row scope: the
// installation must match, and every dimension the request names explicitly
// must agree with the row. An unset request dimension constrains nothing —
// the structured filters provide the narrowing instead.
func scopeCovers(request, row contract.Scope) bool {
	if row.InstallationID != request.InstallationID {
		return false
	}
	if request.OrganizationID != "" && request.OrganizationID != row.OrganizationID {
		return false
	}
	if request.ProjectID != "" && request.ProjectID != row.ProjectID {
		return false
	}
	if request.WorkerID != "" && request.WorkerID != row.WorkerID {
		return false
	}
	if request.TaskID != "" && request.TaskID != row.TaskID {
		return false
	}
	return true
}

// wire projects a stored row onto the wire Connection shape.
func (r connectionRow) wire() wireConnection {
	return wireConnection{
		ID:              r.ID,
		Version:         r.Version,
		Scope:           scopeFromContract(r.Scope),
		Provider:        r.Provider,
		AccountIdentity: r.AccountIdentity,
		CredentialRef:   r.CredentialRef,
		Destinations:    r.Destinations,
		AllowedScopes:   r.AllowedScopes,
		ValidationState: r.ValidationState,
		ValidatedAt:     r.ValidatedAt,
		ValidUntil:      r.ValidUntil,
	}
}

// updateConnectionState applies a validation-state transition with an
// optimistic version check. Timestamp overrides keep probe freshness exact.
func (s *Service) updateConnectionState(ctx context.Context, unit contract.Unit, row connectionRow, state string, validatedAt, validUntil *time.Time) error {
	var validated, validUntilAny any
	if validatedAt != nil {
		validated = formatStamp(*validatedAt)
	}
	if validUntil != nil {
		validUntilAny = formatStamp(*validUntil)
	}
	res, err := unit.ExecContext(ctx, `
		UPDATE connections_connections
		SET version = ?, validation_state = ?, validated_at = ?, valid_until = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		row.Version+1, state, validated, validUntilAny, formatStamp(s.clock.Now()),
		string(row.ID), row.Version)
	if err != nil {
		return fmt.Errorf("connections: update connection state: %w", err)
	}
	return expectOneRow(res, "connection", row.ID)
}

// expectOneRow refuses silent no-op writes: an optimistic update that hits
// zero rows is a lost race, reported as stale_version.
func expectOneRow(res sql.Result, kind string, id contract.ID) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("connections: %s update result: %w", kind, err)
	}
	if n != 1 {
		return staleVersion("%s %s changed concurrently; retry with the current version", kind, id)
	}
	return nil
}
