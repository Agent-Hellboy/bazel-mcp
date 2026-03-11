package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/proshan/bazel-mcp/internal/bazelmcp"
)

type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to determine current directory: %v\n", err)
		os.Exit(1)
	}

	defaultWorkspace := envOrDefault("BAZEL_MCP_WORKSPACE", cwd)
	defaultBazel := envOrDefault("BAZEL_MCP_BAZEL_BIN", "bazel")
	defaultTimeout := envInt("BAZEL_MCP_TIMEOUT_SECONDS", 300)
	defaultMaxOutput := envInt("BAZEL_MCP_MAX_OUTPUT_BYTES", 1<<20)

	var startupFlags stringSlice
	var commonFlags stringSlice

	workspace := flag.String("workspace", defaultWorkspace, "Bazel workspace root used for all commands.")
	bazelBinary := flag.String("bazel", defaultBazel, "Path to the bazel or bazelisk executable.")
	timeoutSeconds := flag.Int("timeout-seconds", defaultTimeout, "Default timeout in seconds for Bazel commands.")
	maxOutputBytes := flag.Int("max-output-bytes", defaultMaxOutput, "Maximum stdout or stderr bytes captured per command.")
	flag.Var(&startupFlags, "startup-flag", "Bazel startup flag to prepend before the command. Repeatable.")
	flag.Var(&commonFlags, "flag", "Bazel command flag appended after the command name. Repeatable.")
	flag.Parse()

	if *timeoutSeconds <= 0 {
		fmt.Fprintln(os.Stderr, "timeout-seconds must be greater than zero")
		os.Exit(1)
	}

	if *maxOutputBytes <= 0 {
		fmt.Fprintln(os.Stderr, "max-output-bytes must be greater than zero")
		os.Exit(1)
	}

	workspaceRoot, err := filepath.Abs(*workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to resolve workspace path %q: %v\n", *workspace, err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	server := bazelmcp.New(
		bazelmcp.Config{
			WorkspaceRoot:  workspaceRoot,
			BazelBinary:    *bazelBinary,
			StartupFlags:   []string(startupFlags),
			CommonFlags:    []string(commonFlags),
			DefaultTimeout: time.Duration(*timeoutSeconds) * time.Second,
			MaxOutputBytes: *maxOutputBytes,
			Logger:         logger,
		},
		bazelmcp.RealRunner{},
	)

	if err := server.ServeStdio(context.Background()); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

func envOrDefault(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}
