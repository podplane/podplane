// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package keychain

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/99designs/keyring"
	"github.com/ebitengine/purego"
)

const utf8Encoding = 0x08000100

// native contains the Security and CoreFoundation entry points. CF references
// are uintptrs: these objects live in native memory, not on the Go heap.
type native struct {
	constants map[string]uintptr

	release          func(uintptr)
	dictionaryCreate func(uintptr, int, uintptr, uintptr) uintptr
	dictionarySet    func(uintptr, uintptr, uintptr)
	dictionaryGet    func(uintptr, uintptr) uintptr
	dictionaryRemove func(uintptr, uintptr)
	stringCreate     func(uintptr, []byte, int, uint32, bool) uintptr
	stringLength     func(uintptr) int
	stringMaxSize    func(int, uint32) int
	stringCString    func(uintptr, []byte, int, uint32) bool
	dataCreate       func(uintptr, int) uintptr
	dataAppend       func(uintptr, []byte, int)
	dataLength       func(uintptr) int
	dataBytes        func(uintptr) *byte
	arrayCreate      func(uintptr, uintptr, int, uintptr) uintptr
	accessCreate     func(uintptr, uintptr, *uintptr) int32
	itemAdd          func(uintptr, *uintptr) int32
	itemUpdate       func(uintptr, uintptr) int32
	itemCopyMatching func(uintptr, *uintptr) int32
	itemDelete       func(uintptr) int32
	errorMessage     func(int32, uintptr) uintptr
}

var loadNative = sync.OnceValues(openNative)

// openNative loads only Apple's system frameworks, retaining them for the
// process lifetime so registered functions and constants remain valid.
func openNative() (*native, error) {
	n := &native{constants: make(map[string]uintptr)}
	for _, framework := range []struct {
		name      string
		functions map[string]any
		constants []string
		callbacks []string
	}{
		{
			name: "CoreFoundation",
			functions: map[string]any{
				"CFRelease":                         &n.release,
				"CFDictionaryCreateMutable":         &n.dictionaryCreate,
				"CFDictionarySetValue":              &n.dictionarySet,
				"CFDictionaryGetValue":              &n.dictionaryGet,
				"CFDictionaryRemoveValue":           &n.dictionaryRemove,
				"CFStringCreateWithBytes":           &n.stringCreate,
				"CFStringGetLength":                 &n.stringLength,
				"CFStringGetMaximumSizeForEncoding": &n.stringMaxSize,
				"CFStringGetCString":                &n.stringCString,
				"CFDataCreateMutable":               &n.dataCreate,
				"CFDataAppendBytes":                 &n.dataAppend,
				"CFDataGetLength":                   &n.dataLength,
				"CFDataGetBytePtr":                  &n.dataBytes,
				"CFArrayCreate":                     &n.arrayCreate,
			},
			constants: []string{"kCFBooleanTrue"},
			callbacks: []string{"kCFTypeDictionaryKeyCallBacks", "kCFTypeDictionaryValueCallBacks", "kCFTypeArrayCallBacks"},
		},
		{
			name: "Security",
			functions: map[string]any{
				"SecAccessCreate":           &n.accessCreate,
				"SecItemAdd":                &n.itemAdd,
				"SecItemUpdate":             &n.itemUpdate,
				"SecItemCopyMatching":       &n.itemCopyMatching,
				"SecItemDelete":             &n.itemDelete,
				"SecCopyErrorMessageString": &n.errorMessage,
			},
			constants: []string{
				"kSecClass", "kSecClassGenericPassword", "kSecAttrService", "kSecAttrAccount",
				"kSecAttrLabel", "kSecAttrDescription", "kSecAttrAccess", "kSecValueData",
				"kSecReturnData", "kSecReturnAttributes", "kSecMatchLimit", "kSecMatchLimitOne",
			},
		},
	} {
		path := "/System/Library/Frameworks/" + framework.name + ".framework/" + framework.name
		handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", framework.name, err)
		}
		for name, function := range framework.functions {
			symbol, err := purego.Dlsym(handle, name)
			if err != nil {
				return nil, fmt.Errorf("load %s: %w", name, err)
			}
			purego.RegisterFunc(function, symbol)
		}
		for _, name := range framework.constants {
			symbol, err := purego.Dlsym(handle, name)
			if err != nil {
				return nil, fmt.Errorf("load %s: %w", name, err)
			}
			n.constants[name] = constantValue(symbol)
		}
		for _, name := range framework.callbacks {
			symbol, err := purego.Dlsym(handle, name)
			if err != nil {
				return nil, fmt.Errorf("load %s: %w", name, err)
			}
			n.constants[name] = symbol
		}
	}
	return n, nil
}

// constantValue dereferences a framework's exported CF object pointer. Unlike
// Go pointers, these symbol addresses remain valid for the process lifetime.
//
//go:nocheckptr
func constantValue(symbol uintptr) uintptr {
	return *(*uintptr)(unsafe.Pointer(symbol)) //nolint:govet // Dlsym returns a native address, not a Go pointer.
}

// dictionary creates a dictionary that retains its keys and values.
func (n *native) dictionary() uintptr {
	return n.dictionaryCreate(0, 0, n.constants["kCFTypeDictionaryKeyCallBacks"], n.constants["kCFTypeDictionaryValueCallBacks"])
}

// setString copies a UTF-8 string into a dictionary, releasing our ownership.
func (n *native) setString(dictionary uintptr, key, value string) error {
	data := []byte(value)
	ref := n.stringCreate(0, data, len(data), utf8Encoding, false)
	runtime.KeepAlive(data)
	if ref == 0 {
		return fmt.Errorf("create Keychain string %s: invalid UTF-8 or allocation failure", key)
	}
	defer n.release(ref)
	n.dictionarySet(dictionary, n.constants[key], ref)
	return nil
}

// stringValue copies a borrowed CF string into Go memory.
func (n *native) stringValue(ref uintptr) string {
	if ref == 0 {
		return ""
	}
	buffer := make([]byte, n.stringMaxSize(n.stringLength(ref), utf8Encoding)+1)
	if !n.stringCString(ref, buffer, len(buffer), utf8Encoding) {
		return ""
	}
	for i, b := range buffer {
		if b == 0 {
			return string(buffer[:i])
		}
	}
	return string(buffer)
}

// statusError distinguishes missing items from permission, lock and other
// failures. Keep ErrKeyNotFound unwrapped for existing keyring callers.
func (n *native) statusError(operation string, status int32) error {
	if status == 0 {
		return nil
	}
	if status == -25300 { // errSecItemNotFound
		return keyring.ErrKeyNotFound
	}
	if status == -128 { // errSecUserCanceled: the user denied the prompt.
		return fmt.Errorf("%s: Keychain access denied (Keychain status %d)", operation, status)
	}
	message := n.errorMessage(status, 0)
	if message == 0 {
		return fmt.Errorf("%s: Keychain status %d", operation, status)
	}
	defer n.release(message)
	return fmt.Errorf("%s: %s (Keychain status %d)", operation, n.stringValue(message), status)
}
