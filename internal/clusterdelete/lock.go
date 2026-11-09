// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterdelete

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"time"
)

// StateLock protects teardown from other clients using the engine's backend lock.
// EmptyBucket removes everything except the owned lock. Unlock removes only that
// lock; callers must never empty the bucket again after releasing it.
type StateLock interface {
	EmptyBucket(ctx context.Context) error
	Unlock(ctx context.Context) error
}

// StateLockInfo encodes the lock record understood by OpenTofu and Terraform.
func StateLockInfo(path string) ([]byte, error) {
	host, _ := os.Hostname()
	return json.Marshal(struct {
		ID, Operation, Info, Who, Version, Path string
		Created                                 time.Time
	}{
		ID: rand.Text(), Operation: "OperationTypeApply", Info: "Podplane cluster delete",
		Who: os.Getenv("USER") + "@" + host, Path: path, Created: time.Now().UTC(),
	})
}
