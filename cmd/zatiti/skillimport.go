package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/zatiti/zatiti/internal/cli"
	"github.com/zatiti/zatiti/internal/contract"
)

// The skill import-dir helper (Z-M5): a cmd/zatiti-only bulk loop over the
// existing public skill.import operation, for the first mass load of an
// operator's local skill catalog (~/.agents/skills and similar) before a
// later card binds imported skills to workers. It reuses skill.import's
// OWN call path -- the same contract.Operator.Call the generated `skill
// import` command (internal/cli.New, driven by skill.import's descriptor)
// and installation.FirstTaskSequence's documented artifact upload
// begin/chunk/finish sequence already use -- never a hand-rolled request
// shape. It is cmd/zatiti "local bootstrap/helper mechanics" (this
// package's own AGENTS.md phrase), not a new product operation: no
// Descriptor is added, and internal/cli's generated tree is untouched;
// see attachSkillImportHelper below for why it nests under the existing
// generated "skill" group instead.

// skillImportChunkBytes mirrors internal/artifacts/localio.go's unexported
// maxChunkBytes (1 MiB decoded, the wire convention's chunk limit). It is
// not exported by internal/artifacts, so this is a deliberate, documented
// wire-format mirror -- the same pattern cmd/zatiti/helper.go's own
// helperReceiptPrefix/helperReceiptKeyRef already use for a frozen
// cross-package constant Go cannot import unexported.
const skillImportChunkBytes = 1 << 20

// attachSkillImportHelper adds `zatiti skill import-dir` under the
// generated "skill" group internal/cli.New already built from the catalog
// (skill.import/.get/.list/.archive/.evaluate all resolve under a "skill"
// grouping command per internal/cli/cli.go's resolveNode, since every
// skill.* descriptor's own CLI path starts with "skill"). "import-dir" has
// no Descriptor -- it is a cmd/zatiti-only concept -- so it is nested in
// after the fact rather than through internal/cli's descriptor-driven
// path, exactly the way attachConnectionHelper (helper.go) attaches
// "connection helper" under the generated "connection" group. Every
// skill.* descriptor guarantees the "skill" group already exists by the
// time run() calls this; the fallback creates it defensively rather than
// panicking if that ever stops holding.
func attachSkillImportHelper(root *cobra.Command, op contract.Operator, streams cli.IO) {
	var group *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "skill" {
			group = c
			break
		}
	}
	if group == nil {
		group = &cobra.Command{Use: "skill", SilenceUsage: true, SilenceErrors: true}
		root.AddCommand(group)
	}
	group.AddCommand(skillImportDirCommand(op, streams))
}

// skillImportDirCommand is `zatiti skill import-dir <path>`.
func skillImportDirCommand(op contract.Operator, streams cli.IO) *cobra.Command {
	var installationID, license, source string
	cmd := &cobra.Command{
		Use:   "import-dir <path>",
		Short: "bulk-import every <path>/*/SKILL.md skill directory through skill.import",
		Long: "import-dir walks <path>/*/SKILL.md, one skill per immediate subdirectory. For each it zips the " +
			"skill directory, uploads it through the ordinary artifact upload begin/chunk/finish sequence " +
			"(installation.FirstTaskSequence documents the same three calls), and calls skill.import once -- the " +
			"same call path `zatiti skill import` itself uses, looped over a directory rather than hand-rolled. " +
			"It reports an honest per-file summary (imported count, per-file failure with the exact skill.import " +
			"fault code/message) to stderr and exits nonzero on any failure, or if zero skill directories are found.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			if installationID == "" {
				err := errors.New("--installation-id is required")
				return &exitError{code: contract.CLIExit(&contract.Fault{Code: contract.CodeInvalidInput}), err: err}
			}
			summary, err := runSkillImportDir(c.Context(), op, streams, contract.ID(installationID), args[0], source, license)
			if err != nil {
				_, _ = fmt.Fprintf(streams.Err, "zatiti skill import-dir: %v\n", err)
				return &exitError{code: contract.CLIExit(&contract.Fault{Code: contract.CodeInvalidInput}), err: err}
			}
			if summary.failed > 0 {
				err := fmt.Errorf("%d of %d skill directories failed to import", summary.failed, summary.found)
				return &exitError{code: contract.CLIExit(&contract.Fault{Code: contract.CodeInvalidInput}), err: err}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&installationID, "installation-id", "", "the installation scope every imported skill is published into")
	f.StringVar(&license, "license", "", "license label recorded on every imported skill version (optional)")
	f.StringVar(&source, "source", "", "source label recorded on every imported skill version (default: each skill directory's own resolved path)")
	return cmd
}

// importDirSummary is the honest per-run report import-dir prints and
// exits on: found is every */SKILL.md skill directory discovered
// (irrespective of outcome), never inferred from imported+failed alone.
type importDirSummary struct {
	found    int
	imported int
	failed   int
}

// runSkillImportDir walks dir's immediate subdirectories in a stable
// (sorted) order, importing each one that carries a SKILL.md and reporting
// per-file progress to streams.Err as it goes -- so a long real run against
// a large catalog is observable, not silent until the end. A directory
// entry without a SKILL.md is not a skill directory and is silently
// skipped (dotfiles, unrelated tooling directories); "zero files found"
// below means zero SKILL.md files located, never treated as success.
func runSkillImportDir(ctx context.Context, op contract.Operator, streams cli.IO, installationID contract.ID, dir, source, license string) (importDirSummary, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return importDirSummary{}, fmt.Errorf("reading %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var summary importDirSummary
	for _, name := range names {
		skillDir := filepath.Join(dir, name)
		// os.Stat follows symlinks (unlike the DirEntry from ReadDir), so a
		// symlinked skill directory -- a real shape in this operator's own
		// ~/.agents/skills (e.g. "lane" -> a directory elsewhere) -- is
		// still recognized and walked.
		info, statErr := os.Stat(skillDir)
		if statErr != nil || !info.IsDir() {
			continue
		}
		if _, manifestErr := os.Stat(filepath.Join(skillDir, skillManifestFileName)); manifestErr != nil {
			continue
		}
		summary.found++

		skillSource := source
		if skillSource == "" {
			if abs, absErr := filepath.Abs(skillDir); absErr == nil {
				skillSource = abs
			} else {
				skillSource = skillDir
			}
		}

		if err := importOneSkillDir(ctx, op, installationID, skillDir, skillSource, license); err != nil {
			summary.failed++
			_, _ = fmt.Fprintf(streams.Err, "zatiti skill import-dir: %s: FAILED: %v\n", name, err)
			continue
		}
		summary.imported++
		_, _ = fmt.Fprintf(streams.Err, "zatiti skill import-dir: %s: imported\n", name)
	}

	_, _ = fmt.Fprintf(streams.Err, "zatiti skill import-dir: %d found, %d imported, %d failed\n",
		summary.found, summary.imported, summary.failed)
	if summary.found == 0 {
		return summary, fmt.Errorf("no */%s skill directories found under %s", skillManifestFileName, dir)
	}
	return summary, nil
}

// skillManifestFileName mirrors internal/skills/archive.go's unexported
// skillManifestName -- the same deliberate, documented wire-format mirror
// pattern as skillImportChunkBytes above.
const skillManifestFileName = "SKILL.md"

// importOneSkillDir zips one skill directory, uploads it as an artifact and
// calls skill.import against it -- one skill, three to five operation
// calls (upload begin, one or more chunks, upload finish, import).
func importOneSkillDir(ctx context.Context, op contract.Operator, installationID contract.ID, skillDir, source, license string) error {
	zipData, err := zipSkillDir(skillDir)
	if err != nil {
		return fmt.Errorf("building archive: %w", err)
	}
	artifact, err := uploadSkillArchive(ctx, op, installationID, zipData)
	if err != nil {
		return fmt.Errorf("artifact upload: %w", err)
	}
	input := map[string]any{
		"scope":    map[string]any{"installation_id": installationID},
		"artifact": map[string]any{"id": artifact.id, "digest": artifact.digest},
		"source":   source,
		"license":  license,
	}
	if _, err := callOp(ctx, op, "skill.import", importDirSubmissionKey("import"), input); err != nil {
		return fmt.Errorf("skill.import: %w", err)
	}
	return nil
}

// zipSkillDir builds an in-memory zip archive of skillDir's regular files,
// with entry paths relative to skillDir's own root (so SKILL.md sits at
// the archive root, exactly what internal/skills/archive.go's
// extractSkillArchive requires). skillDir itself is resolved through any
// symlink first (filepath.WalkDir does not follow a symlink root: verified
// against this machine's own ~/.agents/skills/lane, a real symlinked skill
// directory, which WalkDir reports as one unwalked symlink entry unless
// the root is resolved first). A symlink found INSIDE the tree is skipped,
// never followed or zipped as a symlink entry: extractSkillArchive rejects
// any symlink entry outright (mode&fs.ModeSymlink), so zipping one would
// only ever turn into a whole-import failure for content this helper has
// no reason to force through as a symlink.
func zipSkillDir(skillDir string) ([]byte, error) {
	root, err := filepath.EvalSymlinks(skillDir)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", skillDir, err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// uploadedSkillArchive is one finished artifact.upload.finish outcome:
// enough to address the artifact from a later skill.import call.
type uploadedSkillArchive struct {
	id     contract.ID
	digest contract.Digest
}

// uploadSkillArchive drives artifact.upload.begin/chunk*/finish for one
// in-memory zip archive -- the same three operations
// installation.FirstTaskSequence documents and
// cmd/zatiti/firsttask_test.go exercises end to end against a real
// controller -- chunking at the wire limit (1 MiB decoded) so an archive
// up to internal/skills/archive.go's own 16 MiB compressed limit uploads
// correctly regardless of size.
func uploadSkillArchive(ctx context.Context, op contract.Operator, installationID contract.ID, data []byte) (uploadedSkillArchive, error) {
	scope := map[string]any{"installation_id": installationID}
	digest := contract.Hash(data)

	beginRes, err := callOp(ctx, op, "artifact.upload.begin", importDirSubmissionKey("begin"), map[string]any{
		"scope": scope, "size": len(data), "digest": digest,
		"media_type": "application/zip", "classification": "internal",
	})
	if err != nil {
		return uploadedSkillArchive{}, fmt.Errorf("artifact.upload.begin: %w", err)
	}
	var begin struct {
		Resource struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(beginRes.Data, &begin); err != nil {
		return uploadedSkillArchive{}, fmt.Errorf("decoding artifact.upload.begin: %w", err)
	}

	uploadID := begin.Resource.ID
	version := begin.Resource.Version
	for offset := 0; offset < len(data); offset += skillImportChunkBytes {
		end := offset + skillImportChunkBytes
		if end > len(data) {
			end = len(data)
		}
		chunk := data[offset:end]
		chunkRes, err := callOp(ctx, op, "artifact.upload.chunk", importDirSubmissionKey("chunk"), map[string]any{
			"scope": scope, "upload_id": uploadID, "offset": offset,
			"bytes_base64": base64.StdEncoding.EncodeToString(chunk),
			"chunk_digest": contract.Hash(chunk),
		})
		if err != nil {
			return uploadedSkillArchive{}, fmt.Errorf("artifact.upload.chunk at offset %d: %w", offset, err)
		}
		var chunkOut struct {
			Resource struct {
				Version int64 `json:"version"`
			} `json:"resource"`
		}
		if err := json.Unmarshal(chunkRes.Data, &chunkOut); err != nil {
			return uploadedSkillArchive{}, fmt.Errorf("decoding artifact.upload.chunk: %w", err)
		}
		version = chunkOut.Resource.Version
	}

	finishRes, err := callOp(ctx, op, "artifact.upload.finish", importDirSubmissionKey("finish"), map[string]any{
		"scope": scope, "upload_id": uploadID, "expected_version": version,
	})
	if err != nil {
		return uploadedSkillArchive{}, fmt.Errorf("artifact.upload.finish: %w", err)
	}
	var finish struct {
		Resource struct {
			ID     contract.ID     `json:"id"`
			Digest contract.Digest `json:"digest"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(finishRes.Data, &finish); err != nil {
		return uploadedSkillArchive{}, fmt.Errorf("decoding artifact.upload.finish: %w", err)
	}
	return uploadedSkillArchive{id: finish.Resource.ID, digest: finish.Resource.Digest}, nil
}

// importDirSubmissionKey mints a fresh, well-bounded idempotency key for
// one mutation call: every mutation descriptor requires a non-empty
// submission_key, 1..128 printable ASCII characters
// (internal/application/dispatch.go, internal/application/submission.go).
// This bulk importer makes each call exactly once per skill, so a fresh
// random key per call (never reused, never derived from directory/skill
// names whose length this helper does not control) is simpler than trying
// to make the calls replay-safe across process restarts.
func importDirSubmissionKey(step string) string {
	return "skill-import-dir/" + step + "/" + string(contract.NewID())
}

// callOp runs one mutation operation call with an explicit submission key
// and normalizes a non-completed outcome (a transport error, or a
// completed transport carrying a fault) into a single Go error -- the same
// normalization pattern cmd/zatiti/helper.go's own callOperation already
// establishes for the connection helper's local-mechanics calls, kept as
// a separate, submission-keyed function here rather than changing that
// existing helper's signature (its own three calls need no key today; Z-M5
// does not touch connection-helper behavior).
func callOp(ctx context.Context, op contract.Operator, operation, submissionKey string, input map[string]any) (contract.Result, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return contract.Result{}, fmt.Errorf("encoding request input: %w", err)
	}
	res, err := op.Call(ctx, operation, contract.Request{Schema: contract.SchemaRequest, SubmissionKey: submissionKey, Input: raw})
	if err != nil {
		return contract.Result{}, err
	}
	if res.Status != contract.StatusCompleted {
		if res.Error != nil {
			return contract.Result{}, res.Error
		}
		return contract.Result{}, fmt.Errorf("status %s", res.Status)
	}
	return res, nil
}
