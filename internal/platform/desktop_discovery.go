package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/zatiti/zatiti/internal/contract"
)

const (
	DesktopDiscoverySchema   = "zatiti.desktop-discovery/v1"
	desktopDiscoveryFile     = "desktop.json"
	desktopDiscoveryMaxBytes = 4096
)

// DesktopDiscovery contains only the local socket and, after committed
// bootstrap, the nonsecret locator of the persisted owner credential.
type DesktopDiscovery struct {
	Schema          string      `json:"schema"`
	ProtocolVersion int         `json:"protocol_version"`
	SocketPath      string      `json:"socket_path"`
	InstallationID  contract.ID `json:"installation_id,omitempty"`
	KeychainService string      `json:"keychain_service,omitempty"`
	KeychainAccount string      `json:"keychain_account,omitempty"`
}

// PublishDesktopDiscovery atomically replaces the owner-only discovery record.
// It does not infer initialized state from the presence of a socket.
func (p *Platform) PublishDesktopDiscovery(ctx context.Context, d DesktopDiscovery) error {
	if err := ctx.Err(); err != nil {
		return errWrap(contractCodeControllerUnavailable, "desktop discovery publication was interrupted", err)
	}
	if err := p.ensureOpen(); err != nil {
		return err
	}
	if err := validateDesktopDiscovery(d, p.stateDir); err != nil {
		return err
	}
	if err := checkDesktopDirectory(p.stateDir); err != nil {
		return err
	}
	path := filepath.Join(p.stateDir, desktopDiscoveryFile)
	if err := checkExistingDesktopFile(path); err != nil {
		return err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return errf(contractCodeInvalidInput, "desktop discovery cannot be encoded")
	}
	if len(raw) > desktopDiscoveryMaxBytes {
		return errf(contractCodeInvalidInput, "desktop discovery exceeds size limit")
	}
	if err := ctx.Err(); err != nil {
		return errWrap(contractCodeControllerUnavailable, "desktop discovery publication was interrupted", err)
	}
	// writeFileSync creates a 0600 same-directory temporary file, fsyncs it,
	// renames it and fsyncs the directory. The old record survives failed writes.
	return writeFileSync(path, raw)
}

// ReadDesktopDiscovery reads a published record without opening secret custody.
// A missing record returns a not_found error; stale records remain readable.
func ReadDesktopDiscovery(stateDir string) (DesktopDiscovery, error) {
	var d DesktopDiscovery
	if !filepath.IsAbs(stateDir) || filepath.Clean(stateDir) != stateDir {
		return d, errf(contractCodeInvalidInput, "desktop discovery state directory is unsafe")
	}
	if err := checkDesktopDirectory(stateDir); err != nil {
		return d, err
	}
	path := filepath.Join(stateDir, desktopDiscoveryFile)
	f, err := openPrivate(path, os.O_RDONLY, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return d, errf(contractCodeNotFound, "desktop discovery has not been published")
	}
	if err != nil {
		return d, errWrap(contractCodeControllerUnavailable, "desktop discovery cannot be opened", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return d, errWrap(contractCodeControllerUnavailable, "desktop discovery cannot be inspected", err)
	}
	if err := validateDesktopFileInfo(info); err != nil {
		return d, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, desktopDiscoveryMaxBytes+1))
	if err != nil {
		return d, errWrap(contractCodeControllerUnavailable, "desktop discovery cannot be read", err)
	}
	if len(raw) > desktopDiscoveryMaxBytes || !utf8.Valid(raw) {
		return d, errf(contractCodeInvalidInput, "desktop discovery is malformed")
	}
	if err := decodeDesktopDiscovery(raw, &d); err != nil {
		return DesktopDiscovery{}, err
	}
	if err := validateDesktopDiscovery(d, stateDir); err != nil {
		return DesktopDiscovery{}, err
	}
	return d, nil
}

func decodeDesktopDiscovery(raw []byte, d *DesktopDiscovery) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	first, err := dec.Token()
	if err != nil || first != json.Delim('{') {
		return errf(contractCodeInvalidInput, "desktop discovery is malformed")
	}
	allowed := map[string]bool{"schema": true, "protocol_version": true, "socket_path": true, "installation_id": true, "keychain_service": true, "keychain_account": true}
	seen := make(map[string]bool, len(allowed))
	values := make(map[string]json.RawMessage, len(allowed))
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return errf(contractCodeInvalidInput, "desktop discovery is malformed")
		}
		key, ok := token.(string)
		if !ok || !allowed[key] || seen[key] {
			return errf(contractCodeInvalidInput, "desktop discovery has an unknown or duplicate field")
		}
		seen[key] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return errf(contractCodeInvalidInput, "desktop discovery is malformed")
		}
		values[key] = value
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return errf(contractCodeInvalidInput, "desktop discovery is malformed")
	}
	if _, err := dec.Token(); err != io.EOF {
		return errf(contractCodeInvalidInput, "desktop discovery has trailing content")
	}
	if !seen["schema"] || !seen["protocol_version"] || !seen["socket_path"] {
		return errf(contractCodeInvalidInput, "desktop discovery is incomplete")
	}
	if err := json.Unmarshal(values["schema"], &d.Schema); err != nil {
		return errf(contractCodeInvalidInput, "desktop discovery schema is malformed")
	}
	if err := json.Unmarshal(values["protocol_version"], &d.ProtocolVersion); err != nil {
		return errf(contractCodeInvalidInput, "desktop discovery protocol is malformed")
	}
	if err := json.Unmarshal(values["socket_path"], &d.SocketPath); err != nil {
		return errf(contractCodeInvalidInput, "desktop discovery socket is malformed")
	}
	if seen["installation_id"] {
		if err := json.Unmarshal(values["installation_id"], &d.InstallationID); err != nil {
			return errf(contractCodeInvalidInput, "desktop discovery installation is malformed")
		}
	}
	if seen["keychain_service"] {
		if err := json.Unmarshal(values["keychain_service"], &d.KeychainService); err != nil {
			return errf(contractCodeInvalidInput, "desktop discovery keychain service is malformed")
		}
	}
	if seen["keychain_account"] {
		if err := json.Unmarshal(values["keychain_account"], &d.KeychainAccount); err != nil {
			return errf(contractCodeInvalidInput, "desktop discovery keychain account is malformed")
		}
	}
	// Optional locator fields must be absent pre-bootstrap, not present as null/empty.
	for _, key := range []string{"installation_id", "keychain_service", "keychain_account"} {
		if seen[key] && (bytes.Equal(values[key], []byte("null")) || bytes.Equal(values[key], []byte(`""`))) {
			return errf(contractCodeInvalidInput, "desktop discovery locator is incomplete")
		}
	}
	return nil
}

func validateDesktopDiscovery(d DesktopDiscovery, stateDir string) error {
	if d.Schema != DesktopDiscoverySchema || d.ProtocolVersion != 1 {
		return errf(contractCodeInvalidInput, "desktop discovery version is unsupported")
	}
	if !filepath.IsAbs(d.SocketPath) || filepath.Clean(d.SocketPath) != d.SocketPath || strings.IndexByte(d.SocketPath, 0) >= 0 || len(d.SocketPath) >= socketPathMax || filepath.Dir(d.SocketPath) != stateDir {
		return errf(contractCodeInvalidInput, "desktop discovery socket path is unsafe")
	}
	initialized := d.InstallationID != "" || d.KeychainService != "" || d.KeychainAccount != ""
	if !initialized {
		return nil
	}
	if !validDesktopUUID(d.InstallationID) || !validKeychainLocatorString(d.KeychainService) || !validKeychainLocatorString(d.KeychainAccount) {
		return errf(contractCodeInvalidInput, "desktop discovery locator is incomplete or unsafe")
	}
	return nil
}

func validDesktopUUID(id contract.ID) bool {
	s := string(id)
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if s[i] != '-' {
				return false
			}
			continue
		}
		switch c := s[i]; {
		case '0' <= c && c <= '9', 'a' <= c && c <= 'f':
		default:
			return false
		}
	}
	return true
}

func validKeychainLocatorString(s string) bool {
	if len(s) == 0 || len(s) > 256 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func checkDesktopDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return errWrap(contractCodeControllerUnavailable, "desktop discovery directory cannot be inspected", err)
	}
	if !info.IsDir() || info.Mode().Perm() != dirPrivate || !desktopOwned(info) {
		return errf(contractCodeInvalidInput, "desktop discovery directory is not private and owner-controlled")
	}
	return nil
}

func checkExistingDesktopFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errWrap(contractCodeControllerUnavailable, "desktop discovery cannot be inspected", err)
	}
	return validateDesktopFileInfo(info)
}

func validateDesktopFileInfo(info os.FileInfo) error {
	if !info.Mode().IsRegular() || info.Mode().Perm() != filePrivate || !desktopOwned(info) {
		return errf(contractCodeInvalidInput, "desktop discovery is not private and owner-controlled")
	}
	return nil
}

func desktopOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
