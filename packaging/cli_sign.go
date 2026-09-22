package packaging

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
)

// cliSign signs a manifest: it is Sign behind a flag parser, reading the
// Ed25519 private key from a PEM PKCS8 file. This package holds no key of
// its own; the caller supplies one, exactly as Sign's own doc comment
// requires.
func cliSign(args []string, stdout io.Writer) error {
	fs := newFlagSet("sign")
	manifestPath := fs.String("manifest", "", "the manifest.json to sign (required)")
	keyPath := fs.String("key", "", "a PEM PKCS8 Ed25519 private key (required)")
	outPath := fs.String("out", "", "defaults to manifest.sig.json next to --manifest")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *manifestPath == "" || *keyPath == "" {
		return errf(CodeInvalidInput, "sign: --manifest and --key are required")
	}
	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		return errWrap(CodeNotFound, "the manifest could not be read", err)
	}
	signer, err := loadEd25519PrivateKey(*keyPath)
	if err != nil {
		return err
	}
	sig, err := Sign(raw, signer)
	if err != nil {
		return err
	}
	out, err := EncodeSignature(sig)
	if err != nil {
		return err
	}
	dest := *outPath
	if dest == "" {
		dest = filepath.Join(filepath.Dir(*manifestPath), SignatureFileName)
	}
	if err := os.WriteFile(dest, out, 0o644); err != nil {
		return errWrap(CodeInternalError, "the signature could not be written", err)
	}
	_, err = stdout.Write(out)
	return err
}

// cliVerify verifies a manifest signature against caller-supplied trusted
// keys and, with --tree, the distribution tree against the manifest: the
// same two checks install performs before it ever plans a step, available
// standalone so an operator or a qualification run can prove a release is
// genuine before installing it anywhere.
func cliVerify(args []string, stdout io.Writer) error {
	fs := newFlagSet("verify")
	manifestPath := fs.String("manifest", "", "the manifest.json to verify (required)")
	sigPath := fs.String("sig", "", "the manifest.sig.json to verify (required)")
	var trustedPaths stringListFlag
	fs.Var(&trustedPaths, "trusted", "a PEM PKIX Ed25519 public key trusted to sign; repeatable (required)")
	treeRoot := fs.String("tree", "", "also verify the distribution tree at this root against the manifest")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *manifestPath == "" || *sigPath == "" || len(trustedPaths) == 0 {
		return errf(CodeInvalidInput, "verify: --manifest, --sig and at least one --trusted key are required")
	}
	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		return errWrap(CodeNotFound, "the manifest could not be read", err)
	}
	sigRaw, err := os.ReadFile(*sigPath)
	if err != nil {
		return errWrap(CodeNotFound, "the signature could not be read", err)
	}
	sig, err := DecodeSignature(sigRaw)
	if err != nil {
		return err
	}
	trusted := make([]ed25519.PublicKey, 0, len(trustedPaths))
	for _, p := range trustedPaths {
		pub, err := loadEd25519PublicKey(p)
		if err != nil {
			return err
		}
		trusted = append(trusted, pub)
	}
	if err := VerifySignature(raw, sig, trusted); err != nil {
		return err
	}
	m, err := Decode(raw)
	if err != nil {
		return err
	}
	if *treeRoot != "" {
		absTree, err := filepath.Abs(*treeRoot)
		if err != nil {
			return errWrap(CodeInvalidInput, "the tree root could not be resolved", err)
		}
		if err := VerifyTree(m, absTree); err != nil {
			return err
		}
	}
	return writeJSONDoc(stdout, map[string]any{"ok": true, "distribution": m.Distribution, "version": m.Version, "key_id": sig.KeyID})
}

func loadEd25519PrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errWrap(CodeNotFound, "the signing key could not be read", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errf(CodeInvalidInput, "the signing key is not PEM-encoded")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errWrap(CodeInvalidInput, "the signing key is not a PKCS8 private key", err)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errf(CodeCapabilityUnsupported, "the signing key must be Ed25519")
	}
	return priv, nil
}

func loadEd25519PublicKey(path string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errWrap(CodeNotFound, "a trusted key could not be read", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errf(CodeInvalidInput, "a trusted key is not PEM-encoded")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errWrap(CodeInvalidInput, "a trusted key is not a PKIX public key", err)
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, errf(CodeCapabilityUnsupported, "a trusted key must be Ed25519")
	}
	return pub, nil
}
