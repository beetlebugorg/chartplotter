package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type connResp struct {
	OK          bool            `json:"ok"`
	Error       string          `json:"error"`
	Connection  connectionDTO   `json:"connection"`
	Connections []connectionDTO `json:"connections"`
}

func doReq(t *testing.T, method, url, body string) (int, connResp) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", jsonCT)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out connResp
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestConnections_CRUDAndPersistence(t *testing.T) {
	dir := t.TempDir()
	s := New("", dir, dir, true, "")
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()

	// Create (disabled so no real dial happens in the test).
	code, r := doReq(t, http.MethodPost, ts.URL+"/api/connections",
		`{"name":"Mux","host":"10.0.0.20","port":2000,"enabled":false}`)
	require.Equal(t, http.StatusOK, code)
	require.True(t, r.OK)
	id := r.Connection.Source.ID
	require.NotEmpty(t, id)
	assert.Equal(t, nmea.TransportTCPClient, r.Connection.Source.Transport) // defaulted
	assert.Equal(t, "nmea0183", r.Connection.Source.Protocol)               // defaulted
	assert.Equal(t, nmea.StateDisabled, r.Connection.Status.State)

	// Persisted to connections.json.
	_, err := os.Stat(filepath.Join(dir, "connections.json"))
	require.NoError(t, err)

	// List shows it.
	code, r = doReq(t, http.MethodGet, ts.URL+"/api/connections", "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, r.Connections, 1)

	// Update (rename + report new port).
	code, r = doReq(t, http.MethodPut, ts.URL+"/api/connections/"+id,
		`{"name":"Mux2","host":"10.0.0.20","port":3000,"enabled":false}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "Mux2", r.Connection.Source.Name)
	assert.Equal(t, 3000, r.Connection.Source.Port)

	// Delete.
	code, _ = doReq(t, http.MethodDelete, ts.URL+"/api/connections/"+id, "")
	require.Equal(t, http.StatusOK, code)
	code, r = doReq(t, http.MethodGet, ts.URL+"/api/connections", "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, r.Connections, 0)
}

func TestConnections_Validation(t *testing.T) {
	dir := t.TempDir()
	s := New("", dir, dir, true, "")
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()

	code, r := doReq(t, http.MethodPost, ts.URL+"/api/connections", `{"name":"x","port":2000}`)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, r.Error, "host")

	code, r = doReq(t, http.MethodPost, ts.URL+"/api/connections",
		`{"host":"h","port":2000,"transport":"serial"}`)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, r.Error, "transport")
}

func TestConnections_PersistAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	s1 := New("", dir, dir, true, "")
	ts1 := httptest.NewServer(s1)
	code, _ := doReq(t, http.MethodPost, ts1.URL+"/api/connections",
		`{"name":"Mux","host":"h","port":2000,"enabled":false}`)
	require.Equal(t, http.StatusOK, code)
	ts1.Close()
	s1.Close()

	// A fresh Server over the same dataDir reloads the connection.
	s2 := New("", dir, dir, true, "")
	defer s2.Close()
	ts2 := httptest.NewServer(s2)
	defer ts2.Close()
	code, r := doReq(t, http.MethodGet, ts2.URL+"/api/connections", "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, r.Connections, 1)
	assert.Equal(t, "Mux", r.Connections[0].Source.Name)
}

func TestVessel_SnapshotAndStream(t *testing.T) {
	dir := t.TempDir()
	s := New("", dir, dir, true, "")
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()

	// Feed the shared store directly (the manager path is covered in nmea tests).
	sent, err := nmea.ParseSentence("$GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W*6A")
	require.NoError(t, err)
	s.vessel.Apply(&nmea.Parser{}, sent)

	// Snapshot.
	resp, err := http.Get(ts.URL + "/api/vessel")
	require.NoError(t, err)
	var vs nmea.VesselState
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&vs))
	resp.Body.Close()
	require.NotNil(t, vs.Navigation.Position)
	assert.InDelta(t, 48.1173, vs.Navigation.Position.Lat, 1e-4)

	// Stream: read the first SSE event and confirm it carries the state.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/vessel/stream", nil)
	sresp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer sresp.Body.Close()

	sc := bufio.NewScanner(sresp.Body)
	got := false
	for sc.Scan() {
		line := sc.Text()
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var streamed nmea.VesselState
		require.NoError(t, json.Unmarshal([]byte(data), &streamed))
		if streamed.Navigation.Position != nil {
			got = true
		}
		break
	}
	assert.True(t, got, "first SSE event should carry the vessel state")
}
