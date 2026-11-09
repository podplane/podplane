---
title: "Terminal UI"
weight: 30
description: "Interactive wizards and OpenTofu/Terraform plan-and-apply dashboards"
---

Podplane uses a few terminal UI styles for interactive commands:

| Style | Used by | Purpose |
| --- | --- | --- |
| Configuration wizard | [cluster create](commands/cluster-create.md), [oidc create](commands/oidc-create.md) | Collect settings when creating a new config. |
| VM boot wizard | [local start](commands/local-start.md) | Show local VM startup tasks and health checks. |
| OpenTofu/Terraform plan-and-apply dashboard | [cluster create](commands/cluster-create.md), [cluster upgrade](commands/cluster-upgrade.md), [oidc create](commands/oidc-create.md) | Review infrastructure changes and track apply progress. |

Before the style-specific controls, here's how to use plain output and stop execution safely.

## Plain output and cancellation

The Podplane Terminal UI (TUI) needs **80 columns × 24 rows**. Resize a
smaller terminal or press `p` to switch to plain output. You can switch at any
size; it does not restart execution or approve the plan, and lasts for the rest
of that run. Non-interactive runs use plain output. Type `yes` to approve, or
use `--auto-approve` to skip confirmation.

**Ctrl-C requests a safe stop.** Wait for cleanup to finish. Cancellation and
failure do not undo applied changes; inspect the logs before retrying.

## OpenTofu/Terraform plan-and-apply dashboard

When applying infrastructure changes, Podplane runs OpenTofu or Terraform and
shows its plan and progress in this dashboard. The footer tracks
**Questions → Init → Plan → Review → Apply**.

### Review and progress

- **Resource table:** lists planned changes before apply starts. Use ↑/↓ to
  select a resource and Enter to inspect its before/after values. Sensitive
  values are hidden. Review shows resources and actions; status icons and the
  Status column appear during apply. Page up with Ctrl+B, PgUp, or `b`; page
  down with Ctrl+F, PgDn, or Space. You can also scroll over the table with
  your mouse wheel and click a resource to open its details.
- **Approval:** Tab selects Cancel or Approve and apply; Enter confirms.
  Inspecting a resource never approves the plan.
- **Progress:** the status, elapsed time, and progress bar show apply activity.
  The percentage counts completed resource changes, not time remaining.
- **Hide completed:** during apply, press `h` to hide or show completed resources.
  The filter starts off, preserves plan order, and does not change overall progress.
- **Details, logs, and outputs:** Enter opens resource details, `l` opens engine
  logs, and `o` opens output changes. Esc closes the panel.

### Panel shortcuts

The mouse wheel scrolls details, logs, and outputs when the pointer is over the panel.

| Action | Keys |
| --- | --- |
| Scroll one line | ↑ / ↓ |
| Page up | Ctrl+B, PgUp, or `b` |
| Page down | Ctrl+F, PgDn, or Space |
| Jump to top | Home or `g` |
| Jump to bottom | End or `G` |
| Close panel | Esc |

Logs open following new output. Scrolling up pauses following; End or `G`
resumes it. The panel shows **Following** or **Paused**.

In logs, drag across text to select and copy it on mouse-release. Selection pauses
updates; scrolling or pressing a key clears it. Press `c` to copy all retained
logs (up to 4 MiB per copy). Logs have a 64 MiB retention budget; reaching it
discards oldest entries and shows a warning. Copying uses the clipboard
on the machine running Podplane; clipboard access must be available there.
