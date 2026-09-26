package platform

import (
	"context"
	"encoding/base64"
	"runtime"
	"strings"
)

// KeychainLocator returns the nonsecret generic-password service and account
// for a readable item. It never returns the credential itself. The runtime
// guard prevents a headless or non-macOS platform from claiming this ability.
func (p *Platform) KeychainLocator(ctx context.Context, opaqueRef string) (service, account string, err error) {
	if runtime.GOOS != "darwin" {
		return "", "", errf(contractCodeCapabilityUnsupported, "Keychain discovery requires macOS")
	}
	if err := p.ensureOpen(); err != nil {
		return "", "", err
	}
	k, ok := p.secrets.impl.(*keychainSecrets)
	if !ok {
		return "", "", errf(contractCodeCapabilityUnsupported, "Keychain discovery requires Keychain custody")
	}
	return keychainLocator(ctx, k, opaqueRef)
}

// keychainLocator is separate from the OS gate so its read/validation path
// can be tested with a fake helper on hosts without a real login Keychain.
func keychainLocator(ctx context.Context, k *keychainSecrets, opaqueRef string) (service, account string, err error) {
	account, err = parseKeychainRef(opaqueRef)
	if err != nil {
		return "", "", err
	}
	if err := validateSecretKey(account); err != nil || keychainRef(account) != opaqueRef || !validKeychainLocatorString(k.service) || !validKeychainLocatorString(account) {
		return "", "", errf(contractCodeNotFound, "credential reference is unknown")
	}
	value, err := k.Get(ctx, opaqueRef)
	if err != nil {
		return "", "", err
	}
	defer zero(value)
	// The owner credential is one complete Authorization header. This also
	// refuses a readable but incorrect or corrupted Keychain item.
	const prefix = "Bearer "
	if !strings.HasPrefix(string(value), prefix) || len(value) != len(prefix)+43 {
		return "", "", errf(contractCodePrerequisiteMissing, "owner Keychain credential is invalid")
	}
	token := value[len(prefix):]
	decoded, err := base64.RawURLEncoding.DecodeString(string(token))
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != string(token) {
		return "", "", errf(contractCodePrerequisiteMissing, "owner Keychain credential is invalid")
	}
	zero(decoded)
	return k.service, account, nil
}
