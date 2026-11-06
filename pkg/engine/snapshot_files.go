package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// copyFile copies src to dst, syncing dst before returning.
//
// Used to keep the previous snapshot around before a rename replaces it. The
// copy is written to a temporary name first so a crash mid-copy cannot leave a
// half-written .bak that looks usable.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("copy %s: %w", src, err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	return nil
}

// syncDir flushes a directory entry so a rename inside it survives a power loss.
//
// Opening a directory is not portable to Windows, where this is a no-op: the
// rename there is already journaled by the filesystem.
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		// Some filesystems reject fsync on a directory handle.
		if isDirSyncUnsupported(err) {
			return nil
		}
		return err
	}
	return nil
}

// isDirSyncUnsupported reports whether err means "this platform cannot fsync a
// directory", as opposed to a real I/O failure.
func isDirSyncUnsupported(err error) bool {
	if pe, ok := err.(*os.PathError); ok {
		err = pe.Err
	}
	msg := err.Error()
	for _, frag := range []string{"invalid argument", "not supported", "operation not permitted"} {
		if containsFold(msg, frag) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if 'A' <= a && a <= 'Z' {
				a += 'a' - 'A'
			}
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// backupPathFor returns the fallback path kept next to a snapshot.
func backupPathFor(snapPath string) string {
	return filepath.Clean(snapPath) + ".bak"
}
