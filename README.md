# ttt-mcp

[![CI](https://github.com/eugenioenko/ttt-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/eugenioenko/ttt-mcp/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/eugenioenko/ttt-mcp)](https://github.com/eugenioenko/ttt-mcp/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A Go MCP server and Lua plugin for discovering and controlling running [ttt](https://github.com/eugenioenko/ttt) editors. Agents can inspect buffers and screens, execute any registered command, edit text, drive keyboard/mouse input, and use the editor and terminal Lua APIs. No `--listen` flag or per-editor port configuration.

```text
ttt + Lua plugin ─┐                         ┌─ agent + ttt-mcp (stdio)
                 ├─ ttt-mcp shared service ─┤
ttt + Lua plugin ─┘                         └─ agent + ttt-mcp (stdio)
```

The first plugin starts the shared service. Other editors join it. Every editor registers a unique session ID, PID, workspace paths, name, active file and version. Agents use `list_sessions` and target subsequent calls with `session_id`. Omitting the ID works only when exactly one editor is connected. Disconnecting an editor removes its session immediately; reconnecting creates a new ID. Actions from different agents are serialized per editor, and a batch holds that editor until its sequence finishes.

## Requirements

**v0.1.0 requires unreleased ttt plugin APIs: `ttt.rpc` and `ttt.automation`. ttt v1.8.0 is not compatible.** Use a development build containing both APIs, or follow [Building a compatible ttt](#building-a-compatible-ttt). Installing a prebuilt ttt-mcp binary does not require Go.

The automation API supplies mouse/paste actions, session metadata, and completion acknowledgments after handling and redraw. It also cancels queued work and refuses actions while plugin approvals are pending. It does not enable the editor's HTTP listener.

## Install

On Linux or macOS, download and run the installer:

```sh
curl -fsSL https://raw.githubusercontent.com/eugenioenko/ttt-mcp/v0.1.0/install.sh -o /tmp/install-ttt-mcp.sh
sh /tmp/install-ttt-mcp.sh v0.1.0
```

The installer verifies the binary's SHA-256 checksum, installs it to `~/.local/bin`, and installs the embedded Lua plugin. Set `INSTALL_DIR` to choose another binary directory. Ensure that directory is on PATH for both ttt and your agent.

On Windows, download `ttt-mcp-windows-amd64.exe` from [Releases](https://github.com/eugenioenko/ttt-mcp/releases), rename it to `ttt-mcp.exe`, place it on PATH, and run `ttt-mcp install-plugin`. The same compatibility requirements apply.

Restart a compatible ttt build and approve the plugin's permissions. The status bar shows `MCP ◉` when connected, `MCP ◌` while connecting or offline, and `MCP ⚠` when the service is unavailable. Command palette entries `MCP: Connect` and `MCP: Disconnect` control participation; disconnect stays off until explicitly reconnected or the plugin reloads.

`install-plugin --dir PATH` writes the two plugin files to a chosen directory. By default it uses `${TTT_CONFIG_DIR:-~/.config/ttt}/plugins/ttt-mcp`. The binary embeds the Lua source and manifest, so no separate plugin download is needed.

Releases include Linux/macOS amd64/arm64 binaries, a Windows amd64 binary, a separate plugin archive, and `checksums.txt`.

The plugin requests editor read/write, input, screen and clipboard reads, a drawer panel, terminal access, file/editor events, workspace file access, and permission to run `ttt-mcp` and connect to its local socket. These capabilities let connected agents control the editor, including its integrated terminals. The plugin never approves another plugin's permissions.

## Agent configuration

Configure a stdio MCP server using the executable on PATH:

```json
{
  "mcpServers": {
    "ttt": {
      "command": "ttt-mcp"
    }
  }
}
```

The stdio process bridges to the shared service; each agent can launch its own. Only MCP messages go to stdout. No client-specific configuration is edited automatically.

If the agent cannot find executables on your shell's PATH, use the absolute binary path in `command`. Shell shortcuts such as `~` are not expanded by every MCP client.

An agent normally calls `list_sessions`, chooses the matching workspace, then includes its `session_id` in subsequent calls. A stale ID produces an error instead of retargeting another editor.

Try asking your agent: “List my open ttt sessions, select the workspace I name, and describe its active buffer.” For an editing test, open a scratch file and ask it to type a line, undo, redo, and capture the screen.

## Tools

| Tool | Purpose |
| --- | --- |
| `list_sessions` | Discover open editor sessions and their workspaces. |
| `list_commands` | Discover all core and plugin command IDs and titles. |
| `execute_command` | Run any command by ID, or exact title with `by_title`. |
| `screenshot` | Return the rendered screen as plain text. |
| `debug_state` | Inspect tabs, focus, cursor, selection, panels, widget tree, diagnostics and output. |
| `read_buffer` | Return the entire active buffer, file path and cursor. |
| `open_file` | Open a path, optionally at a line or read-only. |
| `type_text` / `paste_text` | Type key presses or paste literal text, including Unicode. |
| `press_key` | Press keys, modifiers or chords such as `ctrl+k ctrl+s`. |
| `mouse` | Click, right-click, hover, drag or scroll. |
| `show_panel` | Show/focus a bottom panel by its ID. |
| `read_clipboard` | Read the editor clipboard. Use the `editor.copy` command or `copy` action to copy. |
| `run_actions` | Run up to 256 ordered actions without another agent interleaving. |
| `run_script` | Run the editor's `--exec`/`--listen` action syntax over plugin RPC. |
| `wait_for` | Wait for screen text rather than guessing a delay. |
| `list_api` / `call_api` | Discover/call public `ttt`, `ttt.editor`, and `ttt.terminal` functions with JSON arguments. |
| `read_events` | Poll file/editor/session events using a cursor. |

For example, `run_actions` can paste, save, and capture the result:

```json
{
  "session_id": "<ID from list_sessions>",
  "actions": [
    { "action": "paste", "args": "hello; 世界\nfrom an agent" },
    { "action": "key", "args": "ctrl+s" },
    { "action": "screenshot" }
  ]
}
```

Supported action names: `session`, `commands`, `command` (ID), `exec` (title), `open`, `buffer`, `screenshot`, `debug`, `click`, `rclick`, `hover`, `drag`, `scroll`, `key`, `type`, `paste`, `copy`, `clipboard`, `panel`, `wait`, `wait-for`, `quit`/`shutdown`, `api.list`, and `api.call`. `open` takes `params: {path, line?, readonly?}`. Other ordinary actions take a string in `args`. Coordinates use `x y`, dragging `x1 y1 x2 y2`, scrolling `x y signed-count` (positive down). `api.call` takes `params: {module, function, args}`.

`run_script` supports semicolon-separated actions with the same basic splitting rule as the editor harness. Set `separator` when literal text contains semicolons, or prefer `run_actions` for structured input. For example: `type hello; key enter; type world; key ctrl+s; screenshot`. `screenshot` and `debug` return directly; an optional path also writes through the plugin's scoped filesystem. Script execution stops at the first error or shutdown request. Waits are limited to 30 seconds per action; `wait-for` defaults to five seconds and accepts `timeout=MS`.

`call_api` accepts a module, function name and positional `args` array. For example, `{"module":"ttt.editor","function":"replace","args":[1,1,1,6,"hello"]}` performs an undoable edit. Callbacks, Lua source, panel render functions, and other Lua function values cannot be passed over JSON. The complete API reference is in [ttt's plugin authoring guide](https://github.com/eugenioenko/ttt/blob/main/docs-web/src/content/docs/guides/plugin-authoring.md).

To address a terminal explicitly, call `call_api` with `module: "ttt.terminal"`, `function: "list"`, and `args: []`, then call `send` with `args: ["<terminal ID>", "hello world"]`. No newline means Enter is not pressed. General typing/key tools send input to the focused widget; explicit terminal IDs avoid ambiguity. Some development builds report terminal focus as `other` ([ttt#1059](https://github.com/eugenioenko/ttt/issues/1059)).

Screen coordinates and debug-state positions are zero-based. `ttt.editor` API line/column arguments and `read_buffer` cursor positions are one-based; columns count runes, not bytes or screen cells. `debug_state` caps buffer text at 1000 lines; use `read_buffer` for the whole file. Each RPC message is limited to 32 MiB. Save retains the editor's configured formatting and final-newline behavior, including its undo steps.

Editor actions acknowledge main-thread handling and redraw. External work initiated by a command (search, LSP, git, terminal processes) may continue asynchronously: use `wait_for` or inspect the resulting state. Batches stop on failure but do not roll back earlier actions. Cancellation prevents subsequent queued actions, settles the current action before releasing the editor, and leaves already-applied edits available to normal undo.

Events retain the latest 256 entries. Pass `cursor` from the previous response as `since`; `truncated` indicates that older history was dropped. Events include editor/file changes and session connect/disconnect. Sessions and event history are in memory and reset when the service restarts.

## Local service

```sh
ttt-mcp sessions
ttt-mcp service status
ttt-mcp service stop
```

The service uses `~/.cache/ttt-mcp/hub.sock` on Linux/macOS and `127.0.0.1:4251` on Windows. A private runtime directory contains its authentication token, endpoint descriptor and log. Every connection authenticates before accessing sessions. Unix socket permissions and the runtime directory restrict access to the current user. The Windows port is loopback-only and also requires the token; its fixed port currently allows one shared service per machine. Startup and service-lifetime locks prevent competing daemons. Stopping ttt removes its session; the shared service remains available until stopped.

`TTT_MCP_NAME` gives an editor a friendly session name; otherwise its first workspace path is used. `TTT_MCP_DIR` changes the service directory and must match in ttt and agent environments. For a custom Unix directory, also add its socket endpoint to the plugin's `network.rpc` permission and approve that change. `service run --dir PATH` runs the service in the foreground for debugging. The token returned by `service ensure` is used internally by the Lua plugin and should not be included in logs or agent configuration.

## Troubleshooting

- **No sessions:** check the ttt build requirements, plugin approval, and that `ttt-mcp` is on the editor's PATH. Reload the plugin after changing its files. Check ttt's OUTPUT panel for `ttt-mcp` messages.
- **Several sessions:** call `list_sessions` and supply the desired `session_id`; the server deliberately refuses to guess.
- **Service unavailable:** inspect `~/.cache/ttt-mcp/service.log` or run `ttt-mcp service run` in a terminal. Editors reconnect automatically after a restart.
- **Unknown module `ttt.automation`:** the editor build lacks the required API. Installing the companion alone does not add core editor APIs.

## Build from source

Requires Go 1.25.1 or newer.

```sh
git clone https://github.com/eugenioenko/ttt-mcp.git
cd ttt-mcp
make build
mkdir -p "$HOME/.local/bin"
install -m 755 bin/ttt-mcp "$HOME/.local/bin/ttt-mcp"
ttt-mcp install-plugin
```

### Building a compatible ttt

For this first companion release, the public RPC branch needs the additional automation patch. From the directory containing your `ttt-mcp` checkout, use a separate ttt checkout:

```sh
git clone --branch feat/plugin-rpc --single-branch https://github.com/eugenioenko/ttt.git ttt-rpc
git -C ttt-rpc checkout 46078783513d0a7eacbddad0f1860c4980296b71
git -C ttt-rpc apply ../ttt-mcp/compat/ttt-automation.patch
make -C ttt-rpc build
./ttt-rpc/bin/ttt
```

The pinned revision and patch have been tested together. If your ttt checkout already includes `ttt.automation`, do not apply the patch again. This compatibility step can be retired once the required APIs are released upstream.

## Tests

```sh
make test
make lint
make test-editor TTT_BINARY=/absolute/path/to/compatible/ttt
```

Unit tests cover JSON-RPC framing, bidirectional calls/cancellation, authenticated session discovery/routing, ambiguous and stale IDs, per-editor serialization, retained events, script parsing, and MCP tool schemas/calls through the official Go SDK. The real-editor test starts two ttt processes with separate temporary plugin configurations and drives the compiled companion over MCP stdio. It verifies discovery, target isolation, Unicode paste/save/undo, commands, Lua API edits, mouse input, snapshots, events, and disconnect cleanup. It never uses `--listen` or your real ttt settings.

## License

MIT. The release workflow was adapted from [wp](https://github.com/eugenioenko/wp).
