package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// remoteTLSConfig builds the mutual-TLS configuration of the optional remote
// desktop listener: the controller's own certificate, and a client CA pool
// every peer must chain to. The server maps the verified peer's SPKI
// fingerprint to a provisioned principal; no name in the certificate grants
// anything by itself.
func remoteTLSConfig(cfg config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("loading the remote listener certificate: %w", err)
	}
	caPEM, err := os.ReadFile(cfg.TLSClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("reading the client CA bundle: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("client CA bundle holds no certificate")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS13,
	}, nil
}
