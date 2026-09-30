# Agent Transcript

Read long agent replies in their own tmux pane, without losing your place when you ask the next question.

When an agent gives you a lengthy explanation, you often want to pause halfway through, quote a passage, or ask about a particular code block. Scrolling back through the reply and then returning to the agent's input makes that back-and-forth tedious—and makes it easy to lose the part you were reading.

Agent Transcript opens the saved conversation in a separate Markdown viewer pane. Keep the relevant passage in view, switch to the agent pane to ask your follow-up or paste a quote, then return to the viewer where you left off. Press **prefix + P** to open or refresh the conversation; use your normal tmux pane-switching keys to move between reading and asking.

You also get your Markdown reader's own conveniences: formatted code and tables, search, and heading navigation. Readers such as [Leaf](https://github.com/RivoLink/leaf#features) also let you copy entire code blocks or link URLs, making agent replies easier to reuse. Available features depend on the reader you choose and your terminal's clipboard support.

**Recommended layout:** keep the Markdown viewer above the agent and give it most of the pane's height, leaving only enough room below for the agent's input box. Read and navigate replies in the upper pane; use the lower pane to type questions. The default **top / 90%** layout is a starting point—adjust the percentage or resize the panes so your agent's input remains fully visible.

## Supported agents

| Agent                                                         | Live-tested version | Support scope                                                                                               |
| ------------------------------------------------------------- | ------------------- | ----------------------------------------------------------------------------------------------------------- |
| [Codex](https://github.com/openai/codex) | 0.158.0 | **Requires `codex --no-daemon`.** Shared-daemon and remote-server sessions are not supported. |
| [Gajae Code (GJC)](https://github.com/Yeachan-Heo/gajae-code) | 0.17.4              | macOS only; discovers sessions through the local GJC registry.                                              |
| [Claude Code](https://github.com/anthropics/claude-code) | 2.1.285 | Local foreground CLI; live-verified on macOS through PID registry session correlation. |

All supported agents must run in the target pane's foreground. Background/daemon discovery and subagents as source panes are not supported.

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
- The viewer opens above the agent, uses **90%** of the original pane's height, receives focus, and starts at the bottom of the document.

  To customize placement, size, and focus, add these to `~/.tmux.conf` before initializing TPM or tpack:

  ```tmux
  set -g @agent_transcript_position 'top'   # right | left | top | bottom
  set -g @agent_transcript_size_all '85%'       # smaller viewer leaves more room for input
  set -g @agent_transcript_focus 'off'      # keep focus in the current pane
  ```

  The viewer always starts at the bottom of the document; this is not configurable.

- Press the plugin key again from the source or its viewer to refresh the matching conversation in the same pane. After switching sessions, invoke from the source; a new viewer may open, and stale viewers cannot refresh the new session.
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

Install the plugin and tools on the **same host as the agent**, with the tools available on tmux's `PATH`. For SSH sessions, install them on the remote host. Prebuilt binaries do not require Go, Python, Node, or an agent SDK runtime.

This is an initial release with version-sensitive session discovery. See [Known limitations](#known-limitations) before relying on it.

## Codex: `--no-daemon` required

**Codex is supported only in `--no-daemon` mode, verified on version 0.158.0.** Shared-daemon and remote-server sessions are not supported. Check your version with `codex --version`, then launch:

```sh
codex --no-daemon
```

To reopen a saved conversation in that mode, use `codex --no-daemon resume` and select the session. Finish or stop any ongoing work yourself before switching; the plugin does not restart Codex, change your settings, or shut down the shared server. This is a supported launch mode, **not automatic shared-daemon integration**. Normal conversation rendering and fork selection were checked on 0.158.0 in this mode.

Sessions originally created by an app-server may retain `source: "vscode"` even when resumed locally. Both `cli` and `vscode` origins are accepted, but the target pane's Codex process must own the rollout; noninteractive and subagent origins remain excluded.

## Claude Code: local foreground CLI

[Claude Code](https://github.com/anthropics/claude-code) **2.1.285** was live-verified with the local foreground CLI on macOS. Discovery correlates the foreground process PID and birth time with Claude's local session registry, then reads that session's saved JSONL; the transcript need not remain open as a file descriptor. No extra Python, Node, or SDK runtime is needed by this plugin.

Ordinary conversations, resume, clear, CLI `--fork-session`, and the latest persisted rewind branch are supported within that scope. After a registry session switch, invoke from the source agent pane: a fresh viewer may open, while an old viewer remains bound to its old snapshot and refuses to act for the new session. PID registry session correlation and JSONL parsing are version-sensitive, not an official stable API.

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
set -g @agent_transcript_size_all '90%'        # viewer share, or positive cell count
# Optional agent-specific viewer sizes; leave unset to use size_all.
# set -g @agent_transcript_size_codex '90%'
# set -g @agent_transcript_size_claude '90%'
# set -g @agent_transcript_size_gjc '90%'
set -g @agent_transcript_focus 'on'        # on | off
set -g @agent_transcript_key 'P'           # prefix + P; none disables
set -g @agent_transcript_copy_mode_key 'P' # copy-mode P; none disables

# Follow these settings with the TPM or tpack initialization from Installation.
```

- Size means width for left/right splits and height for top/bottom splits.
- Position and size apply to new panes; refreshing preserves existing geometry.
- Size precedence is explicit CLI `--size` → `@agent_transcript_size_<agent>` → `@agent_transcript_size_all` → built-in `90%`. An unset or empty agent override uses `_all`; an invalid nonempty value is an error, not a fallback.
- To remove an override, run e.g. `tmux set -gu @agent_transcript_size_claude`, then close and reopen its viewer. Rename old `@agent_transcript_size` settings to `@agent_transcript_size_all` and reload the plugin; the old option is no longer read.
- Percentages and positive integers both describe the **viewer** size, not space reserved for the agent. A percentage is relative to the original pane; an integer is rows for top/bottom or columns for left/right.
- Commenting out a tmux option does not clear its running value. To restore the new defaults, run `tmux set -g @agent_transcript_position top` and `tmux set -g @agent_transcript_size_all '90%'`, then close and reopen existing viewers.
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
- **Claude branch/compact:** the latest persisted rewind branch is shown. An unsaved rewind may still show the old persisted branch until the next saved turn. Compact displays the selected compacted chain and summary rather than stitching the earlier original full history back in.
- **Claude ownership:** PID registry correlation proves a local foreground session, not pane-exclusive writes. Concurrent writers to the same session may contribute saved messages. Background sessions, attach flows, subagents as the source, and background work launched through the `/fork` UI are excluded or unverified; they are not foreground support.
- **Version compatibility:** agent storage formats and process ownership can change between releases. Claude JSONL and PID registry formats are version-sensitive implementation details, not an official stable API. No guarantee is made for every Codex/GJC/Claude version or OS version.

## Troubleshooting

- **Unsupported pane:** `Agent Transcript: Run this from a supported agent pane.` means no supported agent was found in the foreground. No viewer is opened.
- **Download failed:** check access to GitHub Releases and the installer dependencies in Requirements, then reload tmux. Failed downloads never replace an existing binary. A missing release asset or checksum mismatch is an error, not a reason to run an unverified download.
- **Missing command:** ensure the selected reader, `ps`, and `lsof` are available on tmux's `PATH`. Minimal Linux environments may need these tools installed separately.
- **No saved messages:** complete a conversation turn, then invoke the plugin again.
- **Multiple sessions found:** the plugin could not select a unique owned conversation. It does not pick whichever file was modified most recently.
- **Split cannot fit or agent input is too small:** reduce the viewer percentage (for example `85%`), enlarge the window, or choose another split direction.
- **Custom GJC storage:** keep `GJC_CODING_AGENT_DIR`, or `GJC_CONFIG_DIR` / `PI_CONFIG_DIR`, available to the plugin process. GJC discovery reads its maintained local registry directly; it does not call the SDK CLI or connect to the broker.

Key-triggered errors appear briefly in the invoking client's tmux status line. After updating the binary, reload your configuration to refresh bindings.
