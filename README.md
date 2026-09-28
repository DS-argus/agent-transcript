# Agent Transcript

Open your agent's saved conversation in a Markdown viewer beside its tmux pane. Press **prefix + P** to open or refresh it.

## Supported agents

| Agent                                                         | Live-tested version | Support scope                                                                                               |
| ------------------------------------------------------------- | ------------------- | ----------------------------------------------------------------------------------------------------------- |
| [Codex](https://github.com/openai/codex) | 0.158.0 | **Requires `codex --no-daemon`.** Shared-daemon and remote-server sessions are not supported. |
| [Gajae Code (GJC)](https://github.com/Yeachan-Heo/gajae-code) | 0.17.4              | macOS only; discovers sessions through the local GJC registry.                                              |

## Supported platforms

| Host                   | Architecture   |
| ---------------------- | -------------- |
| Apple Silicon Mac      | arm64          |
| Intel Mac              | amd64 (x86-64) |
| ARM64 Linux            | arm64          |
| Intel/AMD 64-bit Linux | amd64 (x86-64) |

Linux artifacts are cross-compiled; Linux runtime behavior has not been verified. Agent-specific restrictions are listed above.

## Usage

- Press **prefix + P** in the agent pane, or **P** directly in copy-mode.
- The viewer opens above the agent, uses **95%** of the original pane's height, receives focus, and starts at the bottom of the document.

  To customize placement, size, and focus, add these to `~/.tmux.conf` before initializing TPM or tpack:

  ```tmux
  set -g @agent_transcript_position 'top'   # right | left | top | bottom
  set -g @agent_transcript_size '85%'       # smaller viewer leaves more room for input
  set -g @agent_transcript_focus 'off'      # keep focus in the current pane
  ```

  The viewer always starts at the bottom of the document; this is not configurable.

- Press the plugin key again from the source or its viewer to refresh the matching conversation in the same pane.
- Use the reader's native navigation/search keys. Press **q** to close it; leave search first if needed.
- Use normal tmux pane-switching keys to return to the agent.

The document is a **snapshot**, not a live feed. Refresh loads saved messages and returns to the bottom. Structured transcript views omit tool output and private reasoning. Original agent files are not modified.

For unsupported programs or an idle shell, the key only displays `Agent Transcript: Run this from a supported agent pane.` No viewer is opened and no screen capture is taken. Failures while reading a supported agent's conversation retain their specific error messages.

## Requirements

- **Runtime tools:** tmux >= 3.2, `ps`, `lsof`, and one Markdown reader:
- **First installation / binary updates:** `curl` or `wget`, plus `sha256sum` or `shasum`. Git is required by the plugin manager.

| Reader                                                      | Executable |
| ----------------------------------------------------------- | ---------- |
| [Leaf](https://github.com/RivoLink/leaf) — default          | `leaf`     |
| [Glow](https://github.com/charmbracelet/glow) — alternative | `glow`     |

Install the plugin and tools on the **same host as the agent**, with the tools available on tmux's `PATH`. For SSH sessions, install them on the remote host. Prebuilt binaries do not require Go or Python.

This is an initial release with version-sensitive session discovery. See [Known limitations](#known-limitations) before relying on it.

## Codex: `--no-daemon` required

**Codex is supported only in `--no-daemon` mode, verified on version 0.158.0.** Shared-daemon and remote-server sessions are not supported. Check your version with `codex --version`, then launch:

```sh
codex --no-daemon
```

To reopen a saved conversation in that mode, use `codex --no-daemon resume` and select the session. Finish or stop any ongoing work yourself before switching; the plugin does not restart Codex, change your settings, or shut down the shared server. This is a supported launch mode, **not automatic shared-daemon integration**. Normal conversation rendering and fork selection were checked on 0.158.0 in this mode.

Sessions originally created by an app-server may retain `source: "vscode"` even when resumed locally. Both `cli` and `vscode` origins are accepted, but the target pane's Codex process must own the rollout; noninteractive and subagent origins remain excluded.

## Installation

Install [TPM](https://github.com/tmux-plugins/tpm#installation) or [tpack](https://tmuxpack.github.io/tpack/getting-started/installation/), then choose **one** configuration below. Do not initialize both managers against the same plugin directory.

### TPM

Add to `~/.tmux.conf`, keeping TPM initialization last:

```tmux
set -g @plugin 'tmux-plugins/tpm'
set -g @plugin 'https://github.com/DS-argus/agent-transcript.git'

# Optional: set plugin preferences before initialization.
set -g @agent_transcript_reader 'leaf'
run '~/.tmux/plugins/tpm/tpm'
```

Reload with `tmux source-file ~/.tmux.conf`, then press **prefix + I** (uppercase I).

### tpack

With the standalone `tpack` executable installed on `PATH`, add:

```tmux
set -g @plugin 'https://github.com/DS-argus/agent-transcript.git'
set -g @agent_transcript_reader 'leaf'

# Keep this at the bottom of ~/.tmux.conf.
run 'tpack init'
```

Reload with `tmux source-file ~/.tmux.conf`, then press **prefix + I**, or run `tpack install` followed by `tpack source`.

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
set -g @agent_transcript_position 'top'    # right | left | top | bottom
set -g @agent_transcript_size '95%'        # viewer share, or positive cell count
set -g @agent_transcript_focus 'on'        # on | off
set -g @agent_transcript_key 'P'           # prefix + P; none disables
set -g @agent_transcript_copy_mode_key 'P' # copy-mode P; none disables

# Follow these settings with the TPM or tpack initialization from Installation.
```

- Size means width for left/right splits and height for top/bottom splits.
- Position and size apply to new panes; refreshing preserves existing geometry.
- Percentages and positive integers both describe the **viewer** size, not space reserved for the agent. A percentage is relative to the original pane; an integer is rows for top/bottom or columns for left/right.
- Commenting out a tmux option does not clear its running value. To restore the new defaults, run `tmux set -g @agent_transcript_position top` and `tmux set -g @agent_transcript_size '95%'`, then close and reopen existing viewers.
- `focus=off` preserves the previously active pane.
- Reload the plugin after changing keys. `none` disables an individual binding.
- Reader changes apply on the next invocation, including refresh:

```sh
tmux set -g @agent_transcript_reader glow
```

The selected reader must be installed; no other reader is substituted automatically.

## Known limitations

- **Codex shared server:** Codex 0.157.0 enabled automatic background-server startup for eligible sessions. In 0.158.0, launch the target TUI with `codex --no-daemon` for this plugin. Existing shared-server panes are not supported: their files belong to another process, and no supported per-pane active-thread lookup was found. The plugin never selects an arbitrary daemon thread.

- **Codex fork:** if the original and its forks remain open as one unambiguous ancestry chain, the last fork is preferred. Only messages saved in that file are shown; inherited history is not combined. An empty fork shows an informational notice.
- **Codex resume:** when an ancestor and its fork both remain open, the fork-first policy may select the fork even if the UI displays the ancestor. This plugin cannot exactly track the TUI's in-memory active thread.
- **Codex clear:** before the new rollout is saved, the previous conversation may appear. Multiple unrelated open rollouts or sibling forks produce an ambiguity notice; retry after the transition settles. Do not assume retry always resolves it.
- **GJC clear:** earlier context-cleared segments are included with **Context cleared** markers. The latest saved parent chain is used; unsaved UI branch navigation is not tracked.
- **Version compatibility:** agent storage formats and process ownership can change between releases. No guarantee is made for every Codex/GJC version or OS version.

## Troubleshooting

- **Unsupported pane:** `Agent Transcript: Run this from a supported agent pane.` means no supported agent was found in the foreground. No viewer is opened.
- **Download failed:** check access to GitHub Releases and the installer dependencies in Requirements, then reload tmux. Failed downloads never replace an existing binary. A missing release asset or checksum mismatch is an error, not a reason to run an unverified download.
- **Missing command:** ensure the selected reader, `ps`, and `lsof` are available on tmux's `PATH`. Minimal Linux environments may need these tools installed separately.
- **No saved messages:** complete a conversation turn, then invoke the plugin again.
- **Multiple sessions found:** the plugin could not select a unique owned conversation. It does not pick whichever file was modified most recently.
- **Split cannot fit or agent input is too small:** reduce the viewer percentage (for example `85%`), enlarge the window, or choose another split direction.
- **Custom GJC storage:** keep `GJC_CODING_AGENT_DIR`, or `GJC_CONFIG_DIR` / `PI_CONFIG_DIR`, available to the plugin process. GJC discovery reads its maintained local registry directly; it does not call the SDK CLI or connect to the broker.

Key-triggered errors appear briefly in the invoking client's tmux status line. After updating the binary, reload your configuration to refresh bindings.
