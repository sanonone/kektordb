package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
)

// VerifyContent checks the file digests against the manifest.
//
// Counts alone catch a truncated archive; a digest also catches a corrupted
// line that still parses. Both run before the import touches the target,
// because an import that fails halfway leaves a store that is neither old nor
// new.
func VerifyContent(dir string) (Manifest, error) {
	m, err := ReadManifest(dir)
	if err != nil {
		return m, err
	}

	memSum, err := digestFile(dir, memoriesFile)
	if err != nil {
		return m, err
	}
	if m.Checksum.Memories != "" && memSum != m.Checksum.Memories {
		return m, fmt.Errorf("%s does not match its checksum: the archive is corrupt",
			memoriesFile)
	}

	graphSum, err := digestFile(dir, graphFile)
	if err != nil {
		return m, err
	}
	if m.Checksum.Graph != "" && graphSum != m.Checksum.Graph {
		return m, fmt.Errorf("%s does not match its checksum: the archive is corrupt",
			graphFile)
	}

	return m, nil
}

// digestFile returns the "sha256:…" digest of a file in the archive.
func digestFile(dir, name string) (string, error) {
	f, err := os.Open(joinPath(dir, name))
	if err != nil {
		return "", err
	}
	defer f.Close()

	var h hash.Hash = sha256.New()
	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
