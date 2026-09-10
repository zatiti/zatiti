package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/zatiti/zatiti/internal/contract"
)

// skill.import as a registered local IO operation.
//
// Prepare runs inside the caller's transaction: it validates the request,
// pins the artifact through the artifacts owner and seals a trusted plan.
// Perform runs outside any transaction: it opens the pinned bytes from the
// blob store, verifies their digest, runs the bounded archive validation and
// stages every regular file. Finish runs inside a fresh transaction: it
// rechecks authority, generation and scope, resolves and cycle-checks the
// declared dependencies, publishes the immutable version and registers the
// instruction artifact. Nothing extracted from an archive is ever executed.

// manifestFile records one staged archive file. StagingRef is trusted
// in-memory state produced by Perform and consumed by Finish.
type manifestFile struct {
	Path       string          `json:"path"`
	Digest     contract.Digest `json:"digest"`
	Size       int64           `json:"size"`
	StagingRef string          `json:"staging_ref"`
}

// importManifest is the handoff document Perform produces and Finish
// consumes. It doubles as the stored manifest_json: the full content
// identity of the imported skill, enough to reconstruct every byte.
type importManifest struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	License         string          `json:"license"`
	AllowedTools    []string        `json:"allowed_tools"`
	Dependencies    []dependencyRef `json:"dependencies"`
	InstructionPath string          `json:"instruction_path"`
	ContentDigest   contract.Digest `json:"content_digest"`
	Files           []manifestFile  `json:"files"`
}

// importPrepared is the sealed plan payload produced by Prepare.
type importPrepared struct {
	Scope          wireScope       `json:"scope"`
	ArtifactID     contract.ID     `json:"artifact_id"`
	ArtifactDigest contract.Digest `json:"artifact_digest"`
	ArtifactSize   int64           `json:"artifact_size"`
	Source         string          `json:"source"`
	License        string          `json:"license"`
	DraftID        *contract.ID    `json:"draft_id,omitempty"`
}

// Prepare implements contract.LocalIO for skill.import. It performs no
// local IO of its own: it validates and pins, so that Perform can never run
// on an unpinned request.
func (s *Service) Prepare(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.IOPlan, error) {
	in, err := decodeInto[importInput](s, "skill.import", invocation.Input)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if unit.ReadOnly() {
		return contract.IOPlan{}, invalidInput("operation skill.import is a mutation and requires a write transaction")
	}
	if err := checkInstallation(unit, in.Scope); err != nil {
		return contract.IOPlan{}, err
	}
	if in.Source == "" {
		return contract.IOPlan{}, invalidInput("skill import requires a source")
	}

	// Pin the artifact through the artifacts owner. The metadata must show
	// an available artifact whose digest matches the request's pinned
	// digest; compressed size is bounded before any read.
	meta, err := s.callOwner(ctx, unit, "_artifacts.metadata", map[string]any{
		"scope":     in.Scope.toContract(),
		"artifacts": []wireArtifactRef{in.Artifact},
	})
	if err != nil {
		return contract.IOPlan{}, err
	}
	var listed struct {
		Artifacts []struct {
			ID     contract.ID     `json:"id"`
			Digest contract.Digest `json:"digest"`
			Size   int64           `json:"size"`
			State  string          `json:"state"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(meta, &listed); err != nil {
		return contract.IOPlan{}, faultWrap(internalError("artifact metadata decoding failed"), err)
	}
	if len(listed.Artifacts) != 1 {
		return contract.IOPlan{}, notFound("import artifact %s was not found", in.Artifact.ID)
	}
	artifact := listed.Artifacts[0]
	if artifact.State != "available" {
		return contract.IOPlan{}, conflictFault("import artifact %s is %s, not available", artifact.ID, artifact.State)
	}
	if artifact.Digest != in.Artifact.Digest {
		return contract.IOPlan{}, conflictFault("import artifact %s digest does not match the pinned digest", artifact.ID)
	}
	if artifact.Size > maxArchiveCompressed {
		return contract.IOPlan{}, invalidInput("skill archive exceeds the %d MiB compressed limit", maxArchiveCompressed>>20)
	}

	preparedRaw, err := marshalJSON(importPrepared{
		Scope:          in.Scope,
		ArtifactID:     artifact.ID,
		ArtifactDigest: artifact.Digest,
		ArtifactSize:   artifact.Size,
		Source:         in.Source,
		License:        in.License,
		DraftID:        in.DraftID,
	})
	if err != nil {
		return contract.IOPlan{}, err
	}
	return contract.IOPlan{
		ID:               s.ids.New(),
		Owner:            ownerName,
		Invocation:       invocation,
		Actor:            unit.Actor(),
		Scope:            unit.Scope(),
		Generation:       unit.Generation(),
		ExpectedVersions: map[contract.ID]contract.Version{},
		Prepared:         json.RawMessage(preparedRaw),
	}, nil
}

// Perform implements contract.LocalIO for skill.import. It runs outside any
// transaction against the exact trusted plan and returns either a manifest
// or a fault; on fault every staged blob is removed best-effort.
func (s *Service) Perform(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	if plan.Owner != ownerName || plan.Invocation.Operation != "skill.import" {
		return contract.IOResult{}, permissionDenied("local IO plan does not belong to skills skill.import")
	}
	prepared, err := decodeJSONColumn[importPrepared](string(plan.Prepared))
	if err != nil {
		return contract.IOResult{}, faultWrap(internalError("local IO plan payload is malformed"), err)
	}

	manifest, staged, fault := s.performImport(ctx, plan, prepared)
	if fault != nil {
		// A failed import abandons its staged bytes; the blob store owns
		// their cleanup. A successful import leaves the staging references
		// intact — Finish publishes from them inside the transaction.
		for _, ref := range staged {
			_ = s.blobs.RemoveStaged(ctx, ref)
		}
		return contract.IOResult{Fault: fault}, nil
	}
	raw, err := marshalJSON(manifest)
	if err != nil {
		return contract.IOResult{}, err
	}
	return contract.IOResult{Data: json.RawMessage(raw)}, nil
}

// performImport opens the pinned archive bytes, validates the zip, parses
// the SKILL.md frontmatter, stages every regular file and seals the manifest.
func (s *Service) performImport(ctx context.Context, plan contract.IOPlan, prepared importPrepared) (importManifest, []string, *contract.Fault) {
	// Open exactly the pinned size from the content-addressed store; the
	// extra byte read catches a body that grew past its metadata.
	rc, err := s.blobs.Open(ctx, prepared.ArtifactDigest, 0, prepared.ArtifactSize+1)
	if err != nil {
		return importManifest{}, nil, conflictFault("pinned artifact bytes cannot be opened: %v", err)
	}
	defer func() { _ = rc.Close() }()
	zipData, err := io.ReadAll(io.LimitReader(rc, prepared.ArtifactSize+1))
	if err != nil {
		return importManifest{}, nil, conflictFault("pinned artifact bytes cannot be read: %v", err)
	}
	if int64(len(zipData)) != prepared.ArtifactSize {
		return importManifest{}, nil, conflictFault("pinned artifact size does not match its pinned metadata")
	}
	if contract.Hash(zipData) != prepared.ArtifactDigest {
		return importManifest{}, nil, conflictFault("pinned artifact bytes do not match their digest")
	}

	files, err := extractSkillArchive(zipData)
	if err != nil {
		return importManifest{}, nil, faultOf(err)
	}

	var skillFile *archiveFile
	for i := range files {
		if files[i].Path == skillManifestName {
			skillFile = &files[i]
			break
		}
	}
	if skillFile == nil {
		return importManifest{}, nil, invalidInput("skill archive does not contain a root %s", skillManifestName)
	}
	fm, _, err := parseFrontmatter(skillFile.Data)
	if err != nil {
		return importManifest{}, nil, faultOf(err)
	}

	// Stage every regular file. The store computes its own digest; it must
	// match the bytes we validated, or the plan is abandoned.
	staged := make([]string, 0, len(files))
	entries := make([]manifestFile, 0, len(files))
	for _, f := range files {
		ref, digest, size, err := s.blobs.Stage(ctx, bytes.NewReader(f.Data), int64(len(f.Data)))
		if err != nil {
			return importManifest{}, staged, conflictFault("blob staging failed: %v", err)
		}
		if digest != contract.Hash(f.Data) || size != int64(len(f.Data)) {
			staged = append(staged, ref)
			return importManifest{}, staged, conflictFault("blob store digest does not match staged bytes")
		}
		staged = append(staged, ref)
		entries = append(entries, manifestFile{Path: f.Path, Digest: digest, Size: size, StagingRef: ref})
	}

	// The content digest pins the whole skill content: SHA-256 over the
	// sorted (digest, path, size) manifest, independent of zip entry order.
	var manifestBytes bytes.Buffer
	manifestBytes.WriteString("[")
	for i, e := range entries {
		if i > 0 {
			manifestBytes.WriteString(",")
		}
		fmt.Fprintf(&manifestBytes, `{"digest":%q,"path":%q,"size":%d}`, string(e.Digest), e.Path, e.Size)
	}
	manifestBytes.WriteString("]")

	manifest := importManifest{
		Name:            fm.Name,
		Description:     fm.Description,
		License:         fm.License,
		AllowedTools:    fm.AllowedTools,
		Dependencies:    fm.Dependencies,
		InstructionPath: skillManifestName,
		ContentDigest:   contract.Hash(manifestBytes.Bytes()),
		Files:           entries,
	}
	return manifest, staged, nil
}

// Finish implements contract.LocalIO for skill.import. It rechecks
// authority and generation, resolves the declared dependencies against the
// current snapshot, rejects dependency cycles, publishes the immutable
// version and registers the instruction artifact — all in one transaction.
func (s *Service) Finish(ctx context.Context, unit contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	if plan.Owner != ownerName || plan.Invocation.Operation != "skill.import" {
		return contract.Payload{}, permissionDenied("local IO plan does not belong to skills skill.import")
	}
	if plan.Generation != unit.Generation() {
		return contract.Payload{}, conflictFault("concurrent configuration change: local IO plan generation %d does not match transaction generation %d", plan.Generation, unit.Generation())
	}
	if plan.Actor.PrincipalID != unit.Actor().PrincipalID {
		return contract.Payload{}, permissionDenied("local IO plan actor does not match the transaction actor")
	}
	if plan.Scope.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, permissionDenied("local IO plan scope does not match the transaction scope")
	}
	if unit.ReadOnly() {
		return contract.Payload{}, invalidInput("operation skill.import is a mutation and requires a write transaction")
	}
	if result.Fault != nil {
		// A fault from Perform commits no mutation; the failed status is
		// the result, not an error at the boundary.
		return contract.Payload{Status: contract.StatusFailed, Error: result.Fault}, nil
	}

	manifest, err := decodeJSONColumn[importManifest](string(result.Data))
	if err != nil {
		return contract.Payload{}, faultWrap(internalError("local IO result payload is malformed"), err)
	}
	prepared, err := decodeJSONColumn[importPrepared](string(plan.Prepared))
	if err != nil {
		return contract.Payload{}, faultWrap(internalError("local IO plan payload is malformed"), err)
	}

	// Resolve every declared dependency against the current snapshot.
	// Unpinned names resolve to the latest active version; a name@N pin
	// resolves to that exact version. Unresolvable references reject the
	// import before publication.
	installation := unit.Scope().InstallationID
	resolved := make([]dependencyRow, 0, len(manifest.Dependencies))
	for _, ref := range manifest.Dependencies {
		var row *skillRow
		if ref.Version > 0 {
			row, err = fetchSkillByNameVersion(ctx, unit, installation, ref.Name, ref.Version)
		} else {
			row, err = fetchActiveSkillByName(ctx, unit, installation, ref.Name)
		}
		if err != nil {
			return contract.Payload{}, err
		}
		if row == nil {
			return contract.Payload{}, invalidInput("skill dependency %q cannot be resolved", ref.Name)
		}
		resolved = append(resolved, dependencyRow{
			InstallationID: installation,
			SkillID:        contract.ID(""), // filled after identity resolution
			SkillVersion:   0,
			DepName:        ref.Name,
			DepID:          row.ID,
			DepVersion:     row.Version,
		})
	}
	if err := rejectDependencyCycles(ctx, unit, installation, manifest.Name, resolved); err != nil {
		return contract.Payload{}, err
	}

	// Publish every staged blob so the version's bytes become addressable.
	for _, f := range manifest.Files {
		if err := s.blobs.Publish(ctx, f.StagingRef, f.Digest); err != nil {
			return contract.Payload{}, faultWrap(conflictFault("blob publication failed"), err)
		}
	}

	// Register the instruction artifact through the artifacts owner. The
	// SKILL.md file is retained byte-exact as the instruction document.
	var instruction manifestFile
	for _, f := range manifest.Files {
		if f.Path == manifest.InstructionPath {
			instruction = f
			break
		}
	}
	artifactOut, err := s.callOwner(ctx, unit, "_artifacts.publish", map[string]any{
		"scope":          prepared.Scope.toContract(),
		"digest":         instruction.Digest,
		"size":           instruction.Size,
		"media_type":     "text/markdown",
		"classification": "internal",
		"encrypted":      true,
	})
	if err != nil {
		return contract.Payload{}, err
	}
	var published struct {
		Resource struct {
			ID contract.ID `json:"id"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(artifactOut, &published); err != nil {
		return contract.Payload{}, faultWrap(internalError("artifact publication decoding failed"), err)
	}

	// Identity: an existing name continues its version line; a new name
	// mints a fresh identity at version 1.
	skillID := s.ids.New()
	version := int64(1)
	existing, err := fetchSkillByName(ctx, unit, installation, manifest.Name)
	if err != nil {
		return contract.Payload{}, err
	}
	if existing != nil {
		skillID = existing.ID
		version = existing.Version + 1
	}

	requirementsJSON, err := marshalJSON(manifest.AllowedTools)
	if err != nil {
		return contract.Payload{}, err
	}
	depsForJSON := make([]wireRef, 0, len(resolved))
	for _, d := range resolved {
		depsForJSON = append(depsForJSON, wireRef{ID: d.DepID, Version: contract.Version(d.DepVersion)})
	}
	dependenciesJSON, err := marshalJSON(depsForJSON)
	if err != nil {
		return contract.Payload{}, err
	}
	manifestJSON, err := marshalJSON(manifest)
	if err != nil {
		return contract.Payload{}, err
	}
	now := s.clock.Now().UTC()
	_, err = unit.ExecContext(ctx, `
		INSERT INTO skills_versions
			(id, version, installation_id, name, description, state,
			 content_digest, instruction_digest, instruction_artifact_id,
			 manifest_json, requirements_json, dependencies_json,
			 input_schema_json, output_schema_json, source, license,
			 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'draft', ?, ?, ?, ?, ?, ?, '{}', '{}', ?, ?, ?, ?)`,
		string(skillID), version, string(installation), manifest.Name, manifest.Description,
		string(manifest.ContentDigest), string(instruction.Digest), string(published.Resource.ID),
		manifestJSON, requirementsJSON, dependenciesJSON,
		prepared.Source, prepared.License,
		now.Format(timeLayout), now.Format(timeLayout))
	if err != nil {
		return contract.Payload{}, faultWrap(internalError("skill version persistence failed"), err)
	}
	for _, d := range resolved {
		d.SkillID = skillID
		d.SkillVersion = version
		d.InstallationID = installation
		if err := insertDependency(ctx, unit, d); err != nil {
			return contract.Payload{}, err
		}
	}

	if err := s.emitSkillImported(ctx, unit, skillID, version, manifest.Name, manifest.ContentDigest); err != nil {
		return contract.Payload{}, err
	}

	row, err := fetchSkillVersion(ctx, unit, installation, skillID, version)
	if err != nil {
		return contract.Payload{}, err
	}
	resource, err := wireSkillOf(ctx, unit, row)
	if err != nil {
		return contract.Payload{}, err
	}

	// An explicit draft continues in the configuration compiler; the change
	// carries the same definition the version row sealed.
	if prepared.DraftID != nil {
		action := "update"
		expected := version - 1
		if version == 1 {
			action = "create"
			expected = 0
		}
		if _, err := s.callOwner(ctx, unit, "_configuration.stage", map[string]any{
			"scope":    prepared.Scope.toContract(),
			"draft_id": *prepared.DraftID,
			"change": map[string]any{
				"kind":             "skill",
				"action":           action,
				"id":               skillID,
				"expected_version": expected,
				"definition":       resource,
			},
		}); err != nil {
			return contract.Payload{}, err
		}
	}

	return s.completed(importOutput{Resource: &resource})
}

// rejectDependencyCycles walks the resolved dependency graph breadth of the
// candidate skill and rejects any cycle the import would close. Edges come
// from stored dependency pins; the candidate itself is a node, so a
// dependency on the skill's own name — direct or transitive — is a cycle.
func rejectDependencyCycles(ctx context.Context, unit contract.Unit, installation contract.ID, name string, resolved []dependencyRow) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	colors := map[string]int{}

	// depNames loads the dependency names pinned by one stored version.
	depNames := func(id contract.ID, version int64) ([]string, error) {
		rows, err := skillDependencies(ctx, unit, installation, id, version)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.DepName)
		}
		return out, nil
	}

	var visit func(node string, deps []string) error
	visit = func(node string, deps []string) error {
		colors[node] = gray
		for _, dep := range deps {
			if dep == node || colors[dep] == gray {
				return invalidInput("skill dependency cycle through %q", dep)
			}
			if colors[dep] != white {
				continue
			}
			// Resolve the stored version that carries this name to walk
			// its own pins. The version was resolved during import, so a
			// missing row is an internal inconsistency.
			row, err := fetchSkillByName(ctx, unit, installation, dep)
			if err != nil {
				return err
			}
			if row == nil {
				return invalidInput("skill dependency %q cannot be resolved", dep)
			}
			next, err := depNames(row.ID, row.Version)
			if err != nil {
				return err
			}
			if err := visit(dep, next); err != nil {
				return err
			}
		}
		colors[node] = black
		return nil
	}

	names := make([]string, 0, len(resolved))
	for _, d := range resolved {
		names = append(names, d.DepName)
	}
	return visit(name, names)
}

// callOwner marshals input and calls a peer owner operation through the
// injected ports; the same unit, actor, scope, generation and transaction
// are retained and no authority is minted.
func (s *Service) callOwner(ctx context.Context, unit contract.Unit, op string, input any) (json.RawMessage, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, internalError("outgoing call encoding failed")
	}
	payload, err := s.ports.Call(ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
	if err != nil {
		return nil, err
	}
	if payload.Error != nil {
		return nil, &faultError{fault: payload.Error}
	}
	return payload.Data, nil
}
