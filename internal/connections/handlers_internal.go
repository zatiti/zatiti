package connections

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Internal operations consumed by the compiler, the application apply
// boundary, trusted dispatchers and the controller.

// candidateIn is the sealed candidate slice the compiler hands each owner.
type candidateIn struct {
	Candidate struct {
		PlanID          contract.ID  `json:"plan_id"`
		BaseRevision    int64        `json:"base_revision"`
		CandidateDigest string       `json:"candidate_digest"`
		Changes         []wireChange `json:"changes"`
		Dependencies    []wireRef    `json:"dependencies"`
	} `json:"candidate"`
}

type activateOut struct {
	Versions []wireRef `json:"versions"`
}

// validationOut is the _connections.validate output body.
type validationOut struct {
	Resource wireValidation `json:"resource"`
}

// handleActivate applies the owned sealed candidate slice inside the
// configuration-apply transaction. The plan id is the replay fence: a replay
// returns the recorded versions only when digest and base revision match
// exactly; any difference refuses as stale_version.
func handleActivate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[candidateIn](s, "_connections.activate", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	cand := in.Candidate
	if prior, found, ferr := s.loadAppliedPlan(ctx, unit, cand.PlanID); ferr != nil {
		return contract.Payload{}, ferr
	} else if found {
		if prior.CandidateDigest != cand.CandidateDigest || prior.BaseRevision != cand.BaseRevision {
			return contract.Payload{}, staleVersion(
				"plan %s was already applied with a different candidate digest or base revision",
				cand.PlanID)
		}
		return s.completed(activateOut{Versions: prior.Versions})
	}
	versions := make([]wireRef, 0, len(cand.Changes))
	for i, ch := range cand.Changes {
		if ch.Kind != kindConnection {
			return contract.Payload{}, invalidInput(
				"candidate change %d carries kind %q; the connections owner applies connection changes only", i, ch.Kind)
		}
		ref, aerr := s.applyChange(ctx, unit, ch)
		if aerr != nil {
			return contract.Payload{}, aerr
		}
		versions = append(versions, ref)
	}
	if err := s.insertAppliedPlan(ctx, unit, appliedPlanRow{
		PlanID:          cand.PlanID,
		CandidateDigest: cand.CandidateDigest,
		BaseRevision:    cand.BaseRevision,
		Versions:        versions,
		AppliedAt:       s.clock.Now(),
	}); err != nil {
		return contract.Payload{}, err
	}
	if len(versions) > 0 {
		if err := s.emit(ctx, unit, "connections.connection.applied", contract.ID(versions[0].ID), versions[0].Version, map[string]any{
			"plan_id": cand.PlanID, "versions": versions,
		}); err != nil {
			return contract.Payload{}, err
		}
	}
	return s.completed(activateOut{Versions: versions})
}

// applyChange applies one typed connection change. Optimistic checks refuse
// concurrent edits with stale_version; creation refuses existing ids.
func (s *Service) applyChange(ctx context.Context, unit contract.Unit, ch wireChange) (wireRef, error) {
	var def struct {
		wireConnection
	}
	if err := contract.DecodeStrict(ch.Definition, &def); err != nil {
		return wireRef{}, invalidInput("connection change definition does not match the Connection schema: %v", err)
	}
	w := def.wireConnection
	if f := checkInstallation(unit, w.Scope); f != nil {
		return wireRef{}, f
	}
	switch ch.Action {
	case actionCreate:
		if _, found, err := s.loadConnection(ctx, unit, ch.ID); err != nil {
			return wireRef{}, err
		} else if found {
			return wireRef{}, conflictFault("connection %s already exists; create requires a fresh identity", ch.ID)
		}
		now := formatStamp(s.clock.Now())
		if _, err := unit.ExecContext(ctx, `
			INSERT INTO connections_connections
				(id, version, installation_id, scope_json, provider, account_identity,
				 credential_ref, destinations_json, allowed_scopes_json, validation_state,
				 lifecycle_state, validated_at, valid_until, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			string(w.ID), w.Version, string(w.Scope.InstallationID), mustScopeJSON(w.Scope),
			w.Provider, w.AccountIdentity, w.CredentialRef,
			marshalStrings(w.Destinations), marshalStrings(w.AllowedScopes),
			w.ValidationState, connLifecycleActive, stampOrNull(w.ValidatedAt),
			stampOrNull(w.ValidUntil), now, now); err != nil {
			return wireRef{}, fmt.Errorf("connections: insert connection: %w", err)
		}
		if err := s.emit(ctx, unit, "connections.connection.created", w.ID, w.Version, map[string]any{
			"id": w.ID, "version": w.Version, "provider": w.Provider,
			"account_identity": w.AccountIdentity, "validation_state": w.ValidationState,
		}); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: w.ID, Version: w.Version}, nil

	case actionUpdate:
		row, found, err := s.loadConnection(ctx, unit, ch.ID)
		if err != nil {
			return wireRef{}, err
		}
		if !found || row.Scope.InstallationID != unit.Scope().InstallationID {
			return wireRef{}, notFound("connection %s is unknown in this installation", ch.ID)
		}
		if row.Version != ch.ExpectedVersion {
			return wireRef{}, staleVersion("connection %s version %d does not match expected version %d",
				ch.ID, row.Version, ch.ExpectedVersion)
		}
		if w.Version != row.Version+1 {
			return wireRef{}, invalidInput("connection %s staged version %d does not project from current version %d",
				ch.ID, w.Version, row.Version)
		}
		if err := s.upsertConnection(ctx, unit, row, w, row.LifecycleState); err != nil {
			return wireRef{}, err
		}
		if err := s.emit(ctx, unit, "connections.connection.updated", w.ID, w.Version, map[string]any{
			"id": w.ID, "version": w.Version, "validation_state": w.ValidationState,
		}); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: w.ID, Version: w.Version}, nil

	case actionArchive:
		row, found, err := s.loadConnection(ctx, unit, ch.ID)
		if err != nil {
			return wireRef{}, err
		}
		if !found || row.Scope.InstallationID != unit.Scope().InstallationID {
			return wireRef{}, notFound("connection %s is unknown in this installation", ch.ID)
		}
		if row.Version != ch.ExpectedVersion {
			return wireRef{}, staleVersion("connection %s version %d does not match expected version %d",
				ch.ID, row.Version, ch.ExpectedVersion)
		}
		if _, err := unit.ExecContext(ctx, `
			UPDATE connections_connections
			SET lifecycle_state = ?, version = ?, updated_at = ?
			WHERE id = ? AND version = ?`,
			connLifecycleArchived, row.Version+1, formatStamp(s.clock.Now()),
			string(row.ID), row.Version); err != nil {
			return wireRef{}, fmt.Errorf("connections: archive connection: %w", err)
		}
		if err := s.emit(ctx, unit, "connections.connection.archived", row.ID, row.Version+1, map[string]any{
			"id": row.ID, "version": row.Version + 1,
		}); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: row.ID, Version: row.Version + 1}, nil

	case actionDelete:
		return wireRef{}, invalidInput("connections are never deleted; archive retains history and obligations")

	default:
		return wireRef{}, invalidInput("unknown connection change action %q", ch.Action)
	}
}

// upsertConnection writes the projected definition over the current row with
// an optimistic version check. Credential, account or provider edits land
// unverified: the staged definition already carries the reset state.
func (s *Service) upsertConnection(ctx context.Context, unit contract.Unit, row connectionRow, w wireConnection, lifecycle string) error {
	res, err := unit.ExecContext(ctx, `
		UPDATE connections_connections
		SET version = ?, scope_json = ?, provider = ?, account_identity = ?, credential_ref = ?,
		    destinations_json = ?, allowed_scopes_json = ?, validation_state = ?,
		    lifecycle_state = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		w.Version, mustScopeJSON(w.Scope), w.Provider, w.AccountIdentity, w.CredentialRef,
		marshalStrings(w.Destinations), marshalStrings(w.AllowedScopes), w.ValidationState,
		lifecycle, formatStamp(s.clock.Now()),
		string(row.ID), row.Version)
	if err != nil {
		return fmt.Errorf("connections: update connection: %w", err)
	}
	return expectOneRow(res, "connection", row.ID)
}

// mustScopeJSON encodes a scope for storage; scope fields are plain strings,
// so encoding cannot fail at runtime.
func mustScopeJSON(s wireScope) string {
	raw, err := json.Marshal(s.toContract())
	if err != nil {
		panic("connections: scope encoding failed: " + err.Error())
	}
	return string(raw)
}

// stampOrNull renders an optional timestamp for storage.
func stampOrNull(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatStamp(*t)
}

// handleResolve resolves one exact connection/tool/destination triple for a
// trusted dispatcher: lifecycle, validation freshness, revocation and the
// bound destinations on both the tool contract and the connection must all
// admit the destination.
func handleResolve(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope       wireScope `json:"scope"`
		Connection  wireRef   `json:"connection"`
		Tool        wireRef   `json:"tool"`
		Destination string    `json:"destination"`
	}](s, "_connections.resolve", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	row, found, rerr := s.loadConnection(ctx, unit, in.Connection.ID)
	if rerr != nil {
		return contract.Payload{}, rerr
	}
	if !found || row.Scope.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, notFound("connection %s is unknown in this installation", in.Connection.ID)
	}
	if !scopeCovers(in.Scope.toContract(), row.Scope) {
		return contract.Payload{}, notFound("connection %s is unknown in this scope", in.Connection.ID)
	}
	if row.ID != in.Connection.ID || row.Version != in.Connection.Version {
		return contract.Payload{}, staleVersion("connection %s is at version %d; dispatch pinned version %d",
			row.ID, row.Version, in.Connection.Version)
	}
	if row.LifecycleState != connLifecycleActive {
		return contract.Payload{}, prerequisiteMissing("connection %s is archived and cannot dispatch", row.ID)
	}
	switch row.ValidationState {
	case connStateValid:
		if row.ValidUntil != nil && !row.ValidUntil.After(s.clock.Now()) {
			return contract.Payload{}, prerequisiteMissing("connection %s validation expired at %s",
				row.ID, formatStamp(*row.ValidUntil))
		}
	case connStateUnverified:
		return contract.Payload{}, prerequisiteMissing("connection %s has never been validated", row.ID)
	case connStateExpired:
		return contract.Payload{}, prerequisiteMissing("connection %s validation expired", row.ID)
	case connStateInvalid:
		return contract.Payload{}, prerequisiteMissing("connection %s validation failed", row.ID)
	case connStateRevoked:
		return contract.Payload{}, prerequisiteMissing("connection %s is revoked", row.ID)
	default:
		return contract.Payload{}, internalError("connection %s carries unknown validation state %q", row.ID, row.ValidationState)
	}
	tool, found, terr := s.loadContract(ctx, unit, in.Tool.ID)
	if terr != nil {
		return contract.Payload{}, terr
	}
	if !found {
		return contract.Payload{}, notFound("tool %s is unknown", in.Tool.ID)
	}
	if tool.Version != in.Tool.Version {
		return contract.Payload{}, staleVersion("tool %s is at version %d; dispatch pinned version %d",
			tool.ID, tool.Version, in.Tool.Version)
	}
	if !contains(tool.Destinations, in.Destination) {
		return contract.Payload{}, permissionDenied(
			"tool %s is not bound to destination %s", tool.ID, in.Destination)
	}
	if !contains(row.Destinations, in.Destination) {
		return contract.Payload{}, permissionDenied(
			"connection %s is not bound to destination %s", row.ID, in.Destination)
	}
	return s.completed(struct {
		Connection wireConnection `json:"connection"`
		Tool       wireTool       `json:"tool"`
	}{Connection: row.wire(), Tool: tool.wire()})
}

// contains reports whether v is present in vs.
func contains(vs []string, v string) bool {
	for _, s := range vs {
		if s == v {
			return true
		}
	}
	return false
}

// validationFreshness is the owner-local freshness bound granted by one
// succeeded probe. Longer windows require a new probe, never an assertion.
const validationFreshness = 24 * time.Hour

// handleValidate validates the owned candidate slice against the current
// snapshot: no live changes, no network. Expected-version zero is
// create-only; account substitution and provider changes surface as review
// requirements under old effective authority.
func handleValidate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[candidateIn](s, "_connections.validate", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	cand := in.Candidate
	out := wireValidation{
		Diagnostics:  []wireDiagnostic{},
		Requirements: []wireRequirement{},
		Dependencies: []wireRef{},
	}
	for i, ch := range cand.Changes {
		if ch.Kind != kindConnection {
			return contract.Payload{}, invalidInput(
				"candidate change %d carries kind %q; the connections owner validates connection changes only", i, ch.Kind)
		}
		path := fmt.Sprintf("candidate.changes[%d]", i)
		switch ch.Action {
		case actionCreate:
			if ch.ExpectedVersion != 0 {
				out.Diagnostics = append(out.Diagnostics,
					errorDiagnostic(path, "stale_version", "expected-version zero is create-only"))
			}
			var def struct{ wireConnection }
			if derr := contract.DecodeStrict(ch.Definition, &def); derr != nil {
				out.Diagnostics = append(out.Diagnostics, errorDiagnostic(path, "schema",
					fmt.Sprintf("definition does not match the Connection schema: %v", derr)))
				continue
			}
			if def.Version != 1 {
				out.Diagnostics = append(out.Diagnostics, errorDiagnostic(path, "version",
					fmt.Sprintf("created connections start at version 1, staged %d", def.Version)))
			}
			if _, found, lerr := s.loadConnection(ctx, unit, ch.ID); lerr != nil {
				return contract.Payload{}, lerr
			} else if found {
				out.Diagnostics = append(out.Diagnostics, errorDiagnostic(path, "conflict",
					fmt.Sprintf("connection %s already exists", ch.ID)))
			}

		case actionUpdate:
			row, found, lerr := s.loadConnection(ctx, unit, ch.ID)
			if lerr != nil {
				return contract.Payload{}, lerr
			}
			if !found || row.Scope.InstallationID != unit.Scope().InstallationID {
				out.Diagnostics = append(out.Diagnostics, errorDiagnostic(path, "not_found",
					fmt.Sprintf("connection %s does not exist in this installation", ch.ID)))
				continue
			}
			out.Dependencies = append(out.Dependencies, wireRef{ID: row.ID, Version: row.Version})
			if row.Version != ch.ExpectedVersion {
				out.Diagnostics = append(out.Diagnostics, errorDiagnostic(path, "stale_version",
					fmt.Sprintf("connection %s is at version %d, candidate expects %d",
						row.ID, row.Version, ch.ExpectedVersion)))
				continue
			}
			var def struct{ wireConnection }
			if derr := contract.DecodeStrict(ch.Definition, &def); derr != nil {
				out.Diagnostics = append(out.Diagnostics, errorDiagnostic(path, "schema",
					fmt.Sprintf("definition does not match the Connection schema: %v", derr)))
				continue
			}
			if def.Scope.toContract() != row.Scope {
				out.Diagnostics = append(out.Diagnostics, errorDiagnostic(path, "scope_move",
					"connection scope cannot move; archive and recreate instead"))
			}
			if def.AccountIdentity != row.AccountIdentity {
				out.Diagnostics = append(out.Diagnostics, warnDiagnostic(path, "account_substitution",
					fmt.Sprintf("account changes from %q to %q; substitution requires review",
						row.AccountIdentity, def.AccountIdentity)))
				out.Requirements = append(out.Requirements, requirement(
					"connections.account_substitution",
					fmt.Sprintf("connection %s substitutes account %q for %q; human review required",
						row.ID, row.AccountIdentity, def.AccountIdentity),
					row.ID, ""))
			} else if def.Provider != row.Provider {
				out.Diagnostics = append(out.Diagnostics, warnDiagnostic(path, "provider_change",
					fmt.Sprintf("provider changes from %q to %q", row.Provider, def.Provider)))
				out.Requirements = append(out.Requirements, requirement(
					"connections.provider_change",
					fmt.Sprintf("connection %s changes provider from %q to %q; review required",
						row.ID, row.Provider, def.Provider),
					row.ID, ""))
			}
			if def.CredentialRef != row.CredentialRef {
				out.Diagnostics = append(out.Diagnostics, infoDiagnostic(path, "credential_replacement",
					"credential reference changes; the connection re-enters unverified until a probe succeeds"))
			}

		case actionArchive:
			row, found, lerr := s.loadConnection(ctx, unit, ch.ID)
			if lerr != nil {
				return contract.Payload{}, lerr
			}
			if !found || row.Scope.InstallationID != unit.Scope().InstallationID {
				out.Diagnostics = append(out.Diagnostics, errorDiagnostic(path, "not_found",
					fmt.Sprintf("connection %s does not exist in this installation", ch.ID)))
				continue
			}
			out.Dependencies = append(out.Dependencies, wireRef{ID: row.ID, Version: row.Version})
			if row.Version != ch.ExpectedVersion {
				out.Diagnostics = append(out.Diagnostics, errorDiagnostic(path, "stale_version",
					fmt.Sprintf("connection %s is at version %d, candidate expects %d",
						row.ID, row.Version, ch.ExpectedVersion)))
			}

		case actionDelete:
			out.Diagnostics = append(out.Diagnostics, errorDiagnostic(path, "omission",
				"connections are never deleted; archive retains history and obligations"))

		default:
			return contract.Payload{}, invalidInput("unknown connection change action %q", ch.Action)
		}
	}
	return s.completed(validationOut{Resource: out})
}

// evidenceBody is the owner-decoded subset of an observation's evidence.
// Evidence is an open object; unknown keys ride along into storage inert.
type evidenceBody struct {
	AccountIdentity string   `json:"account_identity"`
	AllowedScopes   []string `json:"allowed_scopes"`
}

// handleValidationRecord records an authorized probe observation: identity,
// scopes and freshness. Succeeded probes grant a bounded freshness window;
// failed probes invalidate immediately; other dispositions record without
// state change. Account substitution inside evidence refuses outright.
func handleValidationRecord(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		ConnectionID    contract.ID     `json:"connection_id"`
		ExpectedVersion int64           `json:"expected_version"`
		Observation     wireObservation `json:"observation"`
	}](s, "_connections.validation.record", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	row, found, lerr := s.loadConnection(ctx, unit, in.ConnectionID)
	if lerr != nil {
		return contract.Payload{}, lerr
	}
	if !found || row.Scope.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, notFound("connection %s is unknown in this installation", in.ConnectionID)
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("connection %s version %d does not match expected version %d",
			row.ID, row.Version, in.ExpectedVersion)
	}
	var ev evidenceBody
	if len(in.Observation.Evidence) > 0 {
		if uerr := json.Unmarshal(in.Observation.Evidence, &ev); uerr != nil {
			return contract.Payload{}, invalidInput("observation evidence is not a JSON object: %v", uerr)
		}
	}
	if ev.AccountIdentity != "" && ev.AccountIdentity != row.AccountIdentity {
		return contract.Payload{}, verificationFailed(
			"observation reports account %q but connection %s is bound to %q; substitution is refused",
			ev.AccountIdentity, row.ID, row.AccountIdentity)
	}
	scopeSubset := true
	for _, sc := range ev.AllowedScopes {
		if !contains(row.AllowedScopes, sc) {
			scopeSubset = false
			break
		}
	}
	confirmed := s.clock.Now()
	if in.Observation.ConfirmedAt != nil {
		confirmed = *in.Observation.ConfirmedAt
	}
	obs := observationRow{
		ID:                s.ids.New(),
		InstallationID:    row.Scope.InstallationID,
		ConnectionID:      row.ID,
		ConnectionVersion: row.Version,
		Disposition:       in.Observation.Disposition,
		ObservedAccount:   ev.AccountIdentity,
		ObservedScopes:    ev.AllowedScopes,
		Evidence:          in.Observation.Evidence,
		Usage:             in.Observation.Usage,
		ProviderReference: in.Observation.ProviderReference,
		ConfirmedAt:       confirmed,
	}
	switch in.Observation.Disposition {
	case obsSucceeded:
		if !scopeSubset {
			if err := s.updateConnectionState(ctx, unit, row, connStateInvalid, &confirmed, nil); err != nil {
				return contract.Payload{}, err
			}
		} else {
			until := confirmed.Add(validationFreshness)
			if err := s.updateConnectionState(ctx, unit, row, connStateValid, &confirmed, &until); err != nil {
				return contract.Payload{}, err
			}
		}
	case obsFailed:
		if err := s.updateConnectionState(ctx, unit, row, connStateInvalid, &confirmed, nil); err != nil {
			return contract.Payload{}, err
		}
	case obsAccepted, obsUnknown, obsNotSent:
		// Record only: accepted/unknown/not_sent dispositions carry no
		// established account truth, so no validation state may move.
	default:
		return contract.Payload{}, invalidInput("unknown observation disposition %q", in.Observation.Disposition)
	}
	if err := s.insertObservation(ctx, unit, obs); err != nil {
		return contract.Payload{}, err
	}
	if err := s.emit(ctx, unit, "connections.validation.recorded", row.ID, row.Version+1, map[string]any{
		"id": row.ID, "version": row.Version + 1, "disposition": in.Observation.Disposition,
	}); err != nil {
		return contract.Payload{}, err
	}
	updated, found, uerr := s.loadConnection(ctx, unit, row.ID)
	if uerr != nil {
		return contract.Payload{}, uerr
	}
	if !found {
		return contract.Payload{}, internalError("connection %s disappeared during observation recording", row.ID)
	}
	return s.completed(resourceOut{Resource: updated.wire()})
}
