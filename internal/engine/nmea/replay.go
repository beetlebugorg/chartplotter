package nmea

import (
	"bufio"
	"io"
	"strings"
)

// ReplayStats summarizes a replay/ingest pass: useful for tests now and for the
// connection status badges (rate, errors, sentence types seen) later.
type ReplayStats struct {
	Lines  int            // non-empty lines read
	Parsed int            // sentences framed without error
	Errors int            // framing/checksum failures
	ByType map[string]int // count per sentence type (RMC, GGA…)
}

// Replay reads line-framed NMEA0183 from r, applying each recognized sentence to
// vs, and returns ingest stats. It is the headless harness behind the unit tests
// and the basis of the future File-replay transport. Framing errors are counted,
// not fatal. A nil parser uses a default (wall-clock) parser.
func Replay(r io.Reader, p *Parser, vs *VesselState) (ReplayStats, error) {
	if p == nil {
		p = &Parser{}
	}
	st := ReplayStats{ByType: map[string]int{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		st.Lines++
		s, err := ParseSentence(line)
		if err != nil {
			st.Errors++
			continue
		}
		st.Parsed++
		st.ByType[s.Type]++
		p.Apply(s, vs)
	}
	return st, sc.Err()
}
