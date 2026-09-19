package integration_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/client"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/server"
)

// Synthetic certificate authority and leaves, minted per test; never
// checked-in key material.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func randomSerial(t *testing.T) *big.Int {
	t.Helper()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	return serial
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: randomSerial(t), Subject: pkix.Name{CommonName: "integration-test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("ca certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ca parse: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool}
}

// leaf mints a CA-signed server or client certificate.
func (ca *testCA) leaf(t *testing.T, name string, isServer bool) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("leaf key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: randomSerial(t), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	if isServer {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.DNSNames = []string{"localhost"}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("leaf certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("leaf parse: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: cert}, cert
}

// serveRemote starts the real server with both the local socket and the
// mutual-TLS remote listener on an ephemeral loopback port.
func (f *fixture) serveRemote(ca *testCA, serverCert tls.Certificate) (sock, addr string) {
	f.t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		f.t.Fatalf("reserve port: %v", err)
	}
	addr = l.Addr().String()
	_ = l.Close()
	sock = f.socketPath()
	srv, err := server.New(server.Config{
		SocketPath: sock, RemoteAddress: addr, MaxBodyBytes: 4 << 20,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{serverCert}, ClientCAs: ca.pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS13},
	}, f.app)
	if err != nil {
		f.t.Fatalf("server.New: %v", err)
	}
	f.runServer(srv)
	return sock, addr
}

// remoteClient is internal/client configured for the remote listener with
// the given client certificate.
func remoteClient(t *testing.T, addr string, ca *testCA, clientCert tls.Certificate, creds contract.CredentialSource) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{
		RemoteURL: "https://" + addr, Timeout: 30 * time.Second,
		TLSConfig: &tls.Config{RootCAs: ca.pool, Certificates: []tls.Certificate{clientCert}, ServerName: "localhost", MinVersion: tls.VersionTLS13},
	}, creds)
	if err != nil {
		t.Fatalf("client.New remote: %v", err)
	}
	return c
}

// mapCertificate provisions a credential whose custodied bytes are the
// certificate's SubjectPublicKeyInfo, so identity's stored SHA-256 equals
// the SPKI fingerprint the TLS layer presents (internal/server/auth.go
// certificateFingerprint; internal/identity/authn.go AuthenticateCertificate).
func (f *fixture) mapCertificate(principal contract.ID, cert *x509.Certificate, key string) contract.ID {
	f.t.Helper()
	ref, err := f.secrets.Put(context.Background(), "integration/certificate/"+key, cert.RawSubjectPublicKeyInfo)
	if err != nil {
		f.t.Fatalf("custody certificate: %v", err)
	}
	res := f.must(f.owner, "credential.provision", "certificate-"+key, map[string]any{
		"scope": f.scope(), "principal_id": principal, "store_ref": ref,
	})
	var out struct {
		Resource struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
		} `json:"resource"`
	}
	decode(f.t, res.Data, &out)
	return out.Resource.ID
}

// TestRemoteCertificateMapsToProvisionedPrincipal (Z01 unauthorized
// credential access; Z13; desktop contract mutual-TLS mapping): over the
// remote listener a verified client certificate resolves to exactly the
// principal its SPKI fingerprint was provisioned for, an unprovisioned
// certificate signed by the same CA is refused, a bearer credential naming
// another principal alongside the certificate is refused, the remote
// listener never bootstraps, and revoking the certificate's credential
// denies the very next call while the local socket keeps working.
func TestRemoteCertificateMapsToProvisionedPrincipal(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	f.provisionTransportCredential(transportToken)
	ca := newTestCA(t)
	serverCert, _ := ca.leaf(t, "controller", true)
	ownerCert, ownerLeaf := ca.leaf(t, "owner-desktop", false)
	strayCert, _ := ca.leaf(t, "stray-desktop", false)
	sock, addr := f.serveRemote(ca, serverCert)
	credential := f.mapCertificate(f.owner.PrincipalID, ownerLeaf, "owner")
	agent := f.newAgent("remote-bearer", f.scope())

	// The mapped certificate acts as the owner: its mutation is retained
	// under the owner's principal.
	viaCert := remoteClient(t, addr, ca, ownerCert, nil)
	res, err := viaCert.Call(context.Background(), "principal.create", contract.Request{
		Schema: contract.SchemaRequest, SubmissionKey: "remote-1", Input: mustJSON(principalInput(f, "remote-agent")),
	})
	if err != nil {
		t.Fatalf("mapped certificate call: %v", err)
	}
	cmd, err := f.command("principal.create", "remote-1")
	if err != nil || cmd.ID != res.CommandID {
		t.Fatalf("remote command %+v (err %v), want %s retained", cmd, err, res.CommandID)
	}
	var retained struct {
		Resource struct {
			PrincipalID contract.ID `json:"principal_id"`
		} `json:"resource"`
	}
	lookup := f.must(f.owner, "command.get", "", map[string]any{"scope": f.scope(), "submission_key": "remote-1", "operation": "principal.create", "operation_version": 1})
	decode(t, lookup.Data, &retained)
	if retained.Resource.PrincipalID != f.owner.PrincipalID {
		t.Fatalf("remote command was attributed to %s, the certificate maps to the owner %s", retained.Resource.PrincipalID, f.owner.PrincipalID)
	}

	statusInput := mustJSON(map[string]any{"scope": f.scope()})
	// An unprovisioned certificate from the trusted CA carries no authority.
	// identity refuses an unknown credential, bearer or certificate alike,
	// as verification_failed (internal/identity/authn.go authenticateDigest),
	// while the server's own mismatch check below is permission_denied: an
	// unauthenticated caller sees two codes (HTTP 422 vs 403) depending on
	// which layer refused it. Recorded as an observation for identity/server.
	_, err = remoteClient(t, addr, ca, strayCert, nil).Call(context.Background(), "installation.status", contract.Request{Schema: contract.SchemaRequest, Input: statusInput})
	if code := faultCode(err); code != contract.CodePermissionDenied && code != contract.CodeVerificationFailed {
		t.Errorf("unprovisioned certificate: %v, want a refusal", err)
	}
	// A bearer credential alongside the certificate must name the same
	// principal; the agent's does not.
	_, err = remoteClient(t, addr, ca, ownerCert, staticCredential(agent.token)).Call(context.Background(), "installation.status", contract.Request{Schema: contract.SchemaRequest, Input: statusInput})
	if faultCode(err) != contract.CodePermissionDenied {
		t.Errorf("mismatched bearer alongside the certificate: %v, want permission_denied", err)
	}
	// The owner's own bearer alongside the owner's certificate is fine.
	if _, err := remoteClient(t, addr, ca, ownerCert, staticCredential(transportToken)).Call(context.Background(), "installation.status", contract.Request{Schema: contract.SchemaRequest, Input: statusInput}); err != nil {
		t.Errorf("matching bearer alongside the certificate: %v", err)
	}
	// Remote cannot bootstrap, even as the mapped owner.
	_, err = viaCert.Call(context.Background(), "installation.init", contract.Request{Schema: contract.SchemaRequest, Input: initInput()})
	if faultCode(err) != contract.CodePermissionDenied {
		t.Errorf("remote bootstrap: %v, want permission_denied", err)
	}

	// Revocation of the certificate credential denies the next remote call;
	// the owner's local credential is untouched.
	f.must(f.owner, "credential.revoke", "revoke-certificate", map[string]any{"scope": f.scope(), "id": credential, "expected_version": 1})
	_, err = viaCert.Call(context.Background(), "installation.status", contract.Request{Schema: contract.SchemaRequest, Input: statusInput})
	if code := faultCode(err); code != contract.CodePermissionDenied && code != contract.CodeVerificationFailed {
		t.Errorf("revoked certificate credential: %v, want a refusal", err)
	}
	if _, err := newClient(t, sock, staticCredential(transportToken)).Call(context.Background(), "installation.status", contract.Request{Schema: contract.SchemaRequest, Input: statusInput}); err != nil {
		t.Errorf("local socket after remote revocation: %v", err)
	}
}
