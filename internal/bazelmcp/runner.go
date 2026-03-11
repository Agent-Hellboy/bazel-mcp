package bazelmcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner executes shell commands and returns captured output.
type Runner interface {
	Run(ctx context.Context, request CommandRequest) (CommandResult, error)
}

// CommandRequest specifies a command to run: path, args, working directory, and output limit.
type CommandRequest struct {
	Path           string   // Executable path
	Args           []string // Command-line arguments
	Dir            string   // Working directory
	MaxOutputBytes int     // Max bytes to capture from stdout/stderr
}

// CommandResult holds stdout, stderr, exit code, duration, and truncation flags.
type CommandResult struct {
	Command   []string        // Full command that was run
	Stdout    string          // Captured stdout
	Stderr    string          // Captured stderr
	ExitCode  int             // Process exit code
	Duration  time.Duration   // Elapsed time
	Truncated bool            // True if output was truncated
	TimedOut  bool            // True if command exceeded timeout
}

// RealRunner runs commands via os/exec and captures output.
type RealRunner struct{}

// Run executes the command and returns captured stdout, stderr, and exit code.
func (RealRunner) Run(ctx context.Context, request CommandRequest) (CommandResult, error) {
	command := exec.CommandContext(ctx, request.Path, request.Args...)
	command.Dir = request.Dir

	var stdout limitedBuffer
	stdout.Limit = request.MaxOutputBytes

	var stderr limitedBuffer
	stderr.Limit = request.MaxOutputBytes

	command.Stdout = &stdout
	command.Stderr = &stderr

	started := time.Now()
	err := command.Run()
	duration := time.Since(started)

	result := CommandResult{
		Command:   append([]string{request.Path}, request.Args...),
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		ExitCode:  0,
		Duration:  duration,
		Truncated: stdout.Truncated || stderr.Truncated,
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		result.ExitCode = -1
		result.TimedOut = true
		result.Stderr = appendLine(result.Stderr, fmt.Sprintf("command timed out after %s", duration.Round(time.Millisecond)))
		return result, nil
	}

	if err == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}

	return result, err
}

type limitedBuffer struct {
	Limit     int
	buffer    bytes.Buffer
	Truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	originalLength := len(p)

	if b.Limit <= 0 {
		return originalLength, nil
	}

	remaining := b.Limit - b.buffer.Len()
	if remaining <= 0 {
		b.Truncated = true
		return originalLength, nil
	}

	if len(p) > remaining {
		p = p[:remaining]
		b.Truncated = true
	}

	_, err := b.buffer.Write(p)
	return originalLength, err
}

func (b *limitedBuffer) String() string {
	return b.buffer.String()
}

func appendLine(current string, line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return current
	}

	current = strings.TrimRight(current, "\n")
	if current == "" {
		return line
	}

	return current + "\n" + line
}
