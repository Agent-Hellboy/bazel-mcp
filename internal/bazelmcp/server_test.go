package bazelmcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeRunner struct {
	calls  []CommandRequest
	result CommandResult
	err    error
}

func (f *fakeRunner) Run(_ context.Context, request CommandRequest) (CommandResult, error) {
	f.calls = append(f.calls, request)
	result := f.result
	if len(result.Command) == 0 {
		result.Command = append([]string{request.Path}, request.Args...)
	}
	if result.Duration == 0 {
		result.Duration = 125 * time.Millisecond
	}
	return result, f.err
}

func TestInitializeAndToolsList(t *testing.T) {
	server := newTestServer(&fakeRunner{})
	session := newTestClientSession(t, server)

	initialize := session.InitializeResult()
	if initialize == nil {
		t.Fatal("expected initialize result")
	}
	if initialize.ServerInfo == nil {
		t.Fatal("expected server info in initialize result")
	}
	if initialize.ServerInfo.Name != serverName {
		t.Fatalf("unexpected server name: got %q want %q", initialize.ServerInfo.Name, serverName)
	}
	if !strings.Contains(initialize.Instructions, "/tmp/workspace") {
		t.Fatalf("unexpected instructions: %q", initialize.Instructions)
	}

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}

	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}

	want := []string{
		"bazel_aquery",
		"bazel_build",
		"bazel_cquery",
		"bazel_info",
		"bazel_query",
		"bazel_run",
		"bazel_test",
	}

	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected tools: got %v want %v", names, want)
	}
}

func TestBazelBuildCallsRunnerWithTargetsAndFlags(t *testing.T) {
	runner := &fakeRunner{
		result: CommandResult{
			Stdout:   "Build completed successfully",
			ExitCode: 0,
		},
	}
	server := newTestServer(runner)
	session := newTestClientSession(t, server)

	if len(runner.calls) != 0 {
		t.Fatalf("expected initialize-free runner state, got %d call(s)", len(runner.calls))
	}

	result, err := session.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "bazel_build",
		Arguments: map[string]any{
			"targets":         []string{"//cmd/server:server", "//lib:all"},
			"flags":           []string{"--config=ci"},
			"timeout_seconds": 45,
		},
	})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected one runner call, got %d", len(runner.calls))
	}

	gotArgs := strings.Join(runner.calls[0].Args, " ")
	wantArgs := "build --config=ci //cmd/server:server //lib:all"
	if gotArgs != wantArgs {
		t.Fatalf("unexpected bazel args: got %q want %q", gotArgs, wantArgs)
	}

	if result.IsError {
		t.Fatal("expected successful build result")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected one content item, got %d", len(result.Content))
	}
	if !strings.Contains(joinToolContent(result), "Exit code: 0") {
		t.Fatalf("expected exit code in response text, got %q", joinToolContent(result))
	}
}

func TestBazelRunCallsRunnerWithTargetAndArgs(t *testing.T) {
	runner := &fakeRunner{
		result: CommandResult{
			Stdout:   "run-target-output",
			ExitCode: 0,
		},
	}
	server := newTestServer(runner)
	session := newTestClientSession(t, server)

	result, err := session.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "bazel_run",
		Arguments: map[string]any{
			"target": "//cmd/server:server",
			"args":   []string{"--port", "8080"},
			"flags":  []string{"--config=release"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected one runner call, got %d", len(runner.calls))
	}

	gotArgs := strings.Join(runner.calls[0].Args, " ")
	wantArgs := "run --config=release //cmd/server:server -- --port 8080"
	if gotArgs != wantArgs {
		t.Fatalf("unexpected bazel args: got %q want %q", gotArgs, wantArgs)
	}

	if result.IsError {
		t.Fatal("expected successful run result")
	}
}

func TestBazelTestFailureMarksToolResultAsError(t *testing.T) {
	runner := &fakeRunner{
		result: CommandResult{
			Stdout:   "1 test failed",
			Stderr:   "ERROR: test target failed",
			ExitCode: 3,
		},
	}
	server := newTestServer(runner)
	session := newTestClientSession(t, server)

	result, err := session.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "bazel_test",
		Arguments: map[string]any{"targets": "//pkg:test"},
	})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}

	if !result.IsError {
		t.Fatal("expected failing test to set isError")
	}
	if !strings.Contains(joinToolContent(result), "ERROR: test target failed") {
		t.Fatalf("expected stderr in result text, got %q", joinToolContent(result))
	}
}

func TestUnknownArgumentsReturnInvalidParams(t *testing.T) {
	server := newTestServer(&fakeRunner{})
	session := newTestClientSession(t, server)

	_, err := session.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "bazel_query",
		Arguments: map[string]any{
			"expression": "//...",
			"unexpected": true,
		},
	})
	if err == nil {
		t.Fatal("expected invalid params error")
	}

	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected JSON-RPC error, got %T: %v", err, err)
	}
	if rpcErr.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("unexpected error code: got %d want %d", rpcErr.Code, jsonrpc.CodeInvalidParams)
	}
}

func newTestServer(runner Runner) *Server {
	return New(
		Config{
			WorkspaceRoot:  "/tmp/workspace",
			BazelBinary:    "bazel",
			DefaultTimeout: 30 * time.Second,
			MaxOutputBytes: 4096,
		},
		runner,
	)
}

func newTestClientSession(t *testing.T, server *Server) *sdkmcp.ClientSession {
	t.Helper()

	ctx := context.Background()
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect failed: %v", err)
	}
	t.Cleanup(func() {
		_ = serverSession.Close()
	})

	client := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "test-client", Version: "1.0.0"},
		&sdkmcp.ClientOptions{Capabilities: &sdkmcp.ClientCapabilities{}},
	)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
	})

	return session
}

func joinToolContent(result *sdkmcp.CallToolResult) string {
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		text, ok := content.(*sdkmcp.TextContent)
		if !ok {
			parts = append(parts, "")
			continue
		}
		parts = append(parts, text.Text)
	}
	return strings.Join(parts, "\n")
}
