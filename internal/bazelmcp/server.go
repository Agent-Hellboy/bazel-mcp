// Package bazelmcp provides an MCP server that exposes Bazel workflows (info, query,
// build, test, run) over the Model Context Protocol.
package bazelmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName    = "bazel-mcp"
	serverVersion = "0.1.0"
)

// Config holds server configuration for workspace, Bazel binary, and execution limits.
type Config struct {
	WorkspaceRoot  string        // Bazel workspace root for all commands
	BazelBinary    string        // Path to bazel or bazelisk executable
	StartupFlags   []string      // Flags passed before the command (e.g. --batch)
	CommonFlags    []string      // Flags passed after the command name
	DefaultTimeout time.Duration // Max time per Bazel invocation
	MaxOutputBytes int           // Max stdout/stderr bytes captured before truncation
	Logger         *slog.Logger  // Logger for server events
}

// Server is an MCP server that runs Bazel commands in a configured workspace.
type Server struct {
	cfg       Config
	runner    Runner
	mcpServer *sdkmcp.Server
}

type toolDefinition struct {
	tool   *sdkmcp.Tool
	handle sdkmcp.ToolHandler
}

// New creates and configures a Server. If runner is nil, RealRunner is used.
func New(cfg Config, runner Runner) *Server {
	cfg = withDefaults(cfg)
	if runner == nil {
		runner = RealRunner{}
	}

	server := &Server{
		cfg:    cfg,
		runner: runner,
		mcpServer: sdkmcp.NewServer(
			&sdkmcp.Implementation{
				Name:    serverName,
				Version: serverVersion,
			},
			&sdkmcp.ServerOptions{
				Capabilities: &sdkmcp.ServerCapabilities{},
				Instructions: fmt.Sprintf(
					"Use the Bazel tools to inspect, build, and test the workspace at %s with %s.",
					cfg.WorkspaceRoot,
					cfg.BazelBinary,
				),
				Logger: cfg.Logger,
			},
		),
	}
	server.registerTools()
	return server
}

func withDefaults(cfg Config) Config {
	if strings.TrimSpace(cfg.WorkspaceRoot) == "" {
		cfg.WorkspaceRoot = "."
	}
	if strings.TrimSpace(cfg.BazelBinary) == "" {
		cfg.BazelBinary = "bazel"
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = 5 * time.Minute
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 1 << 20
	}
	return cfg
}

// ServeStdio runs the server over stdin/stdout for MCP clients.
func (s *Server) ServeStdio(ctx context.Context) error {
	return s.Run(ctx, &sdkmcp.StdioTransport{})
}

// ServeIO runs the server over the given reader and writer (e.g., pipes or sockets).
func (s *Server) ServeIO(ctx context.Context, reader io.ReadCloser, writer io.WriteCloser) error {
	return s.Run(ctx, &sdkmcp.IOTransport{
		Reader: reader,
		Writer: writer,
	})
}

// Run starts the server with the given transport.
func (s *Server) Run(ctx context.Context, transport sdkmcp.Transport) error {
	return s.mcpServer.Run(ctx, transport)
}

// Connect creates a server session for testing or custom transport handling.
func (s *Server) Connect(ctx context.Context, transport sdkmcp.Transport, opts *sdkmcp.ServerSessionOptions) (*sdkmcp.ServerSession, error) {
	return s.mcpServer.Connect(ctx, transport, opts)
}

func (s *Server) registerTools() {
	tools := []toolDefinition{
		{
			tool: &sdkmcp.Tool{
				Name:        "bazel_info",
				Description: "Run `bazel info` in the configured workspace to inspect Bazel settings and output paths.",
				InputSchema: objectSchema(
					map[string]any{
						"key":             stringProperty("Optional `bazel info` key, for example `workspace` or `output_base`."),
						"flags":           stringArrayProperty("Additional flags passed after `bazel info`."),
						"timeout_seconds": timeoutProperty(),
					},
				),
			},
			handle: s.handleBazelInfo,
		},
		{
			tool: &sdkmcp.Tool{
				Name:        "bazel_query",
				Description: "Run `bazel query` against the current workspace.",
				InputSchema: objectSchema(
					map[string]any{
						"expression":      stringProperty("The Bazel query expression to evaluate."),
						"flags":           stringArrayProperty("Additional flags passed after `bazel query`."),
						"timeout_seconds": timeoutProperty(),
					},
					"expression",
				),
			},
			handle: s.handleBazelQuery,
		},
		{
			tool: &sdkmcp.Tool{
				Name:        "bazel_cquery",
				Description: "Run `bazel cquery` to inspect configured targets.",
				InputSchema: objectSchema(
					map[string]any{
						"expression":      stringProperty("The configured query expression to evaluate."),
						"flags":           stringArrayProperty("Additional flags passed after `bazel cquery`."),
						"timeout_seconds": timeoutProperty(),
					},
					"expression",
				),
			},
			handle: s.handleBazelCquery,
		},
		{
			tool: &sdkmcp.Tool{
				Name:        "bazel_aquery",
				Description: "Run `bazel aquery` to inspect actions generated for targets.",
				InputSchema: objectSchema(
					map[string]any{
						"expression":      stringProperty("The action query expression to evaluate."),
						"flags":           stringArrayProperty("Additional flags passed after `bazel aquery`."),
						"timeout_seconds": timeoutProperty(),
					},
					"expression",
				),
			},
			handle: s.handleBazelAquery,
		},
		{
			tool: &sdkmcp.Tool{
				Name:        "bazel_build",
				Description: "Run `bazel build` for one or more targets.",
				InputSchema: objectSchema(
					map[string]any{
						"targets":         stringArrayProperty("One or more Bazel targets to build."),
						"flags":           stringArrayProperty("Additional flags passed after `bazel build`."),
						"timeout_seconds": timeoutProperty(),
					},
					"targets",
				),
			},
			handle: s.handleBazelBuild,
		},
		{
			tool: &sdkmcp.Tool{
				Name:        "bazel_test",
				Description: "Run `bazel test` for one or more targets.",
				InputSchema: objectSchema(
					map[string]any{
						"targets":         stringArrayProperty("One or more Bazel targets to test."),
						"flags":           stringArrayProperty("Additional flags passed after `bazel test`."),
						"timeout_seconds": timeoutProperty(),
					},
					"targets",
				),
			},
			handle: s.handleBazelTest,
		},
		{
			tool: &sdkmcp.Tool{
				Name:        "bazel_run",
				Description: "Run `bazel run` for a single target. Builds the target if needed, then executes it. Supports binaries, sh_binary, and other runnable targets including prebuilt binaries wrapped in runnable rules.",
				InputSchema: objectSchema(
					map[string]any{
						"target":          stringProperty("The Bazel target to run (e.g. //path:binary)."),
						"args":            stringArrayProperty("Arguments passed to the runnable after `--`. Omitted if empty."),
						"flags":           stringArrayProperty("Additional flags passed after `bazel run` and before `--`."),
						"timeout_seconds": timeoutProperty(),
					},
					"target",
				),
			},
			handle: s.handleBazelRun,
		},
	}

	for _, tool := range tools {
		s.mcpServer.AddTool(tool.tool, tool.handle)
	}
}

func (s *Server) handleBazelInfo(ctx context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
	args, timeout, err := s.parseInfoArguments(request.Params.Arguments)
	if err != nil {
		return nil, err
	}
	return s.runBazel(ctx, "info", args, timeout)
}

func (s *Server) handleBazelQuery(ctx context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
	args, timeout, err := s.parseExpressionArguments(request.Params.Arguments, "expression")
	if err != nil {
		return nil, err
	}
	return s.runBazel(ctx, "query", args, timeout)
}

func (s *Server) handleBazelCquery(ctx context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
	args, timeout, err := s.parseExpressionArguments(request.Params.Arguments, "expression")
	if err != nil {
		return nil, err
	}
	return s.runBazel(ctx, "cquery", args, timeout)
}

func (s *Server) handleBazelAquery(ctx context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
	args, timeout, err := s.parseExpressionArguments(request.Params.Arguments, "expression")
	if err != nil {
		return nil, err
	}
	return s.runBazel(ctx, "aquery", args, timeout)
}

func (s *Server) handleBazelBuild(ctx context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
	args, timeout, err := s.parseTargetArguments(request.Params.Arguments)
	if err != nil {
		return nil, err
	}
	return s.runBazel(ctx, "build", args, timeout)
}

func (s *Server) handleBazelTest(ctx context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
	args, timeout, err := s.parseTargetArguments(request.Params.Arguments)
	if err != nil {
		return nil, err
	}
	return s.runBazel(ctx, "test", args, timeout)
}

func (s *Server) handleBazelRun(ctx context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
	args, timeout, err := s.parseRunArguments(request.Params.Arguments)
	if err != nil {
		return nil, err
	}
	return s.runBazel(ctx, "run", args, timeout)
}

func (s *Server) parseInfoArguments(raw json.RawMessage) ([]string, time.Duration, error) {
	fields, err := parseArgumentObject(raw)
	if err != nil {
		return nil, 0, invalidParams(err.Error())
	}

	key, _, err := consumeString(fields, "key")
	if err != nil {
		return nil, 0, invalidParams(err.Error())
	}

	flags, timeout, err := s.consumeFlagsAndTimeout(fields)
	if err != nil {
		return nil, 0, err
	}

	if err := ensureNoUnknownFields(fields); err != nil {
		return nil, 0, invalidParams(err.Error())
	}

	args := append([]string{}, flags...)
	if strings.TrimSpace(key) != "" {
		args = append(args, key)
	}
	return args, timeout, nil
}

func (s *Server) parseExpressionArguments(raw json.RawMessage, key string) ([]string, time.Duration, error) {
	fields, err := parseArgumentObject(raw)
	if err != nil {
		return nil, 0, invalidParams(err.Error())
	}

	expression, ok, err := consumeString(fields, key)
	if err != nil {
		return nil, 0, invalidParams(err.Error())
	}
	if !ok || strings.TrimSpace(expression) == "" {
		return nil, 0, invalidParams(fmt.Sprintf("%s is required", key))
	}

	flags, timeout, err := s.consumeFlagsAndTimeout(fields)
	if err != nil {
		return nil, 0, err
	}

	if err := ensureNoUnknownFields(fields); err != nil {
		return nil, 0, invalidParams(err.Error())
	}

	args := append([]string{}, flags...)
	args = append(args, expression)
	return args, timeout, nil
}

func (s *Server) parseTargetArguments(raw json.RawMessage) ([]string, time.Duration, error) {
	fields, err := parseArgumentObject(raw)
	if err != nil {
		return nil, 0, invalidParams(err.Error())
	}

	targets, ok, err := consumeStringSlice(fields, "targets")
	if err != nil {
		return nil, 0, invalidParams(err.Error())
	}
	if !ok || len(targets) == 0 {
		return nil, 0, invalidParams("targets is required")
	}

	flags, timeout, err := s.consumeFlagsAndTimeout(fields)
	if err != nil {
		return nil, 0, err
	}

	if err := ensureNoUnknownFields(fields); err != nil {
		return nil, 0, invalidParams(err.Error())
	}

	args := append([]string{}, flags...)
	args = append(args, targets...)
	return args, timeout, nil
}

func (s *Server) parseRunArguments(raw json.RawMessage) ([]string, time.Duration, error) {
	fields, err := parseArgumentObject(raw)
	if err != nil {
		return nil, 0, invalidParams(err.Error())
	}

	target, ok, err := consumeString(fields, "target")
	if err != nil {
		return nil, 0, invalidParams(err.Error())
	}
	if !ok || strings.TrimSpace(target) == "" {
		return nil, 0, invalidParams("target is required")
	}

	runArgs, _, err := consumeStringSlice(fields, "args")
	if err != nil {
		return nil, 0, invalidParams(err.Error())
	}

	flags, timeout, err := s.consumeFlagsAndTimeout(fields)
	if err != nil {
		return nil, 0, err
	}

	if err := ensureNoUnknownFields(fields); err != nil {
		return nil, 0, invalidParams(err.Error())
	}

	args := append([]string{}, flags...)
	args = append(args, target)
	if len(runArgs) > 0 {
		args = append(args, "--")
		args = append(args, runArgs...)
	}
	return args, timeout, nil
}

func (s *Server) consumeFlagsAndTimeout(fields map[string]json.RawMessage) ([]string, time.Duration, error) {
	flags, _, err := consumeStringSlice(fields, "flags")
	if err != nil {
		return nil, 0, invalidParams(err.Error())
	}

	timeout := s.cfg.DefaultTimeout
	timeoutSeconds, ok, err := consumeInt(fields, "timeout_seconds")
	if err != nil {
		return nil, 0, invalidParams(err.Error())
	}
	if ok {
		if timeoutSeconds <= 0 {
			return nil, 0, invalidParams("timeout_seconds must be greater than zero")
		}
		timeout = time.Duration(timeoutSeconds) * time.Second
	}

	return flags, timeout, nil
}

func (s *Server) runBazel(ctx context.Context, command string, commandArgs []string, timeout time.Duration) (*sdkmcp.CallToolResult, error) {
	args := make([]string, 0, len(s.cfg.StartupFlags)+1+len(s.cfg.CommonFlags)+len(commandArgs))
	args = append(args, s.cfg.StartupFlags...)
	args = append(args, command)
	args = append(args, s.cfg.CommonFlags...)
	args = append(args, commandArgs...)

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request := CommandRequest{
		Path:           s.cfg.BazelBinary,
		Args:           args,
		Dir:            s.cfg.WorkspaceRoot,
		MaxOutputBytes: s.cfg.MaxOutputBytes,
	}

	result, err := s.runner.Run(runCtx, request)
	if err != nil {
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: formatRunFailure(request, err)},
			},
			IsError: true,
		}, nil
	}

	if len(result.Command) == 0 {
		result.Command = append([]string{request.Path}, request.Args...)
	}

	isError := result.TimedOut || result.ExitCode != 0
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: formatCommandResult(s.cfg.WorkspaceRoot, result)},
		},
		IsError: isError,
	}, nil
}

func parseArgumentObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return map[string]json.RawMessage{}, nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object")
	}

	return fields, nil
}

func consumeString(fields map[string]json.RawMessage, key string) (string, bool, error) {
	raw, ok := fields[key]
	if !ok {
		return "", false, nil
	}
	delete(fields, key)

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false, fmt.Errorf("%s must be a string", key)
	}

	if strings.TrimSpace(value) == "" {
		return "", true, nil
	}

	return value, true, nil
}

func consumeStringSlice(fields map[string]json.RawMessage, key string) ([]string, bool, error) {
	raw, ok := fields[key]
	if !ok {
		return nil, false, nil
	}
	delete(fields, key)

	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		if err := validateStringSlice(key, list); err != nil {
			return nil, false, err
		}
		return list, true, nil
	}

	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if strings.TrimSpace(single) == "" {
			return nil, false, fmt.Errorf("%s must not be empty", key)
		}
		return []string{single}, true, nil
	}

	return nil, false, fmt.Errorf("%s must be a string or array of strings", key)
}

func consumeInt(fields map[string]json.RawMessage, key string) (int, bool, error) {
	raw, ok := fields[key]
	if !ok {
		return 0, false, nil
	}
	delete(fields, key)

	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false, fmt.Errorf("%s must be an integer", key)
	}

	return value, true, nil
}

func validateStringSlice(key string, values []string) error {
	for index, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s[%d] must not be empty", key, index)
		}
	}
	return nil
}

func ensureNoUnknownFields(fields map[string]json.RawMessage) error {
	if len(fields) == 0 {
		return nil
	}

	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return fmt.Errorf("unknown arguments: %s", strings.Join(keys, ", "))
}

func invalidParams(message string) error {
	return &jsonrpc.Error{
		Code:    jsonrpc.CodeInvalidParams,
		Message: message,
	}
}

func formatRunFailure(request CommandRequest, err error) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Command: %s\n", shellJoin(append([]string{request.Path}, request.Args...)))
	fmt.Fprintf(&builder, "Workspace: %s\n", request.Dir)
	fmt.Fprintf(&builder, "Error: %v", err)
	return builder.String()
}

func formatCommandResult(workspace string, result CommandResult) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Command: %s\n", shellJoin(result.Command))
	fmt.Fprintf(&builder, "Workspace: %s\n", workspace)
	fmt.Fprintf(&builder, "Duration: %s\n", result.Duration.Round(time.Millisecond))
	fmt.Fprintf(&builder, "Exit code: %d\n", result.ExitCode)

	if result.TimedOut {
		builder.WriteString("Timed out: yes\n")
	}
	if result.Truncated {
		builder.WriteString("Output truncated: yes\n")
	}

	if strings.TrimSpace(result.Stdout) != "" {
		builder.WriteString("\nStdout:\n")
		builder.WriteString(result.Stdout)
		if !strings.HasSuffix(result.Stdout, "\n") {
			builder.WriteByte('\n')
		}
	}

	if strings.TrimSpace(result.Stderr) != "" {
		builder.WriteString("\nStderr:\n")
		builder.WriteString(result.Stderr)
		if !strings.HasSuffix(result.Stderr, "\n") {
			builder.WriteByte('\n')
		}
	}

	return strings.TrimRight(builder.String(), "\n")
}

func shellJoin(parts []string) string {
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || strings.ContainsAny(part, " \t\r\n\"'\\") {
			quoted = append(quoted, strconv.Quote(part))
			continue
		}
		quoted = append(quoted, part)
	}
	return strings.Join(quoted, " ")
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringProperty(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
	}
}

func stringArrayProperty(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items": map[string]any{
			"type": "string",
		},
	}
}

func timeoutProperty() map[string]any {
	return map[string]any{
		"type":        "integer",
		"description": "Optional timeout in seconds for this Bazel command.",
		"minimum":     1,
		"maximum":     3600,
	}
}
