package configuration

import (
	"context"
	"database/sql"

	"github.com/zatiti/zatiti/internal/contract"
)

// Owned-slice activation. Applies the sealed candidate slice to the
// effective configuration_ tables in FK-safe order with optimistic version
// fencing, records before/after lineage, and re-proves the structural
// invariants. Everything runs inside the caller's transaction.

// applyRank orders effective writes so foreign keys hold regardless of stage
// order: organizations land first (workers, teams and projects reference
// them), then workers, then teams/projects, then bindings/profiles. Deletes
// invert the order (dependents leave first) and run after all upserts so a
// bundle that swaps references never trips a foreign key mid-apply.
var applyRank = map[string]int{
	kindOrganization:     0,
	kindWorker:           1,
	kindTeam:             2,
	kindProject:          2,
	kindBinding:          3,
	kindExecutionProfile: 3,
}

// applyOrder is the sort key for one change inside a bundle.
func applyOrder(c wireChange) int {
	if c.Action == actionDelete {
		return 100 + (len(applyRank) - applyRank[c.Kind])
	}
	return applyRank[c.Kind]
}

// activateOwned applies the owned candidate slice to the effective tables,
// capturing before/after lineage per object. Any failure returns an error and
// the caller's transaction rolls back the entire bundle.
func (s *Service) activateOwned(ctx context.Context, unit contract.Unit, plan *planRow, changes []wireChange) ([]revisionObject, error) {
	ranked := make([]wireChange, 0, len(changes))
	for _, c := range changes {
		if ownedKinds[c.Kind] {
			ranked = append(ranked, c)
		}
	}
	// Stable insertion sort by apply order keeps intra-kind stage order.
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && applyOrder(ranked[j]) < applyOrder(ranked[j-1]); j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}
	objects := make([]revisionObject, 0, len(ranked))
	for _, c := range ranked {
		before, after, err := s.applyOwnedChange(ctx, unit, c)
		if err != nil {
			return nil, err
		}
		objects = append(objects, revisionObject{
			Kind: c.Kind, ObjectID: c.ID, Action: c.Action,
			BeforeJSON: before, AfterJSON: after,
		})
	}
	return objects, nil
}

// applyOwnedChange performs one effective mutation with optimistic version
// control and returns the before/after definitions.
func (s *Service) applyOwnedChange(ctx context.Context, unit contract.Unit, c wireChange) (before, after string, err error) {
	switch c.Kind {
	case kindOrganization:
		return s.applyOrgChange(ctx, unit, c)
	case kindTeam:
		return s.applyTeamChange(ctx, unit, c)
	case kindProject:
		return s.applyProjectChange(ctx, unit, c)
	case kindWorker:
		return s.applyWorkerChange(ctx, unit, c)
	case kindBinding:
		return s.applyBindingChange(ctx, unit, c)
	case kindExecutionProfile:
		return s.applyProfileChange(ctx, unit, c)
	default:
		return "", "", invalidInput("kind %s is not owned by configuration", c.Kind)
	}
}

// applyOrgChange applies one organization change.
func (s *Service) applyOrgChange(ctx context.Context, unit contract.Unit, c wireChange) (string, string, error) {
	def, err := decodeOrgDef(c.Definition)
	if err != nil {
		return "", "", err
	}
	now := s.clock.Now().UTC()
	switch c.Action {
	case actionCreate:
		row := &orgRow{
			ID: c.ID, Version: 1, InstallationID: s.installOf(ctx, unit), Key: def.Key,
			Name: def.Name, ChiefID: def.ChiefID, LimitsJSON: marshalJSON(def.Limits),
			ExtensionsJSON: marshalJSON(def.Extensions), State: stateActive,
			CreatedAt: now, UpdatedAt: now,
		}
		if def.ParentID != nil {
			row.ParentID = *def.ParentID
		}
		if err := insertOrg(ctx, unit, row); err != nil {
			return "", "", err
		}
		return "", marshalJSON(def), nil
	case actionUpdate, actionArchive, actionDelete:
		install := s.installOf(ctx, unit)
		row, err := fetchOrgByID(ctx, unit, install, c.ID)
		if err != nil {
			return "", "", err
		}
		if row == nil {
			return "", "", notFound("organization %s does not exist", c.ID)
		}
		if c.ExpectedVersion != row.Version {
			return "", "", staleVersion("organization %s is at version %d", c.ID, row.Version)
		}
		beforeDef := orgDef(row)
		switch c.Action {
		case actionUpdate:
			row.Key = def.Key
			row.Name = def.Name
			row.ChiefID = def.ChiefID
			if def.ParentID != nil {
				row.ParentID = *def.ParentID
			} else {
				row.ParentID = ""
			}
			row.LimitsJSON = marshalJSON(def.Limits)
			row.ExtensionsJSON = marshalJSON(def.Extensions)
		case actionArchive:
			row.State = stateArchived
		case actionDelete:
			if err := s.guardOrgDelete(ctx, unit, row); err != nil {
				return "", "", err
			}
		}
		row.Version = c.ExpectedVersion + 1
		row.UpdatedAt = now
		if err := writeOrg(ctx, unit, row); err != nil {
			return "", "", err
		}
		if c.Action == actionDelete {
			if err := deleteOrg(ctx, unit, row.ID); err != nil {
				return "", "", err
			}
			return marshalJSON(beforeDef), "", nil
		}
		return marshalJSON(beforeDef), marshalJSON(orgDef(row)), nil
	}
	return "", "", invalidInput("unsupported organization action %s", c.Action)
}

// guardOrgDelete refuses deleting an organization whose rows are still
// referenced: child organizations, teams, projects and workers block the
// delete regardless of their own state (the foreign keys are state-blind).
// A subtree removal stages the dependents' own deletes first.
func (s *Service) guardOrgDelete(ctx context.Context, unit contract.Unit, row *orgRow) error {
	var n int
	if err := unit.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM configuration_organizations WHERE installation_id = ? AND parent_id = ?",
		row.InstallationID, row.ID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return conflictFault("organization %s still has %d child organizations; remove them first", row.ID, n)
	}
	if err := unit.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM configuration_teams WHERE installation_id = ? AND organization_id = ?",
		row.InstallationID, row.ID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return conflictFault("organization %s still has %d teams; remove them first", row.ID, n)
	}
	if err := unit.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM configuration_projects WHERE installation_id = ? AND organization_id = ?",
		row.InstallationID, row.ID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return conflictFault("organization %s still has %d projects; remove them first", row.ID, n)
	}
	if err := unit.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM configuration_workers WHERE installation_id = ? AND organization_id = ?",
		row.InstallationID, row.ID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return conflictFault("organization %s still has %d workers; remove them first", row.ID, n)
	}
	return nil
}

// applyTeamChange applies one team change.
func (s *Service) applyTeamChange(ctx context.Context, unit contract.Unit, c wireChange) (string, string, error) {
	def, err := decodeTeamDef(c.Definition)
	if err != nil {
		return "", "", err
	}
	now := s.clock.Now().UTC()
	install := s.installOf(ctx, unit)
	if c.Action == actionCreate {
		row := &teamRow{
			ID: c.ID, Version: 1, InstallationID: install, OrganizationID: def.OrganizationID,
			Key: def.Key, Name: def.Name, WorkerIDsJSON: marshalJSON(def.WorkerIDs),
			ExtensionsJSON: marshalJSON(def.Extensions), State: stateActive,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := insertTeam(ctx, unit, row); err != nil {
			return "", "", err
		}
		return "", marshalJSON(def), nil
	}
	row, err := fetchTeamByID(ctx, unit, install, c.ID)
	if err != nil {
		return "", "", err
	}
	if row == nil {
		return "", "", notFound("team %s does not exist", c.ID)
	}
	if c.ExpectedVersion != row.Version {
		return "", "", staleVersion("team %s is at version %d", c.ID, row.Version)
	}
	beforeDef := teamDef(row)
	switch c.Action {
	case actionUpdate:
		row.OrganizationID = def.OrganizationID
		row.Key = def.Key
		row.Name = def.Name
		row.WorkerIDsJSON = marshalJSON(def.WorkerIDs)
		row.ExtensionsJSON = marshalJSON(def.Extensions)
	case actionArchive:
		row.State = stateArchived
	case actionDelete:
		// Teams carry no dependents.
	}
	row.Version = c.ExpectedVersion + 1
	row.UpdatedAt = now
	if err := writeTeam(ctx, unit, row); err != nil {
		return "", "", err
	}
	if c.Action == actionDelete {
		if err := deleteTeam(ctx, unit, row.ID); err != nil {
			return "", "", err
		}
		return marshalJSON(beforeDef), "", nil
	}
	return marshalJSON(beforeDef), marshalJSON(teamDef(row)), nil
}

// applyProjectChange applies one project change.
func (s *Service) applyProjectChange(ctx context.Context, unit contract.Unit, c wireChange) (string, string, error) {
	def, err := decodeProjectDef(c.Definition)
	if err != nil {
		return "", "", err
	}
	now := s.clock.Now().UTC()
	install := s.installOf(ctx, unit)
	if c.Action == actionCreate {
		row := &projectRow{
			ID: c.ID, Version: 1, InstallationID: install, OrganizationID: def.OrganizationID,
			Key: def.Key, Name: def.Name, Repositories: def.Repositories,
			BindingsJSON: marshalJSON(def.Bindings), Classification: def.Classification,
			LimitsJSON: marshalJSON(def.Limits), ExtensionsJSON: marshalJSON(def.Extensions),
			State: stateActive, CreatedAt: now, UpdatedAt: now,
		}
		if err := insertProject(ctx, unit, row); err != nil {
			return "", "", err
		}
		return "", marshalJSON(def), nil
	}
	row, err := fetchProjectByID(ctx, unit, install, c.ID)
	if err != nil {
		return "", "", err
	}
	if row == nil {
		return "", "", notFound("project %s does not exist", c.ID)
	}
	if c.ExpectedVersion != row.Version {
		return "", "", staleVersion("project %s is at version %d", c.ID, row.Version)
	}
	beforeDef := projectDef(row)
	switch c.Action {
	case actionUpdate:
		row.OrganizationID = def.OrganizationID
		row.Key = def.Key
		row.Name = def.Name
		row.Repositories = def.Repositories
		row.BindingsJSON = marshalJSON(def.Bindings)
		row.Classification = def.Classification
		row.LimitsJSON = marshalJSON(def.Limits)
		row.ExtensionsJSON = marshalJSON(def.Extensions)
	case actionArchive:
		row.State = stateArchived
	case actionDelete:
		// Projects are referenced by scope records only; definitions stay.
	}
	row.Version = c.ExpectedVersion + 1
	row.UpdatedAt = now
	if err := writeProject(ctx, unit, row); err != nil {
		return "", "", err
	}
	if c.Action == actionDelete {
		if err := deleteProject(ctx, unit, row.ID); err != nil {
			return "", "", err
		}
		return marshalJSON(beforeDef), "", nil
	}
	return marshalJSON(beforeDef), marshalJSON(projectDef(row)), nil
}

// applyWorkerChange applies one worker change.
func (s *Service) applyWorkerChange(ctx context.Context, unit contract.Unit, c wireChange) (string, string, error) {
	def, err := decodeWorkerDef(c.Definition)
	if err != nil {
		return "", "", err
	}
	now := s.clock.Now().UTC()
	install := s.installOf(ctx, unit)
	if c.Action == actionCreate {
		row := &workerRow{
			ID: c.ID, Version: 1, InstallationID: install, OrganizationID: def.OrganizationID,
			Key: def.Key, Name: def.Name, Purpose: def.Purpose, Instructions: def.Instructions,
			SkillVersions: def.SkillVersions, BindingsJSON: marshalJSON(def.Bindings),
			ProfileJSON: profileOrNull(def.Profile), LimitsJSON: limitsOrNull(def.Limits),
			ExtensionsJSON: marshalJSON(def.Extensions), State: stateActive,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := insertWorker(ctx, unit, row); err != nil {
			return "", "", err
		}
		return "", marshalJSON(def), nil
	}
	row, err := fetchWorkerByID(ctx, unit, install, c.ID)
	if err != nil {
		return "", "", err
	}
	if row == nil {
		return "", "", notFound("worker %s does not exist", c.ID)
	}
	if c.ExpectedVersion != row.Version {
		return "", "", staleVersion("worker %s is at version %d", c.ID, row.Version)
	}
	beforeDef := workerDef(row)
	switch c.Action {
	case actionUpdate:
		row.OrganizationID = def.OrganizationID
		row.Key = def.Key
		row.Name = def.Name
		row.Purpose = def.Purpose
		row.Instructions = def.Instructions
		row.SkillVersions = def.SkillVersions
		row.BindingsJSON = marshalJSON(def.Bindings)
		row.ProfileJSON = profileOrNull(def.Profile)
		row.LimitsJSON = limitsOrNull(def.Limits)
		row.ExtensionsJSON = marshalJSON(def.Extensions)
	case actionArchive:
		row.State = stateArchived
	case actionDelete:
		if err := s.guardWorkerDelete(ctx, unit, row); err != nil {
			return "", "", err
		}
	}
	row.Version = c.ExpectedVersion + 1
	row.UpdatedAt = now
	if err := writeWorker(ctx, unit, row); err != nil {
		return "", "", err
	}
	if c.Action == actionDelete {
		if err := deleteWorker(ctx, unit, row.ID); err != nil {
			return "", "", err
		}
		return marshalJSON(beforeDef), "", nil
	}
	return marshalJSON(beforeDef), marshalJSON(workerDef(row)), nil
}

// guardWorkerDelete refuses deleting a worker that still designates an active
// organization chief role or team membership.
func (s *Service) guardWorkerDelete(ctx context.Context, unit contract.Unit, row *workerRow) error {
	var n int
	if err := unit.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM configuration_organizations WHERE installation_id = ? AND chief_id = ? AND state = ?",
		row.InstallationID, row.ID, stateActive).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return conflictFault("worker %s is still the designated chief of %d active organizations", row.ID, n)
	}
	return nil
}

// applyBindingChange applies one binding change.
func (s *Service) applyBindingChange(ctx context.Context, unit contract.Unit, c wireChange) (string, string, error) {
	def, err := decodeBindingDef(c.Definition)
	if err != nil {
		return "", "", err
	}
	now := s.clock.Now().UTC()
	install := s.installOf(ctx, unit)
	if c.Action == actionCreate {
		row := &bindingRow{
			ID: c.ID, Version: 1, InstallationID: install, ScopeJSON: marshalJSON(def.Scope),
			Kind: def.Kind, TargetID: def.TargetID, Permissions: def.Permissions,
			SourceScopeJSON: marshalJSON(def.SourceScope), Destinations: def.Destinations,
			State: stateActive, CreatedAt: now, UpdatedAt: now,
		}
		if err := insertBinding(ctx, unit, row); err != nil {
			return "", "", err
		}
		return "", marshalJSON(def), nil
	}
	row, err := fetchBindingByID(ctx, unit, install, c.ID)
	if err != nil {
		return "", "", err
	}
	if row == nil {
		return "", "", notFound("binding %s does not exist", c.ID)
	}
	if c.ExpectedVersion != row.Version {
		return "", "", staleVersion("binding %s is at version %d", c.ID, row.Version)
	}
	beforeDef := bindingDef(row)
	switch c.Action {
	case actionUpdate:
		row.ScopeJSON = marshalJSON(def.Scope)
		row.Kind = def.Kind
		row.TargetID = def.TargetID
		row.Permissions = def.Permissions
		row.SourceScopeJSON = marshalJSON(def.SourceScope)
		row.Destinations = def.Destinations
	case actionArchive:
		row.State = stateArchived
	case actionDelete:
	}
	row.Version = c.ExpectedVersion + 1
	row.UpdatedAt = now
	if err := writeBinding(ctx, unit, row); err != nil {
		return "", "", err
	}
	if c.Action == actionDelete {
		if err := deleteBinding(ctx, unit, row.ID); err != nil {
			return "", "", err
		}
		return marshalJSON(beforeDef), "", nil
	}
	return marshalJSON(beforeDef), marshalJSON(bindingDef(row)), nil
}

// applyProfileChange applies one execution-profile change.
func (s *Service) applyProfileChange(ctx context.Context, unit contract.Unit, c wireChange) (string, string, error) {
	def, err := decodeProfileDef(c.Definition)
	if err != nil {
		return "", "", err
	}
	now := s.clock.Now().UTC()
	install := s.installOf(ctx, unit)
	if c.Action == actionCreate {
		row := &profileRow{
			ID: c.ID, Version: 1, InstallationID: install, Executor: def.Executor,
			Model: def.Model, ConnectionID: def.ConnectionID, ProviderDestination: def.ProviderDestination,
			Capabilities: def.Capabilities, CostBoundJSON: marshalJSON(def.CostBound),
			Classification: def.Classification, ContextCapture: def.ContextCapture,
			State: stateActive, CreatedAt: now, UpdatedAt: now,
		}
		if err := insertProfile(ctx, unit, row); err != nil {
			return "", "", err
		}
		return "", marshalJSON(def), nil
	}
	row, err := fetchProfileByID(ctx, unit, install, c.ID)
	if err != nil {
		return "", "", err
	}
	if row == nil {
		return "", "", notFound("execution profile %s does not exist", c.ID)
	}
	if c.ExpectedVersion != row.Version {
		return "", "", staleVersion("execution profile %s is at version %d", c.ID, row.Version)
	}
	beforeDef := profileDef(row)
	switch c.Action {
	case actionUpdate:
		row.Executor = def.Executor
		row.Model = def.Model
		row.ConnectionID = def.ConnectionID
		row.ProviderDestination = def.ProviderDestination
		row.Capabilities = def.Capabilities
		row.CostBoundJSON = marshalJSON(def.CostBound)
		row.Classification = def.Classification
		row.ContextCapture = def.ContextCapture
	case actionArchive:
		row.State = stateArchived
	case actionDelete:
	}
	row.Version = c.ExpectedVersion + 1
	row.UpdatedAt = now
	if err := writeProfile(ctx, unit, row); err != nil {
		return "", "", err
	}
	if c.Action == actionDelete {
		if err := deleteProfile(ctx, unit, row.ID); err != nil {
			return "", "", err
		}
		return marshalJSON(beforeDef), "", nil
	}
	return marshalJSON(beforeDef), marshalJSON(profileDef(row)), nil
}

// checkInvariants re-proves the structural invariants after activation:
// every active organization designates an active worker as chief and the
// parent tree stays acyclic. The whole bundle rolls back on violation.
func (s *Service) checkInvariants(ctx context.Context, unit contract.Unit, install contract.ID) error {
	if err := s.invariantChiefsExist(ctx, unit, install); err != nil {
		return err
	}
	return s.invariantAcyclicOrgs(ctx, unit, install)
}

// invariantChiefsExist requires every active organization to name an active
// worker as its designated chief.
func (s *Service) invariantChiefsExist(ctx context.Context, unit contract.Unit, install contract.ID) error {
	rows, err := unit.QueryContext(ctx,
		"SELECT id, chief_id FROM configuration_organizations WHERE installation_id = ? AND state = ?",
		install, stateActive)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var pairs []chiefPair
	for rows.Next() {
		var p chiefPair
		if err := rows.Scan(&p.org, &p.chief); err != nil {
			return err
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range pairs {
		var state string
		err := unit.QueryRowContext(ctx,
			"SELECT state FROM configuration_workers WHERE id = ? AND installation_id = ?", p.chief, install).Scan(&state)
		if err != nil {
			return conflictFault("organization %s designates chief %s which does not exist", p.org, p.chief)
		}
		if state != stateActive {
			return conflictFault("organization %s designates chief %s which is %s", p.org, p.chief, state)
		}
	}
	return nil
}

type chiefPair struct {
	org   contract.ID
	chief contract.ID
}

// invariantAcyclicOrgs walks each organization's parent chain and refuses
// cycles.
func (s *Service) invariantAcyclicOrgs(ctx context.Context, unit contract.Unit, install contract.ID) error {
	rows, err := unit.QueryContext(ctx,
		"SELECT id, parent_id FROM configuration_organizations WHERE installation_id = ? AND state = ?",
		install, stateActive)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	parent := make(map[contract.ID]contract.ID)
	for rows.Next() {
		var id contract.ID
		var parentID sql.NullString
		if err := rows.Scan(&id, &parentID); err != nil {
			return err
		}
		if parentID.String != "" {
			parent[id] = contract.ID(parentID.String)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id := range parent {
		seen := map[contract.ID]bool{id: true}
		cur := parent[id]
		for cur != "" {
			if seen[cur] {
				return conflictFault("organization hierarchy contains a cycle at %s", cur)
			}
			seen[cur] = true
			next, ok := parent[cur]
			if !ok {
				break
			}
			cur = next
		}
	}
	return nil
}
