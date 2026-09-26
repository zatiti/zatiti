package packaging

import (
	"bytes"
	"os"
	"path/filepath"
)

// verifiedNoop accepts a repeat install only when the installed bytes and
// launchers still describe exactly the requested release.
func verifiedNoop(l Layout, m Manifest, launchers map[string][]byte) (Plan, error) {
	want, err := Encode(m)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Kind: PlanNoop, Layout: l, Version: m.Version, PreviousVersion: m.Version,
		ExpectedManifest: want, ExpectedLaunchers: launchers}
	if err := verifyNoop(p); err != nil {
		return Plan{}, err
	}
	p.Notices = []string{"The installed release already matches the requested release; no files or services were changed."}
	return p, nil
}

func verifyNoop(p Plan) error {
	installed, err := Inspect(p.Layout)
	if err != nil {
		return err
	}
	if installed.Current != p.Version {
		return errf(CodeConflict, "the active release changed before repeat installation")
	}
	raw, err := readSmallFile(filepath.Join(p.Layout.VersionDir(p.Version), ManifestFileName))
	if err != nil || !bytes.Equal(raw, p.ExpectedManifest) {
		return errf(CodeConflict, "the installed release manifest differs from the requested release")
	}
	if p.Layout.Manager == ManagerNone {
		if err := AuditDesktopInstalled(p.Layout); err != nil {
			return err
		}
	} else {
		// AuditInstalled checks the release tree. Exact launcher content is
		// checked below; its label argument is not needed here.
		if err := AuditInstalled(p.Layout, nil); err != nil {
			return err
		}
	}
	for path, want := range p.ExpectedLaunchers {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			return errf(CodeConflict, "an installed launcher differs from the requested release")
		}
	}
	for path, want := range p.ExpectedLinks {
		got, err := os.Readlink(path)
		if err != nil || got != want {
			return errf(CodeConflict, "an installed application link differs from the requested release")
		}
	}
	return nil
}
