// Package server implements the main KektorDB server logic.
//
// This file provides functions for parsing and formatting a subset of the RESP
// (Redis Serialization Protocol). It is used for handling client commands from
// the TCP interface and for serializing commands to the Append-Only File (AOF).
// The implementation is binary-safe.

package server

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

// ParseRESP reads a RESP-formatted command from a bufio.Reader.
// It requires a bufio.Reader because a single command can span multiple lines.
func ParseRESP(reader *bufio.Reader) (*Command, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}

	line = strings.TrimSpace(line)
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

// formatCommandAsRESP formats a command name and its arguments into a single
// RESP-formatted string. It correctly handles nil arguments by writing a RESP
// null bulk string.
func formatCommandAsRESP(commandName string, args ...[]byte) string {
	var b strings.Builder

	// Write the array header: number of elements.
	totalArgs := 1 + len(args)
	b.WriteString(fmt.Sprintf("*%d\r\n", totalArgs))

	// Write the command name.
	b.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(commandName), commandName))

	// Write each argument.
	for _, arg := range args {
		if arg == nil {
			b.WriteString("$-1\r\n") // RESP representation for nil
		} else {
			b.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(arg), string(arg)))
		}
	}

	return b.String()
}
