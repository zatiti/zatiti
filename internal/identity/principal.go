// internal/identity/principal.go
//
// T1.4: principal metadata persistence. The principal ID is stored
// beside its key as a JSON sidecar so that ProvisionOwner is idempotent:
// one keystore yields exactly one stable PrincipalRef per named
// principal. Metadata is public; nothing secret is serialized here.

package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// principalMeta is the persisted public metadata for one principal.
type principalMeta struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"created_at"`
}

// errMetaCorrupt: metadata that fails to parse is an operator-repair
// condition, never silently regenerated (a regenerated ID would break
// referential continuity with persisted state).
var errMetaCorrupt = errors.New("identity: principal metadata is corrupt")

// loadOrCreateMeta returns the persisted metadata for name, creating it
// via newMeta on first use. Writes are atomic like key writes.
func (k *Keystore) loadOrCreateMeta(name string, newMeta func() principalMeta) (principalMeta, error) {
	path := filepath.Join(k.dir, name+".meta.json")
	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		meta := newMeta()
		raw, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			return meta, fmt.Errorf("identity: encode meta: %w", err)
		}
		tmp, err := os.CreateTemp(k.dir, ".meta-*")
		if err != nil {
			return meta, fmt.Errorf("identity: temp meta: %w", err)
		}
		tmpName := tmp.Name()
		// Metadata is public; 0644 is correct, unlike key material.
		if err := tmp.Chmod(0o644); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return meta, fmt.Errorf("identity: chmod meta: %w", err)
		}
		if _, err := tmp.Write(raw); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return meta, fmt.Errorf("identity: write meta: %w", err)
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpName)
			return meta, fmt.Errorf("identity: close meta: %w", err)
		}
		if err := os.Rename(tmpName, path); err != nil {
			os.Remove(tmpName)
			return meta, fmt.Errorf("identity: place meta: %w", err)
		}
		return meta, nil
	case err != nil:
		return principalMeta{}, fmt.Errorf("identity: read meta: %w", err)
	}
	var meta principalMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return principalMeta{}, fmt.Errorf("%w: %v", errMetaCorrupt, err)
	}
	if meta.ID == "" {
		return principalMeta{}, fmt.Errorf("%w: empty id", errMetaCorrupt)
	}
	return meta, nil
}

// ProvisionOwner (final form, superseding the File 6 body): returns the
// stable PrincipalRef for "owner", generating key and metadata on first
// use. The context is honored before any I/O.
func (p *Provisioner) ProvisionOwner(ctx context.Context) (PrincipalRef, error) {
	select {
	case <-ctx.Done():
		return PrincipalRef{}, ctx.Err()
	default:
	}

	// Key first (LoadOrCreate handles generation), then, so a
	// crash between the two leaves a retryable state: the next call
	// loads the existing key and creates the missing metadata.
	_, err := p.keystore.LoadOrCreate("owner", func() ([]byte, error) {
		priv, err := generateP256()
		if err != nil {
			return nil, err
		}
		return encodePEM(priv)
	})
	if err != nil {
		return PrincipalRef{}, err
	}

	meta, err := p.keystore.loadOrCreateMeta("owner", func() principalMeta {
		return principalMeta{ID: newUUID(), CreatedAt: time.Now().Unix()}
	})
	if err != nil {
		return PrincipalRef{}, err
	}

	key, err := p.keystore.LoadOrCreate("owner", func() ([]byte, error) {
		return nil, errors.New("identity: key vanished between calls")
	})
	if err != nil {
		return PrincipalRef{}, err
	}
	pub, err := marshalPublic(key)
	if err != nil {
		return PrincipalRef{}, err
	}
	return PrincipalRef{ID: meta.ID, PublicKey: pub, CreatedAt: meta.CreatedAt}, nil
}
