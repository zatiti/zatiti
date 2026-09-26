package packaging

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Frozen identifiers a revision 2 release manifest must declare.
const (
	ManifestSchema = "zatiti.release_manifest/v1"

	ContractRevision   int64 = 2
	OperationAPI             = "v1"
	RequestSchema            = "zatiti.request/v1"
	ResultSchema             = "zatiti.result/v1"
	MCPProtocolVersion       = "2025-11-25"

	// ManifestFileName and SignatureFileName are the two files in a
	// distribution tree that the manifest does not list: the manifest
	// cannot contain its own digest, and the signature covers the manifest.
	ManifestFileName  = "manifest.json"
	SignatureFileName = "manifest.sig.json"

	// ProjectComponent is the license entry for Zatiti itself.
	ProjectComponent = "github.com/zatiti/zatiti"
	ProjectLicense   = "Apache-2.0"
	LicensePath      = "LICENSE"

	// ApacheLicenseSHA256 is the digest of the unmodified Apache License 2.0
	// text committed at the repository root. A distribution whose LICENSE
	// differs fails validation.
	ApacheLicenseSHA256 = "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"

	maxManifestBytes = 1 << 20
)

// Distributions. The controller and the desktop client are packaged
// separately: a headless host installs the controller distribution alone.
const (
	DistributionController = "controller"
	DistributionDesktop    = "desktop"
)

// Artifact kinds.
const (
	KindControllerBinary    = "controller_binary"
	KindCredentialHelper    = "credential_helper"
	KindDesktopBundle       = "desktop_bundle"
	KindSerenityRuntime     = "serenity_runtime"
	KindSerenityReadFacade  = "serenity_read_facade"
	KindServiceTemplate     = "service_template"
	KindLicense             = "license"
	KindNotice              = "notice"
	KindSBOM                = "sbom"
	KindAttestationEvidence = "attestation_evidence"
	KindDocumentation       = "documentation"
)

// Secure helper kinds.
const (
	HelperOSKeychain        = "os_keychain"
	HelperHeadlessMasterKey = "headless_master_key"
)

// Attestation kinds. An attestation is a pointer at evidence in the tree, not
// a claim this package can establish.
const (
	AttestationCodeSignature = "code_signature"
	AttestationNotarization  = "notarization"
	AttestationQualification = "qualification"
)

// Manifest describes one release for one target. It carries no timestamp so
// that the same inputs always produce the same bytes.
type Manifest struct {
	Schema           string          `json:"schema"`
	ContractRevision int64           `json:"contract_revision"`
	Distribution     string          `json:"distribution"`
	Version          string          `json:"version"`
	Target           Target          `json:"target"`
	SourceRevision   string          `json:"source_revision"`
	Toolchain        string          `json:"toolchain"`
	Artifacts        []Artifact      `json:"artifacts"`
	Licenses         []LicenseNotice `json:"licenses"`
	SBOM             SBOMRef         `json:"sbom"`
	Protocols        Protocols       `json:"protocols"`
	Profiles         []Profile       `json:"profiles"`
	Serenity         SerenityPin     `json:"serenity"`
	SecureHelper     SecureHelper    `json:"secure_helper"`
	Desktop          *DesktopClient  `json:"desktop,omitempty"`
	Attestations     []Attestation   `json:"attestations"`
}

// Target is the operating system and architecture a release was built for.
type Target struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// Artifact is one regular file in the distribution tree.
type Artifact struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// Mode is the four-digit octal permission string, for example "0755".
	Mode string `json:"mode"`
}

// LicenseNotice binds one shipped component to its license and the notice
// file in the tree that preserves its attribution.
type LicenseNotice struct {
	Component string `json:"component"`
	Version   string `json:"version"`
	SPDX      string `json:"spdx"`
	Notice    string `json:"notice"`
}

// SBOMRef names the software bill of materials in the tree.
type SBOMRef struct {
	Format string `json:"format"` // spdx-json | cyclonedx-json
	Path   string `json:"path"`
}

// Protocols lists the wire contracts the release speaks.
type Protocols struct {
	OperationAPI  string `json:"operation_api"`
	RequestSchema string `json:"request_schema"`
	ResultSchema  string `json:"result_schema"`
	MCP           string `json:"mcp"`
}

// Profile is one adapter profile the release advertises as supported. A
// profile is listed only with the qualification evidence that supports it.
type Profile struct {
	Adapter  string `json:"adapter"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Evidence string `json:"evidence"`
}

// SerenityPin records the pinned Serenity distribution exactly as qualified.
// Every field comes from the dependency lock report and the qualification
// run; nothing is defaulted.
type SerenityPin struct {
	Source           string `json:"source"`
	Version          string `json:"version"`
	Revision         string `json:"revision"`
	License          string `json:"license"`
	InterfaceVersion string `json:"interface_version"`
	Runtime          string `json:"runtime"`
	ReadFacade       string `json:"read_facade"`
	Evidence         string `json:"evidence"`
}

// SecureHelper names the trusted local mechanism that custodies secrets on
// the target.
type SecureHelper struct {
	Kind string `json:"kind"`
	// Path is the absolute path of the OS-provided helper for os_keychain.
	// It is a well-known system path, not a packaged file. Empty for
	// headless_master_key.
	Path string `json:"path,omitempty"`
}

// Attestation points at evidence, in the tree, about one artifact.
type Attestation struct {
	Kind     string `json:"kind"`
	Subject  string `json:"subject"`
	Evidence string `json:"evidence"`
}

var (
	versionPattern   = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$`)
	revisionPattern  = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
	toolchainPattern = regexp.MustCompile(`^go1\.[0-9]+(\.[0-9]+)?$`)
	digestPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	modePattern      = regexp.MustCompile(`^0[0-7]{3}$`)
	tokenPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,199}$`)
)

var supportedTargets = map[Target]bool{
	{OS: "darwin", Arch: "amd64"}: true,
	{OS: "darwin", Arch: "arm64"}: true,
	{OS: "linux", Arch: "amd64"}:  true,
	{OS: "linux", Arch: "arm64"}:  true,
}

var artifactKinds = map[string]bool{
	KindControllerBinary: true, KindCredentialHelper: true, KindDesktopBundle: true,
	KindSerenityRuntime: true, KindSerenityReadFacade: true,
	KindServiceTemplate: true, KindLicense: true, KindNotice: true,
	KindSBOM: true, KindAttestationEvidence: true, KindDocumentation: true,
}

var executableKinds = map[string]bool{
	KindControllerBinary: true, KindCredentialHelper: true, KindSerenityRuntime: true, KindSerenityReadFacade: true,
}

// Validate checks every structural rule of a release manifest. For the
// controller distribution the optional local Serenity pin is checked last.
func (m Manifest) Validate() error {
	if m.Schema != ManifestSchema {
		return errf(CodeInvalidInput, "manifest schema must be %s", ManifestSchema)
	}
	if m.ContractRevision != ContractRevision {
		return errf(CodeCapabilityUnsupported, "contract revision %d is not supported", m.ContractRevision)
	}
	if !versionPattern.MatchString(m.Version) {
		return errf(CodeInvalidInput, "version must be a semantic version without build metadata")
	}
	if !supportedTargets[m.Target] {
		return errf(CodeCapabilityUnsupported, "target %s/%s is not a supported release target", printable(m.Target.OS), printable(m.Target.Arch))
	}
	if !revisionPattern.MatchString(m.SourceRevision) {
		return errf(CodeInvalidInput, "source revision must be a full lowercase hexadecimal commit identifier")
	}
	if !toolchainPattern.MatchString(m.Toolchain) {
		return errf(CodeInvalidInput, "toolchain must name a Go release")
	}
	index, err := m.validateArtifacts()
	if err != nil {
		return err
	}
	if err := m.validateLicenses(index); err != nil {
		return err
	}
	if err := m.validateSBOM(index); err != nil {
		return err
	}
	if m.Protocols != (Protocols{OperationAPI: OperationAPI, RequestSchema: RequestSchema, ResultSchema: ResultSchema, MCP: MCPProtocolVersion}) {
		return errf(CodeCapabilityUnsupported, "protocols must match contract revision %d exactly", ContractRevision)
	}
	if err := m.validateProfiles(index); err != nil {
		return err
	}
	if err := m.validateAttestations(index); err != nil {
		return err
	}
	switch m.Distribution {
	case DistributionController:
		return m.validateController(index)
	case DistributionDesktop:
		return m.validateDesktop(index)
	}
	return errf(CodeInvalidInput, "distribution must be %s or %s", DistributionController, DistributionDesktop)
}

// validateController applies the rules of the controller distribution: one
// controller binary, the secure helper, and nothing of the desktop client.
func (m Manifest) validateController(index map[string]Artifact) error {
	controllers := 0
	helpers := 0
	for _, a := range m.Artifacts {
		switch a.Kind {
		case KindControllerBinary:
			controllers++
		case KindCredentialHelper:
			helpers++
			if m.Target.OS != "darwin" || a.Path != "bin/zatiti-credential-helper" {
				return errf(CodeInvalidInput, "the credential helper belongs only at the fixed Mac controller path")
			}
		case KindDesktopBundle:
			return errf(CodeInvalidInput, "the desktop bundle is packaged separately from the controller distribution")
		}
	}
	if controllers != 1 {
		return errf(CodeInvalidInput, "a controller distribution carries exactly one controller binary, found %d", controllers)
	}
	if m.Target.OS == "darwin" && helpers != 1 {
		return errf(CodeInvalidInput, "a Mac controller distribution requires exactly one credential helper")
	}
	if m.Target.OS == "darwin" {
		signed := false
		for _, at := range m.Attestations {
			if at.Kind == AttestationCodeSignature && at.Subject == "bin/zatiti-credential-helper" {
				signed = true
			}
		}
		if !signed {
			return errf(CodeInvalidInput, "the Mac credential helper requires code-signature evidence")
		}
	}
	if m.Desktop != nil {
		return errf(CodeInvalidInput, "the desktop section belongs to the desktop distribution")
	}
	if err := m.validateSecureHelper(); err != nil {
		return err
	}
	return m.validateSerenity(index)
}

func (m Manifest) validateArtifacts() (map[string]Artifact, error) {
	index := make(map[string]Artifact, len(m.Artifacts))
	for i, a := range m.Artifacts {
		if err := validateRelPath(a.Path); err != nil {
			return nil, err
		}
		if a.Path == ManifestFileName || a.Path == SignatureFileName {
			return nil, errf(CodeInvalidInput, "artifact %s is reserved and cannot be listed", a.Path)
		}
		if i > 0 && m.Artifacts[i-1].Path >= a.Path {
			return nil, errf(CodeInvalidInput, "artifacts must be unique and sorted by path")
		}
		if !artifactKinds[a.Kind] {
			return nil, errf(CodeInvalidInput, "artifact %s has an unknown kind", a.Path)
		}
		if !digestPattern.MatchString(a.SHA256) {
			return nil, errf(CodeInvalidInput, "artifact %s must carry a lowercase hexadecimal SHA-256 digest", a.Path)
		}
		if a.Size < 0 {
			return nil, errf(CodeInvalidInput, "artifact %s has a negative size", a.Path)
		}
		mode, err := parseMode(a.Mode)
		if err != nil {
			return nil, errf(CodeInvalidInput, "artifact %s mode must be a four-digit octal permission", a.Path)
		}
		if mode&0o022 != 0 {
			return nil, errf(CodeInvalidInput, "artifact %s must not be group or world writable", a.Path)
		}
		if executableKinds[a.Kind] && mode&0o100 == 0 {
			return nil, errf(CodeInvalidInput, "artifact %s must be executable by its owner", a.Path)
		}
		index[a.Path] = a
	}
	for p := range index {
		for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
			if _, clash := index[dir]; clash {
				return nil, errf(CodeInvalidInput, "artifact %s is both a file and a directory", dir)
			}
		}
	}
	return index, nil
}

func (m Manifest) validateLicenses(index map[string]Artifact) error {
	license, ok := index[LicensePath]
	if !ok || license.Kind != KindLicense {
		return errf(CodeInvalidInput, "the distribution must include the project license at %s", LicensePath)
	}
	if license.SHA256 != ApacheLicenseSHA256 {
		return errf(CodeVerificationFailed, "the project license is not the unmodified Apache License 2.0 text")
	}
	project := false
	for i, l := range m.Licenses {
		if !tokenPattern.MatchString(l.Component) || !tokenPattern.MatchString(l.Version) || !tokenPattern.MatchString(l.SPDX) {
			return errf(CodeInvalidInput, "license entry %d must name a component, version and SPDX identifier", i)
		}
		if i > 0 && m.Licenses[i-1].Component >= l.Component {
			return errf(CodeInvalidInput, "license entries must be unique and sorted by component")
		}
		notice, ok := index[l.Notice]
		if !ok || (notice.Kind != KindLicense && notice.Kind != KindNotice) {
			return errf(CodeInvalidInput, "license entry for %s must reference a license or notice file in the tree", l.Component)
		}
		if l.Component == ProjectComponent {
			if l.SPDX != ProjectLicense || l.Notice != LicensePath || l.Version != m.Version {
				return errf(CodeInvalidInput, "the project license entry must be %s at %s for the release version", ProjectLicense, LicensePath)
			}
			project = true
		}
	}
	if !project {
		return errf(CodeInvalidInput, "license entries must include %s", ProjectComponent)
	}
	return nil
}

func (m Manifest) validateSBOM(index map[string]Artifact) error {
	if m.SBOM.Format != SBOMFormatSPDX && m.SBOM.Format != SBOMFormatCycloneDX {
		return errf(CodeInvalidInput, "SBOM format must be %s or %s", SBOMFormatSPDX, SBOMFormatCycloneDX)
	}
	if a, ok := index[m.SBOM.Path]; !ok || a.Kind != KindSBOM {
		return errf(CodeInvalidInput, "the SBOM must reference an sbom file in the tree")
	}
	return nil
}

func (m Manifest) validateProfiles(index map[string]Artifact) error {
	for i, p := range m.Profiles {
		if !tokenPattern.MatchString(p.Adapter) || !tokenPattern.MatchString(p.Name) || !tokenPattern.MatchString(p.Version) {
			return errf(CodeInvalidInput, "profile entry %d must name an adapter, profile and version", i)
		}
		if i > 0 {
			prev := m.Profiles[i-1]
			if prev.Adapter+"\x00"+prev.Name >= p.Adapter+"\x00"+p.Name {
				return errf(CodeInvalidInput, "profile entries must be unique and sorted by adapter and name")
			}
		}
		if a, ok := index[p.Evidence]; !ok || a.Kind != KindAttestationEvidence {
			return errf(CodeInvalidInput, "profile %s/%s is advertised without qualification evidence in the tree", p.Adapter, p.Name)
		}
	}
	return nil
}

func (m Manifest) validateSecureHelper() error {
	switch m.SecureHelper.Kind {
	case HelperOSKeychain:
		if m.Target.OS != "darwin" {
			return errf(CodeCapabilityUnsupported, "the OS keychain helper is supported on darwin targets only")
		}
		if !path.IsAbs(m.SecureHelper.Path) || path.Clean(m.SecureHelper.Path) != m.SecureHelper.Path || hasControl(m.SecureHelper.Path) {
			return errf(CodeInvalidInput, "the OS keychain helper must be a clean absolute system path")
		}
	case HelperHeadlessMasterKey:
		if m.SecureHelper.Path != "" {
			return errf(CodeInvalidInput, "the headless master key helper carries no path; the key location is provisioned per installation")
		}
	default:
		return errf(CodeInvalidInput, "secure helper kind must be %s or %s", HelperOSKeychain, HelperHeadlessMasterKey)
	}
	return nil
}

func (m Manifest) validateAttestations(index map[string]Artifact) error {
	for i, at := range m.Attestations {
		switch at.Kind {
		case AttestationCodeSignature, AttestationNotarization, AttestationQualification:
		default:
			return errf(CodeInvalidInput, "attestation entry %d has an unknown kind", i)
		}
		if _, ok := index[at.Subject]; !ok {
			return errf(CodeInvalidInput, "attestation entry %d names a subject that is not in the tree", i)
		}
		if a, ok := index[at.Evidence]; !ok || a.Kind != KindAttestationEvidence {
			return errf(CodeInvalidInput, "attestation entry %d makes a %s claim without evidence in the tree", i, at.Kind)
		}
		if at.Subject == at.Evidence {
			return errf(CodeInvalidInput, "attestation entry %d uses its subject as its own evidence", i)
		}
	}
	return nil
}

func (m Manifest) validateSerenity(index map[string]Artifact) error {
	s := m.Serenity
	if m.Target.OS == "darwin" && s == (SerenityPin{}) {
		for _, a := range m.Artifacts {
			if a.Kind == KindSerenityRuntime || a.Kind == KindSerenityReadFacade {
				return errf(CodeInvalidInput, "hosted Mac controller must not bundle a Serenity runtime or read facade")
			}
		}
		return nil
	}
	if s == (SerenityPin{}) {
		return errf(CodePrerequisiteMissing, "the Serenity distribution pin is not resolved; a release cannot be described until the runtime, read facade and qualification evidence are pinned")
	}
	for _, field := range []string{s.Source, s.Version, s.License, s.InterfaceVersion} {
		if !tokenPattern.MatchString(field) {
			return errf(CodeInvalidInput, "the Serenity pin must name its source, version, license and interface version")
		}
	}
	if !revisionPattern.MatchString(s.Revision) {
		return errf(CodeInvalidInput, "the Serenity pin must carry a full source revision")
	}
	if a, ok := index[s.Runtime]; !ok || a.Kind != KindSerenityRuntime {
		return errf(CodeInvalidInput, "the Serenity pin must reference a serenity_runtime file in the tree")
	}
	if a, ok := index[s.ReadFacade]; !ok || (a.Kind != KindSerenityReadFacade && a.Kind != KindSerenityRuntime) {
		return errf(CodeInvalidInput, "the Serenity pin must reference its read facade in the tree")
	}
	if a, ok := index[s.Evidence]; !ok || a.Kind != KindAttestationEvidence {
		return errf(CodeInvalidInput, "the Serenity pin must reference qualification evidence in the tree")
	}
	for _, l := range m.Licenses {
		if l.Component == s.Source && l.Version == s.Version && l.SPDX == s.License {
			return nil
		}
	}
	return errf(CodeInvalidInput, "the Serenity pin must have a matching license entry")
}

// Encode validates m and returns its canonical JSON: sorted keys, no HTML
// escaping, one trailing newline.
func Encode(m Manifest) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return canonicalJSON(m)
}

// Decode strictly parses and validates a manifest. Unknown fields, duplicate
// keys, trailing data and oversized input are rejected.
func Decode(data []byte) (Manifest, error) {
	var m Manifest
	if err := strictDecode(data, &m); err != nil {
		return Manifest{}, err
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func strictDecode(data []byte, v any) error {
	if len(data) > maxManifestBytes {
		return errf(CodeInvalidInput, "document exceeds %d bytes", maxManifestBytes)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errWrap(CodeInvalidInput, "document is not valid for its schema", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errf(CodeInvalidInput, "document has trailing data")
	}
	return nil
}

func canonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, errWrap(CodeInternalError, "document could not be encoded", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return nil, errWrap(CodeInternalError, "document could not be canonicalized", err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// encoding/json writes map keys in sorted order.
	if err := enc.Encode(generic); err != nil {
		return nil, errWrap(CodeInternalError, "document could not be canonicalized", err)
	}
	return buf.Bytes(), nil
}

// rejectDuplicateKeys walks the token stream because encoding/json silently
// keeps the last of two equal keys.
func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := walkValue(dec); err != nil {
		var perr *Error
		if errors.As(err, &perr) {
			return err
		}
		return errWrap(CodeInvalidInput, "document is not valid JSON", err)
	}
	return nil
}

func walkValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := keyTok.(string)
			if seen[key] {
				return errf(CodeInvalidInput, "document repeats the key %s", strconv.Quote(key))
			}
			seen[key] = true
			if err := walkValue(dec); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := walkValue(dec); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token() // closing delimiter
	return err
}

// validateRelPath accepts a clean, slash-separated, relative path with no
// parent traversal, backslash or control character.
func validateRelPath(p string) error {
	if p == "" || len(p) > 512 {
		return errf(CodeInvalidInput, "artifact path must be between 1 and 512 bytes")
	}
	if hasControl(p) || strings.ContainsRune(p, '\\') {
		return errf(CodeInvalidInput, "artifact path contains a forbidden character")
	}
	if path.IsAbs(p) || path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return errf(CodeInvalidInput, "artifact path %s must be clean and relative to the tree", strconv.Quote(p))
	}
	return nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// printable bounds and quotes an untrusted token before it enters a message.
func printable(s string) string {
	if len(s) > 32 {
		s = s[:32]
	}
	return quoteASCII(s)
}

func quoteASCII(s string) string {
	return strings.Trim(strconv.QuoteToASCII(s), `"`)
}

func parseMode(s string) (fs.FileMode, error) {
	if !modePattern.MatchString(s) {
		return 0, fmt.Errorf("mode %q is not four octal digits", s)
	}
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, err
	}
	return fs.FileMode(v), nil
}

func formatMode(m fs.FileMode) string {
	return fmt.Sprintf("%04o", uint32(m.Perm()))
}

func sortArtifacts(a []Artifact) {
	sort.Slice(a, func(i, j int) bool { return a[i].Path < a[j].Path })
}
