package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type probeResult int

const (
	probeFree  probeResult = iota // connection refused — nobody listening
	probeOurs                     // chartplotter answered /api/health
	probeOther                    // something answered, but not chartplotter
)

var probeClient = &http.Client{Timeout: 2 * time.Second}

// probe classifies port: a 200 /api/health whose body says
// "app":"chartplotter" — or the pre-field shape "ok":true — is ours (adoptable);
// refused means free; anything else means the port belongs to someone else.
func probe(port int) probeResult {
	resp, err := probeClient.Get(fmt.Sprintf("http://127.0.0.1:%d/api/health", port))
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) && errors.Is(err, errConnRefused) {
			return probeFree
		}
		return probeOther
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != http.StatusOK {
		return probeOther
	}
	s := string(body)
	if strings.Contains(s, `"app":"chartplotter"`) || strings.Contains(s, `"ok":true`) {
		return probeOurs
	}
	return probeOther
}
