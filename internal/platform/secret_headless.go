package platform

import (
	"context"
	"path/filepath"
	"sync"
)

// headlessSecrets is the explicitly provisioned credential store for
// headless hosts. Values are encrypted at rest with AES-256-GCM under a
// purpose-bound subkey of the master key and stored as 0600 files in the
// 0700 secrets directory. There is no plaintext fallback: when the master
// key is unavailable the store is unavailable.
type headlessSecrets struct {
	dir      string // <state>/secrets
	blobsDir string // <state>/secrets/blobs
	encKey   []byte

	mu     sync.Mutex
	loaded bool
	zeroed bool              // keys wiped by Platform.Close: the store fails closed
	index  map[string]string // caller-facing key -> opaque ref
}

const (
	secretRefPrefix = "hl1:"
	secretMaxValue  = 1 << 20 // 1 MiB
)

type secretIndexFile struct {
	Version int               `json:"v"`
	Keys    map[string]string `json:"keys"`
}

func newHeadlessSecrets(stateDir string, key []byte) (*headlessSecrets, error) {
	sub, err := deriveSubkey(key, hkdfInfoInternal)
	if err != nil {
		return nil, err
	}
	return &headlessSecrets{
		dir:      filepath.Join(stateDir, dirNameSecrets),
		blobsDir: filepath.Join(stateDir, dirNameSecrets, dirNameSecretVals),
		encKey:   sub,
	}, nil
}

func (h *headlessSecrets) zeroKeys() {
	h.mu.Lock()
	defer h.mu.Unlock()
	zero(h.encKey)
	h.zeroed = true
}

// closed fails the call once keys are wiped; a closed store never serves
// decrypted material.
func (h *headlessSecrets) closed() error {
	if h.zeroed {
		return errf(contractCodeControllerUnavailable, "platform is closed")
	}
	return nil
}

// secretRef maps a stored file id to its opaque reference.
func secretRef(id string) string { return secretRefPrefix + id }

func parseSecretRef(ref string) (string, error) {
	if len(ref) != len(secretRefPrefix)+32 || ref[:len(secretRefPrefix)] != secretRefPrefix || !isHex(ref[len(secretRefPrefix):], 32) {
		return "", errf(contractCodeNotFound, "credential reference is unknown")
	}
	return ref[len(secretRefPrefix):], nil
}

func (h *headlessSecrets) refForKey(key string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	_ = h.load()
	return h.index[key]
}

func (h *headlessSecrets) Put(ctx context.Context, key string, secret []byte) (string, error) {
	if err := validateSecretKey(key); err != nil {
		return "", err
	}
	if len(secret) == 0 {
		return "", errf(contractCodeInvalidInput, "credential value must not be empty")
	}
	if len(secret) > secretMaxValue {
		return "", errf(contractCodeInvalidInput, "credential exceeds the headless store size limit")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.closed(); err != nil {
		return "", err
	}
	if err := h.load(); err != nil {
		return "", err
	}
	id := randHex(16)
	envelope, err := sealSecret(h.encKey, []byte(id), secret)
	if err != nil {
		return "", err
	}
	if err := writeFileSync(filepath.Join(h.blobsDir, id), envelope); err != nil {
		return "", err
	}
	old := h.index[key]
	h.index[key] = secretRef(id)
	if err := h.saveIndex(); err != nil {
		h.index[key] = old
		return "", err
	}
	if old != "" && old != secretRef(id) {
		if oldID, perr := parseSecretRef(old); perr == nil {
			_ = removeFile(filepath.Join(h.blobsDir, oldID))
		}
	}
	return secretRef(id), nil
}

func (h *headlessSecrets) Get(ctx context.Context, ref string) ([]byte, error) {
	id, err := parseSecretRef(ref)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.closed(); err != nil {
		return nil, err
	}
	data, err := readFilePrivate(filepath.Join(h.blobsDir, id))
	if err != nil {
		return nil, errf(contractCodeNotFound, "credential reference is unknown")
	}
	value, err := openSecret(h.encKey, []byte(id), data)
	if err != nil {
		return nil, errf(contractCodeControllerUnavailable, "stored credential failed integrity verification")
	}
	return value, nil
}

func (h *headlessSecrets) Delete(ctx context.Context, ref string) error {
	id, err := parseSecretRef(ref)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.closed(); err != nil {
		return err
	}
	if err := h.load(); err != nil {
		return err
	}
	changed := false
	for k, v := range h.index {
		if v == ref {
			delete(h.index, k)
			changed = true
		}
	}
	if err := removeFile(filepath.Join(h.blobsDir, id)); err != nil {
		return err
	}
	if changed {
		return h.saveIndex()
	}
	return nil
}

// load reads the key->reference index. A missing or corrupt index is not
// fatal: references stay resolvable on their own, only the convenience
// lookup is lost.
func (h *headlessSecrets) load() error {
	if h.loaded {
		return nil
	}
	h.loaded = true
	h.index = make(map[string]string)
	data, err := readFilePrivate(filepath.Join(h.dir, fileSecretIndex))
	if err != nil {
		return nil // absent index: empty map
	}
	var parsed secretIndexFile
	if err := strictUnmarshal(data, &parsed); err != nil || parsed.Version != 1 {
		return nil // corrupt index degrades to empty; refs stay valid
	}
	if parsed.Keys != nil {
		h.index = parsed.Keys
	}
	return nil
}

func (h *headlessSecrets) saveIndex() error {
	doc := secretIndexFile{Version: 1, Keys: h.index}
	if doc.Keys == nil {
		doc.Keys = map[string]string{}
	}
	data, err := marshal(doc)
	if err != nil {
		return errf(contractCodeControllerUnavailable, "credential store index cannot be encoded")
	}
	return writeFileSync(filepath.Join(h.dir, fileSecretIndex), data)
}
