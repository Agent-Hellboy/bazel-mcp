//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServerEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("bazel"); err != nil {
		t.Skipf("bazel is required for the e2e test: %v", err)
	}

	repoRoot := repositoryRoot(t)
	tempRoot := t.TempDir()
	workspaceRoot := filepath.Join(tempRoot, "workspace")
	copyDir(t, filepath.Join(repoRoot, "e2e", "testdata", "workspace"), workspaceRoot)

	binaryPath := filepath.Join(tempRoot, "bazel-mcp")
	buildServerBinary(t, repoRoot, binaryPath, tempRoot)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	outputUserRoot := filepath.Join(tempRoot, "bazel-root")
	command := exec.Command(
		binaryPath,
		"--workspace", workspaceRoot,
		"--startup-flag=--batch",
		"--startup-flag=--output_user_root="+outputUserRoot,
		"--timeout-seconds=120",
	)

	var stderr bytes.Buffer
	command.Stderr = &stderr

	client := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "bazel-mcp-e2e", Version: "1.0.0"},
		&sdkmcp.ClientOptions{Capabilities: &sdkmcp.ClientCapabilities{}},
	)
	session, err := client.Connect(ctx, &sdkmcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("failed to connect to server: %v\nstderr:\n%s", err, stderr.String())
	}
	defer session.Close()

	initializeResult := session.InitializeResult()
	if initializeResult == nil {
		t.Fatal("expected initialize result")
	}
	if initializeResult.ServerInfo == nil {
		t.Fatal("expected server info")
	}
	if initializeResult.ServerInfo.Name != "bazel-mcp" {
		t.Fatalf("unexpected server name: got %q want %q", initializeResult.ServerInfo.Name, "bazel-mcp")
	}

	toolsListResult, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v\nstderr:\n%s", err, stderr.String())
	}

	var toolNames []string
	for _, tool := range toolsListResult.Tools {
		toolNames = append(toolNames, tool.Name)
	}

	for _, required := range []string{
		"bazel_info",
		"bazel_query",
		"bazel_cquery",
		"bazel_aquery",
		"bazel_build",
		"bazel_test",
	} {
		if !slices.Contains(toolNames, required) {
			t.Fatalf("tools/list missing %q: %v", required, toolNames)
		}
	}

	infoResult, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "bazel_info",
		Arguments: map[string]any{
			"key": "workspace",
		},
	})
	if err != nil {
		t.Fatalf("bazel_info failed: %v\nstderr:\n%s", err, stderr.String())
	}
	assertToolResult(t, infoResult, workspaceRoot, "Exit code: 0", false)

	buildResult, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "bazel_build",
		Arguments: map[string]any{
			"targets": []string{"//:hello"},
		},
	})
	if err != nil {
		t.Fatalf("bazel_build failed: %v\nstderr:\n%s", err, stderr.String())
	}
	assertToolResult(t, buildResult, "Command: bazel", "Exit code: 0", false)

	testResult, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "bazel_test",
		Arguments: map[string]any{
			"targets": []string{"//:pass_test"},
		},
	})
	if err != nil {
		t.Fatalf("bazel_test failed: %v\nstderr:\n%s", err, stderr.String())
	}
	assertToolResult(t, testResult, "PASSED", "Exit code: 0", false)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine current file location")
	}

	return filepath.Dir(filepath.Dir(file))
}

func buildServerBinary(t *testing.T, repoRoot string, binaryPath string, tempRoot string) {
	t.Helper()

	cacheRoot := filepath.Join(tempRoot, "go-cache")
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatalf("failed to create Go cache directory: %v", err)
	}

	modCache := os.Getenv("GOMODCACHE")
	if modCache == "" {
		modCache = filepath.Join(cacheRoot, "mod")
	}

	goPath := os.Getenv("GOPATH")
	if goPath == "" {
		goPath = filepath.Join(cacheRoot, "gopath")
	}

	command := exec.Command("go", "build", "-o", binaryPath, "./cmd/bazel-mcp")
	command.Dir = repoRoot
	command.Env = append(
		os.Environ(),
		"GOCACHE="+filepath.Join(cacheRoot, "build"),
		"GOMODCACHE="+modCache,
		"GOPATH="+goPath,
	)

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build server binary: %v\n%s", err, output)
	}
}

func copyDir(t *testing.T, source string, destination string) {
	t.Helper()

	if err := filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relativePath, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(destination, relativePath)
		if d.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		return os.WriteFile(targetPath, data, info.Mode().Perm())
	}); err != nil {
		t.Fatalf("failed to copy %s to %s: %v", source, destination, err)
	}
}

func assertToolResult(t *testing.T, result *sdkmcp.CallToolResult, expectedSubstring string, expectedExitCode string, expectError bool) {
	t.Helper()

	if result.IsError != expectError {
		t.Fatalf("unexpected tool error state: got %v want %v\ncontent:\n%s", result.IsError, expectError, joinToolContent(result))
	}

	content := joinToolContent(result)
	if !strings.Contains(content, expectedSubstring) {
		t.Fatalf("tool output missing %q\nfull output:\n%s", expectedSubstring, content)
	}
	if !strings.Contains(content, expectedExitCode) {
		t.Fatalf("tool output missing %q\nfull output:\n%s", expectedExitCode, content)
	}
}

func joinToolContent(result *sdkmcp.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		text, ok := content.(*sdkmcp.TextContent)
		if !ok {
			continue
		}
		parts = append(parts, text.Text)
	}
	return strings.Join(parts, "\n")
}
