// Command core.tcp-client is the reference chartplotter plugin (spec §12, Phase 1
// milestone): the built-in tcp-client NMEA source reimplemented as a Tier-A WASM
// plugin. It owns no I/O of its own — the host dials the socket (net.tcp-client
// capability) and streams bytes; this plugin only frames lines, parses them with the
// same nmea package the built-in runner uses, and publishes vessel/AIS/raw deltas
// back to the host. The built-in stays; parity between the two is the acceptance test.
//
// Build (Tier A): GOOS=wasip1 GOARCH=wasm go build -o plugin.wasm ./plugins/core.tcp-client
package main

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea"
	"github.com/beetlebugorg/chartplotter/sdk"
)

type tcpClient struct {
	h       *sdk.Host
	store   *nmea.Store
	parser  *nmea.Parser
	ais     *nmea.AISStore
	aisFeed func(line string)

	prev   map[string]any // last-published vessel paths, for diffing
	aisVer uint64
	buf    string // partial-line accumulator across chunks
	handle int
}

func (p *tcpClient) Start(h *sdk.Host) {
	p.h = h
	p.store = &nmea.Store{}
	p.parser = &nmea.Parser{}
	p.ais = nmea.NewAISStore(0)
	p.aisFeed = p.ais.Feeder()
	p.prev = map[string]any{}

	host, port := target(h.Config())
	if host == "" || port == 0 {
		h.Status("degraded", "no server configured")
		return
	}
	// The host dials; we only ever see bytes. All callbacks fire on the read loop.
	h.TCPConnect(host, port, sdk.TCPHandlers{
		OnConnect: func(handle int) {
			p.handle = handle
			h.Status("running", "connected to "+host+":"+strconv.Itoa(port))
		},
		OnData:  func(_ int, data []byte) { p.onData(data) },
		OnError: func(_ int, err error) { h.Status("degraded", "connection closed") },
	})
}

func (p *tcpClient) Stop() {
	if p.handle != 0 {
		p.h.CloseHandle(p.handle)
	}
}

// onData buffers inbound chunks and frames complete newline-terminated sentences —
// line framing is the plugin's job, the host delivers chunks (spec §10).
func (p *tcpClient) onData(chunk []byte) {
	p.buf += string(chunk)
	for {
		i := strings.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(p.buf[:i])
		p.buf = p.buf[i+1:]
		if line != "" {
			p.handleLine(line)
		}
	}
}

// handleLine mirrors the built-in runner's readLoop branch (nmea/source.go): raw →
// sniffer, VDM/VDO → AIS decode, everything else → the vessel store.
func (p *tcpClient) handleLine(line string) {
	p.h.PublishRaw(line)
	s, err := nmea.ParseSentence(line)
	if err != nil {
		return
	}
	if s.Type == "VDM" || s.Type == "VDO" {
		p.aisFeed(line)
		p.publishAIS()
		return
	}
	p.store.Apply(p.parser, s)
	p.publishVessel()
}

// publishVessel diffs the current vessel snapshot against the last published set and
// emits only the changed paths (attributed to this plugin host-side).
func (p *tcpClient) publishVessel() {
	cur := vesselPaths(p.store.Snapshot())
	var deltas []sdk.Delta
	for path, v := range cur {
		if prev, ok := p.prev[path]; !ok || !reflect.DeepEqual(prev, v) {
			deltas = append(deltas, sdk.DeltaOf(path, v))
		}
	}
	if len(deltas) > 0 {
		p.h.PublishVessel(deltas...)
		p.prev = cur
	}
}

// publishAIS republishes the current target set on any change (Upsert merges, so
// republishing is idempotent and converges the host store to this plugin's).
func (p *tcpClient) publishAIS() {
	if v := p.ais.Version(); v != p.aisVer {
		p.aisVer = v
		targets := p.ais.Snapshot()
		out := make([]sdk.AISTarget, 0, len(targets))
		for _, t := range targets {
			out = append(out, aisToDTO(t))
		}
		p.h.PublishAIS(out...)
	}
}

func main() {
	if err := sdk.Run(&tcpClient{}); err != nil {
		panic(err)
	}
}

// --- config + mapping helpers ----------------------------------------------

// target resolves host+port from config: either "host"+"port", or a combined
// "server" ("host:port").
func target(cfg map[string]any) (string, int) {
	if h, ok := cfg["host"].(string); ok && h != "" {
		return h, cfgInt(cfg, "port")
	}
	if s, ok := cfg["server"].(string); ok && s != "" {
		host, portStr, _ := strings.Cut(s, ":")
		port, _ := strconv.Atoi(portStr)
		return host, port
	}
	return "", 0
}

func cfgInt(cfg map[string]any, key string) int {
	switch v := cfg[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

// vesselPaths flattens the set (non-nil) fields of a VesselState into the dotted
// paths the host's vessel.write schema accepts (nmea/publish.go).
func vesselPaths(vs nmea.VesselState) map[string]any {
	m := map[string]any{}
	nav := vs.Navigation
	if nav.Position != nil {
		m["navigation.position"] = *nav.Position
	}
	putF(m, "navigation.cogTrue", nav.COGTrue)
	putF(m, "navigation.sog", nav.SOG)
	putF(m, "navigation.headingTrue", nav.HeadingTrue)
	putF(m, "navigation.headingMagnetic", nav.HeadingMagnetic)
	putF(m, "navigation.magneticVariation", nav.MagneticVariation)
	putF(m, "navigation.rateOfTurn", nav.RateOfTurn)
	putF(m, "navigation.speedThroughWater", nav.SpeedThroughWater)
	if nav.Datetime != nil {
		m["navigation.datetime"] = *nav.Datetime
	}
	env := vs.Environment
	putF(m, "environment.depth.belowTransducer", env.Depth.BelowTransducer)
	putF(m, "environment.depth.belowKeel", env.Depth.BelowKeel)
	putF(m, "environment.depth.belowSurface", env.Depth.BelowSurface)
	putF(m, "environment.water.temperature", env.Water.Temperature)
	putF(m, "environment.wind.angleApparent", env.Wind.AngleApparent)
	putF(m, "environment.wind.speedApparent", env.Wind.SpeedApparent)
	putF(m, "environment.wind.angleTrue", env.Wind.AngleTrue)
	putF(m, "environment.wind.speedTrue", env.Wind.SpeedTrue)
	putF(m, "environment.wind.directionTrue", env.Wind.DirectionTrue)
	putF(m, "route.xte", vs.Route.XTE)
	putF(m, "route.bearingToWaypoint", vs.Route.BearingToWaypoint)
	putF(m, "route.distanceToWaypoint", vs.Route.DistanceToWaypoint)
	if vs.Route.ActiveWaypoint != "" {
		m["route.activeWaypoint"] = vs.Route.ActiveWaypoint
	}
	return m
}

func putF(m map[string]any, path string, v *float64) {
	if v != nil {
		m[path] = *v
	}
}

func aisToDTO(t nmea.AISTarget) sdk.AISTarget {
	return sdk.AISTarget{
		MMSI: t.MMSI, Lat: t.Lat, Lon: t.Lon,
		COG: t.COG, SOG: t.SOG, Heading: t.Heading,
		Name: t.Name, CallSign: t.CallSign, ShipType: t.ShipType, TypeName: t.TypeName,
		Destination: t.Destination, Length: t.Length, Beam: t.Beam, Draught: t.Draught,
		Status: t.Status, Class: t.Class,
	}
}
