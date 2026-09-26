//go:build !darwin || !cgo

package platform

import "context"

// ProvisionMacMasterKey is unavailable without macOS Security.framework.
func ProvisionMacMasterKey(context.Context, string) error {
	return errf(contractCodeCapabilityUnsupported, "Mac master Keychain provisioning requires macOS and cgo")
}
