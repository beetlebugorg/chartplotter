package server

import (
	"net/url"
	"strings"
)

// chartProvider is an ENC source the server is allowed to download from. It is the
// single source of truth for the download allowlist: every caller-supplied URL the
// server fetches (the /api/cell proxy, /api/proxy, and /api/import) must be handled
// by some provider, so an attacker can't point the server at an internal address
// (SSRF). To support a new provider, add an entry here — nothing else needs to
// change.
type chartProvider struct {
	Name  string   // human label, e.g. "NOAA"
	Hosts []string // allowed hosts; an exact match or any subdomain is accepted
}

// chartProviders is the registry of allowed download sources.
var chartProviders = []chartProvider{
	{Name: "NOAA", Hosts: []string{"charts.noaa.gov"}},
	{Name: "Inland ENC", Hosts: []string{"ienccloud.us"}},
}

// handles reports whether host belongs to this provider: an exact host match or any
// subdomain of one of its hosts (".charts.noaa.gov" matches "www.charts.noaa.gov",
// but "evil-charts.noaa.gov" and "charts.noaa.gov.evil.com" do not).
func (p chartProvider) handles(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, h := range p.Hosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// providerForHost returns the provider that serves host, or nil. Used where only a
// host is known (e.g. validating a redirect hop).
func providerForHost(host string) *chartProvider {
	for i := range chartProviders {
		if chartProviders[i].handles(host) {
			return &chartProviders[i]
		}
	}
	return nil
}

// allowedChartURL reports whether raw is an http(s) URL that some provider handles.
// This is the gate every caller-supplied download URL must pass.
func allowedChartURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return providerForHost(u.Hostname()) != nil
}
