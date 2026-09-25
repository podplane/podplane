# Podplane — Agent Development Guide

## Important

- **Always use `make build`** to build the CLI — never use `go build` directly for release/confidence checks. `make build` injects version info via linker flags.
- **Prefer Makefile targets** over raw tool commands for full-repo validation.
- Before editing, check `git status --short` and do not overwrite or revert other changes.

## Build & Test Commands

- **Setup**: `make setup` — verify required tools and install git hooks
- **Build**: `make build` — build `bin/podplane` with version metadata
- **Test**: `make test` — run all tests with race detector
- **Format**: `make fmt` — format Go source files
- **Lint**: `make lint` — run golangci-lint
- **Precommit**: `make precommit` — check formatting and run lint
- **Clean**: `make clean` — remove `bin/`
- **Focused tests**: `go test ./internal/cmd ./internal/kubectl` — acceptable for targeted iteration

## CLI Command & Flag Conventions

Keep command behavior aligned with the command context model documented in
[`docs/reference/cli/overview.md#config-files--context`](docs/reference/cli/overview.md#config-files--context).

- **Cluster config commands** use `-f, --cluster-config`, defaulting to `./podplane.cluster.jsonc`: `login`, `cluster *`, `install`, `uninstall`.
- **OIDC server config commands** use `-f, --oidc-config`, defaulting to `./podplane.oidc.jsonc`: `oidc *`.
- **Kubernetes context commands** use `--context` / `--kubeconfig`, defaulting to the current kubeconfig context: `deploy`, `remove`, `logout`.
- **Local/cache/info commands** should not require config/context by default: `local *`, `deps *`, `version`, `completion`, `help`.
- `deps download` may optionally accept `--cluster-config` for targeted dependency prefetching.
- `logout` may accept `--cluster` or explicit `-f, --cluster-config` for cleanup, but must not implicitly read `./podplane.cluster.jsonc`.
- Hide integration-only commands from top-level help when they are not user-facing, such as `hooks`.

## Config Schemas

- JSON Schemas for user-authored config files live in `schemas/` and are embedded by the `schemas` Go package.
- Generated `podplane.cluster.jsonc` and `podplane.oidc.jsonc` should use local relative `$schema` references (`./podplane.cluster.schema.json`, `./podplane.oidc.schema.json`) so tools using them can work offline.
- When changing config structs, validation rules, defaults, or config docs, update the corresponding schema file and tests in `internal/clusterconfig` or `internal/oidcconfig`.
- Generated schema copies (`podplane.*.schema.json`) are CLI-managed support files, similar to generated `podplane.*.tf` files; the source of truth is the checked-in file under `schemas/`.

## TUI / Terminal UI Guidance

When building or changing Podplane CLI TUI output:

- Treat `80x24` as the minimum supported terminal size.
- Design primary flows to remain usable at `80` columns.
- Avoid layouts that require wide terminals for essential information.
- Prefer vertical stacking over multi-column layouts unless the terminal is wide enough.
- Truncate or wrap secondary text before breaking command usability.
- Important actions, prompts, errors, and status messages must remain visible at `80x24`.
- If richer layouts are useful, progressively enhance for wider terminals rather than depending on them.
- Do not assume full-screen TUI mode unless the command explicitly enters an interactive UI.
- Non-interactive CLI output should remain readable in narrow terminals and logs.
- When using TUI libraries such as Bubble Tea or Lip Gloss, measure terminal size at runtime and adapt layout rather than hard-coding `80x24` as a design constant.

## Code Style

- **File headers**: include the Podplane copyright and SPDX header on Go files.
- **Packages**: keep `internal/cmd` focused on Cobra command wiring and orchestration. Do not add command-local helper functions for business logic; put reusable behavior in domain packages such as `internal/kubectl`, `internal/clusterconfig`, `internal/components`, or `internal/local`. Small Cobra-specific helpers may stay in `internal/cmd` only when they are strictly command UI/wiring concerns.
- **Imports**: stdlib → third-party → local (`github.com/podplane/podplane/*`).
- **Naming**: use concise Go names. Avoid `Get` prefixes; prefer names like `ClusterIDFromContext`.
- **Errors**: return early and wrap with `fmt.Errorf("...: %w", err)` when adding context.
- **Comments**: exported functions should have comments beginning with the function/type name.
- **Dependencies**: do not add new Go module dependencies without explicit confirmation.

## Testing

- Use the standard `testing` package.
- Prefer focused package tests while iterating, then broader checks when touching shared behavior.
- Put tests in the package they exercise unless black-box behavior is important.
- Use hand-written fakes or existing test hooks; avoid mock libraries.
- Use `t.Helper()` for helpers and `t.Cleanup()` for teardown.

## Directory Structure

```
docs/                   # Comprehensive Podplane documentation
internal/cmd/           # Cobra command wiring
internal/clusterconfig/ # Cluster config parsing, validation, mutation
internal/components/    # Platform Components read/patch logic
internal/config/        # CLI user config, storage dirs, auth metadata
internal/deploy/        # App deploy/remove helpers
internal/kubectl/       # kubeconfig key conventions and kubectl integration
internal/local/         # Local VM cluster lifecycle and generated local config
internal/tfgen/         # Generated Terraform/OpenTofu files
internal/tui/           # Terminal prompts and progress UI
pkg/                    # Public packages consumed outside internal code
```
