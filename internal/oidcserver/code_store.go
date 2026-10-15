// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidcserver

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// authCodeTTL is how long an /authorize-issued code is valid before /token
// must be called.
const authCodeTTL = 5 * time.Minute

// authCode holds the per-/authorize state needed to validate the
// corresponding /token request.
type authCode struct {
	ClientID        string
	RedirectURI     string
	CodeChallenge   string
	ChallengeMethod string
	ExpiresAt       time.Time
}

// codeStore is a tiny goroutine-safe in-memory map of code → authCode.
type codeStore struct {
	mu    sync.Mutex
	codes map[string]*authCode
}

// newCodeStore creates an empty authorization code store.
func newCodeStore() *codeStore {
	return &codeStore{codes: map[string]*authCode{}}
}

// put stores authorization state under a code while holding the store lock.
func (s *codeStore) put(code string, c *authCode) {
	s.mu.Lock()
	s.codes[code] = c
	s.mu.Unlock()
}

// take returns the authCode for code (or nil if missing or expired) and
// removes it from the store.
func (s *codeStore) take(code string) *authCode {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.codes[code]
	if !ok {
		return nil
	}
	delete(s.codes, code)
	if time.Now().After(c.ExpiresAt) {
		return nil
	}
	return c
}

// randomHex returns 2*n hex characters of cryptographic randomness.
func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
