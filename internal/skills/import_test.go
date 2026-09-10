package skills

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// skill.import acceptance: the local IO phases, archive safety, content
// identity, version line continuation and dependency fences.

// importFixture registers an artifact carrying zipData and returns the
// import input that pins it.
func (e *testEnv) importFixture(zipData []byte) importInput {
	e.t.Helper()
	id := e.ids.New()
	digest := contract.Hash(zipData)
	e.ports.addArtifact(fakeArtifact{
		ID: id, Version: 1, Digest: digest, Size: int64(len(zipData)), State: "available",
	})
	e.blobs.seed(digest, zipData)
	return importInput{
		Scope:    e.scope,
		Artifact: wireArtifactRef{ID: id, Digest: digest},
		Source:   "test-fixture",
		License:  "MIT",
	}
}

func TestImportHappyPath(t *testing.T) {
	e := newEnv(t)
	payload, err := e.runImport(e.importFixture(validArchive()))
	if err != nil {
		t.Fatalf("skill.import failed: %v", err)
	}
	if payload.Error != nil {
		t.Fatalf("skill.import fault: %s: %s", payload.Error.Code, payload.Error.Message)
	}
	var out importOutput
	e.decode(payload.Data, &out)
	if out.Resource == nil {
		t.Fatalf("skill.import returned no resource: %s", string(payload.Data))
	}
	res := out.Resource
	if res.Version != 1 {
		t.Fatalf("first import version = %d, want 1", res.Version)
	}
	if res.Name != "greeter" {
		t.Fatalf("name = %q, want greeter", res.Name)
	}
	if res.License != "MIT" || res.Source != "test-fixture" {
		t.Fatalf("provenance not retained: source %q license %q", res.Source, res.License)
	}
	if len(res.Requirements) != 2 || res.Requirements[0] != "tool:echo" || res.Requirements[1] != "tool:clock" {
		t.Fatalf("allowed-tools not pinned as requirements: %v", res.Requirements)
	}
	if res.InstructionArtifact.ID == "" || res.InstructionArtifact.Digest == "" {
		t.Fatalf("instruction artifact not registered: %+v", res.InstructionArtifact)
	}
	if len(res.Dependencies) != 0 {
		t.Fatalf("unexpected dependencies: %v", res.Dependencies)
	}

	// Every archive file is published content-addressed, and the
	// instruction artifact is registered through the artifacts owner.
	if e.blobs.publishCalls() != 2 {
		t.Fatalf("published blobs = %d, want 2 (SKILL.md and scripts/run.sh)", e.blobs.publishCalls())
	}
	if calls := e.ports.callsOf("_artifacts.publish"); len(calls) != 1 {
		t.Fatalf("artifact publish calls = %d, want 1", len(calls))
	}

	// The import event is emitted in the same transaction.
	events, err := e.db.Events(e.ctx, 0, 100)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != "skills.skill.imported" {
		t.Fatalf("events = %+v, want one skills.skill.imported", events)
	}

	// skill.get serves the staged draft.
	got := e.mustOK("skill.get", skillGetInput{Scope: e.scope, ID: res.ID})
	var outGet skillOutput
	e.decode(got.Data, &outGet)
	if outGet.Resource.ID != res.ID || outGet.Resource.Version != 1 {
		t.Fatalf("skill.get returned %s v%d, want %s v1", outGet.Resource.ID, outGet.Resource.Version, res.ID)
	}
	if state, ok := e.rowState(res.ID, 1); !ok || state != "draft" {
		t.Fatalf("imported skill state = %q (found %v), want draft", state, ok)
	}
}

func TestImportByteExactRetention(t *testing.T) {
	e := newEnv(t)
	in := e.importFixture(validArchive())
	payload, err := e.runImport(in)
	if err != nil || payload.Error != nil {
		t.Fatalf("import failed: %v %v", err, payload.Error)
	}
	var out importOutput
	e.decode(payload.Data, &out)

	// The instruction artifact opens to the exact SKILL.md bytes.
	plan, err := e.runImportPrepare(in)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	_ = plan
	rc, err := e.blobs.Open(e.ctx, out.Resource.InstructionArtifact.Digest, 0, -1)
	if err != nil {
		t.Fatalf("open instruction artifact: %v", err)
	}
	defer func() { _ = rc.Close() }()
	stored := new(strings.Builder)
	buf := make([]byte, 4096)
	for {
		n, err := rc.Read(buf)
		stored.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if stored.String() != validSkillMD {
		t.Fatalf("instruction artifact is not byte-exact:\n got %q\nwant %q", stored.String(), validSkillMD)
	}
}

// runImportPrepare drives only the Prepare phase, for tests that inspect
// the sealed plan.
func (e *testEnv) runImportPrepare(in importInput) (contract.IOPlan, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal import input: %v", err)
	}
	var plan contract.IOPlan
	err = e.db.Write(e.ctx, e.actor, in.Scope.toContract(), func(unit contract.Unit) error {
		p, err := e.svc.Prepare(e.ctx, unit, contract.Invocation{Operation: "skill.import", Version: 1, Input: raw})
		plan = p
		return err
	})
	return plan, err
}

func TestImportContentDigestOrderIndependent(t *testing.T) {
	a := buildZip([]zipEntry{
		{Name: "SKILL.md", Data: []byte(minimalSkillMD)},
		{Name: "b.txt", Data: []byte("b")},
		{Name: "a.txt", Data: []byte("a")},
	})
	b := buildZip([]zipEntry{
		{Name: "a.txt", Data: []byte("a")},
		{Name: "b.txt", Data: []byte("b")},
		{Name: "SKILL.md", Data: []byte(minimalSkillMD)},
	})
	digests := map[contract.Digest]bool{}
	for _, zipData := range [][]byte{a, b} {
		e := newEnv(t)
		payload, err := e.runImport(e.importFixture(zipData))
		if err != nil || payload.Error != nil {
			t.Fatalf("import failed: %v %v", err, payload.Error)
		}
		var out importOutput
		e.decode(payload.Data, &out)
		if out.Resource == nil {
			t.Fatal("no resource")
		}
		digests[out.Resource.ContentDigest] = true
	}
	if len(digests) != 1 {
		t.Fatalf("content digest depends on zip entry order: %v", digests)
	}
}

func TestImportReimportSameBytesNewVersion(t *testing.T) {
	e := newEnv(t)
	in := e.importFixture(validArchive())

	first, err := e.runImport(in)
	if err != nil || first.Error != nil {
		t.Fatalf("first import failed: %v %v", err, first.Error)
	}
	second, err := e.runImport(in)
	if err != nil || second.Error != nil {
		t.Fatalf("second import failed: %v %v", err, second.Error)
	}
	var out1, out2 importOutput
	e.decode(first.Data, &out1)
	e.decode(second.Data, &out2)
	if out1.Resource.ID != out2.Resource.ID {
		t.Fatalf("re-import minted a new identity: %s vs %s", out1.Resource.ID, out2.Resource.ID)
	}
	if out2.Resource.Version != 2 {
		t.Fatalf("re-import version = %d, want 2", out2.Resource.Version)
	}
	if out2.Resource.ContentDigest != out1.Resource.ContentDigest {
		t.Fatalf("identical bytes produced a different content digest")
	}
}

func TestImportPinnedArtifactFences(t *testing.T) {
	t.Run("unknown artifact", func(t *testing.T) {
		e := newEnv(t)
		in := e.importFixture(validArchive())
		in.Artifact.ID = e.ids.New()
		e.runImportExpectFault(in, contract.CodeNotFound)
	})
	t.Run("digest mismatch", func(t *testing.T) {
		e := newEnv(t)
		in := e.importFixture(validArchive())
		in.Artifact.Digest = contract.Digest(strings.Repeat("f", 64))
		e.runImportExpectFault(in, contract.CodeConflict)
	})
	t.Run("artifact in fault state", func(t *testing.T) {
		e := newEnv(t)
		in := e.importFixture(validArchive())
		e.ports.addArtifact(fakeArtifact{
			ID: in.Artifact.ID, Version: 1, Digest: in.Artifact.Digest,
			Size: int64(len(validArchive())), State: "fault",
		})
		e.runImportExpectFault(in, contract.CodeConflict)
	})
	t.Run("blob bytes missing", func(t *testing.T) {
		e := newEnv(t)
		in := e.importFixture(validArchive())
		e.blobs.mu.Lock()
		delete(e.blobs.published, in.Artifact.Digest)
		e.blobs.mu.Unlock()
		e.runImportExpectFault(in, contract.CodeConflict)
	})
	t.Run("artifact not a zip", func(t *testing.T) {
		e := newEnv(t)
		e.runImportExpectFault(e.importFixture([]byte("definitely not a zip")), contract.CodeInvalidInput)
	})
	t.Run("prepare refuses read-only unit", func(t *testing.T) {
		e := newEnv(t)
		in := e.importFixture(validArchive())
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.db.Read(e.ctx, e.actor, in.Scope.toContract(), func(unit contract.Unit) error {
			_, err := e.svc.Prepare(e.ctx, unit, contract.Invocation{Operation: "skill.import", Version: 1, Input: raw})
			if err == nil {
				t.Fatal("Prepare on read-only unit succeeded")
			}
			var f *contract.Fault
			if !asFault(err, &f) || f.Code != contract.CodeInvalidInput {
				t.Fatalf("Prepare on read-only unit: got %v, want invalid_input", err)
			}
			return nil
		}); err != nil {
			t.Fatalf("read: %v", err)
		}
	})
}

func TestImportDependencyFences(t *testing.T) {
	t.Run("unresolved dependency rejects", func(t *testing.T) {
		e := newEnv(t)
		md := strings.Replace(minimalSkillMD, "name: minimal", "name: dependent", 1)
		md = strings.Replace(md, "description: A minimal skill.",
			"description: A minimal skill.\ndependencies:\n  - missing-skill", 1)
		zipData := buildZip([]zipEntry{{Name: "SKILL.md", Data: []byte(md)}})
		e.runImportExpectFault(e.importFixture(zipData), contract.CodeInvalidInput)
		// Nothing persisted, nothing published.
		if e.blobs.publishCalls() != 0 {
			t.Fatalf("published blobs = %d, want 0", e.blobs.publishCalls())
		}
	})
	t.Run("self dependency rejects", func(t *testing.T) {
		e := newEnv(t)
		md := strings.Replace(minimalSkillMD, "name: minimal", "name: loopy", 1)
		md = strings.Replace(md, "description: A minimal skill.",
			"description: A minimal skill.\ndependencies:\n  - loopy", 1)
		zipData := buildZip([]zipEntry{{Name: "SKILL.md", Data: []byte(md)}})
		e.runImportExpectFault(e.importFixture(zipData), contract.CodeInvalidInput)
	})
	t.Run("two skill cycle rejects", func(t *testing.T) {
		e := newEnv(t)
		// Seed "a" active depending on "b" (also active).
		bRow := e.seedSkill("b", 1, "active")
		aRow := e.seedSkill("a", 1, "active", dependencyRef{Name: "b"})
		_ = aRow
		_ = bRow
		// Importing a new "b" that depends on "a" closes the cycle a→b→a.
		md := strings.Replace(minimalSkillMD, "name: minimal", "name: b", 1)
		md = strings.Replace(md, "description: A minimal skill.",
			"description: A minimal skill.\ndependencies:\n  - a", 1)
		zipData := buildZip([]zipEntry{{Name: "SKILL.md", Data: []byte(md)}})
		e.runImportExpectFault(e.importFixture(zipData), contract.CodeInvalidInput)
	})
	t.Run("pinned dependency resolves to exact version", func(t *testing.T) {
		e := newEnv(t)
		e.seedSkill("dep-skill", 1, "active")
		e.seedSkill("dep-skill", 2, "draft")
		md := strings.Replace(minimalSkillMD, "name: minimal", "name: pinning", 1)
		md = strings.Replace(md, "description: A minimal skill.",
			"description: A minimal skill.\ndependencies:\n  - dep-skill@1", 1)
		zipData := buildZip([]zipEntry{{Name: "SKILL.md", Data: []byte(md)}})
		payload, err := e.runImport(e.importFixture(zipData))
		if err != nil || payload.Error != nil {
			t.Fatalf("import failed: %v %v", err, payload.Error)
		}
		var out importOutput
		e.decode(payload.Data, &out)
		if len(out.Resource.Dependencies) != 1 || out.Resource.Dependencies[0].Version != 1 {
			t.Fatalf("pinned dependency resolved wrong: %+v", out.Resource.Dependencies)
		}
	})
	t.Run("unpinned dependency resolves to active version", func(t *testing.T) {
		e := newEnv(t)
		e.seedSkill("dep-skill", 1, "archived")
		e.seedSkill("dep-skill", 2, "active")
		e.seedSkill("dep-skill", 3, "draft")
		md := strings.Replace(minimalSkillMD, "name: minimal", "name: pinning", 1)
		md = strings.Replace(md, "description: A minimal skill.",
			"description: A minimal skill.\ndependencies:\n  - dep-skill", 1)
		zipData := buildZip([]zipEntry{{Name: "SKILL.md", Data: []byte(md)}})
		payload, err := e.runImport(e.importFixture(zipData))
		if err != nil || payload.Error != nil {
			t.Fatalf("import failed: %v %v", err, payload.Error)
		}
		var out importOutput
		e.decode(payload.Data, &out)
		if len(out.Resource.Dependencies) != 1 || out.Resource.Dependencies[0].Version != 2 {
			t.Fatalf("unpinned dependency resolved wrong: %+v", out.Resource.Dependencies)
		}
	})
}

func TestImportDraftContinuation(t *testing.T) {
	e := newEnv(t)
	in := e.importFixture(validArchive())
	draftID := e.ids.New()
	in.DraftID = &draftID
	payload, err := e.runImport(in)
	if err != nil || payload.Error != nil {
		t.Fatalf("import failed: %v %v", err, payload.Error)
	}
	calls := e.ports.callsOf("_configuration.stage")
	if len(calls) != 1 {
		t.Fatalf("configuration stage calls = %d, want 1", len(calls))
	}
	var staged struct {
		Change struct {
			Kind            string           `json:"kind"`
			Action          string           `json:"action"`
			ID              contract.ID      `json:"id"`
			ExpectedVersion contract.Version `json:"expected_version"`
		} `json:"change"`
	}
	if err := json.Unmarshal(calls[0].Input, &staged); err != nil {
		t.Fatalf("decode stage input: %v", err)
	}
	if staged.Change.Kind != "skill" || staged.Change.Action != "create" {
		t.Fatalf("staged change kind/action = %s/%s, want skill/create", staged.Change.Kind, staged.Change.Action)
	}
	if staged.Change.ExpectedVersion != 0 {
		t.Fatalf("create change expected_version = %d, want 0", staged.Change.ExpectedVersion)
	}

	// A re-import with a draft continues the line as update at the current
	// maximum version.
	second := e.importFixture(validArchive())
	second.DraftID = &draftID
	if _, err := e.runImport(second); err != nil {
		t.Fatalf("second import: %v", err)
	}
	calls = e.ports.callsOf("_configuration.stage")
	if len(calls) != 2 {
		t.Fatalf("configuration stage calls = %d, want 2", len(calls))
	}
	if err := json.Unmarshal(calls[1].Input, &staged); err != nil {
		t.Fatalf("decode stage input: %v", err)
	}
	if staged.Change.Action != "update" {
		t.Fatalf("second staged action = %s, want update", staged.Change.Action)
	}
	if staged.Change.ExpectedVersion != 1 {
		t.Fatalf("update expected_version = %d, want 1", staged.Change.ExpectedVersion)
	}
}

func TestImportFrontmatterRejections(t *testing.T) {
	cases := []struct {
		name    string
		skillMD string
	}{
		{"missing frontmatter", "# Just a body\n"},
		{"unclosed frontmatter", "---\nname: x\ndescription: y\n"},
		{"unknown key", "---\nname: x\ndescription: y\nsecret-instruction: do bad\n---\nbody"},
		{"duplicate key", "---\nname: x\nname: y\ndescription: z\n---\nbody"},
		{"missing description", "---\nname: x\n---\nbody"},
		{"uppercase name", "---\nname: Big\ndescription: y\n---\nbody"},
		{"bad dependency form", "---\nname: x\ndescription: y\ndependencies:\n  - not a ref!\n---\nbody"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			zipData := buildZip([]zipEntry{{Name: "SKILL.md", Data: []byte(tc.skillMD)}})
			e.runImportExpectFault(e.importFixture(zipData), contract.CodeInvalidInput)
		})
	}
}
