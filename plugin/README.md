# MCP Integration

Connect AI agents to running [ttt](https://github.com/eugenioenko/ttt) editors through MCP. Agents can discover editor sessions, read buffers, edit text, run commands, capture the screen, and interact with integrated terminals.

The plugin connects each editor to a shared local service provided by the `ttt-mcp` executable. No `--listen` flag or per-editor port configuration is needed.

## Requirements

- ttt v1.9.0 or newer.
- The `ttt-mcp` executable on PATH for both ttt and your agent.

Installing this plugin from ttt's Plugins panel does not install the executable.

## Install the companion executable

On Linux or macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/eugenioenko/ttt-mcp/v0.2.0/install.sh -o /tmp/install-ttt-mcp.sh
sh /tmp/install-ttt-mcp.sh v0.2.0
```

The installer verifies the download, installs the executable to `~/.local/bin`, and installs the bundled plugin. Ensure `~/.local/bin` is on PATH for both ttt and your agent.

On Windows, download `ttt-mcp-windows-amd64.exe` from the [releases page](https://github.com/eugenioenko/ttt-mcp/releases), rename it to `ttt-mcp.exe`, place it on PATH, and run:

```sh
ttt-mcp install-plugin
```

Restart ttt and approve the plugin's permissions.

## Configure your agent

Add a stdio MCP server to your agent's configuration:

```json
{
  "mcpServers": {
    "ttt": {
      "command": "ttt-mcp"
    }
  }
}
```

For Claude Code:

```sh
claude mcp add ttt -- ttt-mcp
```

If your agent cannot find `ttt-mcp`, use the executable's absolute path. Ask the agent to list your open ttt sessions, select the workspace, and describe the active buffer.

## Connection controls

The status bar shows `MCP ◉` when connected, `MCP ◌` while connecting or offline, and `MCP ⚠` when the service is unavailable.

- **MCP: Connect** connects the current editor to the shared service.
- **MCP: Disconnect** disconnects it until you reconnect or reload the plugin.

The first connected editor starts the service. Agents use `list_sessions` to discover editors and include `session_id` when targeting one of several sessions.

## Permissions

The plugin requests editor read/write, input, screen and clipboard reads, a drawer panel, terminal access, file/editor events, workspace file access, and permission to run `ttt-mcp` and connect to its local service. These capabilities let connected agents control the editor and its integrated terminals.

## Troubleshooting

If no sessions appear, check the ttt version, plugin approval, and the executable's PATH. Look for `ttt-mcp` messages in ttt's OUTPUT panel. You can also inspect the service from a terminal:

```sh
ttt-mcp sessions
ttt-mcp service status
```

See the [full documentation](https://github.com/eugenioenko/ttt-mcp#readme) for available tools, configuration, service management, and troubleshooting.
