package plugin

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// manifest.go defines plugin.json (spec §3) and its validation. The manifest is the
// content-addressed root of a plugin archive: its `files` map lists every archive
// file with its sha256, so verifying the manifest (and, in a later phase, its
// signature) verifies the whole zip (§2).

// CorePrefix marks in-tree ("built-in") plugin ids. Third-party archives claiming it
// are rejected by the installer (spec §7 "Reserved for the host").
const CorePrefix = "core."

// Capability names enforced by the broker (spec §6). Everything is opt-in.
const (
	CapVesselRead   = "vessel.read"
	CapVesselWrite  = "vessel.write"
	CapAISRead      = "ais.read"
	CapAISWrite     = "ais.write"
	CapSerial       = "serial"
	CapTCPClient    = "net.tcp-client"
	CapUDP          = "net.udp"
	CapHTTP         = "net.http"
	CapStorage      = "storage"
	CapNotify       = "notify"
	CapHTTPRegister = "http.register"
	CapUISettings   = "ui.settings"
	CapUIPanel      = "ui.panel"
	CapUIMapLayer   = "ui.map-layer"
	CapUIHUD        = "ui.hud"
)

// Capability is one entry in the manifest `capabilities` list, or one element of the
// grant set. Hosts/Devices/Quota carry the capability's parameters (allowlist,
// device set, storage quota); which apply depends on Cap.
type Capability struct {
	Cap     string   `json:"cap"`
	Hosts   []string `json:"hosts,omitempty"`   // net.* allowlist patterns
	Devices []string `json:"devices,omitempty"` // serial device allowlist (resolved at grant)
	Quota   string   `json:"quota,omitempty"`   // storage quota, e.g. "10MB"
}

// Entry names a plugin's runtime entry points. At least one of WASM / Native is
// required for a plugin that runs host-side code; a pure-UI built-in (registered in
// the frontend) may instead carry only UI.
type Entry struct {
	WASM   string            `json:"wasm,omitempty"`   // Tier A: wasip1 module path in the archive
	Native map[string]string `json:"native,omitempty"` // Tier B: "linux-amd64" → path
}

// UISlot is a declared UI contribution point (panel/mapLayer/hud entry).
type UISlot struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

// UI is the manifest `ui` block (spec §8): the entry module plus declared slots.
type UI struct {
	Entry     string          `json:"entry,omitempty"`
	Settings  json.RawMessage `json:"settings,omitempty"`
	Panels    []UISlot        `json:"panels,omitempty"`
	MapLayers []UISlot        `json:"mapLayers,omitempty"`
	HUD       []UISlot        `json:"hud,omitempty"`
}

// Service is a provides/consumes declaration (spec §7).
type Service struct {
	Service    string `json:"service"`
	APIVersion int    `json:"apiVersion,omitempty"`
	Optional   bool   `json:"optional,omitempty"`
}

// Manifest is the parsed plugin.json.
type Manifest struct {
	ManifestVersion int               `json:"manifestVersion"`
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Description     string            `json:"description,omitempty"`
	Publisher       string            `json:"publisher,omitempty"`
	License         string            `json:"license,omitempty"`
	Homepage        string            `json:"homepage,omitempty"`
	APIVersion      int               `json:"apiVersion"`
	Entry           Entry             `json:"entry"`
	Capabilities    []Capability      `json:"capabilities,omitempty"`
	UI              *UI               `json:"ui,omitempty"`
	Provides        []Service         `json:"provides,omitempty"`
	Consumes        []Service         `json:"consumes,omitempty"`
	Files           map[string]string `json:"files,omitempty"`
}

var (
	// idPattern: reverse-DNS, [a-z0-9.-], must contain a dot, no leading/trailing dot.
	idPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	// semverPattern: a pragmatic MAJOR.MINOR.PATCH with optional -pre/+build.
	semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
)

// ParseManifest decodes and validates plugin.json bytes.
func ParseManifest(b []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields() // catch typo'd manifest keys early
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse plugin.json: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks structural validity. It does NOT enforce the core.* installer
// policy (that is caller policy — see Install) so in-tree built-ins can validate.
func (m *Manifest) Validate() error {
	if m.ManifestVersion != 1 {
		return fmt.Errorf("unsupported manifestVersion %d (want 1)", m.ManifestVersion)
	}
	if !idPattern.MatchString(m.ID) {
		return fmt.Errorf("invalid id %q (want reverse-DNS [a-z0-9.-])", m.ID)
	}
	if !semverPattern.MatchString(m.Version) {
		return fmt.Errorf("invalid version %q (want semver)", m.Version)
	}
	if m.APIVersion != APIVersion {
		return fmt.Errorf("plugin apiVersion %d not supported (host: %d)", m.APIVersion, APIVersion)
	}
	if m.Entry.WASM == "" && len(m.Entry.Native) == 0 && m.UI == nil {
		return fmt.Errorf("manifest declares no entry point (wasm, native, or ui)")
	}
	for _, c := range m.Capabilities {
		if c.Cap == "" {
			return fmt.Errorf("capability with empty cap")
		}
	}
	return nil
}

// HasCap reports whether the given capability list grants cap (by name).
func HasCap(caps []Capability, cap string) (Capability, bool) {
	for _, c := range caps {
		if c.Cap == cap {
			return c, true
		}
	}
	return Capability{}, false
}
