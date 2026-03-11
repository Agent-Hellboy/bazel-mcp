# bazel-mcp

`bazel-mcp` is a small Model Context Protocol server that exposes common Bazel workflows over stdio. It is implemented in Go on top of the official MCP Go SDK.

**Installation**

Download a pre-built binary from [Releases](https://github.com/Agent-Hellboy/bazel-mcp/releases) (or build from source below).

| Platform   | Download |
|-----------|----------|
| macOS ARM64 | `bazel-mcp_darwin_arm64.tar.gz` |
| macOS x86_64 | `bazel-mcp_darwin_amd64.tar.gz` |
| Linux x86_64 | `bazel-mcp_linux_amd64.tar.gz` |
| Linux ARM64 | `bazel-mcp_linux_arm64.tar.gz` |
| Windows x86_64 | `bazel-mcp_windows_amd64.zip` |

```bash
# Example: macOS ARM64
curl -sL https://github.com/Agent-Hellboy/bazel-mcp/releases/latest/download/bazel-mcp_darwin_arm64.tar.gz | tar xz -C /usr/local/bin
chmod +x /usr/local/bin/bazel-mcp
```

Or place the binary anywhere and use its full path in your MCP config.

**Tools**

- `bazel_info`: inspect Bazel configuration and output paths
- `bazel_query`: query the target graph
- `bazel_cquery`: inspect configured targets
- `bazel_aquery`: inspect generated build actions
- `bazel_build`: build one or more targets
- `bazel_test`: test one or more targets
- `bazel_run`: run a single target (builds if needed, then executes; supports binaries, sh_binary, and prebuilt binaries wrapped in runnable rules)

All tools run inside a configured Bazel workspace and return structured text that includes the executed command, workspace, duration, exit code, and any captured stdout or stderr.

**Build from source** (optional; binaries are available from [Releases](https://github.com/Agent-Hellboy/bazel-mcp/releases))

```bash
go build -o bazel-mcp ./cmd/bazel-mcp
```

**Run**

```bash
bazel-mcp --workspace /path/to/workspace
# or: go run ./cmd/bazel-mcp --workspace /path/to/workspace
```

Useful flags:

- `--bazel`: Bazel or Bazelisk executable to run. Defaults to `bazel`.
- `--workspace`: working directory for all Bazel commands. Defaults to the current directory.
- `--startup-flag`: repeatable Bazel startup flags inserted before the command.
- `--flag`: repeatable Bazel command flags inserted after the command name.
- `--timeout-seconds`: default timeout for each tool call. Defaults to `300`.
- `--max-output-bytes`: maximum stdout and stderr bytes captured per command before truncation. Defaults to `1048576`.

Environment variables mirror the main scalar flags:

- `BAZEL_MCP_WORKSPACE`
- `BAZEL_MCP_BAZEL_BIN`
- `BAZEL_MCP_TIMEOUT_SECONDS`
- `BAZEL_MCP_MAX_OUTPUT_BYTES`

**MCP client setup**

**Cursor**

Add to `~/.cursor/mcp.json` (global) or `.cursor/mcp.json` (project).

Using downloaded binary:

```json
{
  "mcpServers": {
    "bazel-mcp": {
      "type": "stdio",
      "command": "/usr/local/bin/bazel-mcp",
      "args": ["--workspace", "/path/to/your/bazel/workspace"]
    }
  }
}
```

Use the full path to wherever you installed the binary.

Using source (requires Go):

```json
{
  "mcpServers": {
    "bazel-mcp": {
      "type": "stdio",
      "command": "sh",
      "args": [
        "-c",
        "cd /path/to/bazel-mcp && go run /path/to/bazel-mcp/cmd/bazel-mcp --workspace /path/to/your/bazel/workspace"
      ]
    }
  }
}
```

Use full absolute paths; some clients mis-resolve `./cmd/bazel-mcp`.

Restart Cursor after changing the config.

**Other MCP clients** (Claude Desktop, Continue, Windsurf, etc.): Use the same stdio config format. Refer to your client's docs for config file location and structure.

**Development**

```bash
gofmt -w ./cmd ./internal ./e2e
go test ./...
```

Run the Bazel-backed end-to-end check locally with:

```bash
go test -tags=e2e -v ./e2e
```

That test requires `bazel` on `PATH`. CI runs both the unit suite and this end-to-end check on every push to `main` and on pull requests.
