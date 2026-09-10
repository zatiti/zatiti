package configuration

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

// Change-schema validation, canonicalization and the owned-slice engine:
// validate resolves current state and dependencies without mutating; activate
// applies the exact sealed slice with optimistic version checks and records
// before/after lineage for rollback planning.

// changeDocOnce composes the Change validation document exactly once.
var changeDocOnce sync.Once

var changeDoc json.RawMessage

// changeSchemaDocument composes the $defs/Change oneOf with the full
// definition catalog so document-local $ref resolution works. Composed once;
// the source JSON is machine-pinned so a malformed catalog panics at first
// use rather than silently accepting untyped changes.
func changeSchemaDocument() json.RawMessage {
	changeDocOnce.Do(func() {
		var doc map[string]json.RawMessage
		if err := contract.DecodeStrict([]byte(schemaDefs), &doc); err != nil {
			panic("configuration: embedded definition catalog is not a valid object: " + err.Error())
		}
		// The catalog embeds one top-level {"$defs": {...}} object; the Change
		// oneOf lives inside it alongside every definition it references.
		defs, ok := doc["$defs"]
		if !ok {
			panic("configuration: embedded definition catalog has no $defs object")
		}
		var defMap map[string]json.RawMessage
		if err := contract.DecodeStrict(defs, &defMap); err != nil {
			panic("configuration: embedded definition catalog $defs is not an object: " + err.Error())
		}
		change, ok := defMap["Change"]
		if !ok {
			panic("configuration: embedded definition catalog has no Change def")
		}
		raw, err := json.Marshal(map[string]json.RawMessage{"$defs": defs, "oneOf": change})
		if err != nil {
			panic("configuration: change schema composition failed: " + err.Error())
		}
		changeDoc = raw
	})
	return changeDoc
}

// validateChangeDef validates one staged change against the Change oneOf:
// kind discriminator, action enum and a complete typed definition of the
// registered concrete kind.
func validateChangeDef(change wireChange) error {
	raw, err := json.Marshal(change)
	if err != nil {
		return internalError("change encoding failed")
	}
	if err := contract.ValidateSchema(changeSchemaDocument(), raw); err != nil {
		return invalidInput("staged change does not match the Change schema: %v", err)
	}
	return nil
}

// canonicalizeChange returns the canonical JSON of one change so that
// semantically equal candidates hash equally.
func canonicalizeChange(change wireChange) ([]byte, error) {
	raw, err := json.Marshal(change)
	if err != nil {
		return nil, internalError("change encoding failed")
	}
	canon, err := contract.Canonicalize(raw)
	if err != nil {
		return nil, invalidInput("change is not canonicalizable: %v", err)
	}
	return canon, nil
}

// computeCandidateDigest hashes the canonical forms of every change in
// staged order into the 64-hex candidate digest. Forms are joined with the
// ASCII unit separator, which canonical JSON cannot contain.
func computeCandidateDigest(changes []wireChange) (string, error) {
	flat := make([]byte, 0, 4096)
	for _, c := range changes {
		canon, err := canonicalizeChange(c)
		if err != nil {
			return "", err
		}
		flat = append(flat, canon...)
		flat = append(flat, 0x1f)
	}
	return string(contract.Hash(flat)), nil
}

// appendPeerDeps merges peer-reported dependencies, dropping duplicates.
func appendPeerDeps(dst, src []wireRef) []wireRef {
	seen := make(map[wireRef]bool, len(dst))
	for _, r := range dst {
		seen[r] = true
	}
	for _, r := range src {
		if !seen[r] {
			seen[r] = true
			dst = append(dst, r)
		}
	}
	return dst
}

// objectVersion extracts the version field of a serialized definition.
func objectVersion(raw string) (int64, bool) {
	if raw == "" {
		return 0, false
	}
	var def struct {
		Version int64 `json:"version"`
	}
	if err := json.Unmarshal([]byte(raw), &def); err != nil || def.Version < 1 {
		return 0, false
	}
	return def.Version, true
}

// objectVersionOf returns the version encoded in raw, or fallback when raw is
// empty or unreadable.
func objectVersionOf(raw string, fallback int64) int64 {
	if v, ok := objectVersion(raw); ok {
		return v
	}
	return fallback
}

// objectVersionOrOne returns the definition version of a change, defaulting
// to 1 for events.
func objectVersionOrOne(c wireChange) int64 {
	var def struct {
		Version int64 `json:"version"`
	}
	if len(c.Definition) > 0 {
		if err := json.Unmarshal(c.Definition, &def); err == nil && def.Version >= 1 {
			return def.Version
		}
	}
	return 1
}

// revisionObjectRefs converts recorded lineage into activated version refs.
func revisionObjectRefs(objects []revisionObject) []wireRef {
	refs := make([]wireRef, 0, len(objects))
	for _, o := range objects {
		if o.AfterJSON == "" {
			continue
		}
		if v, ok := objectVersion(o.AfterJSON); ok {
			refs = append(refs, wireRef{ID: o.ObjectID, Version: v})
		}
	}
	return refs
}

// sliceEntry is the projected state of one object this candidate touches, so
// intra-bundle references (a chief created in the same bundle as its
// organization) resolve without a pre-existing row. Worker entries carry the
// definition's home organization so membership checks resolve intra-bundle.
type sliceEntry struct {
	kind    string
	version int64
	state   string
	org     contract.ID
}

// sliceView projects every create/update/archive in the candidate: object
// identity to projected version/state after the bundle applies.
type sliceView struct {
	objects map[contract.ID]*sliceEntry
}

// newSliceView projects the candidate's creates and version advances.
func newSliceView(changes []wireChange) (*sliceView, error) {
	v := &sliceView{objects: make(map[contract.ID]*sliceEntry)}
	for _, c := range changes {
		if !ownedKinds[c.Kind] {
			continue
		}
		switch c.Action {
		case actionCreate:
			e := &sliceEntry{kind: c.Kind, version: 1, state: stateActive}
			if c.Kind == kindWorker {
				var def wireWorker
				if err := contract.DecodeStrict(c.Definition, &def); err == nil {
					e.org = def.OrganizationID
				}
			}
			v.objects[c.ID] = e
		case actionUpdate:
			if e, ok := v.objects[c.ID]; ok {
				e.version = c.ExpectedVersion + 1
				if c.Kind == kindWorker {
					var def wireWorker
					if err := contract.DecodeStrict(c.Definition, &def); err == nil {
						e.org = def.OrganizationID
					}
				}
			} else {
				e := &sliceEntry{kind: c.Kind, version: c.ExpectedVersion + 1, state: stateActive}
				if c.Kind == kindWorker {
					var def wireWorker
					if err := contract.DecodeStrict(c.Definition, &def); err == nil {
						e.org = def.OrganizationID
					}
				}
				v.objects[c.ID] = e
			}
		case actionArchive:
			if e, ok := v.objects[c.ID]; ok {
				e.version = c.ExpectedVersion + 1
				e.state = stateArchived
			} else {
				v.objects[c.ID] = &sliceEntry{kind: c.Kind, version: c.ExpectedVersion + 1, state: stateArchived}
			}
		}
	}
	return v, nil
}

// sortRefs orders dependencies deterministically by id then version.
func sortRefs(refs []wireRef) {
	for i := 1; i < len(refs); i++ {
		for j := i; j > 0 && refLess(refs[j], refs[j-1]); j-- {
			refs[j], refs[j-1] = refs[j-1], refs[j]
		}
	}
}

func refLess(a, b wireRef) bool {
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.Version < b.Version
}

// sliceValidator is the per-run validation context: one per validateOwnedSlice
// invocation. Diagnostics accumulate on out; dependencies resolve to
// pre-existing rows only (intra-bundle creates are not external dependencies).
type sliceValidator struct {
	s       *Service
	ctx     context.Context
	unit    contract.Unit
	install contract.ID
	view    *sliceView
	out     *wireValidation
	deps    map[wireRef]bool
	keys    map[string]contract.ID // install|kind|org|key -> id for intra-bundle key collisions
}

func (v *sliceValidator) add(dep wireRef) {
	if dep.Version >= 1 {
		v.deps[dep] = true
	}
}

func (v *sliceValidator) diag(path, code, message string) {
	v.out.Diagnostics = append(v.out.Diagnostics, errorDiagnostic(path, code, message))
}

func (v *sliceValidator) warn(path, code, message string) {
	v.out.Diagnostics = append(v.out.Diagnostics, warnDiagnostic(path, code, message))
}

// keyTaken reports whether the scope-qualified key is used by a different
// object in current state or elsewhere in this candidate.
func (v *sliceValidator) keyTaken(install, org contract.ID, kind, key string, self contract.ID) (bool, error) {
	if key == "" {
		return false, nil
	}
	path := sliceKeyPath(install, org, kind, key)
	if id, ok := v.keys[path]; ok && id != self {
		return true, nil
	}
	switch kind {
	case kindOrganization:
		row, err := fetchOrgByKey(v.ctx, v.unit, install, key)
		if err != nil {
			return false, err
		}
		return row != nil && row.ID != self, nil
	case kindTeam:
		row, err := fetchTeamByKey(v.ctx, v.unit, install, org, key)
		if err != nil {
			return false, err
		}
		return row != nil && row.ID != self, nil
	case kindProject:
		row, err := fetchProjectByKey(v.ctx, v.unit, install, org, key)
		if err != nil {
			return false, err
		}
		return row != nil && row.ID != self, nil
	case kindWorker:
		row, err := fetchWorkerByKey(v.ctx, v.unit, install, org, key)
		if err != nil {
			return false, err
		}
		return row != nil && row.ID != self, nil
	}
	return false, nil
}

func sliceKeyPath(install, org contract.ID, kind, key string) string {
	return string(install) + "|" + kind + "|" + string(org) + "|" + key
}

// validateOwnedSlice validates the configuration-owned changes against
// current effective state plus the candidate's own projected state:
// concrete definition schemas, identity allocation rules, existence and
// version fencing, scope collisions and dependency identities. No live
// changes and no network.
func (s *Service) validateOwnedSlice(ctx context.Context, unit contract.Unit, scope wireScope, changes []wireChange) (wireValidation, error) {
	out := wireValidation{
		Diagnostics:  []wireDiagnostic{},
		Requirements: []wireRequirement{},
		Dependencies: []wireRef{},
	}
	view, err := newSliceView(changes)
	if err != nil {
		return out, err
	}
	v := &sliceValidator{
		s:       s,
		ctx:     ctx,
		unit:    unit,
		install: scope.InstallationID,
		view:    view,
		out:     &out,
		deps:    make(map[wireRef]bool),
		keys:    make(map[string]contract.ID),
	}
	seen := make(map[string]bool, len(changes))
	for i, c := range changes {
		if !ownedKinds[c.Kind] {
			continue // other owners validate their own slices
		}
		// One change per object per bundle: version fencing makes a second
		// touch of the same object unappliable, and the revision-object
		// lineage primary key forbids it.
		dup := c.Kind + "|" + string(c.ID)
		if seen[dup] {
			v.diag("changes["+strconv.Itoa(i)+"]", "duplicate_change",
				c.Kind+" "+string(c.ID)+" is changed more than once in this candidate")
			continue
		}
		seen[dup] = true
		path := "changes[" + strconv.Itoa(i) + "]"
		if err := validateChangeDef(c); err != nil {
			return out, err
		}
		if err := s.checkInstallation(unit, scope.InstallationID); err != nil {
			return out, err
		}
		switch c.Kind {
		case kindOrganization:
			v.validateOrgChange(path, c)
		case kindTeam:
			v.validateTeamChange(path, c)
		case kindProject:
			v.validateProjectChange(path, c)
		case kindWorker:
			v.validateWorkerChange(path, c)
		case kindBinding:
			v.validateBindingChange(path, c)
		case kindExecutionProfile:
			v.validateProfileChange(path, c)
		}
	}
	for ref := range v.deps {
		out.Dependencies = append(out.Dependencies, ref)
	}
	sortRefs(out.Dependencies)
	return out, nil
}
