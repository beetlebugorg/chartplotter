package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// dev.go implements `chartplotter plugin dev <dir>`: run an unpacked plugin directory
// under the real broker with auto-restart, for iteration without packaging (spec §11).
// It reuses the same runtime + broker + capability path as the server, so a plugin
// that works under dev works installed.

// DevRun runs the plugin unpacked at dir against host, restarting it (with backoff)
// whenever it exits, until ctx is cancelled. config/grants are the settings and
// capability grants the plugin should see — pass the manifest's declared capabilities
// to grant everything during development.
func DevRun(ctx context.Context, dir string, config map[string]any, grants []Capability, host Host, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	b, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return fmt.Errorf("read plugin.json: %w", err)
	}
	man, err := ParseManifest(b)
	if err != nil {
		return err
	}
	if grants == nil {
		grants = man.Capabilities // dev default: grant everything the manifest asks for
	}
	storeDir := filepath.Join(dir, "data")

	backoff := defaultBackoff
	for ctx.Err() == nil {
		logf("plugin %s: starting (dir %s)", man.ID, dir)
		err := devRunOnce(ctx, dir, man, storeDir, config, grants, host)
		if ctx.Err() != nil {
			return nil
		}
		logf("plugin %s exited: %v; restarting in %s", man.ID, err, backoff)
		if !sleepCtx(ctx, backoff) {
			return nil
		}
		backoff = minDur(backoff*2, defaultMaxBackoff)
	}
	return nil
}

func devRunOnce(parent context.Context, dir string, man *Manifest, storeDir string, config map[string]any, grants []Capability, host Host) error {
	logw := &lineLogger{logf: func(level, msg string) { host.Log(man.ID, level, msg) }}
	inst, err := startInstance(parent, dir, man, false, storeDir, logw)
	if err != nil {
		return err
	}
	sess := newStdioSession(inst.Stdout(), inst.Stdin(), inst.Kill)
	b := newBrokerSession(man.ID, sess, host, storeDir, grants, config)

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	// serve() blocks reading the plugin's stdout; a context cancel (Ctrl-C →
	// signal.NotifyContext) won't unblock it on its own, so kill the session on
	// cancellation to make the read return EOF and DevRun exit promptly.
	go func() {
		<-ctx.Done()
		sess.Kill()
	}()
	serveDone := make(chan error, 1)
	go func() { serveDone <- b.serve(ctx) }()

	hctx, hcancel := context.WithTimeout(ctx, handshakeTimeout)
	_, err = b.handshake(hctx)
	hcancel()
	if err != nil {
		sess.Kill()
		<-serveDone
		_ = inst.Wait()
		return fmt.Errorf("handshake: %w", err)
	}
	host.UpdateStatus(man.ID, PluginStatus{State: "running"})

	// Ping liveness (dev doesn't restart on missed pings — the outer loop handles
	// exits — but keeps the same cadence so a wedged plugin still gets killed).
	go func() {
		t := time.NewTicker(pingInterval)
		defer t.Stop()
		misses := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, c := context.WithTimeout(ctx, pingTimeout)
				_, err := b.request(pctx, MethodPluginPing, nil)
				c()
				if err != nil {
					if misses++; misses >= maxPingMisses {
						sess.Kill()
						return
					}
				} else {
					misses = 0
				}
			}
		}
	}()

	serveErr := <-serveDone
	sess.Kill()
	_ = inst.Wait()
	return serveErr
}
