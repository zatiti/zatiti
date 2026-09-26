//go:build darwin && cgo && zatiti_carchive

package main

/*
#include <stddef.h>
#include <stdint.h>
*/
import "C"

import (
	"context"
	"encoding/json"
	"unsafe"
)

const maxBridgeOutputBytes = 16384

var installedNativeBridge = newNativeBridge(installedBridgeDependencies)

// bridgeWrite copies a small, redacted JSON result to caller-owned memory.
// Return value is bytes written, or -1 for invalid pointers/capacity.
func bridgeWrite(out *C.char, capacity C.size_t, result nativeBridgeResult) C.int {
	if out == nil || capacity == 0 || capacity > maxBridgeOutputBytes {
		return -1
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > int(capacity) {
		return -1
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(out)), int(capacity)), raw)
	return C.int(len(raw))
}

func bridgeID(ptr *C.char, length C.size_t) (string, bool) {
	if ptr == nil || length != 36 {
		return "", false
	}
	return string(C.GoBytes(unsafe.Pointer(ptr), 36)), true
}

func bridgeHandle(ptr *C.char, length C.size_t) (string, bool) {
	if ptr == nil || length != 64 {
		return "", false
	}
	return string(C.GoBytes(unsafe.Pointer(ptr), 64)), true
}

// ZatitiPrepare takes two exact UUID byte slices and returns a one-use handle
// plus nonsecret provider/account labels. No process argument carries a key.
//
//export ZatitiPrepare
func ZatitiPrepare(installation *C.char, installationLen C.size_t, connection *C.char, connectionLen C.size_t, out *C.char, outCapacity C.size_t) C.int {
	if out == nil || outCapacity == 0 || outCapacity > maxBridgeOutputBytes {
		return -1
	}
	i, iOK := bridgeID(installation, installationLen)
	c, cOK := bridgeID(connection, connectionLen)
	if !iOK || !cOK {
		return bridgeWrite(out, outCapacity, bridgeFailure(bridgePrepareSchema, "invalid_identity"))
	}
	return bridgeWrite(out, outCapacity, installedNativeBridge.prepare(context.Background(), i, c))
}

// ZatitiCommit checks credential length before C.GoBytes or string conversion.
// The bounded Go copy is zeroed after the shared engine returns.
//
//export ZatitiCommit
func ZatitiCommit(handle *C.char, handleLen C.size_t, credential *C.uchar, credentialLen C.size_t, out *C.char, outCapacity C.size_t) C.int {
	if out == nil || outCapacity == 0 || outCapacity > maxBridgeOutputBytes {
		return -1
	}
	h, ok := bridgeHandle(handle, handleLen)
	if !ok {
		return bridgeWrite(out, outCapacity, bridgeFailure(bridgeResultSchema, "invalid_handle"))
	}
	if credential == nil || credentialLen == 0 || credentialLen > maxHelperCredentialBytes {
		// Consume the handle so an invalid attempt cannot be followed by an
		// unreviewed second submission from the same secure field.
		if s, ok := installedNativeBridge.take(h); ok {
			s.deps.close()
		}
		return bridgeWrite(out, outCapacity, bridgeFailure(bridgeResultSchema, "invalid_credential"))
	}
	secret := C.GoBytes(unsafe.Pointer(credential), C.int(credentialLen))
	defer zero(secret)
	return bridgeWrite(out, outCapacity, installedNativeBridge.commit(context.Background(), h, secret))
}

// ZatitiCancel consumes a handle while retaining durable recovery intent.
//
//export ZatitiCancel
func ZatitiCancel(handle *C.char, handleLen C.size_t, out *C.char, outCapacity C.size_t) C.int {
	if out == nil || outCapacity == 0 || outCapacity > maxBridgeOutputBytes {
		return -1
	}
	h, ok := bridgeHandle(handle, handleLen)
	if !ok {
		return bridgeWrite(out, outCapacity, bridgeFailure(bridgeResultSchema, "invalid_handle"))
	}
	return bridgeWrite(out, outCapacity, installedNativeBridge.cancel(h))
}
