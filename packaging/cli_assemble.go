package packaging

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
)

// assembleDescriptor is the JSON input to the "assemble" driver command.
// Every nested type is the manifest's own exported type with its own JSON
// tags (Target, LicenseNotice, SBOMRef, Profile, SerenityPin, SecureHelper,
// DesktopClient, Attestation), so a descriptor reads like the manifest
// section it produces and this file defines no field this package does not
// already validate through Build and Manifest.Validate.
type assembleDescriptor struct {
	Distribution   string            `json:"distribution"`
	Version        string            `json:"version"`
	Target         Target            `json:"target"`
	SourceRevision string            `json:"source_revision"`
	Toolchain      string            `json:"toolchain"`
	Kinds          map[string]string `json:"kinds"`
	Licenses       []LicenseNotice   `json:"licenses"`
	SBOM           SBOMRef           `json:"sbom"`
	Profiles       []Profile         `json:"profiles,omitempty"`
	Serenity       SerenityPin       `json:"serenity,omitempty"`
	SecureHelper   SecureHelper      `json:"secure_helper,omitempty"`
	Desktop        *DesktopClient    `json:"desktop,omitempty"`
	Attestations   []Attestation     `json:"attestations,omitempty"`
}

// cliAssemble measures a staged tree into a manifest: it is Build, driven
// from a descriptor file instead of a Go literal. Given the same tree and
// descriptor it always produces the same bytes, because Build does.
func cliAssemble(args []string, stdout io.Writer) error {
	fs := newFlagSet("assemble")
	root := fs.String("root", "", "the staged distribution tree (required)")
	descriptorPath := fs.String("descriptor", "", "the assembly descriptor JSON file (required)")
	write := fs.Bool("write", false, "write manifest.json into --root")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *root == "" || *descriptorPath == "" {
		return errf(CodeInvalidInput, "assemble: --root and --descriptor are required")
	}
	raw, err := os.ReadFile(*descriptorPath)
	if err != nil {
		return errWrap(CodeNotFound, "the assembly descriptor could not be read", err)
	}
	var d assembleDescriptor
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return errWrap(CodeInvalidInput, "the assembly descriptor is not valid for its schema", err)
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		return errWrap(CodeInvalidInput, "the tree root could not be resolved", err)
	}
	m, err := Build(BuildInput{
		Root: absRoot, Distribution: d.Distribution, Version: d.Version, Target: d.Target,
		SourceRevision: d.SourceRevision, Toolchain: d.Toolchain, Kinds: d.Kinds,
		Licenses: d.Licenses, SBOM: d.SBOM, Profiles: d.Profiles, Serenity: d.Serenity,
		SecureHelper: d.SecureHelper, Desktop: d.Desktop, Attestations: d.Attestations,
	})
	if err != nil {
		return err
	}
	out, err := Encode(m)
	if err != nil {
		return err
	}
	if *write {
		if err := os.WriteFile(filepath.Join(absRoot, ManifestFileName), out, 0o644); err != nil {
			return errWrap(CodeInternalError, "the manifest could not be written", err)
		}
	}
	_, err = stdout.Write(out)
	return err
}

// cliAssembleBundle archives a built Flutter bundle deterministically: it is
// AssembleBundle behind a flag parser.
func cliAssembleBundle(args []string, stdout io.Writer) error {
	fs := newFlagSet("assemble-bundle")
	dir := fs.String("dir", "", "the built application bundle directory (required)")
	archive := fs.String("archive", "", "the output archive path (required)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *dir == "" || *archive == "" {
		return errf(CodeInvalidInput, "assemble-bundle: --dir and --archive are required")
	}
	absDir, err := filepath.Abs(*dir)
	if err != nil {
		return errWrap(CodeInvalidInput, "the bundle directory could not be resolved", err)
	}
	absArchive, err := filepath.Abs(*archive)
	if err != nil {
		return errWrap(CodeInvalidInput, "the archive path could not be resolved", err)
	}
	sum, size, err := AssembleBundle(absDir, absArchive)
	if err != nil {
		return err
	}
	return writeJSONDoc(stdout, map[string]any{"sha256": sum, "size": size})
}

// cliKeygen generates a local Ed25519 key pair for development and testing
// signatures only. This repository holds no release identity: a real
// release signer's key must come from one the release workflow owner
// authorizes separately, and this command must never be used to mint one for
// an actual release. No command in this driver signs, notarizes or
// publishes with a real identity.
func cliKeygen(args []string, stdout io.Writer) error {
	fs := newFlagSet("keygen")
	privOut := fs.String("private-out", "", "path for the new PEM PKCS8 private key (required)")
	pubOut := fs.String("public-out", "", "path for the new PEM PKIX public key (required)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *privOut == "" || *pubOut == "" {
		return errf(CodeInvalidInput, "keygen: --private-out and --public-out are required")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return errWrap(CodeInternalError, "the OS random source failed", err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return errWrap(CodeInternalError, "the private key could not be encoded", err)
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return errWrap(CodeInternalError, "the public key could not be encoded", err)
	}
	if err := writeNewFile(*privOut, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}), 0o600); err != nil {
		return err
	}
	if err := writeNewFile(*pubOut, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}), 0o644); err != nil {
		return err
	}
	return writeJSONDoc(stdout, map[string]any{"key_id": KeyID(pub)})
}

// writeNewFile creates path; it never overwrites an existing file, the same
// discipline ProvisionMasterKey uses for the same reason: a silently
// replaced key destroys whatever it protected.
func writeNewFile(path string, content []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return errWrap(CodeConflict, "the key file could not be created; it must not already exist", err)
	}
	_, err = f.Write(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return errWrap(CodeInternalError, "the key file could not be written", err)
	}
	return nil
}

// writeJSONDoc writes v as indented JSON with a trailing newline. Every
// driver command's success output goes through this one function.
func writeJSONDoc(w io.Writer, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errWrap(CodeInternalError, "the result could not be encoded", err)
	}
	raw = append(raw, '\n')
	_, err = w.Write(raw)
	return err
}
