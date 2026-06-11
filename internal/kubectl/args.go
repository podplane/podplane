// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package kubectl

// Args returns leading kubectl flags for an optional context and kubeconfig
// path.
func Args(kubeContext, kubeconfig string) []string {
	args := []string{}
	if kubeContext != "" {
		args = append(args, "--context", kubeContext)
	}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	return args
}
