package persistence

import (
	"bufio"
	"strings"
	"testing"
)

// TestParseCommand_RejectsExcessiveArgs verifies that a command declaring more
// arguments than MaxArgsPerCommand is rejected before any allocation happens.
// Regression for P0-1: a corrupted or hostile AOF frame of empty arguments
// could otherwise force a multi-GB make([][]byte, numArgs) during replay.
func TestParseCommand_RejectsExcessiveArgs(t *testing.T) {
	input := "*2147483647\r\n$1\r\nx\r\n"
	_, err := ParseCommand(bufio.NewReader(strings.NewReader(input)))
	if err == nil {
		t.Fatal("expected error for excessive numArgs, got nil")
	}
	if !strings.Contains(err.Error(), "too many arguments") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestParseCommand_RejectsOversizedArgLength verifies that an argument length
// beyond MaxPayloadSize is rejected before the argument buffer is allocated.
// Regression for P0-1: a corrupted length field would previously trigger a
// 1GB+ allocation that then failed on io.ReadFull.
func TestParseCommand_RejectsOversizedArgLength(t *testing.T) {
	input := "*1\r\n$2147483647\r\nx\r\n"
	_, err := ParseCommand(bufio.NewReader(strings.NewReader(input)))
	if err == nil {
		t.Fatal("expected error for oversized lenArg, got nil")
	}
	if !strings.Contains(err.Error(), "argument length") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestParseCommand_ValidCommandStillParses guards against over-restricting the
// parser: a well-formed command must still round-trip after adding the limits.
func TestParseCommand_ValidCommandStillParses(t *testing.T) {
	cmd, err := ParseCommand(bufio.NewReader(strings.NewReader("*2\r\n$3\r\nSET\r\n$5\r\nhello\r\n")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Name != "SET" {
		t.Fatalf("unexpected command name: %q", cmd.Name)
	}
	if len(cmd.Args) != 1 || string(cmd.Args[0]) != "hello" {
		t.Fatalf("unexpected args: %v", cmd.Args)
	}
}
