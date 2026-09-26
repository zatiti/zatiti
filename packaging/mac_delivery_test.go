package packaging

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"
)

func deliveryFixture(t *testing.T) (MacDelivery, []byte, MacDeliverySignature, ed25519.PublicKey, ed25519.PrivateKey, time.Time) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	release, err := AssembleMacRelease(macParts(t, priv), []ed25519.PublicKey{pub})
	if err != nil {
		t.Fatal(err)
	}
	releaseRaw, err := EncodeMacRelease(release)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := SignMacRelease(releaseRaw, priv)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	d := MacDelivery{Schema: MacDeliverySchema, Release: release, ReleaseSignature: inner, Channel: "stable", ReleaseSequence: 12, PublishedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339Nano)}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, role := range []string{"controller", "desktop", "installer"} {
			ext := "tar.gz"
			if role == "installer" {
				ext = "pkg"
			}
			filename := "zatiti-" + release.Version + "-darwin-" + arch + "-" + role + "." + ext
			d.Assets = append(d.Assets, MacDeliveryAsset{Arch: arch, Distribution: role, Filename: filename, URL: "https://downloads.zatiti.example/releases/" + filename, Size: 1024, SHA256: strings.Repeat("a", 64)})
		}
	}
	raw, err := EncodeMacDelivery(d)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := SignMacDelivery(raw, priv)
	if err != nil {
		t.Fatal(err)
	}
	return d, raw, sig, pub, priv, now
}

func TestMacDeliveryRoundTripAndNativePlans(t *testing.T) {
	d, raw, sig, pub, _, now := deliveryFixture(t)
	got, err := VerifyMacDelivery(raw, sig, []ed25519.PublicKey{pub}, now, 11)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, mustEncodeDelivery(t, got)) {
		t.Fatal("roundtrip changed canonical bytes")
	}
	if len(raw) > MaxMacDeliveryBytes {
		t.Fatal("record exceeds bound")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		plan, err := SelectMacDownloadPlan(got, arch)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Arch != arch || plan.Controller.Arch != arch || plan.Desktop.Arch != arch || plan.Installer.Arch != arch || plan.ReleaseSequence != d.ReleaseSequence {
			t.Fatalf("wrong plan: %+v", plan)
		}
	}
	if _, err := SelectMacDownloadPlan(got, "386"); Code(err) != CodeCapabilityUnsupported {
		t.Fatalf("unsupported arch: %v", err)
	}
}
func mustEncodeDelivery(t *testing.T, d MacDelivery) []byte {
	t.Helper()
	raw, err := EncodeMacDelivery(d)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMacDeliveryRejectsTamperWrongKeyAndTimeReplay(t *testing.T) {
	d, raw, sig, pub, priv, now := deliveryFixture(t)
	bad := append([]byte(nil), raw...)
	i := bytes.Index(bad, []byte(`"release_sequence":12`))
	if i < 0 {
		t.Fatal("missing sequence")
	}
	bad[i+len(`"release_sequence":1`)] = '3'
	if _, err := VerifyMacDelivery(bad, sig, []ed25519.PublicKey{pub}, now, 11); Code(err) != CodeVerificationFailed {
		t.Fatalf("tamper: %v", err)
	}
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := VerifyMacDelivery(raw, sig, []ed25519.PublicKey{otherPub}, now, 11); Code(err) != CodeVerificationFailed {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := VerifyMacDelivery(raw, sig, nil, now, 11); Code(err) != CodePrerequisiteMissing {
		t.Fatalf("no key: %v", err)
	}
	if _, err := VerifyMacDelivery(raw, sig, []ed25519.PublicKey{pub}, now, 12); Code(err) != CodeVerificationFailed {
		t.Fatalf("replay: %v", err)
	}
	if _, err := VerifyMacDelivery(raw, sig, []ed25519.PublicKey{pub}, now.Add(-2*time.Hour), 11); Code(err) != CodeVerificationFailed {
		t.Fatalf("future: %v", err)
	}
	if _, err := VerifyMacDelivery(raw, sig, []ed25519.PublicKey{pub}, now.Add(25*time.Hour), 11); Code(err) != CodeVerificationFailed {
		t.Fatalf("expired: %v", err)
	}
	d.ReleaseSignature.Signature = "not-a-signature"
	innerTamper := mustEncodeDelivery(t, d)
	innerSig, err := SignMacDelivery(innerTamper, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyMacDelivery(innerTamper, innerSig, []ed25519.PublicKey{pub}, now, 11); err == nil {
		t.Fatal("accepted invalid inner signature")
	}
	sig.Schema = MacReleaseSignatureSchema
	if _, err := VerifyMacDelivery(raw, sig, []ed25519.PublicKey{pub}, now, 11); Code(err) != CodeCapabilityUnsupported {
		t.Fatalf("domain confusion: %v", err)
	}
}

func TestMacDeliveryRejectsAssetAndURLTricks(t *testing.T) {
	d, _, _, _, _, _ := deliveryFixture(t)
	for _, tc := range []struct {
		name   string
		change func(*MacDelivery)
	}{
		{"missing", func(d *MacDelivery) { d.Assets = d.Assets[:5] }},
		{"duplicate", func(d *MacDelivery) { d.Assets[1] = d.Assets[0] }},
		{"unsorted", func(d *MacDelivery) { d.Assets[0], d.Assets[1] = d.Assets[1], d.Assets[0] }},
		{"oversize", func(d *MacDelivery) { d.Assets[0].Size = maxMacAssetBytes + 1 }},
		{"wrong basename", func(d *MacDelivery) { d.Assets[0].Filename = "other.tar.gz" }},
		{"query secret", func(d *MacDelivery) { d.Assets[0].URL += "?token=secret" }},
		{"userinfo", func(d *MacDelivery) {
			d.Assets[0].URL = strings.Replace(d.Assets[0].URL, "https://", "https://user:pass@", 1)
		}},
		{"ip literal", func(d *MacDelivery) {
			d.Assets[0].URL = strings.Replace(d.Assets[0].URL, "downloads.zatiti.example", "127.0.0.1", 1)
		}},
		{"encoded traversal", func(d *MacDelivery) { d.Assets[0].URL = strings.Replace(d.Assets[0].URL, "/releases/", "/%2e%2e/", 1) }},
		{"path traversal", func(d *MacDelivery) {
			d.Assets[0].URL = strings.Replace(d.Assets[0].URL, "/releases/", "/releases/../", 1)
		}},
		{"different URL basename", func(d *MacDelivery) { d.Assets[0].URL += "-other" }},
		{"fragment", func(d *MacDelivery) { d.Assets[0].URL += "#secret" }},
		{"HTTP", func(d *MacDelivery) { d.Assets[0].URL = strings.Replace(d.Assets[0].URL, "https:", "http:", 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := d
			copy.Assets = append([]MacDeliveryAsset(nil), d.Assets...)
			tc.change(&copy)
			if _, err := EncodeMacDelivery(copy); err == nil {
				t.Fatal("accepted unsafe record")
			}
		})
	}
}

func TestMacDeliveryCanonicalAndValidityBounds(t *testing.T) {
	d, raw, _, _, _, now := deliveryFixture(t)
	noncanonical := bytes.Replace(raw, []byte(`"channel":"stable"`), []byte(`"channel": "stable"`), 1)
	if _, err := DecodeMacDelivery(noncanonical); Code(err) != CodeInvalidInput {
		t.Fatalf("noncanonical: %v", err)
	}
	if _, err := DecodeMacDelivery(append(raw, bytes.Repeat([]byte(" "), MaxMacDeliveryBytes)...)); Code(err) != CodeInvalidInput {
		t.Fatalf("oversize: %v", err)
	}
	d.ExpiresAt = now.Add(31 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := EncodeMacDelivery(d); Code(err) != CodeInvalidInput {
		t.Fatalf("long validity: %v", err)
	}
	d.ExpiresAt = now.Add(time.Hour).Format(time.RFC3339Nano)
	d.PublishedAt = now.Format(time.RFC3339Nano)
	d.PublishedAt = strings.Replace(d.PublishedAt, "Z", "+00:00", 1)
	if _, err := EncodeMacDelivery(d); Code(err) != CodeInvalidInput {
		t.Fatalf("non-UTC: %v", err)
	}
}

func TestMacDeliverySignatureCodecAndVerifiedPlan(t *testing.T) {
	_, raw, sig, pub, _, now := deliveryFixture(t)
	encoded, err := EncodeMacDeliverySignature(sig)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMacDeliverySignature(encoded)
	if err != nil || decoded != sig {
		t.Fatalf("signature roundtrip: %+v %v", decoded, err)
	}
	plan, err := VerifyMacDownloadPlan(raw, decoded, []ed25519.PublicKey{pub}, now, 11, "arm64")
	if err != nil || plan.Installer.Arch != "arm64" {
		t.Fatalf("verified plan: %+v %v", plan, err)
	}
	if _, err := VerifyMacDownloadPlan(raw, decoded, []ed25519.PublicKey{pub}, now, 12, "arm64"); Code(err) != CodeVerificationFailed {
		t.Fatalf("replayed plan: %v", err)
	}
}
