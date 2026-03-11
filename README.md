# bazel-mcp

`bazel-mcp` is a small Model Context Protocol server that exposes common Bazel workflows over stdio. It is implemented in Go on top of the official MCP Go SDK, so it can be built and run with the local Go toolchain alone.

**Tools**

- `bazel_info`: inspect Bazel configuration and output paths
- `bazel_query`: query the target graph
- `bazel_cquery`: inspect configured targets
- `bazel_aquery`: inspect generated build actions
- `bazel_build`: build one or more targets
- `bazel_test`: test one or more targets
- `bazel_run`: run a single target (builds if needed, then executes; supports binaries, sh_binary, and prebuilt binaries wrapped in runnable rules)

All tools run inside a configured Bazel workspace and return structured text that includes the executed command, workspace, duration, exit code, and any captured stdout or stderr.

**Build**

```bash
go build ./cmd/bazel-mcp
```

**Run**

```bash
go run ./cmd/bazel-mcp --workspace /path/to/workspace
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

Add `bazel-mcp` to your MCP client configuration. An example config is in `mcp.json.example`; copy and adjust paths.

Config file locations:
- **Cursor**: `~/.cursor/mcp.json` (global) or `.cursor/mcp.json` (project)
- **Claude Desktop**: `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS), `%APPDATA%\Claude\claude_desktop_config.json` (Windows)
- **Continue / Windsurf**: see each client’s MCP setup docs

**Cursor** (`~/.cursor/mcp.json` or `.cursor/mcp.json` in your project):

Use full absolute paths for reliability (some clients mis-resolve `./cmd/bazel-mcp`):

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

**Claude Desktop** (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS):

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

**Using a built binary** (avoids `go run` on each start):

```bash
go build -o ~/bin/bazel-mcp ./cmd/bazel-mcp
```

```json
{
  "mcpServers": {
    "bazel-mcp": {
      "command": "/Users/you/bin/bazel-mcp",
      "args": ["--workspace", "/path/to/workspace"]
    }
  }
}
```

Restart the client after changing the config.

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
