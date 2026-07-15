// Package native runs a Tier-B plugin: a per-platform binary launched as a child
// process (spec §1). Unlike the WASM tier this is NOT a sandbox — the OS boundary is
// the user's account; the broker's capability checks keep honest plugins honest and
// keep the programming model identical, but a native plugin can open any file/socket
// the user can (spec §9 "Honest limits"). The child is started with a minimal env,
// closed extra fds, and cwd set to its data dir.
//
// Like the wasm package, this returns a concrete *Instance the plugin.Manager wraps
// into a plugin.Session, so there is no import cycle.
package native

import (
	"context"
	"fmt"
	"io"
	"os/exec"
)

// Config parameterises a child-process launch.
type Config struct {
	Path   string    // absolute path to the plugin binary
	Args   []string  // args after arg0
	Env    []string  // minimal environment (spec §9)
	Dir    string    // cwd — the plugin's data dir
	Stderr io.Writer // fd2 log stream; optional
}

// Instance is a running native plugin child process.
type Instance struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
	done   chan struct{}
	err    error
}

// Start launches the child and wires its stdio. It returns once the process is
// spawned; Wait blocks for exit.
func Start(ctx context.Context, cfg Config) (*Instance, error) {
	cmd := exec.CommandContext(ctx, cfg.Path, cfg.Args...)
	cmd.Dir = cfg.Dir
	cmd.Env = cfg.Env
	cmd.Stderr = cfg.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start plugin process: %w", err)
	}

	inst := &Instance{cmd: cmd, stdin: stdin, stdout: stdout, done: make(chan struct{})}
	go func() {
		defer close(inst.done)
		inst.err = cmd.Wait()
	}()
	return inst, nil
}

func (i *Instance) Stdin() io.WriteCloser { return i.stdin }
func (i *Instance) Stdout() io.Reader     { return i.stdout }

// Kill terminates the child process immediately.
func (i *Instance) Kill() {
	if i.cmd.Process != nil {
		_ = i.cmd.Process.Kill()
	}
}

// Wait blocks until the process exits and returns its exit error.
func (i *Instance) Wait() error {
	<-i.done
	return i.err
}
