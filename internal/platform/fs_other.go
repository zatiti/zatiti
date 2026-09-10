//go:build !darwin && !linux

package platform

// platformSupported gates Open on the platforms the product contract
// actually claims. Windows support is not inferred; other hosts return a
// named refusal instead of degrading lock and permission guarantees.
func platformSupported() error {
	return errf(contractCodeCapabilityUnsupported, "this operating system is not a supported platform")
}

func checkFilesystemLocal(dir string) error {
	return errf(contractCodeCapabilityUnsupported, "this operating system is not a supported platform")
}

func defaultProbeFreeSpace(dir string) (uint64, error) {
	return 0, errf(contractCodeCapabilityUnsupported, "this operating system is not a supported platform")
}

const defaultBackendIsKeychain = false
