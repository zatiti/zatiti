package packaging

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// SBOM formats.
const (
	SBOMFormatSPDX      = "spdx-json"
	SBOMFormatCycloneDX = "cyclonedx-json"

	maxSBOMBytes = 16 << 20
)

type sbomComponent struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// SPDX spells the version field differently.
	VersionInfo string `json:"versionInfo"`
}

type sbomDocument struct {
	SPDXVersion string          `json:"spdxVersion"`
	Packages    []sbomComponent `json:"packages"`

	BOMFormat string `json:"bomFormat"`
	Metadata  struct {
		Component *sbomComponent `json:"component"`
	} `json:"metadata"`
	Components []sbomComponent `json:"components"`
}

// auditSBOM is the license notice audit that Build and VerifyTree run after
// the tree itself is known to match the manifest (so every notice file is
// present and unmodified, and LICENSE is the unmodified Apache License 2.0).
// It reads the SBOM from the tree and cross-checks it against the
// manifest's license entries in both directions. A component in the SBOM
// without a license entry ships without its notice; a license entry without
// an SBOM component ships something the bill of materials does not list.
func auditSBOM(m Manifest, root string) error {
	components, err := readSBOM(m.SBOM, root)
	if err != nil {
		return err
	}
	var findings []Finding
	licensed := make(map[string]string, len(m.Licenses))
	for _, l := range m.Licenses {
		licensed[l.Component] = l.Version
	}
	listed := make(map[string]bool, len(components))
	for _, c := range components {
		listed[c.Name] = true
		version, ok := licensed[c.Name]
		switch {
		case !ok:
			findings = append(findings, Finding{Path: m.SBOM.Path, Problem: "component " + printablePath(c.Name) + " has no license entry"})
		case c.Version != "" && c.Version != version:
			findings = append(findings, Finding{Path: m.SBOM.Path, Problem: "component " + printablePath(c.Name) + " version differs from its license entry"})
		}
	}
	for _, l := range m.Licenses {
		if !listed[l.Component] {
			findings = append(findings, Finding{Path: m.SBOM.Path, Problem: "license entry " + printablePath(l.Component) + " is not in the SBOM"})
		}
	}
	findings = append(findings, desktopSBOMFindings(m, components)...)
	if len(findings) != 0 {
		sortFindings(findings)
		return errFindings("the license notices and the SBOM disagree", findings)
	}
	return nil
}

func readSBOM(ref SBOMRef, root string) ([]sbomComponent, error) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(ref.Path)))
	if err != nil {
		return nil, errWrap(CodeNotFound, "the SBOM is not in the tree", err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxSBOMBytes+1))
	if err != nil {
		return nil, errWrap(CodeInternalError, "the SBOM could not be read", err)
	}
	if len(raw) > maxSBOMBytes {
		return nil, errf(CodeInvalidInput, "the SBOM exceeds %d bytes", maxSBOMBytes)
	}
	var doc sbomDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, errWrap(CodeInvalidInput, "the SBOM is not valid JSON", err)
	}
	var components []sbomComponent
	switch ref.Format {
	case SBOMFormatSPDX:
		if doc.SPDXVersion == "" {
			return nil, errf(CodeInvalidInput, "the SBOM does not declare an SPDX version")
		}
		for _, p := range doc.Packages {
			p.Version = p.VersionInfo
			components = append(components, p)
		}
	case SBOMFormatCycloneDX:
		if doc.BOMFormat != "CycloneDX" {
			return nil, errf(CodeInvalidInput, "the SBOM does not declare the CycloneDX format")
		}
		if doc.Metadata.Component != nil {
			components = append(components, *doc.Metadata.Component)
		}
		components = append(components, doc.Components...)
	default:
		return nil, errf(CodeInvalidInput, "SBOM format must be %s or %s", SBOMFormatSPDX, SBOMFormatCycloneDX)
	}
	if len(components) == 0 {
		return nil, errf(CodeInvalidInput, "the SBOM lists no components")
	}
	for _, c := range components {
		if c.Name == "" {
			return nil, errf(CodeInvalidInput, "the SBOM holds a component without a name")
		}
	}
	return components, nil
}
