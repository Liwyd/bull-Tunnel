// Package probe measures the path between two servers.
//
// The panel wants to know which carrier is the best one between an Iran
// server and a foreign server right now. The answer cannot come from the
// panel: it has to come from the servers themselves, over the carrier.
//
// The probe runs on the side that dials out, because that is the way the
// tunnel goes. It opens the carrier, measures how long the setup took,
// completes the session handshake and then measures the round trip of the
// session with a few pings. The result is one small JSON object per
// carrier.
package probe

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/Liwyd/Bull-Tunn/engine/internal/carrier"
	"github.com/Liwyd/Bull-Tunn/engine/internal/config"
	"github.com/Liwyd/Bull-Tunn/engine/internal/proto"
)

// Result is what one carrier did.
type Result struct {
	Transport   string  `json:"transport"`
	OK          bool    `json:"ok"`
	ConnectMs   float64 `json:"connect_ms"`
	HandshakeMs float64 `json:"handshake_ms"`
	RttMs       float64 `json:"rtt_ms"`
	Samples     int     `json:"samples"`
	Error       string  `json:"error,omitempty"`
}

// Options are the settings of a measurement.
type Options struct {
	// Samples is the number of round trips that are measured.
	Samples int
	// Timeout bounds one carrier.
	Timeout time.Duration
	// WithSession tells whether the session handshake and the round trip
	// are measured too. Without a token only the setup of the carrier can
	// be measured.
	WithSession bool
}

// Measure tests one carrier and returns what it did.
func Measure(ctx context.Context, cfg *config.Client, transport string, opts Options) Result {
	res := Result{Transport: transport}
	if opts.Samples <= 0 {
		opts.Samples = 3
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 20 * time.Second
	}
	c, err := carrier.For(transport)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	carrierOpts := carrier.Options{
		Path:             cfg.Path,
		StealthPrefix:    cfg.StealthPrefix,
		TLSCertFile:      cfg.TLSCertFile,
		TLSKeyFile:       cfg.TLSKeyFile,
		TLSServerName:    cfg.TLSServerName,
		HandshakeTimeout: opts.Timeout,
	}

	start := time.Now()
	conn, err := c.Dial(cfg.RemoteAddr, carrierOpts)
	res.ConnectMs = ms(time.Since(start))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer conn.Close()
	if !opts.WithSession || cfg.Token == "" {
		res.OK = true
		return res
	}

	start = time.Now()
	sess, err := proto.ClientHandshake(conn, cfg.Token, proto.SessionOptions{
		MaxStreams: cfg.MuxStreams,
		Heartbeat:  cfg.Heartbeat,
	})
	res.HandshakeMs = ms(time.Since(start))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer sess.Close()

	var rtts []time.Duration
	for i := 0; i < opts.Samples; i++ {
		rtt, err := sess.Ping(ctx)
		if err != nil {
			res.Error = err.Error()
			break
		}
		rtts = append(rtts, rtt)
		time.Sleep(50 * time.Millisecond)
	}
	res.Samples = len(rtts)
	if len(rtts) > 0 {
		sort.Slice(rtts, func(i, j int) bool { return rtts[i] < rtts[j] })
		res.RttMs = ms(rtts[len(rtts)/2]) // the middle one
	}
	res.OK = res.Error == "" && len(rtts) > 0
	return res
}

// MeasureAll tests a list of carriers and returns the results, best first.
// The carriers are tested at the same time: the panel waits for the slowest
// one, not for the sum of all of them.
func MeasureAll(ctx context.Context, cfg *config.Client, transports []string, opts Options) []Result {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	out := make([]Result, len(transports))
	var wg sync.WaitGroup
	for i, t := range transports {
		wg.Add(1)
		go func(i int, t string) {
			defer wg.Done()
			out[i] = Measure(ctx, cfg, t, opts)
		}(i, t)
	}
	wg.Wait()
	sortResults(out)
	return out
}

// sortResults puts the working carriers first, the quickest of them at the
// top. The round trip is what matters for a tunnel; without a session the
// setup of the carrier is the only number there is.
func sortResults(out []Result) {
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.OK != b.OK {
			return a.OK
		}
		if a.OK {
			if a.RttMs != b.RttMs {
				return a.RttMs < b.RttMs
			}
			return a.ConnectMs < b.ConnectMs
		}
		return false
	})
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// JSON writes the results as one object per line, so the panel can read
// them whatever happens in between.
func JSON(results []Result) string {
	out := ""
	for _, r := range results {
		blob, err := json.Marshal(r)
		if err != nil {
			continue
		}
		out += string(blob) + "\n"
	}
	return out
}
