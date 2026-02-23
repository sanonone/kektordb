//go:build unix || darwin || linux
// +build unix darwin linux

package mmap

import (
	"golang.org/x/sys/unix"
)

// mmapFile maps a file descriptor into memory.
// It uses PROT_READ and PROT_WRITE to allow both reading and writing.
// MAP_SHARED ensures that changes are carried through to the underlying file.
func mmapFile(fd uintptr, size int) ([]byte, error) {
	// unix.Mmap translates directly to the POSIX mmap syscall.
	return unix.Mmap(int(fd), 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
}

// munmapFile unmaps the memory region, freeing the virtual memory space.
func munmapFile(data []byte) error {
	return unix.Munmap(data)
}
