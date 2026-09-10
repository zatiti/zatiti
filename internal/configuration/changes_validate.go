package configuration

import (
	"strconv"

	"github.com/zatiti/zatiti/internal/contract"
)

// Per-kind validators for the configuration-owned slice. Each resolves the
// change against current effective state and the candidate's projected state;
// semantic failures become error-severity diagnostics, never panics.

// refActive resolves one owned reference: a live active row or an active
// projection inside this candidate. Dependencies record live rows only.
func (v *sliceValidator) refActive(kind string, id contract.ID, live func(contract.ID) (int64, string, error)) bool {
	version, state, err := live(id)
	if err != nil {
		return false
	}
	if version >= 1 {
		if state == stateActive {
			v.add(wireRef{ID: id, Version: version})
			return true
		}
		return false
	}
	if e, ok := v.view.objects[id]; ok && e.kind == kind {
		return e.state == stateActive
	}
	return false
}

// workerInOrg resolves one worker reference that must be an active member of
// the organization: live rows and intra-bundle creations both qualify, and a
// worker homed in another organization is refused — cross-scope references
// never pass without an explicit binding.
func (v *sliceValidator) workerInOrg(field string, wid contract.ID, org contract.ID) bool {
	row, err := fetchWorkerByID(v.ctx, v.unit, v.install, wid)
	if err != nil {
		v.diag(field, "storage", err.Error())
		return false
	}
	if row != nil {
		if row.State != stateActive {
			v.diag(field, "unknown_reference",
				"worker "+string(wid)+" is not an active worker in this installation")
			return false
		}
		if row.OrganizationID != org {
			v.diag(field, "cross_organization_reference",
				"worker "+string(wid)+" does not belong to organization "+string(org))
			return false
		}
		v.add(wireRef{ID: wid, Version: row.Version})
		return true
	}
	if e, ok := v.view.objects[wid]; ok && e.kind == kindWorker && e.state == stateActive && e.org == org {
		return true
	}
	v.diag(field, "unknown_reference",
		"worker "+string(wid)+" is not an active worker in this organization")
	return false
}

// validateOrgChange checks one organization change against current state.
func (v *sliceValidator) validateOrgChange(path string, c wireChange) {
	var def wireOrganization
	if err := contract.DecodeStrict(c.Definition, &def); err != nil {
		v.diag(path+".definition", "decode", "organization definition is not decodable")
		return
	}
	if c.Action == actionCreate {
		if c.ExpectedVersion != 0 {
			v.diag(path+".expected_version", "expected_version", "organization create must carry expected_version 0")
			return
		}
		if def.Version != 1 {
			v.diag(path+".definition.version", "version", "organization create definition must carry version 1")
			return
		}
		if def.ID != c.ID {
			v.diag(path+".definition.id", "identity_mismatch", "definition identity does not match the change")
			return
		}
		// Identity collision consults live rows only: the candidate's own
		// projection must never reject its own create.
		row, err := fetchOrgByID(v.ctx, v.unit, v.install, c.ID)
		if err != nil {
			v.diag(path, "storage", err.Error())
			return
		}
		if row != nil {
			v.diag(path+".id", "identity_collision", "organization "+string(c.ID)+" already exists")
			return
		}
		taken, err := v.keyTaken(v.install, "", kindOrganization, def.Key, c.ID)
		if err != nil {
			v.diag(path+".key", "storage", err.Error())
			return
		}
		if taken {
			v.diag(path+".key", "key_collision", "organization key "+def.Key+" is already used in this installation")
			return
		}
		v.keys[sliceKeyPath(v.install, "", kindOrganization, def.Key)] = c.ID
		if def.ParentID != nil && *def.ParentID != "" {
			if *def.ParentID == c.ID {
				v.diag(path+".definition.parent_id", "cycle", "organization cannot be its own parent")
				return
			}
			if !v.refActive(kindOrganization, *def.ParentID, func(id contract.ID) (int64, string, error) {
				row, err := fetchOrgByID(v.ctx, v.unit, v.install, id)
				if err != nil || row == nil {
					return 0, "", err
				}
				return row.Version, row.State, nil
			}) {
				v.diag(path+".definition.parent_id", "unknown_reference",
					"parent organization "+string(*def.ParentID)+" is not active in this installation")
				return
			}
		}
		if !v.refActive(kindWorker, def.ChiefID, func(id contract.ID) (int64, string, error) {
			row, err := fetchWorkerByID(v.ctx, v.unit, v.install, id)
			if err != nil || row == nil {
				return 0, "", err
			}
			return row.Version, row.State, nil
		}) {
			v.diag(path+".definition.chief_id", "unknown_reference",
				"designated chief "+string(def.ChiefID)+" is not an active worker in this installation")
		}
		return
	}
	// update / archive / delete against a live row.
	row, err := fetchOrgByID(v.ctx, v.unit, v.install, c.ID)
	if err != nil {
		v.diag(path, "storage", err.Error())
		return
	}
	if row == nil {
		if _, ok := v.view.objects[c.ID]; ok {
			v.diag(path+".id", "inactive_target", "organization "+string(c.ID)+" is created in this same bundle and cannot be revised again in it")
		} else {
			v.diag(path+".id", "unknown_reference", "organization "+string(c.ID)+" does not exist")
		}
		return
	}
	v.add(wireRef{ID: row.ID, Version: row.Version})
	if row.State != stateActive {
		v.diag(path+".id", "inactive_target", "organization "+string(c.ID)+" is "+row.State+" and refuses changes")
		return
	}
	if c.ExpectedVersion != row.Version {
		v.diag(path+".expected_version", "stale_version",
			"organization "+string(c.ID)+" is at version "+strconv.FormatInt(row.Version, 10))
		return
	}
	if def.ID != c.ID {
		v.diag(path+".definition.id", "identity_mismatch", "definition identity does not match the change")
		return
	}
	if c.Action == actionUpdate {
		if def.Version != c.ExpectedVersion+1 {
			v.diag(path+".definition.version", "version",
				"organization update definition must carry the post-apply version "+strconv.FormatInt(c.ExpectedVersion+1, 10))
			return
		}
		if def.ParentID != nil && *def.ParentID == c.ID {
			v.diag(path+".definition.parent_id", "cycle", "organization cannot be its own parent")
			return
		}
		if !v.refActive(kindWorker, def.ChiefID, func(id contract.ID) (int64, string, error) {
			w, err := fetchWorkerByID(v.ctx, v.unit, v.install, id)
			if err != nil || w == nil {
				return 0, "", err
			}
			return w.Version, w.State, nil
		}) {
			v.diag(path+".definition.chief_id", "unknown_reference",
				"designated chief "+string(def.ChiefID)+" is not an active worker in this installation")
		}
	}
	if c.Action == actionArchive {
		v.warn(path, "restrictive_side_effect",
			"archive disables new admissions when applied; definitions and obligations are retained")
	}
}

// validateTeamChange checks one team change against current state.
func (v *sliceValidator) validateTeamChange(path string, c wireChange) {
	var def wireTeam
	if err := contract.DecodeStrict(c.Definition, &def); err != nil {
		v.diag(path+".definition", "decode", "team definition is not decodable")
		return
	}
	if !v.refActive(kindOrganization, def.OrganizationID, func(id contract.ID) (int64, string, error) {
		row, err := fetchOrgByID(v.ctx, v.unit, v.install, id)
		if err != nil || row == nil {
			return 0, "", err
		}
		return row.Version, row.State, nil
	}) {
		v.diag(path+".definition.organization_id", "unknown_reference",
			"organization "+string(def.OrganizationID)+" is not active in this installation")
		return
	}
	for _, wid := range def.WorkerIDs {
		if !v.workerInOrg(path+".definition.worker_ids", wid, def.OrganizationID) {
			return
		}
	}
	if c.Action == actionCreate {
		if c.ExpectedVersion != 0 {
			v.diag(path+".expected_version", "expected_version", "team create must carry expected_version 0")
			return
		}
		if def.Version != 1 || def.ID != c.ID {
			v.diag(path+".definition", "identity_mismatch", "team create definition must carry version 1 and the change identity")
			return
		}
		taken, err := v.keyTaken(v.install, def.OrganizationID, kindTeam, def.Key, c.ID)
		if err != nil {
			v.diag(path+".key", "storage", err.Error())
			return
		}
		if taken {
			v.diag(path+".definition.key", "key_collision", "team key "+def.Key+" is already used in this organization")
			return
		}
		v.keys[sliceKeyPath(v.install, def.OrganizationID, kindTeam, def.Key)] = c.ID
		return
	}
	v.validateExisting(path, c, def.ID, def.Version, kindTeam, func() (int64, string, error) {
		row, err := fetchTeamByID(v.ctx, v.unit, v.install, c.ID)
		if err != nil || row == nil {
			return 0, "", err
		}
		return row.Version, row.State, nil
	})
}

// validateProjectChange checks one project change against current state.
func (v *sliceValidator) validateProjectChange(path string, c wireChange) {
	var def wireProject
	if err := contract.DecodeStrict(c.Definition, &def); err != nil {
		v.diag(path+".definition", "decode", "project definition is not decodable")
		return
	}
	if !v.refActive(kindOrganization, def.OrganizationID, func(id contract.ID) (int64, string, error) {
		row, err := fetchOrgByID(v.ctx, v.unit, v.install, id)
		if err != nil || row == nil {
			return 0, "", err
		}
		return row.Version, row.State, nil
	}) {
		v.diag(path+".definition.organization_id", "unknown_reference",
			"organization "+string(def.OrganizationID)+" is not active in this installation")
		return
	}
	for _, b := range def.Bindings {
		if !v.refActive(kindBinding, b, func(id contract.ID) (int64, string, error) {
			row, err := fetchBindingByID(v.ctx, v.unit, v.install, id)
			if err != nil || row == nil {
				return 0, "", err
			}
			return row.Version, row.State, nil
		}) {
			v.diag(path+".definition.bindings", "unknown_reference",
				"binding "+string(b)+" is not active in this installation")
			return
		}
	}
	if c.Action == actionCreate {
		if c.ExpectedVersion != 0 {
			v.diag(path+".expected_version", "expected_version", "project create must carry expected_version 0")
			return
		}
		if def.Version != 1 || def.ID != c.ID {
			v.diag(path+".definition", "identity_mismatch", "project create definition must carry version 1 and the change identity")
			return
		}
		taken, err := v.keyTaken(v.install, def.OrganizationID, kindProject, def.Key, c.ID)
		if err != nil {
			v.diag(path+".key", "storage", err.Error())
			return
		}
		if taken {
			v.diag(path+".definition.key", "key_collision", "project key "+def.Key+" is already used in this organization")
			return
		}
		v.keys[sliceKeyPath(v.install, def.OrganizationID, kindProject, def.Key)] = c.ID
		return
	}
	v.validateExisting(path, c, def.ID, def.Version, kindProject, func() (int64, string, error) {
		row, err := fetchProjectByID(v.ctx, v.unit, v.install, c.ID)
		if err != nil || row == nil {
			return 0, "", err
		}
		return row.Version, row.State, nil
	})
}

// validateWorkerChange checks one worker change against current state.
func (v *sliceValidator) validateWorkerChange(path string, c wireChange) {
	var def wireWorker
	if err := contract.DecodeStrict(c.Definition, &def); err != nil {
		v.diag(path+".definition", "decode", "worker definition is not decodable")
		return
	}
	if !v.refActive(kindOrganization, def.OrganizationID, func(id contract.ID) (int64, string, error) {
		row, err := fetchOrgByID(v.ctx, v.unit, v.install, id)
		if err != nil || row == nil {
			return 0, "", err
		}
		return row.Version, row.State, nil
	}) {
		v.diag(path+".definition.organization_id", "unknown_reference",
			"organization "+string(def.OrganizationID)+" is not active in this installation")
		return
	}
	for _, ref := range def.SkillVersions {
		if ref.Version < 1 {
			v.diag(path+".definition.skill_versions", "unknown_reference",
				"skill "+string(ref.ID)+" carries no pinned version")
			continue
		}
		// Skills belong to the skills owner; the ref becomes a dependency.
		if _, live := v.view.objects[ref.ID]; !live {
			v.add(ref)
		}
	}
	for _, b := range def.Bindings {
		if !v.refActive(kindBinding, b, func(id contract.ID) (int64, string, error) {
			row, err := fetchBindingByID(v.ctx, v.unit, v.install, id)
			if err != nil || row == nil {
				return 0, "", err
			}
			return row.Version, row.State, nil
		}) {
			v.diag(path+".definition.bindings", "unknown_reference",
				"binding "+string(b)+" is not active in this installation")
			return
		}
	}
	if c.Action == actionCreate {
		if c.ExpectedVersion != 0 {
			v.diag(path+".expected_version", "expected_version", "worker create must carry expected_version 0")
			return
		}
		if def.Version != 1 || def.ID != c.ID {
			v.diag(path+".definition", "identity_mismatch", "worker create definition must carry version 1 and the change identity")
			return
		}
		taken, err := v.keyTaken(v.install, def.OrganizationID, kindWorker, def.Key, c.ID)
		if err != nil {
			v.diag(path+".key", "storage", err.Error())
			return
		}
		if taken {
			v.diag(path+".definition.key", "key_collision", "worker key "+def.Key+" is already used in this organization")
			return
		}
		v.keys[sliceKeyPath(v.install, def.OrganizationID, kindWorker, def.Key)] = c.ID
		return
	}
	v.validateExisting(path, c, def.ID, def.Version, kindWorker, func() (int64, string, error) {
		row, err := fetchWorkerByID(v.ctx, v.unit, v.install, c.ID)
		if err != nil || row == nil {
			return 0, "", err
		}
		return row.Version, row.State, nil
	})
}

// validateBindingChange checks one binding change against current state.
func (v *sliceValidator) validateBindingChange(path string, c wireChange) {
	var def wireBinding
	if err := contract.DecodeStrict(c.Definition, &def); err != nil {
		v.diag(path+".definition", "decode", "binding definition is not decodable")
		return
	}
	if def.Kind == "worker" && !v.refActive(kindWorker, def.TargetID, func(id contract.ID) (int64, string, error) {
		w, err := fetchWorkerByID(v.ctx, v.unit, v.install, id)
		if err != nil || w == nil {
			return 0, "", err
		}
		return w.Version, w.State, nil
	}) {
		v.diag(path+".definition.target_id", "unknown_reference",
			"bound worker "+string(def.TargetID)+" is not active in this installation")
		return
	}
	if c.Action == actionCreate {
		if c.ExpectedVersion != 0 {
			v.diag(path+".expected_version", "expected_version", "binding create must carry expected_version 0")
			return
		}
		if def.Version != 1 || def.ID != c.ID {
			v.diag(path+".definition", "identity_mismatch", "binding create definition must carry version 1 and the change identity")
			return
		}
		return
	}
	v.validateExisting(path, c, def.ID, def.Version, kindBinding, func() (int64, string, error) {
		row, err := fetchBindingByID(v.ctx, v.unit, v.install, c.ID)
		if err != nil || row == nil {
			return 0, "", err
		}
		return row.Version, row.State, nil
	})
}

// validateProfileChange checks one execution-profile change against current
// state and names the executable prerequisites (resolved connection and priced
// cost bound) that executable use requires.
func (v *sliceValidator) validateProfileChange(path string, c wireChange) {
	var def wireExecutionProfile
	if err := contract.DecodeStrict(c.Definition, &def); err != nil {
		v.diag(path+".definition", "decode", "execution profile definition is not decodable")
		return
	}
	v.out.Requirements = append(v.out.Requirements, requirement(contract.CodePrerequisiteMissing,
		"execution profile "+string(def.ID)+" requires a resolved connection "+
			string(def.ConnectionID)+" and a priced cost bound before executable use",
		string(def.ConnectionID)))
	if c.Action == actionCreate {
		if c.ExpectedVersion != 0 {
			v.diag(path+".expected_version", "expected_version", "execution profile create must carry expected_version 0")
			return
		}
		if def.Version != 1 || def.ID != c.ID {
			v.diag(path+".definition", "identity_mismatch", "execution profile create definition must carry version 1 and the change identity")
			return
		}
		return
	}
	v.validateExisting(path, c, def.ID, def.Version, kindExecutionProfile, func() (int64, string, error) {
		row, err := fetchProfileByID(v.ctx, v.unit, v.install, c.ID)
		if err != nil || row == nil {
			return 0, "", err
		}
		return row.Version, row.State, nil
	})
}

// validateExisting enforces the shared identity/version rules for
// update/archive/delete of team, project, worker, binding and profile
// changes: existence, activity, exact expected_version and post-apply
// definition version continuity.
func (v *sliceValidator) validateExisting(path string, c wireChange, defID contract.ID, defVersion int64, resource string, lookup func() (int64, string, error)) {
	if defID != c.ID {
		v.diag(path+".definition.id", "identity_mismatch", "definition identity does not match the change")
		return
	}
	version, state, err := lookup()
	if err != nil {
		v.diag(path, "storage", err.Error())
		return
	}
	if version == 0 {
		v.diag(path+".id", "unknown_reference", resource+" "+string(c.ID)+" does not exist")
		return
	}
	if state != stateActive {
		v.diag(path+".id", "inactive_target", resource+" "+string(c.ID)+" is "+state+" and refuses changes")
		return
	}
	if c.ExpectedVersion != version {
		v.diag(path+".expected_version", "stale_version",
			resource+" "+string(c.ID)+" is at version "+strconv.FormatInt(version, 10))
		return
	}
	if c.Action == actionUpdate && defVersion != c.ExpectedVersion+1 {
		v.diag(path+".definition.version", "version",
			resource+" update definition must carry the post-apply version "+strconv.FormatInt(c.ExpectedVersion+1, 10))
		return
	}
	if c.Action == actionArchive {
		v.warn(path, "restrictive_side_effect",
			"archive disables new admissions when applied; definitions and obligations are retained")
	}
}
