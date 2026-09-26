//go:build !darwin || !cgo

package platform

import "context"

// The keychain backend fails closed without macOS Security.framework. A
// headless host must explicitly select its encrypted headless backend.
func (k *keychainSecrets) putEncoded(ctx context.Context, key string, encoded []byte) error {
	return errf(contractCodePrerequisiteMissing, "the OS keychain helper is unavailable")
}
