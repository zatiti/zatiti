package packaging

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// BuildInput describes a staged distribution tree. Every regular file under
// Root except the manifest and its signature must be classified in Kinds;
// the caller states what each file is, and Build measures it.
type BuildInput struct {
	Root           string
	Distribution   string
	Version        string
	Target         Target
	SourceRevision string
	Toolchain      string
	// Kinds maps a slash-separated path relative to Root to its artifact
	// kind.
	Kinds        map[string]string
	Licenses     []LicenseNotice
	SBOM         SBOMRef
	Profiles     []Profile
	Serenity     SerenityPin
	SecureHelper SecureHelper
	Desktop      *DesktopClient
	Attestations []Attestation
}

type treeFile struct {
	size   int64
	mode   fs.FileMode
	sha256 string
}

// Build measures the staged tree and returns a validated manifest. It
// refuses a tree that holds a symlink, a special file, an unclassified file
// or a classification for a file that does not exist. The protocol section is
// fixed by the contract revision, not supplied by the caller.
func Build(in BuildInput) (Manifest, error) {
	files, findings, err := scanTree(in.Root)
	if err != nil {
		return Manifest{}, err
	}
	if len(findings) != 0 {
		return Manifest{}, errFindings("the staged tree holds entries that cannot be distributed", findings)
	}
	m := Manifest{
		Schema:           ManifestSchema,
		ContractRevision: ContractRevision,
		Distribution:     in.Distribution,
		Version:          in.Version,
		Target:           in.Target,
		SourceRevision:   in.SourceRevision,
		Toolchain:        in.Toolchain,
		Artifacts:        make([]Artifact, 0, len(files)),
		Licenses:         append([]LicenseNotice{}, in.Licenses...),
		SBOM:             in.SBOM,
		Protocols:        Protocols{OperationAPI: OperationAPI, RequestSchema: RequestSchema, ResultSchema: ResultSchema, MCP: MCPProtocolVersion},
		Profiles:         append([]Profile{}, in.Profiles...),
		Serenity:         in.Serenity,
		SecureHelper:     in.SecureHelper,
		Desktop:          in.Desktop,
		Attestations:     append([]Attestation{}, in.Attestations...),
	}
	for rel, f := range files {
		kind, ok := in.Kinds[rel]
		if !ok {
			return Manifest{}, errf(CodeInvalidInput, "staged file %s has no declared kind", printablePath(rel))
		}
		m.Artifacts = append(m.Artifacts, Artifact{Path: rel, Kind: kind, SHA256: f.sha256, Size: f.size, Mode: formatMode(f.mode)})
	}
	for rel := range in.Kinds {
		if _, ok := files[rel]; !ok {
			return Manifest{}, errf(CodeNotFound, "declared file %s is not in the staged tree", printablePath(rel))
		}
	}
	sortArtifacts(m.Artifacts)
	sort.Slice(m.Licenses, func(i, j int) bool { return m.Licenses[i].Component < m.Licenses[j].Component })
	sort.Slice(m.Profiles, func(i, j int) bool {
		if m.Profiles[i].Adapter != m.Profiles[j].Adapter {
			return m.Profiles[i].Adapter < m.Profiles[j].Adapter
		}
		return m.Profiles[i].Name < m.Profiles[j].Name
	})
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	if err := auditSBOM(m, in.Root); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// VerifyTree checks a distribution tree against its manifest: every listed
// file is present as a regular file with the recorded size, digest and
// permissions, and the tree holds nothing the manifest does not list. It
// then audits the license notices against the SBOM. Every defect is
// reported, not only the first.
func VerifyTree(m Manifest, root string) error {
	if err := m.Validate(); err != nil {
		return err
	}
	files, findings, err := scanTree(root)
	if err != nil {
		return err
	}
	for _, a := range m.Artifacts {
		f, ok := files[a.Path]
		if !ok {
			findings = append(findings, Finding{Path: a.Path, Problem: "listed file is missing"})
			continue
		}
		delete(files, a.Path)
		if f.size != a.Size {
			findings = append(findings, Finding{Path: a.Path, Problem: "size differs from the manifest"})
		}
		if f.sha256 != a.SHA256 {
			findings = append(findings, Finding{Path: a.Path, Problem: "SHA-256 digest differs from the manifest"})
		}
		if formatMode(f.mode) != a.Mode {
			findings = append(findings, Finding{Path: a.Path, Problem: "permissions differ from the manifest"})
		}
	}
	for rel := range files {
		findings = append(findings, Finding{Path: rel, Problem: "file is not listed in the manifest"})
	}
	if len(findings) != 0 {
		sortFindings(findings)
		return errFindings("the distribution tree does not match its manifest", findings)
	}
	return auditSBOM(m, root)
}

// scanTree hashes every regular file under root. Symlinks and special files
// are findings, never followed. The manifest and signature files at the top
// level are skipped.
func scanTree(root string) (map[string]treeFile, []Finding, error) {
	if err := requireRealDir(root); err != nil {
		return nil, nil, err
	}
	files := map[string]treeFile{}
	var findings []Finding
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		relOS, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(relOS)
		if d.IsDir() {
			return nil
		}
		if rel == ManifestFileName || rel == SignatureFileName {
			return nil
		}
		if !d.Type().IsRegular() {
			findings = append(findings, Finding{Path: rel, Problem: "entry is a symlink or special file"})
			return nil
		}
		if err := validateRelPath(rel); err != nil {
			findings = append(findings, Finding{Path: printablePath(rel), Problem: "path is not distributable"})
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sum, size, err := hashFile(p)
		if err != nil {
			return err
		}
		files[rel] = treeFile{size: size, mode: info.Mode().Perm(), sha256: sum}
		return nil
	})
	if walkErr != nil {
		return nil, nil, errWrap(CodeInternalError, "the distribution tree could not be read", walkErr)
	}
	sortFindings(findings)
	return files, findings, nil
}

func requireRealDir(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return errf(CodeInvalidInput, "a tree root must be a clean absolute path")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return errWrap(CodeNotFound, "the tree root does not exist", err)
	}
	if !info.IsDir() {
		return errf(CodeInvalidInput, "the tree root must be a directory, not a symlink or file")
	}
	return nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func sortFindings(f []Finding) {
	sort.Slice(f, func(i, j int) bool {
		if f[i].Path != f[j].Path {
			return f[i].Path < f[j].Path
		}
		return f[i].Problem < f[j].Problem
	})
}

// printablePath bounds and escapes an untrusted relative path for a message.
func printablePath(rel string) string {
	if len(rel) > 128 {
		rel = rel[:128]
	}
	return quoteASCII(rel)
}
