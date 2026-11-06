package core

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// Snapshot file framing.
//
// The file used to be a bare gob stream: no way to tell a truncated file from a
// complete one, no way to detect a flipped bit that still decodes, and no way to
// evolve the format without breaking every existing database.
//
// The stream is now:
//
//	magic  (8 bytes)  \xff K D B \x00 S N A P
//	version(uint32)   format version, currently 1
//	length (uint64)   payload size in bytes
//	crc    (uint32)   CRC-32 (IEEE) of the payload
//	payload           the gob stream, unchanged
//
// The leading 0xFF cannot start a gob stream: gob opens with a varint-encoded
// type identifier, and every byte with the high bit set is a continuation byte,
// so a valid legacy file never begins with 0xFF. That is what makes the format
// self-describing: a reader can tell "framed" from "legacy gob" by looking at
// the first byte alone.
//
// Files written before this change carry no header and are still readable: the
// reader falls back to decoding the stream as gob, which is what it has always
// been. Nothing is written back, so an old file stays an old file until the next
// snapshot replaces it.
const (
	snapshotMagic      = "\xffKDB\x00SNAP"
	snapshotVersion    = 1
	snapshotHeaderSize = len(snapshotMagic) + 4 + 8 + 4 // 24 bytes
)

// maxSnapshotPayload bounds the declared length a header may claim.
//
// A corrupt length field would otherwise make the reader allocate arbitrary
// memory before noticing the file is nonsense.
const maxSnapshotPayload = 1 << 40 // 1 TiB

// snapshotWriteEncode writes a framed snapshot: header, then the gob stream.
//
// The CRC covers the payload only, not the header: a corrupted length field must
// not be able to hide behind a CRC computed over itself.
func snapshotWriteEncode(w io.Writer, s *Snapshot) error {
	var payload bytes.Buffer
	if err := gob.NewEncoder(&payload).Encode(s); err != nil {
		return fmt.Errorf("failed to encode snapshot: %w", err)
	}

	header := make([]byte, snapshotHeaderSize)
	copy(header, snapshotMagic)
	binary.BigEndian.PutUint32(header[len(snapshotMagic):], snapshotVersion)
	binary.BigEndian.PutUint64(header[len(snapshotMagic)+4:], uint64(payload.Len()))
	binary.BigEndian.PutUint32(header[len(snapshotMagic)+12:], crc32.ChecksumIEEE(payload.Bytes()))

	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("failed to write snapshot header: %w", err)
	}
	if _, err := w.Write(payload.Bytes()); err != nil {
		return fmt.Errorf("failed to write snapshot payload: %w", err)
	}
	return nil
}

// snapshotReadDecode reads a snapshot from r, accepting both the framed format
// and the bare gob stream written before framing existed.
//
// The reader works on a bufio.Reader rather than seeking: callers pass
// bytes.Buffer, which is not an io.Seeker, and requiring one would break them.
func snapshotReadDecode(r io.Reader) (*Snapshot, error) {
	br := bufio.NewReaderSize(r, 1<<16)

	peeked, err := br.Peek(len(snapshotMagic))
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, fmt.Errorf("failed to read snapshot header: %w", err)
	}

	// Legacy: no magic, the whole stream is gob.
	if !bytes.HasPrefix(peeked, []byte(snapshotMagic)) {
		return decodeLegacySnapshot(br)
	}

	header := make([]byte, snapshotHeaderSize)
	if _, err := io.ReadFull(br, header); err != nil {
		return nil, fmt.Errorf("failed to read snapshot header: %w", err)
	}

	version := binary.BigEndian.Uint32(header[len(snapshotMagic):])
	if version != snapshotVersion {
		return nil, fmt.Errorf(
			"snapshot format version %d is not supported by this build (expects %d); "+
				"upgrade KektorDB or restore from a backup", version, snapshotVersion)
	}

	length := binary.BigEndian.Uint64(header[len(snapshotMagic)+4:])
	want := binary.BigEndian.Uint32(header[len(snapshotMagic)+12:])

	if length > maxSnapshotPayload {
		return nil, fmt.Errorf("snapshot declares an implausible payload of %d bytes", length)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(br, payload); err != nil {
		return nil, fmt.Errorf(
			"snapshot is truncated: header declares %d payload bytes, file ends early: %w", length, err)
	}

	if got := crc32.ChecksumIEEE(payload); got != want {
		return nil, fmt.Errorf(
			"snapshot checksum mismatch: header %08x, payload %08x — the file is corrupt", want, got)
	}

	var snapshot Snapshot
	if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("failed to decode snapshot: %w", err)
	}
	return &snapshot, nil
}

// decodeLegacySnapshot reads the pre-framing format: a bare gob stream.
func decodeLegacySnapshot(r io.Reader) (*Snapshot, error) {
	var snapshot Snapshot
	if err := gob.NewDecoder(r).Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("failed to decode snapshot: %w", err)
	}
	return &snapshot, nil
}
