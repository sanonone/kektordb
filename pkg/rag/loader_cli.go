package rag

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

const defaultCLITimeout = 2 * time.Minute

// CLILoader implements the Loader interface by delegating text extraction to an
// external CLI tool. The tool is executed as a child process with a configurable
// timeout to prevent hangs on corrupted or oversized files.
//
// The command template uses list-based arguments for safety (no shell injection):
//
//	[]string{"liteparse", "-q", "{{file_path}}", "--format", "text"}
//
// The placeholder {{file_path}} is replaced with the actual file path before execution.
type CLILoader struct {
	commandTemplate []string
	timeout         time.Duration
}

// NewCLILoader creates a new CLI-based loader.
// command is the argument list template (e.g. ["tool", "{{file_path}}"]).
// timeout is the maximum execution time; if <= 0, a 2-minute default is used.
func NewCLILoader(command []string, timeout time.Duration) *CLILoader {
	if timeout <= 0 {
		timeout = defaultCLITimeout
	}
	return &CLILoader{
		commandTemplate: command,
		timeout:         timeout,
	}
}

// Load implements the Loader interface.
func (l *CLILoader) Load(path string) (*Document, error) {
	if len(l.commandTemplate) == 0 {
		return nil, fmt.Errorf("cli loader: empty command template")
	}

	// Build arguments by replacing {{file_path}} placeholders
	args := make([]string, len(l.commandTemplate))
	for i, arg := range l.commandTemplate {
		args[i] = strings.ReplaceAll(arg, "{{file_path}}", path)
	}

	ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("cli parser timed out after %s: %s", l.timeout, args[0])
		}
		return nil, fmt.Errorf("cli parser failed (%v): %s", err, strings.TrimSpace(stderr.String()))
	}

	// Log stderr as warning (non-fatal)
	if stderr.Len() > 0 {
		slog.Warn("[CLI Loader] stderr output", "command", args[0], "stderr", strings.TrimSpace(stderr.String()))
	}

	text := strings.TrimSpace(stdout.String())
	if text == "" {
		return nil, fmt.Errorf("cli parser returned empty output")
	}

	return &Document{Text: text}, nil
}
