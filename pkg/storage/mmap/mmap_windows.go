//go:build windows
// +build windows

package mmap

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// mmapFile on Windows is a two-step process: CreateFileMapping followed by MapViewOfFile.
func mmapFile(fd uintptr, size int) ([]byte, error) {
	// 1. Create a mapping object backed by the file descriptor
	// PAGE_READWRITE allows read and write access to the mapped pages.
	hMap, err := windows.CreateFileMapping(
		windows.Handle(fd),
		nil,
		windows.PAGE_READWRITE,
		uint32(int64(size)>>32),
		uint32(int64(size)&0xFFFFFFFF),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("CreateFileMapping failed: %w", err)
	}
	// We can close the mapping handle safely; the map view will keep it alive.
	defer windows.CloseHandle(hMap)

	// 2. Map the view into the process's address space
	addr, err := windows.MapViewOfFile(hMap, windows.FILE_MAP_WRITE, 0, 0, uintptr(size))
	if err != nil {
		return nil, fmt.Errorf("MapViewOfFile failed: %w", err)
	}

	// 3. Convert the raw memory address into a Go byte slice (Zero-Copy)
	data := unsafe.Slice((*byte)(unsafe.Pointer(addr)), size)

	return data, nil
}

// munmapFile releases the mapped view.
func munmapFile(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	// UnmapViewOfFile requires the pointer to the start of the memory region
	return windows.UnmapViewOfFile(uintptr(unsafe.Pointer(&data[0])))
}
