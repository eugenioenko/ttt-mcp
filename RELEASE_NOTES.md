# ttt-mcp v0.2.0

- `ttt.decorations` through `call_api`: highlight line ranges in a file and draw notes under them.
- `mcp.drawer` through `call_api`: open a side drawer built from a JSON widget spec, update it, and receive widget callbacks as `drawer.*` events. Specs are validated before they are shown.
- The plugin recovers from a malformed service response instead of stalling its reconnect loop.

## Compatibility

**Requires ttt v1.9.0 or newer**, which provides the `ttt.rpc` and `ttt.automation` plugin APIs. Installing ttt-mcp does not modify or upgrade the editor binary.

The plugin requests two new permissions, `editor.decorations` and `panel.drawer`. After upgrading, restart ttt and approve them.

## Downloads

Prebuilt binaries: Linux and macOS on amd64/arm64, plus Windows amd64. Each binary embeds the plugin. A separate `ttt-mcp-plugin.tar.gz` and `checksums.txt` are also provided.

On Linux/macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/eugenioenko/ttt-mcp/v0.2.0/install.sh -o /tmp/install-ttt-mcp.sh
sh /tmp/install-ttt-mcp.sh v0.2.0
```

The installer verifies the download checksum and installs the binary and plugin. Configure your agent to run `ttt-mcp` as a stdio MCP server, start ttt v1.9.0 or newer, and approve the plugin.

See the [README](https://github.com/eugenioenko/ttt-mcp#readme) for agent configuration, examples, permissions and troubleshooting.
