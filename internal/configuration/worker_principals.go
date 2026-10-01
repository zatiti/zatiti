package configuration

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Revision 21: every configured worker has an identity principal of kind
// worker with the same id. Compiler-applied worker changes reach identity
// through _identity.activate; the bootstrap chief and installations created
// before revision 21 are registered through _identity.worker.sync. Identity
// alone decides the principal's authority -- this package only reports
// which workers exist, where, and whether they are still active.

type workerPrincipalsSyncIn struct {
	InstallationID contract.ID `json:"installation_id"`
}

type workerPrincipalRef struct {
	WorkerID       contract.ID `json:"worker_id"`
	OrganizationID contract.ID `json:"organization_id"`
	Active         bool        `json:"active"`
}

type versionsResult struct {
	Versions []wireRef `json:"versions"`
}

// handleWorkerPrincipalsSync implements _configuration.worker.principals.sync.
func handleWorkerPrincipalsSync(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[workerPrincipalsSyncIn](s, "_configuration.worker.principals.sync", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.checkInstallation(unit, in.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	workers, err := listInstallationWorkers(ctx, unit, in.InstallationID)
	if err != nil {
		return contract.Payload{}, err
	}
	versions, err := s.syncWorkerPrincipals(ctx, unit, workers)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(versionsResult{Versions: versions})
}

// syncWorkerPrincipals passes the given workers to identity.
func (s *Service) syncWorkerPrincipals(ctx context.Context, unit contract.Unit, workers []*workerRow) ([]wireRef, error) {
	refs := make([]workerPrincipalRef, 0, len(workers))
	for _, w := range workers {
		refs = append(refs, workerPrincipalRef{
			WorkerID: w.ID, OrganizationID: w.OrganizationID, Active: w.State != stateArchived,
		})
	}
	versions := []wireRef{}
	if len(refs) == 0 {
		return versions, nil
	}
	raw, err := s.callOwner(ctx, unit, "_identity.worker.sync", map[string]any{"workers": refs})
	if err != nil {
		return nil, err
	}
	var out versionsResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, internalError("identity worker sync returned an undecodable result: %v", err)
	}
	if out.Versions != nil {
		versions = out.Versions
	}
	return versions, nil
}

// listInstallationWorkers loads every worker row of one installation.
func listInstallationWorkers(ctx context.Context, unit contract.Unit, install contract.ID) ([]*workerRow, error) {
	rows, err := unit.QueryContext(ctx,
		"SELECT id, version, installation_id, organization_id, key, name, purpose, instructions, skill_versions_json, bindings_json, profile_json, limits_json, extensions_json, state, created_at, updated_at "+
			"FROM configuration_workers WHERE installation_id = ? ORDER BY id", install)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*workerRow
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
