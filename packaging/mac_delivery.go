package packaging

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	MacDeliverySchema                = "zatiti.mac_delivery/v1"
	MacDeliverySignatureSchema       = "zatiti.mac_delivery_signature/v1"
	MaxMacDeliveryBytes              = 64 << 10
	maxMacAssetBytes           int64 = 2 << 30
)

// MacDelivery is an offline, signed download index. It contains no trust key
// or executable installer action; the caller supplies trusted public keys.
type MacDelivery struct {
	Schema           string               `json:"schema"`
	Release          MacReleaseDescriptor `json:"release"`
	ReleaseSignature MacReleaseSignature  `json:"release_signature"`
	Channel          string               `json:"channel"`
	ReleaseSequence  int64                `json:"release_sequence"`
	PublishedAt      string               `json:"published_at"`
	ExpiresAt        string               `json:"expires_at"`
	Assets           []MacDeliveryAsset   `json:"assets"`
}

type MacDeliveryAsset struct {
	Arch         string `json:"arch"`
	Distribution string `json:"distribution"`
	Filename     string `json:"filename"`
	URL          string `json:"url"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
}

type MacDeliverySignature struct {
	Schema    string `json:"schema"`
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

type MacDownloadPlan struct {
	Arch            string
	ReleaseSequence int64
	Controller      MacDeliveryAsset
	Desktop         MacDeliveryAsset
	Installer       MacDeliveryAsset
}

func EncodeMacDelivery(d MacDelivery) ([]byte, error) {
	if err := validateMacDelivery(d); err != nil {
		return nil, err
	}
	raw, err := canonicalJSON(d)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxMacDeliveryBytes {
		return nil, errf(CodeInvalidInput, "Mac delivery record exceeds 64 KiB")
	}
	return raw, nil
}

func DecodeMacDelivery(raw []byte) (MacDelivery, error) {
	var d MacDelivery
	if len(raw) == 0 || len(raw) > MaxMacDeliveryBytes {
		return d, errf(CodeInvalidInput, "Mac delivery record exceeds 64 KiB or is empty")
	}
	if err := strictDecode(raw, &d); err != nil {
		return MacDelivery{}, err
	}
	if err := validateMacDelivery(d); err != nil {
		return MacDelivery{}, err
	}
	canonical, err := canonicalJSON(d)
	if err != nil {
		return MacDelivery{}, err
	}
	if !bytes.Equal(raw, canonical) {
		return MacDelivery{}, errf(CodeInvalidInput, "Mac delivery record is not canonical JSON")
	}
	return d, nil
}

func validateMacDelivery(d MacDelivery) error {
	if d.Schema != MacDeliverySchema {
		return errf(CodeCapabilityUnsupported, "Mac delivery schema is unsupported")
	}
	if d.Channel != "stable" || d.ReleaseSequence <= 0 {
		return errf(CodeInvalidInput, "Mac delivery channel or sequence is invalid")
	}
	if err := validateMacRelease(d.Release); err != nil {
		return err
	}
	published, err := parseDeliveryTime(d.PublishedAt)
	if err != nil {
		return err
	}
	expires, err := parseDeliveryTime(d.ExpiresAt)
	if err != nil {
		return err
	}
	if !expires.After(published) || expires.Sub(published) > 30*24*time.Hour {
		return errf(CodeInvalidInput, "Mac delivery validity window is invalid")
	}
	if len(d.Assets) != 6 {
		return errf(CodeInvalidInput, "Mac delivery needs exactly six assets")
	}
	want := []string{"amd64/controller", "amd64/desktop", "amd64/installer", "arm64/controller", "arm64/desktop", "arm64/installer"}
	names := map[string]bool{}
	for i, a := range d.Assets {
		if a.Arch+"/"+a.Distribution != want[i] {
			return errf(CodeInvalidInput, "Mac delivery assets are missing, duplicate or unsorted")
		}
		ext := "tar.gz"
		if a.Distribution == "installer" {
			ext = "pkg"
		}
		expected := "zatiti-" + d.Release.Version + "-darwin-" + a.Arch + "-" + a.Distribution + "." + ext
		if a.Filename != expected || names[a.Filename] {
			return errf(CodeInvalidInput, "Mac delivery asset filename is invalid or duplicate")
		}
		names[a.Filename] = true
		if a.Size <= 0 || a.Size > maxMacAssetBytes || !digestPattern.MatchString(a.SHA256) {
			return errf(CodeInvalidInput, "Mac delivery asset size or digest is invalid")
		}
		if err := validateDeliveryURL(a.URL, a.Filename); err != nil {
			return err
		}
	}
	return nil
}

func parseDeliveryTime(s string) (time.Time, error) {
	if !strings.HasSuffix(s, "Z") {
		return time.Time{}, errf(CodeInvalidInput, "Mac delivery timestamp must be UTC")
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Format(time.RFC3339Nano) != s {
		return time.Time{}, errf(CodeInvalidInput, "Mac delivery timestamp is invalid or noncanonical")
	}
	return t, nil
}

var deliveryHostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)
var deliverySegmentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validateDeliveryURL(raw, filename string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Host == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return errf(CodeInvalidInput, "Mac delivery URL is unsafe")
	}
	host := u.Hostname()
	if !deliveryHostPattern.MatchString(host) || net.ParseIP(host) != nil || strings.Contains(u.Host, "@") || strings.Contains(u.Host, "%") {
		return errf(CodeInvalidInput, "Mac delivery URL host is unsafe")
	}
	if port := u.Port(); port != "" && port != "443" {
		return errf(CodeInvalidInput, "Mac delivery URL port is unsafe")
	}
	if u.RawPath != "" || strings.Contains(u.Path, "%") || strings.Contains(u.Path, "\\") || strings.Contains(u.Path, "//") || !strings.HasPrefix(u.Path, "/") || path.Clean(u.Path) != u.Path || path.Base(u.Path) != filename {
		return errf(CodeInvalidInput, "Mac delivery URL path is unsafe")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(u.Path, "/"), "/") {
		if !deliverySegmentPattern.MatchString(segment) || segment == "." || segment == ".." {
			return errf(CodeInvalidInput, "Mac delivery URL path is unsafe")
		}
	}
	if u.String() != raw {
		return errf(CodeInvalidInput, "Mac delivery URL is not canonical")
	}
	return nil
}

func macDeliveryMessage(digest string) []byte {
	return []byte(MacDeliverySignatureSchema + "\n" + digest + "\n")
}
func SignMacDelivery(raw []byte, signer crypto.Signer) (MacDeliverySignature, error) {
	if _, err := DecodeMacDelivery(raw); err != nil {
		return MacDeliverySignature{}, err
	}
	if signer == nil {
		return MacDeliverySignature{}, errf(CodePrerequisiteMissing, "no Mac delivery signer is configured")
	}
	pub, ok := signer.Public().(ed25519.PublicKey)
	if !ok || len(pub) != ed25519.PublicKeySize {
		return MacDeliverySignature{}, errf(CodeCapabilityUnsupported, "Mac delivery signer must hold an Ed25519 key")
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	sig, err := signer.Sign(rand.Reader, macDeliveryMessage(digest), crypto.Hash(0))
	if err != nil {
		return MacDeliverySignature{}, errWrap(CodeInternalError, "Mac delivery signer refused to sign", err)
	}
	if !ed25519.Verify(pub, macDeliveryMessage(digest), sig) {
		return MacDeliverySignature{}, errf(CodeInternalError, "Mac delivery signer produced an invalid signature")
	}
	return MacDeliverySignature{Schema: MacDeliverySignatureSchema, Algorithm: signatureAlgorithm, KeyID: KeyID(pub), SHA256: digest, Signature: base64.StdEncoding.EncodeToString(sig)}, nil
}

// VerifyMacDelivery checks both signed metadata layers and the caller's last
// accepted sequence watermark. A new release must have a strictly greater
// sequence; no key can be sourced from the downloaded record.
func VerifyMacDelivery(raw []byte, sig MacDeliverySignature, trusted []ed25519.PublicKey, now time.Time, minSequence int64) (MacDelivery, error) {
	d, err := DecodeMacDelivery(raw)
	if err != nil {
		return MacDelivery{}, err
	}
	if len(trusted) == 0 {
		return MacDelivery{}, errf(CodePrerequisiteMissing, "no trusted Mac delivery key is configured")
	}
	if sig.Schema != MacDeliverySignatureSchema || sig.Algorithm != signatureAlgorithm {
		return MacDelivery{}, errf(CodeCapabilityUnsupported, "Mac delivery signature format is unsupported")
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	if sig.SHA256 != digest {
		return MacDelivery{}, errf(CodeVerificationFailed, "Mac delivery signature covers different metadata")
	}
	signature, err := base64.StdEncoding.DecodeString(sig.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return MacDelivery{}, errf(CodeInvalidInput, "Mac delivery signature bytes are invalid")
	}
	verified := false
	for _, pub := range trusted {
		if len(pub) == ed25519.PublicKeySize && KeyID(pub) == sig.KeyID && ed25519.Verify(pub, macDeliveryMessage(digest), signature) {
			verified = true
			break
		}
	}
	if !verified {
		return MacDelivery{}, errf(CodeVerificationFailed, "Mac delivery is not signed by a trusted key")
	}
	releaseRaw, err := EncodeMacRelease(d.Release)
	if err != nil {
		return MacDelivery{}, err
	}
	if err := VerifyMacReleaseSignature(releaseRaw, d.ReleaseSignature, trusted); err != nil {
		return MacDelivery{}, err
	}
	published, _ := parseDeliveryTime(d.PublishedAt)
	expires, _ := parseDeliveryTime(d.ExpiresAt)
	now = now.UTC()
	if now.Before(published) || !now.Before(expires) {
		return MacDelivery{}, errf(CodeVerificationFailed, "Mac delivery is not within its validity window")
	}
	if minSequence < 0 || d.ReleaseSequence <= minSequence {
		return MacDelivery{}, errf(CodeVerificationFailed, "Mac delivery sequence is not newer than the accepted watermark")
	}
	return d, nil
}

// SelectMacDownloadPlan chooses only the verified record's native-arch assets.
// The caller must pass a hardware architecture, not a translated process arch.
func SelectMacDownloadPlan(d MacDelivery, arch string) (MacDownloadPlan, error) {
	if err := validateMacDelivery(d); err != nil {
		return MacDownloadPlan{}, err
	}
	if arch != "amd64" && arch != "arm64" {
		return MacDownloadPlan{}, errf(CodeCapabilityUnsupported, "Mac hardware architecture is unsupported")
	}
	plan := MacDownloadPlan{Arch: arch, ReleaseSequence: d.ReleaseSequence}
	for _, a := range d.Assets {
		if a.Arch != arch {
			continue
		}
		switch a.Distribution {
		case "controller":
			plan.Controller = a
		case "desktop":
			plan.Desktop = a
		case "installer":
			plan.Installer = a
		}
	}
	return plan, nil
}

// EncodeMacDeliverySignature serializes the detached signature document.
func EncodeMacDeliverySignature(sig MacDeliverySignature) ([]byte, error) { return canonicalJSON(sig) }

// DecodeMacDeliverySignature refuses unknown/duplicate fields and oversized input.
func DecodeMacDeliverySignature(raw []byte) (MacDeliverySignature, error) {
	if len(raw) == 0 || len(raw) > MaxMacDeliveryBytes {
		return MacDeliverySignature{}, errf(CodeInvalidInput, "Mac delivery signature exceeds 64 KiB or is empty")
	}
	var sig MacDeliverySignature
	if err := strictDecode(raw, &sig); err != nil {
		return MacDeliverySignature{}, err
	}
	canonical, err := canonicalJSON(sig)
	if err != nil {
		return MacDeliverySignature{}, err
	}
	if !bytes.Equal(raw, canonical) {
		return MacDeliverySignature{}, errf(CodeInvalidInput, "Mac delivery signature is not canonical JSON")
	}
	return sig, nil
}

// VerifyMacDownloadPlan keeps plan selection behind both signature checks.
func VerifyMacDownloadPlan(raw []byte, sig MacDeliverySignature, trusted []ed25519.PublicKey, now time.Time, minSequence int64, arch string) (MacDownloadPlan, error) {
	d, err := VerifyMacDelivery(raw, sig, trusted, now, minSequence)
	if err != nil {
		return MacDownloadPlan{}, err
	}
	return SelectMacDownloadPlan(d, arch)
}
