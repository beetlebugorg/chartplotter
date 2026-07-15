package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea"
)

// capabilities.go implements the plugin→host surface: it dispatches inbound requests
// and notifications to the granted capability, enforcing the grant set first (spec
// §6). Host-mediated I/O (the host dials the socket; the plugin only sees bytes) and
// the storage KV live here.

// handleRequest handles a plugin→host request and always sends exactly one reply.
func (b *brokerSession) handleRequest(ctx context.Context, m *Message) {
	switch m.Method {
	case MethodConfigGet:
		b.reply(m.ID, b.configCopy())
	case MethodConfigSet:
		b.handleConfigSet(m)
	case MethodTCPConnect:
		b.handleTCPConnect(ctx, m)
	case MethodIOClose:
		b.handleIOClose(m)
	case MethodSerialList:
		b.serialList(m)
	case MethodSerialOpen:
		b.serialOpen(m)
	case MethodStorageGet, MethodStorageSet, MethodStorageDelete, MethodStorageList:
		b.handleStorage(m)
	default:
		// Unknown methods answer MethodNotFound; SDKs surface this as "capability
		// not available" (spec §5).
		b.replyErr(m.ID, CodeMethodNotFound, "unknown method: "+m.Method)
	}
}

// handleNotification handles a plugin→host notification (no reply). Unknown
// notifications are ignored (spec §5: plugins/host ignore unknown notifications).
func (b *brokerSession) handleNotification(m *Message) {
	switch m.Method {
	case MethodVesselPublish:
		b.handleVesselPublish(m)
	case MethodAISPublish:
		b.handleAISPublish(m)
	case MethodRawPublish:
		b.handleRawPublish(m)
	case MethodStatusUpdate:
		b.handleStatusUpdate(m)
	case MethodTCPSend:
		b.handleTCPSend(m)
	}
}

// --- vessel / ais / raw / status -------------------------------------------

func (b *brokerSession) handleVesselPublish(m *Message) {
	if !b.hasCap(CapVesselWrite) {
		return // no grant → silently drop (notification has no reply channel)
	}
	var p VesselPublish
	if json.Unmarshal(m.Params, &p) != nil {
		return
	}
	deltas := make([]nmea.Delta, 0, len(p.Deltas))
	for _, d := range p.Deltas {
		deltas = append(deltas, nmea.Delta{Path: d.Path, Value: d.Value})
	}
	b.host.PublishVessel(b.id, deltas)
}

func (b *brokerSession) handleAISPublish(m *Message) {
	if !b.hasCap(CapAISWrite) {
		return
	}
	var p AISPublish
	if json.Unmarshal(m.Params, &p) != nil {
		return
	}
	targets := make([]nmea.AISTarget, 0, len(p.Targets))
	for _, t := range p.Targets {
		targets = append(targets, aisFromDTO(t))
	}
	b.host.PublishAIS(b.id, targets)
}

func (b *brokerSession) handleRawPublish(m *Message) {
	// raw.publish feeds the sniffer; gate behind being a data source (vessel or AIS
	// write), since a plugin with neither has no business injecting sentences.
	if !b.hasCap(CapVesselWrite) && !b.hasCap(CapAISWrite) {
		return
	}
	var p RawPublish
	if json.Unmarshal(m.Params, &p) != nil {
		return
	}
	b.host.PublishRaw(b.id, p.Lines)
}

func (b *brokerSession) handleStatusUpdate(m *Message) {
	var s StatusUpdate
	if json.Unmarshal(m.Params, &s) != nil {
		return
	}
	b.host.UpdateStatus(b.id, PluginStatus(s))
}

func aisFromDTO(t AISTargetDTO) nmea.AISTarget {
	return nmea.AISTarget{
		MMSI: t.MMSI, Lat: t.Lat, Lon: t.Lon,
		COG: t.COG, SOG: t.SOG, Heading: t.Heading,
		Name: t.Name, CallSign: t.CallSign, ShipType: t.ShipType, TypeName: t.TypeName,
		Destination: t.Destination, Length: t.Length, Beam: t.Beam, Draught: t.Draught,
		Status: t.Status, Class: t.Class,
	}
}

// --- config ----------------------------------------------------------------

func (b *brokerSession) handleConfigSet(m *Message) {
	var kv StorageSet
	if err := json.Unmarshal(m.Params, &kv); err != nil {
		b.replyErr(m.ID, CodeInvalidParams, err.Error())
		return
	}
	var v any
	_ = json.Unmarshal(kv.Value, &v)
	b.mu.Lock()
	if b.config == nil {
		b.config = map[string]any{}
	}
	b.config[kv.Key] = v
	b.mu.Unlock()
	b.reply(m.ID, map[string]any{"ok": true})
}

// --- host-mediated TCP -----------------------------------------------------

func (b *brokerSession) handleTCPConnect(ctx context.Context, m *Message) {
	grant, ok := b.grantFor(CapTCPClient)
	if !ok {
		b.replyErr(m.ID, CodeCapabilityDenied, "net.tcp-client not granted")
		return
	}
	var p TCPConnect
	if err := json.Unmarshal(m.Params, &p); err != nil {
		b.replyErr(m.ID, CodeInvalidParams, err.Error())
		return
	}
	if !matchHostAllow(grant.Hosts, p.Host, p.Port) {
		b.replyErr(m.ID, CodeCapabilityDenied, fmt.Sprintf("%s:%d not in the granted allowlist", p.Host, p.Port))
		return
	}
	dialCtx, cancel := context.WithCancel(ctx)
	conn, err := b.dialer.DialContext(dialCtx, "tcp", net.JoinHostPort(p.Host, strconv.Itoa(p.Port)))
	if err != nil {
		cancel()
		b.replyErr(m.ID, CodeInternalError, "dial: "+err.Error())
		return
	}
	handle := b.addHandle(&ioHandle{conn: conn, cancel: cancel})
	b.reply(m.ID, HandleResult{Handle: handle})
	go b.pumpConn(handle, conn)
}

// pumpConn reads chunks (not lines — line framing is the plugin's job, spec §10) from
// a dialed conn and forwards them as tcp.data notifications, ending with io.closed.
func (b *brokerSession) pumpConn(handle int, conn net.Conn) {
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			_ = b.notify(MethodTCPData, IOData{Handle: handle, Data: chunk, N: n})
		}
		if err != nil {
			_ = b.notify(MethodIOClosed, IOHandle{Handle: handle, Reason: err.Error()})
			b.dropHandle(handle)
			return
		}
	}
}

func (b *brokerSession) handleTCPSend(m *Message) {
	var d IOData
	if json.Unmarshal(m.Params, &d) != nil {
		return
	}
	if h := b.getHandle(d.Handle); h != nil && h.conn != nil {
		_, _ = h.conn.Write(d.Data)
	}
}

func (b *brokerSession) handleIOClose(m *Message) {
	var h IOHandle
	if err := json.Unmarshal(m.Params, &h); err != nil {
		b.replyErr(m.ID, CodeInvalidParams, err.Error())
		return
	}
	b.dropHandle(h.Handle)
	b.reply(m.ID, map[string]any{"ok": true})
}

// --- serial (not wired in this build) --------------------------------------

func (b *brokerSession) serialList(m *Message) {
	if !b.hasCap(CapSerial) {
		b.replyErr(m.ID, CodeCapabilityDenied, "serial not granted")
		return
	}
	// Device enumeration quality across platforms is an open question (spec §13); the
	// serial transport is not wired in this build. Report an empty list rather than
	// failing so settings dropdowns render.
	b.reply(m.ID, map[string][]string{"ports": {}})
}

func (b *brokerSession) serialOpen(m *Message) {
	b.replyErr(m.ID, CodeMethodNotFound, "serial transport not available in this build")
}

// --- handle table ----------------------------------------------------------

func (b *brokerSession) addHandle(h *ioHandle) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextH++
	b.handles[b.nextH] = h
	return b.nextH
}

func (b *brokerSession) getHandle(id int) *ioHandle {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.handles[id]
}

func (b *brokerSession) dropHandle(id int) {
	b.mu.Lock()
	h := b.handles[id]
	delete(b.handles, id)
	b.mu.Unlock()
	if h != nil {
		if h.cancel != nil {
			h.cancel()
		}
		if h.conn != nil {
			_ = h.conn.Close()
		}
	}
}

// --- storage KV ------------------------------------------------------------

func (b *brokerSession) handleStorage(m *Message) {
	if !b.hasCap(CapStorage) {
		b.replyErr(m.ID, CodeCapabilityDenied, "storage not granted")
		return
	}
	switch m.Method {
	case MethodStorageGet:
		var k StorageKey
		if json.Unmarshal(m.Params, &k) != nil {
			b.replyErr(m.ID, CodeInvalidParams, "bad params")
			return
		}
		kv := b.loadKV()
		v, ok := kv[k.Key]
		b.reply(m.ID, StorageValue{Value: v, Found: ok})
	case MethodStorageSet:
		var s StorageSet
		if json.Unmarshal(m.Params, &s) != nil {
			b.replyErr(m.ID, CodeInvalidParams, "bad params")
			return
		}
		kv := b.loadKV()
		kv[s.Key] = s.Value
		if err := b.saveKV(kv); err != nil {
			b.replyErr(m.ID, CodeInternalError, err.Error())
			return
		}
		b.reply(m.ID, map[string]any{"ok": true})
	case MethodStorageDelete:
		var k StorageKey
		if json.Unmarshal(m.Params, &k) != nil {
			b.replyErr(m.ID, CodeInvalidParams, "bad params")
			return
		}
		kv := b.loadKV()
		delete(kv, k.Key)
		_ = b.saveKV(kv)
		b.reply(m.ID, map[string]any{"ok": true})
	case MethodStorageList:
		kv := b.loadKV()
		keys := make([]string, 0, len(kv))
		for k := range kv {
			keys = append(keys, k)
		}
		b.reply(m.ID, StorageList{Keys: keys})
	}
}

func (b *brokerSession) kvPath() string { return filepath.Join(b.storeDir, "storage.json") }

func (b *brokerSession) loadKV() map[string]json.RawMessage {
	kv := map[string]json.RawMessage{}
	data, err := os.ReadFile(b.kvPath())
	if err == nil {
		_ = json.Unmarshal(data, &kv)
	}
	return kv
}

func (b *brokerSession) saveKV(kv map[string]json.RawMessage) error {
	data, err := json.Marshal(kv)
	if err != nil {
		return err
	}
	if b.quota > 0 && int64(len(data)) > b.quota {
		return fmt.Errorf("storage quota exceeded (%d > %d bytes)", len(data), b.quota)
	}
	if err := os.MkdirAll(b.storeDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(b.kvPath(), data, 0o644)
}

// --- allowlist + quota parsing ---------------------------------------------

// matchHostAllow reports whether host:port matches any granted pattern. Patterns
// support a "*." wildcard prefix and an optional ":port" (spec §3). An empty grant
// list denies everything.
func matchHostAllow(patterns []string, host string, port int) bool {
	for _, p := range patterns {
		ph, pp, hasPort := strings.Cut(p, ":")
		if hasPort {
			if n, err := strconv.Atoi(pp); err != nil || n != port {
				continue
			}
		}
		if matchHostPattern(ph, host) {
			return true
		}
	}
	return false
}

func matchHostPattern(pattern, host string) bool {
	if pattern == host {
		return true
	}
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		return host == suffix || strings.HasSuffix(host, "."+suffix)
	}
	return false
}

// storageQuota parses the storage grant's Quota ("10MB", "512KB", "1024") into bytes.
// A storage grant without a quota gets a conservative 5 MiB default.
func storageQuota(grants []Capability) int64 {
	g, ok := HasCap(grants, CapStorage)
	if !ok {
		return 0
	}
	if g.Quota == "" {
		return 5 << 20
	}
	return parseBytes(g.Quota)
}

func parseBytes(s string) int64 {
	s = strings.TrimSpace(strings.ToUpper(s))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "MB"):
		mult, s = 1<<20, strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "KB"):
		mult, s = 1<<10, strings.TrimSuffix(s, "KB")
	case strings.HasSuffix(s, "B"):
		s = strings.TrimSuffix(s, "B")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 5 << 20
	}
	return n * mult
}
