# ttt-mcp v0.1.0

The first release of the Go MCP server and Lua plugin for discovering and controlling running ttt editors.

- Discover open editor sessions by workspace, name, PID and active file.
- Run registered editor/plugin commands and inspect buffers, selections, screen text, debug state and output.
- Drive keys, typing, paste, mouse and terminal input through the plugin RPC connection.
- Run ordered action batches, cancel pending work, and observe editor/file events.
- Start one shared local service automatically and reconnect editors after a service restart.
- Install the embedded Lua plugin with `ttt-mcp install-plugin`.

## Compatibility

**Requires a development ttt build with both `ttt.rpc` and `ttt.automation`. The released ttt v1.8.0 is not compatible.** The README includes a tested public RPC revision and the additional automation patch needed to build a compatible editor. Installing ttt-mcp does not modify or upgrade the editor binary.

## Downloads

Prebuilt binaries: Linux and macOS on amd64/arm64, plus Windows amd64. Each binary embeds the plugin. A separate `ttt-mcp-plugin.tar.gz` and `checksums.txt` are also provided.

On Linux/macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/eugenioenko/ttt-mcp/v0.1.0/install.sh -o /tmp/install-ttt-mcp.sh
sh /tmp/install-ttt-mcp.sh v0.1.0
```

The installer verifies the download checksum and installs the binary and plugin. Configure your agent to run `ttt-mcp` as a stdio MCP server, start a compatible ttt build, and approve the plugin.

See the [README](https://github.com/eugenioenko/ttt-mcp#readme) for agent configuration, examples, permissions and troubleshooting.
