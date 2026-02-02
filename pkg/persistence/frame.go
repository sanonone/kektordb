package persistence

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

// Constants for the AOF binary protocol.
const (
	// MagicByte is the marker used to identify the start of a valid frame.
	// It helps in scanning for recovery if the file is heavily corrupted.
	MagicByte = 0xA5

	// HeaderSize is the fixed size of the frame metadata:
	// 1 byte (Magic) + 1 byte (OpCode) + 4 bytes (Length) + 4 bytes (CRC32) = 10 bytes.
	HeaderSize = 10

	// OpCodeCommand represents a standard database command (e.g., SET, VADD).
	OpCodeCommand = 0x01
)

var (
	// ErrInvalidMagic indicates the file stream lost synchronization or is not a valid AOF.
	ErrInvalidMagic = errors.New("invalid magic byte")
	// ErrChecksumMismatch indicates data corruption within the frame payload.
	ErrChecksumMismatch = errors.New("crc32 checksum mismatch")
	// ErrIncompleteFrame indicates the file ended abruptly (e.g., power loss during write).
	ErrIncompleteFrame = errors.New("incomplete frame")
)

// FrameWriter handles the safe writing of binary frames to an io.Writer.
type FrameWriter struct {
	w io.Writer
}

// NewFrameWriter creates a writer that wraps an underlying io.Writer.
func NewFrameWriter(w io.Writer) *FrameWriter {
	return &FrameWriter{w: w}
}

// WriteFrame encodes the payload into a binary frame and writes it.
// Frame Format: [Magic(1)][OpCode(1)][Length(4)][CRC(4)][Payload(N)]
func (fw *FrameWriter) WriteFrame(payload []byte) error {
	// 1. Prepare Header Buffer
	header := make([]byte, HeaderSize)

	// Magic Byte
	header[0] = MagicByte
	// OpCode (Default to Command for now)
	header[1] = OpCodeCommand

	// Payload Length (uint32 Little Endian)
	length := uint32(len(payload))
	binary.LittleEndian.PutUint32(header[2:6], length)

	// Checksum (IEEE 802.3)
	checksum := crc32.ChecksumIEEE(payload)
	binary.LittleEndian.PutUint32(header[6:10], checksum)

	// 2. Write Header
	// We write header and payload sequentially.
	// Note: In a production OS file write, it's better if 'fw.w' is a bufio.Writer
	// so these two writes become a single syscall, ensuring atomicity.
	if _, err := fw.w.Write(header); err != nil {
		return err
	}

	// 3. Write Payload
	if _, err := fw.w.Write(payload); err != nil {
		return err
	}

	return nil
}

// ReadFrame reads the next frame from the reader.
// It performs validation of the Magic Byte and the CRC32 Checksum.
// Returns the payload, the total bytes read (header + payload), and an error.
func ReadFrame(r io.Reader) ([]byte, int, error) {
	header := make([]byte, HeaderSize)

	// 1. Read Header
	// ReadFull ensures we get exactly HeaderSize bytes or an error.
	if _, err := io.ReadFull(r, header); err != nil {
		// If we are at EOF exactly at the start of a frame, it's a clean exit.
		if err == io.EOF {
			return nil, 0, io.EOF
		}
		// If we read partial bytes (e.g. 5 bytes then EOF), it's a corruption (ErrUnexpectedEOF).
		return nil, 0, ErrIncompleteFrame
	}

	// 2. Validate Magic Byte
	if header[0] != MagicByte {
		return nil, HeaderSize, ErrInvalidMagic
	}

	// (Optional) We can check header[1] for OpCode if we add more types later.

	// 3. Parse Length and Expected CRC
	length := binary.LittleEndian.Uint32(header[2:6])
	expectedCRC := binary.LittleEndian.Uint32(header[6:10])

	// 4. Read Payload
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		// Even if it's EOF here, it's an error because we expected 'length' bytes.
		return nil, HeaderSize, ErrIncompleteFrame
	}

	// 5. Verify Checksum
	actualCRC := crc32.ChecksumIEEE(payload)
	if actualCRC != expectedCRC {
		return nil, HeaderSize + int(length), ErrChecksumMismatch
	}

	return payload, HeaderSize + int(length), nil
}
