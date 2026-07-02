package main

import (
	"archive/zip"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea/sim"
	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

// simulateCmd runs a NMEA0183 traffic generator over TCP: own-ship instruments
// plus moving AIS targets (one optionally on a collision course). Point a
// Connection at it (host:port, default 127.0.0.1:10110) to develop against live-
// shaped data — own-ship, AIS targets/names, and (next) CPA/collision detection —
// without a real receiver.
type simulateCmd struct {
	Host      string  `default:"127.0.0.1" help:"Bind host."`
	Port      int     `default:"10110" help:"Bind port (IANA NMEA-0183-over-IP)."`
	Scenario  string  `help:"Named Annapolis preset (sets start/route/traffic). Use 'list' to print them."`
	Center    string  `default:"38.978,-76.478" help:"Own-ship start as lat,lon (ignored when --scenario is set)."`
	Course    float64 `default:"45" help:"Own-ship course (degrees true)."`
	Speed     float64 `default:"6" help:"Own-ship speed (knots)."`
	Targets   int     `default:"6" help:"Number of AIS targets."`
	Collision bool    `default:"true" negatable:"" help:"Put one target on a collision course."`
	Sailing   bool    `help:"Own-ship tacks (COG weaves) with a varying leeway so heading ≠ COG."`
	DropGPS   int     `name:"drop-gps" help:"Stop own-ship position fixes after N seconds (test stale/lost GPS); 0 = never."`
	Seed      int64   `default:"1" help:"RNG seed (reproducible scenarios)."`
	Cell      string  `type:"existingfile" help:"S-57 cell (.000 or exchange .zip) to keep traffic in navigable water."`
	MinDepth  float64 `name:"min-depth" default:"2" help:"Minimum charted depth (DRVAL1, m) for navigable water when --cell is set."`
}

func (c simulateCmd) Run() error {
	if strings.EqualFold(c.Scenario, "list") {
		fmt.Print("Annapolis scenarios (--scenario <name>):\n", sim.ScenarioList())
		return nil
	}

	var water *sim.WaterMask
	if c.Cell != "" {
		feats, err := loadWaterFeatures(c.Cell)
		if err != nil {
			return fmt.Errorf("load cell %s: %w", c.Cell, err)
		}
		if water = sim.NewWaterMask(feats, c.MinDepth); water == nil {
			fmt.Println("warning: no navigable depth areas (DEPARE ≥ min-depth) in cell; placing traffic unconstrained")
		}
	}

	// A scenario fully defines the world's start/route/traffic; otherwise build it
	// from the position/motion flags.
	var opts sim.Options
	desc := ""
	if c.Scenario != "" {
		sc, ok := sim.ScenarioByName(c.Scenario)
		if !ok {
			return fmt.Errorf("unknown scenario %q; --scenario list to see options", c.Scenario)
		}
		opts = sc.Options(c.Seed, water)
		desc = sc.Desc
	} else {
		lat, lon, err := parseLatLon(c.Center)
		if err != nil {
			return err
		}
		opts = sim.Options{
			Lat: lat, Lon: lon, Course: c.Course, Speed: c.Speed,
			Targets: c.Targets, Collision: c.Collision, Sailing: c.Sailing,
			Seed: c.Seed, Water: water,
		}
	}
	s := sim.New(opts)

	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	defer ln.Close()
	if desc != "" {
		fmt.Printf("scenario %q — %s\n", c.Scenario, desc)
	}
	fmt.Printf("nmea0183 simulator → tcp://%s  (own-ship %.4f,%.4f @ %.0f° %.0fkn, %d AIS targets%s%s%s)\n",
		addr, s.Own.Lat, s.Own.Lon, s.Own.Course, opts.Speed, opts.Targets,
		ifStr(opts.Collision, ", 1 on collision course", ""),
		ifStr(c.DropGPS > 0, fmt.Sprintf(", GPS drops at %ds", c.DropGPS), ""),
		ifStr(water != nil, ", in navigable water from "+filepath.Base(c.Cell), ""))
	fmt.Println("point a Connection at this host:port; Ctrl-C to stop")

	hub := &connHub{conns: map[net.Conn]struct{}{}}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			hub.add(conn)
			fmt.Printf("client connected: %s (%d total)\n", conn.RemoteAddr(), hub.count())
		}
	}()

	// 1 Hz world tick. Own-ship every second; AIS positions every 3 s; AIS static
	// (names/type) every 30 s — roughly the real class-A cadence.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for tick := 0; ; tick++ {
		<-ticker.C
		s.Step(1)
		// After --drop-gps seconds, withhold the position/motion fixes (depth/wind
		// keep flowing) so the client sees a frozen-then-lost GPS.
		var lines []string
		if c.DropGPS > 0 && tick >= c.DropGPS {
			lines = s.EnvSentences()
		} else {
			lines = s.OwnSentences()
		}
		if tick%3 == 0 {
			lines = append(lines, s.AISPositions()...)
		}
		if tick%30 == 0 {
			lines = append(lines, s.AISStatics()...)
		}
		hub.broadcast(lines)
	}
}

// connHub broadcasts sentence lines to all connected clients, dropping any that error.
type connHub struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func (h *connHub) add(c net.Conn) {
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
}

func (h *connHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns)
}

func (h *connHub) broadcast(lines []string) {
	if len(lines) == 0 {
		return
	}
	payload := []byte(strings.Join(lines, "\r\n") + "\r\n")
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns {
		if _, err := c.Write(payload); err != nil {
			c.Close()
			delete(h.conns, c)
		}
	}
}

// loadWaterFeatures opens an S-57 cell — a .000 file or the first .000 inside an
// exchange-set .zip — with the native engine (base edition, no updates) and
// returns its DEPARE/DRGARE features for the water mask.
func loadWaterFeatures(p string) ([]tile57.Feature, error) {
	data, err := readBaseCell(p)
	if err != nil {
		return nil, err
	}
	src, err := tile57.OpenChartBytes(data)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return src.Features("DEPARE", "DRGARE")
}

// readBaseCell returns the raw base-cell bytes from a .000 file or the first
// .000 inside an exchange-set .zip.
func readBaseCell(p string) ([]byte, error) {
	if strings.HasSuffix(strings.ToLower(p), ".zip") {
		zr, err := zip.OpenReader(p)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		for _, f := range zr.File {
			if strings.HasSuffix(strings.ToLower(f.Name), ".000") {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				data, err := io.ReadAll(rc)
				rc.Close()
				if err != nil {
					return nil, err
				}
				return data, nil
			}
		}
		return nil, fmt.Errorf("no .000 cell found in %s", p)
	}
	return os.ReadFile(p)
}

func parseLatLon(s string) (float64, float64, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("center must be lat,lon")
	}
	lat, e1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lon, e2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if e1 != nil || e2 != nil {
		return 0, 0, fmt.Errorf("bad center %q", s)
	}
	return lat, lon, nil
}

func ifStr(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}
