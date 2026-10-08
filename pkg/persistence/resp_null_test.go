package persistence

// Regressione C3: FormatCommand emette "$\r\n-1" (RESP null bulk string) per
// gli argomenti nil, ma ParseCommand lo rifiutava con "invalid argument length".
//
// Conseguenza: VADD senza metadata scriveva un frame con CRC VALIDO che il
// parser non riusciva a leggere. Al recovery il resync scartava tutto fino al
// frame successivo interpretabile — su un AOF reale ha bruciato 111391 byte su
// 115013, perdendo i dati. Riprodotto identico su origin/main (addc5f0).

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// TestFormatThenParseNilArgument: un comando con argomento nil deve fare
// round-trip writer -> parser.
func TestFormatThenParseNilArgument(t *testing.T) {
	// Questo è esattamente ciò che fa VAdd quando metadata è nil.
	var metaBytes []byte // nil: nessun metadata
	cmd := FormatCommand("VADD", []byte("idx"), []byte("id"), []byte("vec"), metaBytes)

	if !strings.Contains(cmd, "$-1\r\n") {
		t.Fatalf("atteso un null bulk string nel comando formattato: %q", cmd)
	}

	parsed, err := ParseCommand(bufio.NewReader(bytes.NewReader([]byte(cmd))))
	if err != nil {
		t.Fatalf("ParseCommand ha rifiutato un comando scritto da FormatCommand: %v\ncomando: %q", err, cmd)
	}
	if parsed.Name != "VADD" {
		t.Errorf("name = %q, want VADD", parsed.Name)
	}
	// Args esclude il nome del comando: index, id, vector, metadata = 4.
	if len(parsed.Args) != 4 {
		t.Fatalf("args = %d, want 4", len(parsed.Args))
	}
	// L'ultimo è il metadata null: deve risultare vuoto, non assente.
	if len(parsed.Args[3]) != 0 {
		t.Errorf("l'argomento null deve risultare vuoto, ottenuto %q", parsed.Args[3])
	}
}

// TestRoundTripAllShapes: il round-trip deve valere per ogni forma di argomento.
func TestRoundTripAllShapes(t *testing.T) {
	cases := []struct {
		name string
		args [][]byte
	}{
		{"tutti valorizzati", [][]byte{[]byte("a"), []byte("b")}},
		{"nil finale (VADD senza metadata)", [][]byte{[]byte("a"), nil}},
		{"nil intermedio", [][]byte{[]byte("a"), nil, []byte("c")}},
		{"stringa vuota non-nil", [][]byte{[]byte("a"), {}}},
		{"solo nil", [][]byte{nil}},
		{"payload lungo", [][]byte{bytes.Repeat([]byte("x"), 5000)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			formatted := FormatCommand("CMD", tc.args...)
			parsed, err := ParseCommand(bufio.NewReader(bytes.NewReader([]byte(formatted))))
			if err != nil {
				t.Fatalf("ParseCommand: %v", err)
			}
			if len(parsed.Args) != len(tc.args) {
				t.Fatalf("args = %d, want %d", len(parsed.Args), len(tc.args))
			}
			for i := range tc.args {
				if len(parsed.Args[i]) != len(tc.args[i]) {
					t.Errorf("arg[%d] len = %d, want %d", i, len(parsed.Args[i]), len(tc.args[i]))
				}
			}
		})
	}
}

// TestParseRejectsTrulyInvalidLength: la convalida non deve diventare permissiva.
// Solo -1 (null) è ammesso: -2 e valori < -1 restano errori.
func TestParseRejectsTrulyInvalidLength(t *testing.T) {
	for _, bad := range []string{"*-2\r\n", "*-1\r\n", "*0\r\n"} {
		if _, err := ParseCommand(bufio.NewReader(bytes.NewReader([]byte(bad)))); err == nil {
			t.Errorf("%q doveva essere rifiutato", bad)
		}
	}
	// Un argomento con lunghezza -2 è invalido.
	bad := "*2\r\n$3\r\nCMD\r\n$-2\r\n"
	if _, err := ParseCommand(bufio.NewReader(bytes.NewReader([]byte(bad)))); err == nil {
		t.Error("una lunghezza di argomento -2 doveva essere rifiutata")
	}
}

// TestFrameWithNilArgumentSurvivesRecoveryPath: il caso reale — un frame scritto
// con argomento null deve essere leggibile dal percorso di recovery
// (ReadFrame + ParseCommand), che è dove si perdeva il dato.
func TestFrameWithNilArgumentSurvivesRecoveryPath(t *testing.T) {
	var buf bytes.Buffer
	fw := NewFrameWriter(&buf)

	var metaBytes []byte // nil, come in VAdd senza metadata
	payload := FormatCommand("VADD", []byte("idx"), []byte("vec-id"), []byte("0f3f"), metaBytes)
	if err := fw.WriteFrame([]byte(payload)); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	// Replica del percorso di recovery.
	reader := bufio.NewReader(bytes.NewReader(buf.Bytes()))
	pl, _, err := ReadFrame(reader)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	cmd, err := ParseCommand(bufio.NewReader(bytes.NewReader(pl)))
	if err != nil {
		t.Fatalf("ParseCommand nel percorso di recovery: %v (questo è il bug C3: il frame è integro ma il parser lo rifiuta)", err)
	}
	if cmd.Name != "VADD" || len(cmd.Args) != 4 {
		t.Errorf("comando decodificato male: name=%q args=%d", cmd.Name, len(cmd.Args))
	}
}
