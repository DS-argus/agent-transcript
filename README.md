# Agent Transcript

Open your agent's saved conversation in a Markdown viewer beside its tmux pane. Press **prefix + P** to open or refresh it.

## Supported agents

| Agent | Live-tested version | Support scope |
|---|---|---|
| Codex | 0.154.0 | Local rollout files owned by the foreground agent process. The 0.157.1 shared-daemon mode is not validated. |
| Gajae Code (GJC) | 0.17.4 | macOS only; discovers sessions through the local GJC registry. |

## Supported platforms

| Host | Architecture | Artifact |
|---|---|---|
| Apple Silicon Mac | arm64 | `agent-transcript-darwin-arm64` |
| Intel Mac | amd64 (x86-64) | `agent-transcript-darwin-amd64` |
| ARM64 Linux | arm64 | `agent-transcript-linux-arm64` |
| Intel/AMD 64-bit Linux | amd64 (x86-64) | `agent-transcript-linux-amd64` |

Linux artifacts are cross-compiled; Linux runtime behavior has not been verified. Agent-specific restrictions are listed above.

## Usage

- Press **prefix + P** in the agent pane, or **P** directly in copy-mode.
- The viewer opens on the right, receives focus, and starts at the bottom.
- Press the plugin key again from the source or its viewer to refresh the matching conversation in the same pane.
- Use the reader's native navigation/search keys. Press **q** to close it; leave search first if needed.
- Use normal tmux pane-switching keys to return to the agent.

The document is a **snapshot**, not a live feed. Refresh loads saved messages and returns to the bottom. Structured transcript views omit tool output and private reasoning. Original agent files are not modified.

For unsupported programs or an idle shell, the same key opens a **screen capture** of the pane's retained scrollback instead. Screen captures are unfiltered terminal text and may include tool output or other sensitive on-screen content. Agent discovery/parsing failures do not silently switch to screen capture.

## Requirements

- **Runtime tools:** tmux >= 3.2, `ps`, `lsof`, and one Markdown reader:
- **First installation / binary updates:** `curl` or `wget`, plus `sha256sum` or `shasum`. Git is required by the plugin manager.

| Reader | Executable |
|---|---|
| [Leaf](https://github.com/RivoLink/leaf) — default | `leaf` |
| [Glow](https://github.com/charmbracelet/glow) — alternative | `glow` |

Install the plugin and tools on the **same host as the agent**, with the tools available on tmux's `PATH`. For SSH sessions, install them on the remote host. Prebuilt binaries do not require Go or Python.

This is an initial release with version-sensitive session discovery. See [Known limitations](#known-limitations) before relying on it.

## Installation

Install [TPM](https://github.com/tmux-plugins/tpm#installation) or [tpack](https://tmuxpack.github.io/tpack/getting-started/installation/), then choose **one** configuration below. Do not initialize both managers against the same plugin directory.

### TPM

Add to `~/.tmux.conf`, keeping TPM initialization last:

```tmux
set -g @plugin 'tmux-plugins/tpm'
set -g @plugin 'DS-argus/agent-transcript'

# Optional: set plugin preferences before initialization.
set -g @agent_transcript_reader 'leaf'
run '~/.tmux/plugins/tpm/tpm'
```

Reload with `tmux source-file ~/.tmux.conf`, then press **prefix + I** (uppercase I).

### tpack

With the standalone `tpack` executable installed on `PATH`, add:

```tmux
set -g @plugin 'DS-argus/agent-transcript'
set -g @agent_transcript_reader 'leaf'

# Keep this at the bottom of ~/.tmux.conf.
run 'tpack init'
```

Reload with `tmux source-file ~/.tmux.conf`, then press **prefix + I**, or run `tpack install`.

### Automatic binary installation and updates

The manager clones the plugin scripts. On first load, the plugin downloads **only your OS/CPU's binary** from [GitHub Releases](https://github.com/DS-argus/agent-transcript/releases), verifies its SHA-256 checksum and version, and installs it into the checkout's `bin/` directory. Leaf/Glow must be installed separately.

The checkout's `VERSION` pins the release tag; it does not fetch an arbitrary latest binary. When your plugin manager updates the checkout, reloading the plugin installs the matching binary if necessary. A matching installed version requires no download. Installation needs a writable plugin directory, `curl` or `wget`, and `sha256sum` or `shasum`. Initial installation and version changes require network access.

### Manual checkout / source build

```sh
git clone https://github.com/DS-argus/agent-transcript.git
```

Load the checkout directly in `~/.tmux.conf` (the same automatic installer runs):

```tmux
run-shell '"/absolute/path/to/agent-transcript/agent-transcript.tmux"'
```

For an offline source build, run `make build` inside the checkout with **Go >= 1.24** and **Make**, then load it. The build uses the tracked `VERSION`, so a matching local build is reused without downloading. No `PATH` change is necessary.

## Configuration

These optional settings show the defaults. Put them **before** loading the plugin:

```tmux
set -g @agent_transcript_reader 'leaf'     # leaf | glow
set -g @agent_transcript_position 'right'  # right | left | top | bottom
set -g @agent_transcript_size '50%'        # 1..99%, or positive cells
set -g @agent_transcript_focus 'on'        # on | off
set -g @agent_transcript_key 'P'           # prefix + P; none disables
set -g @agent_transcript_copy_mode_key 'P' # copy-mode P; none disables

# Follow these settings with the TPM or tpack initialization from Installation.
```

- Size means width for left/right splits and height for top/bottom splits.
- Position and size apply to new panes; refreshing preserves existing geometry.
- `focus=off` preserves the previously active pane.
- Reload the plugin after changing keys. `none` disables an individual binding.
- Reader changes apply on the next invocation, including refresh:

```sh
tmux set -g @agent_transcript_reader glow
```

The selected reader must be installed; no other reader is substituted automatically.

## Known limitations

- **Codex fork:** if the original and its forks remain open as one unambiguous ancestry chain, the last fork is preferred. Only messages saved in that file are shown; inherited history is not combined. An empty fork shows an informational notice.
- **Codex resume:** when an ancestor and its fork both remain open, the fork-first policy may select the fork even if the UI displays the ancestor. This plugin cannot exactly track the TUI's in-memory active thread.
- **Codex clear:** before the new rollout is saved, the previous conversation may appear. Multiple unrelated open rollouts or sibling forks produce an ambiguity notice; retry after the transition settles. Do not assume retry always resolves it.
- **GJC clear:** earlier context-cleared segments are included with **Context cleared** markers. The latest saved parent chain is used; unsaved UI branch navigation is not tracked.
- **Version compatibility:** agent storage formats and process ownership can change between releases. No guarantee is made for every Codex/GJC version or OS version.

## Troubleshooting

- **Download failed:** check access to GitHub Releases and the installer dependencies below, then reload tmux. Failed downloads never replace an existing binary. A missing release asset or checksum mismatch is an error, not a reason to run an unverified download.
- **Missing command:** ensure the selected reader, `ps`, and `lsof` are available on tmux's `PATH`. Minimal Linux environments may need these tools installed separately.
- **No saved messages:** complete a conversation turn, then invoke the plugin again.
- **Multiple sessions found:** the plugin could not select a unique owned conversation. It does not pick whichever file was modified most recently.
- **Split cannot fit:** enlarge the window, reduce the size, or choose another direction.
- **Custom GJC storage:** keep `GJC_CODING_AGENT_DIR`, or `GJC_CONFIG_DIR` / `PI_CONFIG_DIR`, available to the plugin process. GJC discovery reads its maintained local registry directly; it does not call the SDK CLI or connect to the broker.

Key-triggered errors appear briefly in the invoking client's tmux status line. After updating the binary, reload your configuration to refresh bindings.

For source layout and release build details, see the [architecture and distribution guide](docs/plugin-architecture-and-distribution.md).
