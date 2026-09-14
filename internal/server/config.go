package server

import (
	"crypto/tls"
	"fmt"
)

// Config selects the transports New binds. SocketPath is mandatory: the
// controller always serves the local private Unix-domain socket. RemoteAddress
// is optional; when set, the server also binds an authenticated TLS listener
// for explicit remote desktop access on that address, and TLSConfig must
// require and verify a client certificate. MaxBodyBytes bounds every request
// envelope read from either transport.
type Config struct {
	SocketPath    string
	RemoteAddress string
	TLSConfig     *tls.Config
	MaxBodyBytes  int64
}

// validate checks the config shape New requires. It never inspects the
// filesystem or the network; New itself surfaces bind-time failures.
func (c Config) validate() error {
	if c.SocketPath == "" {
		return fmt.Errorf("server: SocketPath is required")
	}
	if c.MaxBodyBytes <= 0 {
		return fmt.Errorf("server: MaxBodyBytes must be positive")
	}
	if c.RemoteAddress == "" {
		return nil
	}
	if c.TLSConfig == nil {
		return fmt.Errorf("server: RemoteAddress requires TLSConfig")
	}
	if c.TLSConfig.ClientAuth != tls.RequireAndVerifyClientCert {
		return fmt.Errorf("server: RemoteAddress requires TLSConfig.ClientAuth = tls.RequireAndVerifyClientCert")
	}
	return nil
}
