// Package wasm runs a Tier-A plugin: a wasip1 module executed in-process by wazero
// (pure Go, no CGO — matches the repo's pure-Go-deps convention). The module is
// instantiated with NO WASI preopens, no sockets, and no environment beyond what the
// caller passes — its only syscall surface is stdio + a clock, so every effect flows
// through the JSON-RPC stdio channel to the host broker for a capability check
// (spec §1, §9). Memory is capped; the host holds a cancel handle to pause/kill it.
//
// This package deliberately does NOT import the parent plugin package (that would be
// an import cycle): it returns a concrete *Instance exposing the module's stdio and a
// kill/wait pair, which the plugin.Manager wraps into a plugin.Session.
package wasm

import (
	"context"
	"fmt"
	"io"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// wasmPageSize is the WebAssembly page size (64 KiB); memory limits are expressed in
// pages.
const wasmPageSize = 64 << 10

// DefaultMemoryBytes is the per-module memory cap (spec §9: 256 MiB default). A
// manifest may request more, surfaced in the grant dialog.
const DefaultMemoryBytes = 256 << 20

// Config parameterises a module instantiation.
type Config struct {
	// Name is the module/arg0 name (shows in traces and the module's os.Args[0]).
	Name string
	// MemoryBytes caps linear memory; 0 → DefaultMemoryBytes. Rounded down to a page.
	MemoryBytes uint64
	// Stderr receives the module's fd2 (a log stream, one record per line). Optional.
	Stderr io.Writer
}

// Instance is a running wasip1 module. Its stdio is piped to/from the host: the host
// writes Stdin (the module's fd0) and reads Stdout (the module's fd1).
type Instance struct {
	stdinW  *io.PipeWriter
	stdinR  *io.PipeReader
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	cancel  context.CancelFunc
	done    chan struct{}
	err     error
}

// Start compiles and instantiates the module, running its `_start` in a goroutine.
// It returns as soon as the instance is wired; the module runs until it returns from
// `_start`, is killed, or its stdin closes.
func Start(ctx context.Context, module []byte, cfg Config) (*Instance, error) {
	memBytes := cfg.MemoryBytes
	if memBytes == 0 {
		memBytes = DefaultMemoryBytes
	}
	pages := uint32(memBytes / wasmPageSize)
	if pages == 0 {
		pages = 1
	}

	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(pages))
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)

	compiled, err := rt.CompileModule(ctx, module)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("compile wasm: %w", err)
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	name := cfg.Name
	if name == "" {
		name = "plugin"
	}
	modCfg := wazero.NewModuleConfig().
		WithName(name).
		WithArgs(name).
		WithStdin(stdinR).
		WithStdout(stdoutW).
		WithStderr(stderrOr(cfg.Stderr)).
		// A clock is the one ambient effect Tier A keeps: Go/TinyGo runtimes need it,
		// and timing side-channels are an accepted risk (spec §9 "Honest limits").
		// No WithFSConfig / preopens / sockets: the module cannot touch fs or network.
		WithSysNanotime().
		WithSysWalltime()

	runCtx, cancel := context.WithCancel(ctx)
	inst := &Instance{
		stdinW: stdinW, stdinR: stdinR,
		stdoutR: stdoutR, stdoutW: stdoutW,
		cancel: cancel,
		done:   make(chan struct{}),
	}

	go func() {
		defer close(inst.done)
		// InstantiateModule runs `_start` and blocks until it returns (or the context
		// is cancelled — how Kill aborts a wedged module). When it returns, close the
		// pipes so the host's Recv sees EOF and its next Send sees a broken pipe.
		_, err := rt.InstantiateModule(runCtx, compiled, modCfg)
		inst.err = err
		_ = stdoutW.Close()
		_ = stdinR.Close()
		_ = rt.Close(context.Background())
	}()

	return inst, nil
}

// Stdin is the host's write side (the module reads it as fd0).
func (i *Instance) Stdin() io.WriteCloser { return i.stdinW }

// Stdout is the host's read side (the module writes it as fd1).
func (i *Instance) Stdout() io.Reader { return i.stdoutR }

// Kill aborts the module immediately (cancels its run context) and unblocks IO.
func (i *Instance) Kill() {
	i.cancel()
	_ = i.stdinW.Close()
	_ = i.stdoutR.Close()
}

// Wait blocks until the module exits and returns its exit error (nil on clean exit).
func (i *Instance) Wait() error {
	<-i.done
	return i.err
}

func stderrOr(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}
