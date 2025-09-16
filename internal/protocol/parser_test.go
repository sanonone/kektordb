// File: internal/protocol/parser_test.go
package protocol

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected *Command
		hasError bool
	}{
		{
			name:  "Comando Semplice PING",
			input: "PING\r\n",
			expected: &Command{
				Name: "PING",
				Args: make([][]byte, 0), // Testiamo con una slice vuota inizializzata
			},
			hasError: false,
		},
		{
			name:  "Comando SET con spazi",
			input: "SET chiave valore con spazi\r\n",
			expected: &Command{
				Name: "SET",
				// Il parser ora divide per ogni spazio
				Args: [][]byte{
					[]byte("chiave"),
					[]byte("valore"),
					[]byte("con"),
					[]byte("spazi"),
				},
			},
			hasError: false,
		},
		{
			name:  "Comando VADD senza metadati",
			input: "VADD my_index vec1 1.0 2.5 -3.0\r\n",
			expected: &Command{
				Name: "VADD",
				// Il parser ora divide tutto
				Args: [][]byte{
					[]byte("my_index"),
					[]byte("vec1"),
					[]byte("1.0"),
					[]byte("2.5"),
					[]byte("-3.0"),
				},
			},
			hasError: false,
		},
		{
			name:  "Comando VADD con metadati",
			input: "VADD my_index vec1 1.0 2.5 -3.0 {\"tag\":\"test\"}\r\n",
			expected: &Command{
				Name: "VADD",
				// Il parser ora divide tutto, incluso il JSON
				Args: [][]byte{
					[]byte("my_index"),
					[]byte("vec1"),
					[]byte("1.0"),
					[]byte("2.5"),
					[]byte("-3.0"),
					[]byte("{\"tag\":\"test\"}"),
				},
			},
			hasError: false,
		},
		{
			name:     "Comando vuoto",
			input:    "\r\n",
			expected: nil, // Ci aspettiamo un errore, quindi il comando è nil
			hasError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := Parse(tc.input)

			if (err != nil) != tc.hasError {
				t.Fatalf("Parse() error = %v, want hasError = %v", err, tc.hasError)
			}

			// Se ci aspettiamo un errore, non confrontiamo il comando
			if tc.hasError {
				return
			}

			if !reflect.DeepEqual(cmd, tc.expected) {
				t.Errorf("Parse() non corrisponde all'atteso.")
				t.Logf("GOT : %#v", cmd)
				t.Logf("WANT: %#v", tc.expected)
			}
		})
	}
}
