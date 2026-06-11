// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"bytes"
	"context"
	"strings"

	"github.com/podplane/podplane/internal/execwrap"
	"github.com/podplane/podplane/internal/kubectl"
)

const certManagerAdmissionCheckManifest = `apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: podplane-health-check
  namespace: default
spec:
  secretName: podplane-health-check-tls
  dnsNames:
    - podplane-health-check.localhost
  issuerRef:
    name: podplane-health-check
    kind: ClusterIssuer
`

// CertManagerAdmissionCheck returns a health check that proves cert-manager's
// Certificate admission webhook is reachable from kube-apiserver.
func CertManagerAdmissionCheck(kubeContext, kubeconfig string, required bool) Check {
	return Check{
		Key:      "cert-manager/admission/certificate-dry-run",
		Name:     "cert-manager admission",
		Kind:     "webhook",
		Required: required,
		Run: func(ctx context.Context) Result {
			if err := ctx.Err(); err != nil {
				return Result{Err: err}
			}
			args := kubectl.Args(kubeContext, kubeconfig)
			args = append(args, "apply", "--dry-run=server", "-f", "-")
			var stdout, stderr bytes.Buffer
			cmd := execwrap.Command("kubectl", args...)
			cmd.Stdin = strings.NewReader(certManagerAdmissionCheckManifest)
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				msg := strings.TrimSpace(stderr.String())
				if msg == "" {
					msg = err.Error()
				}
				return Result{Exists: true, Status: StatusPending, Message: msg}
			}
			return Result{Exists: true, Ready: true, Status: StatusReady, Message: "Certificate admission dry-run succeeded"}
		},
	}
}
