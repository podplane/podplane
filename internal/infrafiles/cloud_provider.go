// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package infrafiles

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"

	"github.com/podplane/podplane/internal/cloud"
	"github.com/podplane/podplane/internal/tui"
)

// SelectCloudProvider asks question with the supported cloud providers,
// returning the chosen provider kind and whether the user cancelled. The
// first provider is highlighted initially, but the user must still choose.
func SelectCloudProvider(ctx context.Context, question string) (string, bool, error) {
	return tui.SelectList(ctx, "Select cloud provider", question, cloudProviderItems())
}

// cloudProviderItems returns a list item for each supported cloud provider,
// followed by Cancel.
func cloudProviderItems() []list.Item {
	var items []list.Item
	for _, p := range cloud.Providers() {
		items = append(items, tui.Item{Key: p.Kind, Label: p.Name})
	}
	return append(items, tui.Item{Label: "Cancel", Cancel: true})
}

// ConfirmAWSAccount suggests the selected credentials' account before the
// caller saves it in configuration and derives a remote-state bucket name.
// Interactive callers can enter an account manually if credentials are not
// available yet; unattended callers must successfully resolve it.
func ConfirmAWSAccount(ctx context.Context, region, profile string, autoApprove bool) (string, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	lookup := func() (string, error) { return cloud.AWSAccount(lookupCtx, region, profile) }
	if autoApprove {
		_, _ = fmt.Fprintln(tui.FlowOutput(ctx), "Resolving AWS account")
		account, err := lookup()
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(tui.FlowOutput(ctx), "Selected AWS credentials resolve to account %s.\n", account)
		return account, nil
	}
	got, err := tui.Run(ctx, awsAccountLookup{
		lookup: lookup, cancel: cancel, profile: profile, spinner: spinner.New(), width: 80,
	})
	if err != nil {
		return "", fmt.Errorf("resolve AWS account: %w", err)
	}
	result := got.(awsAccountLookup)
	if result.aborted {
		return "", fmt.Errorf("AWS account lookup cancelled: %w", context.Canceled)
	}
	return result.account, nil
}
