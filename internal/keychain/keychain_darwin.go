// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

// Package keychain stores generic passwords in the macOS Keychain without cgo.
package keychain

import (
	"runtime"
	"unsafe"

	"github.com/99designs/keyring"
)

// Store addresses generic passwords by service and account in the default
// Keychain search list, matching 99designs/keyring's default macOS backend.
type Store struct {
	service string
	n       *native
}

// New opens a service's credential store. Operations may prompt for Keychain
// access; failures are returned rather than falling back to another store.
func New(service string) (*Store, error) {
	n, err := loadNative()
	if err != nil {
		return nil, err
	}
	return &Store{service: service, n: n}, nil
}

// query creates an owned dictionary selecting this service and account.
func (s *Store) query(key string) (uintptr, error) {
	n := s.n
	query := n.dictionary()
	n.dictionarySet(query, n.constants["kSecClass"], n.constants["kSecClassGenericPassword"])
	if err := n.setString(query, "kSecAttrService", s.service); err != nil {
		n.release(query)
		return 0, err
	}
	if err := n.setString(query, "kSecAttrAccount", key); err != nil {
		n.release(query)
		return 0, err
	}
	return query, nil
}

// Get reads a credential, copying its data out of native memory before release.
func (s *Store) Get(key string) (keyring.Item, error) {
	query, err := s.query(key)
	if err != nil {
		return keyring.Item{}, err
	}
	n := s.n
	defer n.release(query)
	n.dictionarySet(query, n.constants["kSecMatchLimit"], n.constants["kSecMatchLimitOne"])
	n.dictionarySet(query, n.constants["kSecReturnData"], n.constants["kCFBooleanTrue"])
	n.dictionarySet(query, n.constants["kSecReturnAttributes"], n.constants["kCFBooleanTrue"])
	var result uintptr
	if err := n.statusError("read", n.itemCopyMatching(query, &result)); err != nil {
		return keyring.Item{}, err
	}
	defer n.release(result)
	data := n.dictionaryGet(result, n.constants["kSecValueData"])
	return keyring.Item{
		Key:         key,
		Data:        append([]byte{}, unsafe.Slice(n.dataBytes(data), n.dataLength(data))...),
		Label:       n.stringValue(n.dictionaryGet(result, n.constants["kSecAttrLabel"])),
		Description: n.stringValue(n.dictionaryGet(result, n.constants["kSecAttrDescription"])),
	}, nil
}

// Set creates or updates a credential. New entries use the existing backend's
// empty trusted-application list; updates leave the user's access rules intact.
func (s *Store) Set(item keyring.Item) error {
	query, err := s.query(item.Key)
	if err != nil {
		return err
	}
	n := s.n
	defer n.release(query)
	attributes := n.dictionary()
	defer n.release(attributes)
	if err := n.setString(attributes, "kSecAttrLabel", item.Label); err != nil {
		return err
	}
	if err := n.setString(attributes, "kSecAttrDescription", item.Description); err != nil {
		return err
	}
	// Keep a non-null native buffer even for empty data. The file Keychain
	// treats a null data pointer as "leave the existing password unchanged".
	data := n.dataCreate(0, max(1, len(item.Data)))
	n.dataAppend(data, item.Data, len(item.Data))
	runtime.KeepAlive(item.Data)
	n.dictionarySet(attributes, n.constants["kSecValueData"], data)
	n.release(data)

	// Only mutable attributes go into an update: in particular, never replace
	// the ACL, which may include permissions the user has already granted.
	status := n.itemUpdate(query, attributes)
	if status != -25300 { // errSecItemNotFound
		return n.statusError("update", status)
	}
	for _, key := range []string{"kSecAttrLabel", "kSecAttrDescription", "kSecValueData"} {
		n.dictionarySet(query, n.constants[key], n.dictionaryGet(attributes, n.constants[key]))
	}
	applications := n.arrayCreate(0, 0, 0, n.constants["kCFTypeArrayCallBacks"])
	defer n.release(applications)
	var access uintptr
	if err := n.statusError("create access rules", n.accessCreate(n.dictionaryGet(attributes, n.constants["kSecAttrLabel"]), applications, &access)); err != nil {
		return err
	}
	defer n.release(access)
	n.dictionarySet(query, n.constants["kSecAttrAccess"], access)
	status = n.itemAdd(query, nil)
	if status == -25299 { // errSecDuplicateItem: another writer created it.
		for _, key := range []string{"kSecAttrLabel", "kSecAttrDescription", "kSecValueData", "kSecAttrAccess"} {
			n.dictionaryRemove(query, n.constants[key])
		}
		status = n.itemUpdate(query, attributes)
	}
	return n.statusError("store", status)
}

// Remove deletes a credential, returning ErrKeyNotFound if it does not exist.
func (s *Store) Remove(key string) error {
	query, err := s.query(key)
	if err != nil {
		return err
	}
	defer s.n.release(query)
	return s.n.statusError("delete", s.n.itemDelete(query))
}
