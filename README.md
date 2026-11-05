# phi-rewind

Independent Go PXB extension for Phi. `/rewind` selects a user message on the
current branch and restores **code + conversation**, **conversation only**, or
**code only**. Conversation recovery refills the stored prompt.

Select a message, then choose **Restore code and conversation**, **Restore
conversation**, or **Restore code**. Code actions show the recorded file count
and appear only when those files can be restored. Selecting an action performs
the restore without another confirmation. **Never mind** or Esc returns to the
message list; Esc there closes the menu.

## Local development

This plugin requires the unreleased host APIs in
[Phi PR #435](https://github.com/pulseaiclub/phi/pull/435).
There is no standalone release yet. `go.mod` temporarily replaces the SDK with
`../phi/ext/go`, so clone both repositories into the same parent directory.

```sh
git clone --branch feat/rewind-basic https://github.com/MIXYANG/phi.git phi
git -C phi checkout fba2e5dafb1a85b8c599243497dfc801ddd3a9fb
git clone https://github.com/MIXYANG/phi-rewind.git
cd phi-rewind
go test ./...
go build -o phi-rewind .
```

Build the Phi checkout following its contributing guide, and use that binary
to run this extension. After the host APIs and SDK are released, the plugin will
use the released SDK instead of the local replacement.

## Install locally

Like [phi-plan](https://github.com/pulseaiclub/phi-plan), the plugin ships a
binary plus `phi.yaml` and has its own version, independently of Phi.

Windows, from this directory:

```powershell
$target = Join-Path $env:USERPROFILE '.phi/extensions/rewind'
New-Item -ItemType Directory -Force -Path $target | Out-Null
go build -o (Join-Path $target 'phi-rewind.exe') .
(Get-Content phi.yaml -Raw).Replace('./phi-rewind', './phi-rewind.exe') |
    Set-Content (Join-Path $target 'phi.yaml') -Encoding utf8
```

Linux/macOS:

```sh
mkdir -p ~/.phi/extensions/rewind
go build -o ~/.phi/extensions/rewind/phi-rewind .
cp phi.yaml ~/.phi/extensions/rewind/phi.yaml
```

Use the Phi binary built from the host branch, then reload extensions from the
palette or restart Phi. Older Phi versions do not provide the required APIs.

## Recovery boundaries

- Only local `write` / `edit` changes recorded while enabled are recoverable,
  including worker sub-agents in separate workdirs. Earlier turns have no
  snapshots. Files are limited to 8 MiB; symlinks and hard links are unsupported.
  Hard links are rejected before capture and recovery to preserve linked paths.
  Path aliases such as Windows case variations are grouped by filesystem identity.
- Snapshots run in `tool_prepare`, after all argument rewrites and before the
  permission gate. Host execution IDs keep concurrent worker calls separate
  even when the model reuses a tool-call ID.
- Shell commands, MCP writes and manual edits are excluded. Recovery refuses
  files changed after or between recorded tool calls.
- Files created by `write` are deleted; empty parent directories stay.
- The agent and shell must be idle, with no live sub-agent jobs.
- Snapshots contain file contents and persist under `~/.phi/rewind/`.
  The plugin reads and rewrites the full session snapshot journal for each
  recorded change. Long sessions can increase memory use and disk I/O;
  the 8 MiB limit applies per file, not to the total journal.
- Conversation recovery preserves abandoned history using the host's new
  `EntryLeaf` record. Older Phi versions cannot read sessions containing it.
  The current model selection is preserved, including after reopening a session.
- New turns refill the original input before skill/reference expansion and
  extension rewrites. Older turns fall back to stored message text.
  Image attachments are not refilled; the turn list shows their count and a
  reminder to reattach them. Selected skills and references must also be reattached.
- File recovery runs before conversation recovery. File failures attempt
  rollback and leave conversation unchanged. A subsequent session persistence
  failure can leave files restored while conversation remains unchanged.

## Publication later

After the host APIs and SDK are released, add a tag-triggered
Release workflow. Package the binary with a platform-correct `phi.yaml`, using
`phi-rewind_<version>_<os>_<arch>.tar.gz` or `.zip` on Windows.
Release automation and prebuilt packages are not available yet.
