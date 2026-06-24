package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowedChartURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://charts.noaa.gov/ENCs/US5MD1MC.zip", true},
		{"https://www.charts.noaa.gov/ENCs/All_ENCs.zip", true}, // subdomain
		{"http://ienccloud.us/foo.zip", true},
		{"https://charts.noaa.gov.evil.com/x.zip", false}, // suffix spoof
		{"https://evil-charts.noaa.gov/x.zip", false},     // prefix spoof
		{"https://evil.com/x.zip", false},
		{"http://169.254.169.254/latest/meta-data/", false}, // cloud metadata SSRF
		{"http://127.0.0.1:8080/api/settings", false},       // loopback SSRF
		{"file:///etc/passwd", false},                       // non-http scheme
		{"ftp://charts.noaa.gov/x", false},
		{"", false},
		{"not a url", false},
	}
	for _, c := range cases {
		if got := allowedChartURL(c.url); got != c.want {
			t.Errorf("allowedChartURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestCrossSiteWrite(t *testing.T) {
	mk := func(method string, hdr map[string]string) *http.Request {
		r := httptest.NewRequest(method, "http://127.0.0.1:8080/api/settings", nil)
		r.Host = "127.0.0.1:8080"
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		return r
	}
	cases := []struct {
		name string
		req  *http.Request
		want bool // want blocked
	}{
		{"GET always allowed", mk("GET", map[string]string{"Sec-Fetch-Site": "cross-site"}), false},
		{"same-origin write allowed", mk("POST", map[string]string{"Sec-Fetch-Site": "same-origin"}), false},
		{"cross-site write blocked", mk("POST", map[string]string{"Sec-Fetch-Site": "cross-site"}), true},
		{"same-site write blocked", mk("POST", map[string]string{"Sec-Fetch-Site": "same-site"}), true},
		{"no headers (non-browser) allowed", mk("POST", nil), false},
		{"matching Origin allowed", mk("POST", map[string]string{"Origin": "http://127.0.0.1:8080"}), false},
		{"foreign Origin blocked", mk("POST", map[string]string{"Origin": "https://evil.com"}), true},
	}
	for _, c := range cases {
		if got := crossSiteWrite(c.req); got != c.want {
			t.Errorf("%s: crossSiteWrite = %v, want %v", c.name, got, c.want)
		}
	}
}

// A cross-site POST to a state-changing endpoint must be rejected before any
// handler runs, and security headers must be present on every response.
func TestHandlerBlocksCrossSiteAndSetsHeaders(t *testing.T) {
	s := New("", t.TempDir(), t.TempDir(), false)
	defer s.Close()

	r := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/settings", nil)
	r.Host = "127.0.0.1:8080"
	r.Header.Set("Origin", "https://evil.com")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site POST: status = %d, want 403", w.Code)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
}

// The proxy must refuse a non-provider URL (SSRF).
func TestProxyRejectsNonProviderURL(t *testing.T) {
	s := New("", t.TempDir(), t.TempDir(), false)
	defer s.Close()

	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/proxy?url=http://169.254.169.254/", nil)
	r.Host = "127.0.0.1:8080"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("SSRF proxy: status = %d, want 400", w.Code)
	}
}
