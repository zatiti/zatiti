//go:build darwin && cgo

package platform

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <string.h>

static OSStatus zatitiKeychainPut(const char *serviceBytes, CFIndex serviceLen,
                                  const char *accountBytes, CFIndex accountLen,
                                  const unsigned char *valueBytes, CFIndex valueLen) {
    CFStringRef service = CFStringCreateWithBytes(kCFAllocatorDefault,
        (const UInt8 *)serviceBytes, serviceLen, kCFStringEncodingUTF8, false);
    CFStringRef account = CFStringCreateWithBytes(kCFAllocatorDefault,
        (const UInt8 *)accountBytes, accountLen, kCFStringEncodingUTF8, false);
    CFDataRef value = CFDataCreate(kCFAllocatorDefault, valueBytes, valueLen);
    if (service == NULL || account == NULL || value == NULL) {
        if (service != NULL) CFRelease(service);
        if (account != NULL) CFRelease(account);
        if (value != NULL) CFRelease(value);
        return errSecParam;
    }

    const void *queryKeys[] = {kSecClass, kSecAttrService, kSecAttrAccount};
    const void *queryValues[] = {kSecClassGenericPassword, service, account};
    CFDictionaryRef query = CFDictionaryCreate(kCFAllocatorDefault,
        queryKeys, queryValues, 3, &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks);
    const void *addKeys[] = {kSecClass, kSecAttrService, kSecAttrAccount, kSecValueData};
    const void *addValues[] = {kSecClassGenericPassword, service, account, value};
    CFDictionaryRef addition = CFDictionaryCreate(kCFAllocatorDefault,
        addKeys, addValues, 4, &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks);
    OSStatus status = errSecParam;
    if (query != NULL && addition != NULL) {
        status = SecItemAdd(addition, NULL);
        if (status == errSecDuplicateItem) {
            const void *updateKeys[] = {kSecValueData};
            const void *updateValues[] = {value};
            CFDictionaryRef update = CFDictionaryCreate(kCFAllocatorDefault,
                updateKeys, updateValues, 1, &kCFTypeDictionaryKeyCallBacks,
                &kCFTypeDictionaryValueCallBacks);
            if (update != NULL) {
                status = SecItemUpdate(query, update);
                CFRelease(update);
            } else {
                status = errSecParam;
            }
        }
    }
    if (addition != NULL) CFRelease(addition);
    if (query != NULL) CFRelease(query);
    CFRelease(value);
    CFRelease(account);
    CFRelease(service);
    return status;
}
*/
import "C"

import (
	"context"
	"unsafe"
)

func (k *keychainSecrets) putEncoded(ctx context.Context, key string, encoded []byte) error {
	// The helper override exists only for tests. Production always uses the
	// native API and never passes the value through an argv or environment.
	if k.helperPath != defaultSecurityHelper {
		return errf(contractCodePrerequisiteMissing, "the OS keychain helper is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return errWrap(contractCodePrerequisiteMissing, "the OS keychain refused the credential operation", err)
	}
	if k.service == "" {
		return errf(contractCodePrerequisiteMissing, "the OS keychain service is unavailable")
	}
	service := []byte(k.service)
	account := []byte(key)
	status := C.zatitiKeychainPut(
		(*C.char)(unsafe.Pointer(&service[0])), C.CFIndex(len(service)),
		(*C.char)(unsafe.Pointer(&account[0])), C.CFIndex(len(account)),
		(*C.uchar)(unsafe.Pointer(&encoded[0])), C.CFIndex(len(encoded)),
	)
	if status == C.errSecSuccess {
		return nil
	}
	// Never include the Security.framework's result text or the value in a
	// user-facing error. A locked keychain is an explicit prerequisite.
	return errf(contractCodePrerequisiteMissing, "the OS keychain refused the credential operation")
}
