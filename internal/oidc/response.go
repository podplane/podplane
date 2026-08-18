// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"encoding/json"
	"fmt"
	"io"
)

const maxResponseSize = 128 << 10

// decodeResponse decodes a size-limited JSON response.
func decodeResponse(r io.Reader, out any) error {
	b, err := io.ReadAll(io.LimitReader(r, maxResponseSize+1))
	if err != nil {
		return err
	}
	if len(b) > maxResponseSize {
		return fmt.Errorf("response exceeds size limit")
	}
	return json.Unmarshal(b, out)
}
