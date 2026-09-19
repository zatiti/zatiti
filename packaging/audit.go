package packaging

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// AuditInstalled checks an installation on disk: nothing in the distribution
// directory or among the named launchers is group or world writable, the
// active release link is the only symlink and points at a managed release,
// and the active release still matches the manifest installed with it,
// license notices included. Every defect is reported.
func AuditInstalled(l Layout, labels []string) error {
	inst, err := Inspect(l)
	if err != nil {
		return err
	}
	if inst.Current == "" {
		return errf(CodeNotFound, "no release is installed in this layout")
	}
	findings, err := auditWritable(l.DistRoot, map[string]bool{l.Current: true}, "")
	if err != nil {
		return err
	}
	for _, label := range labels {
		name := unitFileName(l.Manager, label)
		info, err := os.Lstat(filepath.Join(l.UnitDir, name))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			findings = append(findings, Finding{Path: name, Problem: "launcher is missing"})
		case err != nil:
			return errWrap(CodeInternalError, "a launcher could not be inspected", err)
		case !info.Mode().IsRegular():
			findings = append(findings, Finding{Path: name, Problem: "launcher is not a regular file"})
		case info.Mode().Perm()&0o022 != 0:
			findings = append(findings, Finding{Path: name, Problem: "launcher is group or world writable"})
		}
	}

	versionDir := l.VersionDir(inst.Current)
	raw, err := readSmallFile(filepath.Join(versionDir, ManifestFileName))
	if err != nil {
		findings = append(findings, Finding{Path: ManifestFileName, Problem: "the active release has no manifest"})
	} else if m, err := Decode(raw); err != nil {
		findings = append(findings, Finding{Path: ManifestFileName, Problem: "the active release manifest is not valid"})
	} else if err := VerifyTree(m, versionDir); err != nil {
		var perr *Error
		if !errors.As(err, &perr) || perr.Code != CodeVerificationFailed {
			return err
		}
		findings = append(findings, perr.Findings...)
	}
	if len(findings) != 0 {
		sortFindings(findings)
		return errFindings("the installation does not pass its audit", findings)
	}
	return nil
}

// auditWritable walks root and reports every group- or world-writable entry
// and every symlink not in allowedLinks. The subtree at skip, when set, is
// not entered: an unpacked application bundle has its own audit.
func auditWritable(root string, allowedLinks map[string]bool, skip string) ([]Finding, error) {
	var findings []Finding
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skip != "" && p == skip {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			if !allowedLinks[p] {
				findings = append(findings, Finding{Path: rel, Problem: "unexpected symlink"})
			}
		case info.Mode().Perm()&0o022 != 0:
			findings = append(findings, Finding{Path: rel, Problem: "entry is group or world writable"})
		}
		return nil
	})
	if walkErr != nil {
		return nil, errWrap(CodeInternalError, "the installation could not be read", walkErr)
	}
	return findings, nil
}

// readSmallFile reads a file this package wrote, bounded by the manifest
// size limit.
func readSmallFile(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxManifestBytes {
		return nil, errf(CodeInvalidInput, "document exceeds %d bytes", maxManifestBytes)
	}
	return raw, nil
}
