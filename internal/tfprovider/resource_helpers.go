// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// stringOr returns a Terraform string value or fallback when unset.
func stringOr(value types.String, fallback string) string {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return value.ValueString()
}
