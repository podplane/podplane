// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package buildvars

// set during build time
var (
	buildVersion = "dev"
	buildDate    = "unknown"
	commitHash   = "unknown"
	commitDate   = "unknown"
	commitBranch = "unknown"
)

// BuildVersion returns immutable build version
func BuildVersion() string {
	return buildVersion
}

// BuildDate returns immutable build date
func BuildDate() string {
	return buildDate
}

// CommitHash returns immutable git commit hash
func CommitHash() string {
	return commitHash
}

// CommitDate returns immutable build date
func CommitDate() string {
	return commitDate
}

// CommitBranch returns immutable commit branch
func CommitBranch() string {
	return commitBranch
}
