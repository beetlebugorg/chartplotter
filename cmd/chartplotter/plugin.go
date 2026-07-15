package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea"
	"github.com/beetlebugorg/chartplotter/internal/engine/plugin"
	"github.com/beetlebugorg/chartplotter/internal/engine/server"
)

// plugin.go adds the `chartplotter plugin …` verb group (spec §2 CLI): install,
// list, enable, disable, remove operate on <dataDir>/plugins.json + the unpacked
// archives; dev runs an unpacked directory under the broker for iteration.

type pluginCmd struct {
	Install pluginInstallCmd `cmd:"" help:"Install a plugin from a .zip (verify + unpack)."`
	List    pluginListCmd    `cmd:"" help:"List installed plugins."`
	Enable  pluginEnableCmd  `cmd:"" help:"Enable a plugin."`
	Disable pluginDisableCmd `cmd:"" help:"Disable a plugin."`
	Remove  pluginRemoveCmd  `cmd:"" help:"Uninstall a plugin."`
	Dev     pluginDevCmd     `cmd:"" help:"Run an unpacked plugin directory under the broker (auto-restart)."`
}

// pluginManager builds a state-only Manager (no runners) for offline CLI management.
func pluginManager(dataDir string) *plugin.Manager {
	if dataDir == "" {
		dataDir = server.DefaultDataDir()
	}
	return plugin.NewManager(context.Background(), plugin.ManagerOpts{DataDir: dataDir, NoStart: true, Logf: log.Printf})
}

type pluginInstallCmd struct {
	Archive  string `arg:"" type:"existingfile" help:"Plugin archive (.zip)."`
	Data     string `help:"Data dir override (default: XDG data)."`
	GrantAll bool   `name:"grant-all" help:"Grant every capability the manifest requests and enable the plugin."`
}

func (c pluginInstallCmd) Run() error {
	m := pluginManager(c.Data)
	man, err := m.Install(c.Archive, plugin.InstallOptions{})
	if err != nil {
		return err
	}
	fmt.Printf("installed %s@%s (%s)\n", man.ID, man.Version, man.Name)
	if len(man.Capabilities) > 0 {
		fmt.Println("requested capabilities:")
		for _, cp := range man.Capabilities {
			fmt.Printf("  - %s\n", cp.Cap)
		}
	}
	if c.GrantAll {
		if err := m.SetGrants(man.ID, man.Capabilities, nil); err != nil {
			return err
		}
		if err := m.Enable(man.ID); err != nil {
			return err
		}
		fmt.Println("granted all capabilities and enabled")
	} else {
		fmt.Printf("review capabilities, then: chartplotter plugin enable %s\n", man.ID)
	}
	return nil
}

type pluginListCmd struct {
	Data string `help:"Data dir override."`
}

func (c pluginListCmd) Run() error {
	for _, info := range pluginManager(c.Data).List() {
		state := "disabled"
		if info.Record.Enabled {
			state = info.Status.State
			if state == "" {
				state = "enabled"
			}
		}
		name := info.Record.ID
		if info.Manifest != nil {
			name = info.Manifest.Name
		}
		fmt.Printf("%-32s v%-8s %-10s %s\n", info.Record.ID, info.Record.Version, state, name)
	}
	return nil
}

type pluginEnableCmd struct {
	ID   string `arg:"" help:"Plugin id."`
	Data string `help:"Data dir override."`
}

func (c pluginEnableCmd) Run() error {
	if err := pluginManager(c.Data).Enable(c.ID); err != nil {
		return err
	}
	fmt.Printf("enabled %s (restart the server to apply)\n", c.ID)
	return nil
}

type pluginDisableCmd struct {
	ID   string `arg:"" help:"Plugin id."`
	Data string `help:"Data dir override."`
}

func (c pluginDisableCmd) Run() error {
	if err := pluginManager(c.Data).Disable(c.ID); err != nil {
		return err
	}
	fmt.Printf("disabled %s (restart the server to apply)\n", c.ID)
	return nil
}

type pluginRemoveCmd struct {
	ID        string `arg:"" help:"Plugin id."`
	Data      string `help:"Data dir override."`
	PurgeData bool   `name:"purge-data" help:"Also delete the plugin's stored data."`
}

func (c pluginRemoveCmd) Run() error {
	if err := pluginManager(c.Data).Remove(c.ID, c.PurgeData); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", c.ID)
	return nil
}

type pluginDevCmd struct {
	Dir    string `arg:"" type:"existingdir" help:"Unpacked plugin directory (containing plugin.json)."`
	Config string `help:"Plugin config as JSON, e.g. '{\"host\":\"127.0.0.1\",\"port\":10110}'."`
}

func (c pluginDevCmd) Run() error {
	var config map[string]any
	if c.Config != "" {
		if err := json.Unmarshal([]byte(c.Config), &config); err != nil {
			return fmt.Errorf("bad --config JSON: %w", err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	fmt.Printf("dev: running %s (Ctrl-C to stop)\n", c.Dir)
	return plugin.DevRun(ctx, c.Dir, config, nil, consoleHost{}, log.Printf)
}

// consoleHost prints capability effects to stdout for `plugin dev`.
type consoleHost struct{}

func (consoleHost) PublishVessel(src string, d []nmea.Delta) {
	fmt.Printf("vessel[%s]: %d delta(s)\n", src, len(d))
}
func (consoleHost) PublishAIS(src string, t []nmea.AISTarget) {
	fmt.Printf("ais[%s]: %d target(s)\n", src, len(t))
}
func (consoleHost) PublishRaw(src string, lines []string) {
	for _, l := range lines {
		fmt.Printf("raw[%s]: %s\n", src, l)
	}
}
func (consoleHost) UpdateStatus(id string, st plugin.PluginStatus) {
	fmt.Printf("status[%s]: %s %s\n", id, st.State, st.Detail)
}
func (consoleHost) Log(id, level, msg string) { fmt.Printf("log[%s] %s: %s\n", id, level, msg) }
