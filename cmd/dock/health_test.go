package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// probe must classify a closed port as free — the refused-connection detection
// is per-OS (ECONNREFUSED vs WSAECONNREFUSED; see errno_windows.go), and
// misclassifying refusal as probeOther makes the dock reject every port in
// 8080..8090 and never spawn the server.
func TestProbeClosedPortIsFree(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close() // now nothing listens there

	if got := probe(port); got != probeFree {
		t.Fatalf("probe(closed port %d) = %v, want probeFree", port, got)
	}
}

func TestProbeClassifiesListeners(t *testing.T) {
	cases := []struct {
		name string
		body string
		want probeResult
	}{
		{"ours", `{"app":"chartplotter","ok":true}`, probeOurs},
		{"other", `{"hello":"world"}`, probeOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			port := srv.Listener.Addr().(*net.TCPAddr).Port
			if got := probe(port); got != tc.want {
				t.Fatalf("probe(%s listener) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
