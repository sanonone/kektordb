//go:build rust

// Package embeddings provides built-in ONNX embedding via Rust/candle CGO.
//
// This file is compiled when the 'rust' build tag is set.
// It wraps the Rust library functions (kektordb_embed_*) via CGO.
//
// Critical dependency: requires a pre-compiled Rust static library
// (libkektordb_compute.a) built with candle-core, candle-onnx, and tokenizers.
package embeddings

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/compute/include
#cgo LDFLAGS: -lkektordb_compute -lstdc++
#include "kektordb_compute.h"
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"sync"
	"unsafe"
)

type localEmbedder struct {
	modelPath     string
	tokenizerPath string

	mu      sync.Mutex // serializes kektordb_embed_init + sequential inference
	initErr error
}

// NewLocalEmbedder creates a local ONNX embedder.
// modelPath and tokenizerPath are files on disk (downloaded via ensureModel).
func NewLocalEmbedder(modelPath, tokenizerPath string) (Embedder, error) {
	return &localEmbedder{
		modelPath:     modelPath,
		tokenizerPath: tokenizerPath,
	}, nil
}

func (e *localEmbedder) Embed(text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Lazy init on first call (model may not be downloaded until now).
	if e.initErr != nil {
		return nil, e.initErr
	}
	if e.modelPath == "" || e.tokenizerPath == "" {
		return nil, fmt.Errorf("local embedder: model or tokenizer path not set")
	}

	// Initialize the Rust model (no-op if already initialized).
	cModelPath := C.CString(e.modelPath)
	defer C.free(unsafe.Pointer(cModelPath))
	cTokenizerPath := C.CString(e.tokenizerPath)
	defer C.free(unsafe.Pointer(cTokenizerPath))

	ret := C.kektordb_embed_init(cModelPath, cTokenizerPath)
	if ret != 0 {
		e.initErr = fmt.Errorf("kektordb_embed_init failed")
		return nil, e.initErr
	}

	// Run inference.
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))

	var cVec *C.float
	var cDim C.int

	ret = C.kektordb_embed(cText, &cVec, &cDim)
	if ret != 0 {
		return nil, fmt.Errorf("kektordb_embed failed")
	}
	defer C.kektordb_free_embedding(cVec, cDim)

	// Copy C heap slice to Go slice.
	dim := int(cDim)
	result := make([]float32, dim)
	// Compute size in bytes and copy via unsafe slice.
	if dim > 0 {
		src := unsafe.Slice((*float32)(unsafe.Pointer(cVec)), dim)
		copy(result, src)
	}
	return result, nil
}
