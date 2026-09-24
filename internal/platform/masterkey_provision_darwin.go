//go:build darwin && cgo

package platform

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int zatitiDefaultIsLogin(const char *home) {
    SecKeychainRef current = NULL;
    if (SecKeychainCopyDefault(&current) != errSecSuccess || current == NULL) return 0;
    char actual[PATH_MAX];
    UInt32 size = sizeof(actual);
    OSStatus status = SecKeychainGetPath(current, &size, actual);
    CFRelease(current);
    if (status != errSecSuccess || size == 0 || size >= sizeof(actual)) return 0;
    actual[size] = '\0';
    char resolvedActual[PATH_MAX];
    if (realpath(actual, resolvedActual) == NULL) return 0;
    char candidate[PATH_MAX];
    char resolvedCandidate[PATH_MAX];
    const char *suffixes[] = {"login.keychain-db", "login.keychain"};
    for (int i = 0; i < 2; i++) {
        int n = snprintf(candidate, sizeof(candidate),
            "%s/Library/Keychains/%s", home, suffixes[i]);
        if (n <= 0 || n >= sizeof(candidate)) continue;
        if (realpath(candidate, resolvedCandidate) != NULL &&
            strcmp(resolvedActual, resolvedCandidate) == 0) return 1;
    }
    return 0;
}

static OSStatus zatitiMasterRead(const char *service, const char *account,
                                  void **out, UInt32 *outLen) {
    SecKeychainRef login = NULL;
    OSStatus status = SecKeychainCopyDefault(&login);
    if (status != errSecSuccess || login == NULL) return status;
    void *contents = NULL;
    UInt32 length = 0;
    status = SecKeychainFindGenericPassword(login,
        (UInt32)strlen(service), service, (UInt32)strlen(account), account,
        &length, &contents, NULL);
    if (status == errSecSuccess) {
        if (length == 0 || length > 4096) {
            status = errSecParam;
        } else {
            *out = malloc(length);
            if (*out == NULL) {
                status = errSecAllocate;
            } else {
                memcpy(*out, contents, length);
                *outLen = length;
            }
        }
        if (contents != NULL) memset(contents, 0, length);
        SecKeychainItemFreeContent(NULL, contents);
    }
    CFRelease(login);
    return status;
}

static OSStatus zatitiMasterCreate(const char *service, const char *account,
                                    const void *bytes, UInt32 length) {
    SecKeychainRef login = NULL;
    OSStatus status = SecKeychainCopyDefault(&login);
    if (status != errSecSuccess || login == NULL) return status;
    CFStringRef serviceRef = CFStringCreateWithCString(kCFAllocatorDefault,
        service, kCFStringEncodingUTF8);
    CFStringRef accountRef = CFStringCreateWithCString(kCFAllocatorDefault,
        account, kCFStringEncodingUTF8);
    CFDataRef valueRef = CFDataCreate(kCFAllocatorDefault, bytes, length);
    if (serviceRef == NULL || accountRef == NULL || valueRef == NULL) {
        status = errSecAllocate;
    } else {
        const void *keys[] = {kSecClass, kSecAttrService, kSecAttrAccount,
                              kSecValueData, kSecUseKeychain};
        const void *values[] = {kSecClassGenericPassword, serviceRef, accountRef,
                                valueRef, login};
        CFDictionaryRef query = CFDictionaryCreate(kCFAllocatorDefault,
            keys, values, 5, &kCFTypeDictionaryKeyCallBacks,
            &kCFTypeDictionaryValueCallBacks);
        if (query == NULL) {
            status = errSecAllocate;
        } else {
            status = SecItemAdd(query, NULL); // create-only; never SecItemUpdate
            CFRelease(query);
        }
    }
    if (serviceRef != NULL) CFRelease(serviceRef);
    if (accountRef != NULL) CFRelease(accountRef);
    if (valueRef != NULL) CFRelease(valueRef);
    CFRelease(login);
    return status;
}
*/
import "C"

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"unsafe"
)

// ProvisionMacMasterKey creates only a missing installation-local master key.
// It never replaces or exports existing material. The bootstrap injects this
// function through a narrow capability before it starts the controller.
func ProvisionMacMasterKey(ctx context.Context, stateDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return errf(contractCodePrerequisiteMissing, "Mac home directory is unavailable")
	}
	want := filepath.Join(home, "Library", "Application Support", "zatiti")
	if stateDir != want {
		return errf(contractCodeInvalidInput, "Mac master provisioning requires the installed state directory")
	}
	chome := C.CString(home)
	defer C.free(unsafe.Pointer(chome))
	if C.zatitiDefaultIsLogin(chome) != 1 {
		return errf(contractCodePrerequisiteMissing, "the current default Keychain is not the login Keychain")
	}
	return provisionMacMasterKey(ctx, stateDir, darwinMacMasterStore{})
}

type darwinMacMasterStore struct{}

func (darwinMacMasterStore) Read(ctx context.Context, service, account string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cs := C.CString(service)
	ca := C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))
	var ptr unsafe.Pointer
	var n C.UInt32
	status := C.zatitiMasterRead(cs, ca, &ptr, &n)
	if ptr != nil {
		defer func() {
			C.memset(ptr, 0, C.size_t(n))
			C.free(ptr)
		}()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if status == C.errSecItemNotFound {
		return nil, fs.ErrNotExist
	}
	if status != C.errSecSuccess || ptr == nil || n == 0 || n > 4096 {
		return nil, errf(contractCodePrerequisiteMissing, "Mac master Keychain item cannot be read")
	}
	return C.GoBytes(ptr, C.int(n)), nil
}

func (darwinMacMasterStore) Create(ctx context.Context, service, account string, encoded []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(encoded) == 0 {
		return false, errf(contractCodeInvalidInput, "Mac master value is empty")
	}
	cs := C.CString(service)
	ca := C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))
	status := C.zatitiMasterCreate(cs, ca, unsafe.Pointer(&encoded[0]), C.UInt32(len(encoded)))
	if status == C.errSecDuplicateItem {
		return false, nil
	}
	if status != C.errSecSuccess {
		return false, errf(contractCodePrerequisiteMissing, "Mac master Keychain item cannot be created")
	}
	return true, nil
}
