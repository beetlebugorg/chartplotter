package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea/sim"
)

// simulateCmd runs a NMEA0183 traffic generator over TCP: own-ship instruments
// plus moving AIS targets (one optionally on a collision course). Point a
// Connection at it (host:port, default 127.0.0.1:10110) to develop against live-
// shaped data — own-ship, AIS targets/names, and (next) CPA/collision detection —
// without a real receiver.
type simulateCmd struct {
	Host      string  `default:"127.0.0.1" help:"Bind host."`
	Port      int     `default:"10110" help:"Bind port (IANA NMEA-0183-over-IP)."`
	Center    string  `default:"38.978,-76.478" help:"Own-ship start as lat,lon."`
	Course    float64 `default:"45" help:"Own-ship course (degrees true)."`
	Speed     float64 `default:"6" help:"Own-ship speed (knots)."`
	Targets   int     `default:"6" help:"Number of AIS targets."`
	Collision bool    `default:"true" negatable:"" help:"Put one target on a collision course."`
	Seed      int64   `default:"1" help:"RNG seed (reproducible scenarios)."`
}

func (c simulateCmd) Run() error {
	lat, lon, err := parseLatLon(c.Center)
	if err != nil {
		return err
	}
	s := sim.New(sim.Options{
		Lat: lat, Lon: lon, Course: c.Course, Speed: c.Speed,
		Targets: c.Targets, Collision: c.Collision, Seed: c.Seed,
	})

	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	defer ln.Close()
	fmt.Printf("nmea0183 simulator → tcp://%s  (own-ship %.4f,%.4f @ %.0f° %.0fkn, %d AIS targets%s)\n",
		addr, lat, lon, c.Course, c.Speed, c.Targets, ifStr(c.Collision, ", 1 on collision course", ""))
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
		lines := s.OwnSentences()
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
