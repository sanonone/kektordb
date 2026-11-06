package engine

import "fmt"

// aofPersist writes one command to the AOF and reports whether it can be
// persisted at all.
//
// LazyAOFWriter.Write is fire-and-forget: it enqueues the command and returns
// nil, and the actual write happens in a background goroutine. Its documented
// contract is bufio-like — call Err() after a Flush() to learn about a permanent
// failure. The engine's single-operation paths never did, so every `if err :=
// e.AOF.Write(...)` check was checking nothing: once the underlying file failed
// (disk full, revoked mount), the writer dropped every subsequent command while
// the engine kept reporting success.
//
// aofPersist does what the contract asks: write, then ask the writer whether a
// permanent failure has already been recorded. It does not flush — durability on
// crash is the flusher's job — it only refuses to report success when the writer
// knows the command will never be written.
//
// Batch paths keep their own Flush, which reports the error directly.
func (e *Engine) aofPersist(cmd string) error {
	if err := e.AOF.Write(cmd); err != nil {
		return fmt.Errorf("persistence error (AOF write failed): %w", err)
	}
	// Err() round-trips through the writer's goroutine, so it observes any
	// failure recorded while this command was being enqueued. Cheap: one
	// channel exchange, no I/O.
	if err := e.AOF.Err(); err != nil {
		return fmt.Errorf("persistence error (AOF is failing): %w", err)
	}
	return nil
}
