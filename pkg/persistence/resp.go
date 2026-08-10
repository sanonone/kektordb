// This file provides functions for parsing and formatting a subset of the RESP
// (Redis Serialization Protocol). It is used for handling client commands from
// the TCP interface and for serializing commands to the Append-Only File (AOF).
// The implementation is binary-safe.

package persistence

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Command represents a parsed command sent by a client.
type Command struct {
	// Name is the command name, e.g., "SET", "GET".
	Name string
	// Args contains the command arguments. It is a slice of byte slices to be
	// binary-safe, allowing any data (images, JSON, null bytes, etc.) to be
	// used as an argument.
	Args [][]byte
}

// ParseCommand reads a RESP-formatted command from a bufio.Reader.
// It requires a bufio.Reader because a single command can span multiple lines.
func ParseCommand(reader *bufio.Reader) (*Command, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}

	line = strings.TrimSpace(line)
	if len(line) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	if line[0] != '*' {
		return nil, fmt.Errorf("invalid command format, expected '*'")
	}

	numArgs, err := strconv.Atoi(line[1:])
	if err != nil || numArgs <= 0 {
		return nil, fmt.Errorf("invalid number of arguments")
	}

	args := make([][]byte, numArgs)
	for i := 0; i < numArgs; i++ {
		// Read the length of the bulk string.
		line, err = reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			return nil, fmt.Errorf("empty argument")
		}
		if line[0] != '$' {
			return nil, fmt.Errorf("invalid argument format, expected '$'")
		}

		lenArg, err := strconv.Atoi(line[1:])
		if err != nil || lenArg < 0 {
			return nil, fmt.Errorf("invalid argument length")
		}

		// Read the argument data.
		argData := make([]byte, lenArg)
		_, err = io.ReadFull(reader, argData)
		if err != nil {
			return nil, err
		}

		// Read the final two bytes: \r\n
		crlf := make([]byte, 2)
		_, err = io.ReadFull(reader, crlf)
		if err != nil {
			return nil, err
		}

		args[i] = argData
	}

	return &Command{
		Name: strings.ToUpper(string(args[0])),
		Args: args[1:],
	}, nil
}

// FormatCommand formats a command name and its arguments into a single
// RESP-formatted string. It correctly handles nil arguments by writing a RESP
// null bulk string.
//
// Fast path (B4): headers and lengths are written with strconv.Itoa into a
// pre-grown builder instead of fmt.Sprintf, avoiding per-argument formatting
// and intermediate []byte→string conversions on the AOF write path.
func FormatCommand(commandName string, args ...[]byte) string {
	// Estimate: headers + payloads (len args are tiny).
	totalArgs := 1 + len(args)
	size := len(commandName) + 16
	for _, arg := range args {
		size += len(arg) + 16
	}
	var b strings.Builder
	b.Grow(size)

	// Write the array header: number of elements.
	b.WriteByte('*')
	b.WriteString(strconv.Itoa(totalArgs))
	b.WriteString("\r\n")

	// Write the command name.
	b.WriteByte('$')
	b.WriteString(strconv.Itoa(len(commandName)))
	b.WriteString("\r\n")
	b.WriteString(commandName)
	b.WriteString("\r\n")

	// Write each argument.
	for _, arg := range args {
		if arg == nil {
			b.WriteString("$-1\r\n") // RESP representation for nil
		} else {
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(len(arg)))
			b.WriteString("\r\n")
			b.Write(arg)
			b.WriteString("\r\n")
		}
	}

	return b.String()
}
