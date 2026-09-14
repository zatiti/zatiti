package server

import (
	"net/http"

	"github.com/zatiti/zatiti/internal/contract"
)

// authHeader carries the selected client credential from secure client
// storage. Its value never appears in operation input, logs or fault
// messages; only this byte-slice boundary carries secret authentication
// material, matching contract.Authenticator's contract.
const authHeader = "Authorization"

// credentialBytes reads the raw Authorization header value. An absent
// header yields nil, which the application layer resolves as an
// unauthenticated credential rather than the server guessing a default
// principal.
func credentialBytes(r *http.Request) []byte {
	v := r.Header.Get(authHeader)
	if v == "" {
		return nil
	}
	return []byte(v)
}

// certificateFingerprint returns the verified TLS peer's SHA-256 SPKI
// fingerprint. Only a connection that already completed mutual TLS
// verification carries PeerCertificates; the leaf leads the chain. No
// header or request field can substitute for this value: operation input
// cannot supply a fingerprint, and no self-asserted certificate name or
// profile name grants authority.
func certificateFingerprint(r *http.Request) (contract.Digest, bool) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return "", false
	}
	leaf := r.TLS.PeerCertificates[0]
	return contract.Hash(leaf.RawSubjectPublicKeyInfo), true
}
