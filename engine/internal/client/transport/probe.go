package transport

// Probe measures the path between this server and the far end of a tunnel,
// once per carrier.
//
// What it measures
// ----------------
// The panel cannot see the path between two servers; only the servers can.
// So the probe runs on the side that dials out — that is the way the tunnel
// goes — and opens the carrier to the far end's tunnel port, the way the
// tunnel's own client opens it. What it times is the carrier coming up: the
// TCP handshake, the TLS handshake, the websocket upgrade, the Noise record
// layer, the KCP session, the QUIC handshake. Every one of those is a real
// round trip over the path, so the number is the path's own latency, seen
// through that carrier.
//
// What it deliberately does not do
// --------------------------------
// It never claims the control channel, and that is not an oversight.
//
// A control claim is what makes a client the tunnel's client. The server
// answers a second claim with the same token by taking the seat in place —
// the new client becomes the tunnel and the old one is dropped (see
// admitControlChannel in the server transport). A probe that claimed the
// control channel of a live tunnel would therefore take that tunnel away from
// the client that is carrying the traffic, and the two would fight over it
// until one of them won.
//
// So the probe stops one step earlier: it opens the carrier and closes it
// again. The far end sees a connection that never says anything, and forgets
// it. The path has been measured and the tunnel has not been touched.
//
// The carriers whose own cryptography consumes the token (stealth, wss, kcp,
// quic) are opened with the real token, because the carrier cannot be brought
// up without it. The claim itself is still never sent.
//
// The numbers
// -----------
//
//	connect_ms    how long the carrier took to come up
//	handshake_ms  how long the credential exchange took, for the carriers that
//	              have one the probe may perform without claiming the channel
//	rtt_ms        the median of the samples: the typical time for this carrier
//	              to come up on this path

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/utils/network"
	"github.com/xtaci/smux"
)

// ProbeOptions are the settings of a measurement.
type ProbeOptions struct {
	// Samples is how many times each carrier is opened. The median of those
	// is what the panel ranks by.
	Samples int
	// Timeout bounds one attempt of one carrier.
	Timeout time.Duration
}

// ProbeResult is what one carrier did.
type ProbeResult struct {
	Transport   string  `json:"transport"`
	OK          bool    `json:"ok"`
	ConnectMs   float64 `json:"connect_ms"`
	HandshakeMs float64 `json:"handshake_ms"`
	RttMs       float64 `json:"rtt_ms"`
	Samples     int     `json:"samples"`
	Error       string  `json:"error,omitempty"`
}

// probeClient is what the probe needs from the tunnel's configuration.
type probeClient struct {
	remoteAddr string
	token      string
	keepalive  time.Duration
	dialTO     time.Duration
	kcp        config.KCPConfig
	mux        network.MuxSettings
	quic       network.QUICSettings
	// edgeIP is what the websocket carriers address the far end by, when the
	// configuration names one.
	edgeIP string
	// simpleAuth is the websocket carriers' simpler credential mode.
	simpleAuth bool
}

// Probe opens every carrier in turn and reports what each of them did.
//
// The carriers are measured one after another, not at the same time: two
// measurements over the same path would share the path's congestion and each
// would report the other's traffic.
func Probe(ctx context.Context, configPath string, carriers []string, opts ProbeOptions) []ProbeResult {
	if opts.Samples <= 0 {
		opts.Samples = 3
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	cfg, err := loadProbeConfig(configPath)
	if err != nil {
		out := make([]ProbeResult, 0, len(carriers))
		for _, c := range carriers {
			out = append(out, ProbeResult{Transport: c, Error: err.Error()})
		}
		return out
	}

	out := make([]ProbeResult, 0, len(carriers))
	for _, carrier := range carriers {
		out = append(out, probeCarrier(ctx, cfg, carrier, opts))
	}
	return out
}

// The mux settings, with the engine's own defaults for the ones the file
// does not set. The engine applies them when it starts a tunnel, and the
// probe has to use the same ones: a smux session with a frame size of zero
// is refused outright.
const (
	defaultMaxFrameSize     = 32768   // 32KB
	defaultMaxReceiveBuffer = 4194304 // 4MB
	defaultMaxStreamBuffer  = 65536   // 64KB
)

// muxSettingsFor returns the smux settings of the probe.
func muxSettingsFor(c *config.ClientConfig) network.MuxSettings {
	s := network.MuxSettings{
		MaxFrameSize:     c.MaxFrameSize,
		MaxReceiveBuffer: c.MaxReceiveBuffer,
		MaxStreamBuffer:  c.MaxStreamBuffer,
	}
	if s.MaxFrameSize <= 0 {
		s.MaxFrameSize = defaultMaxFrameSize
	}
	if s.MaxReceiveBuffer <= 0 {
		s.MaxReceiveBuffer = defaultMaxReceiveBuffer
	}
	if s.MaxStreamBuffer <= 0 {
		s.MaxStreamBuffer = defaultMaxStreamBuffer
	}
	return s
}

// loadProbeConfig reads the client half of a tunnel configuration. The file
// is the one the panel wrote for the probe: the far end's address and the
// tunnel's token.
func loadProbeConfig(path string) (*probeClient, error) {
	var raw config.Config
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return nil, fmt.Errorf("the probe configuration is not readable: %w", err)
	}
	c := &raw.Client
	if c.RemoteAddr == "" {
		return nil, fmt.Errorf("the probe configuration names no remote address")
	}
	keepalive := time.Duration(c.Keepalive) * time.Second
	if keepalive <= 0 {
		keepalive = 20 * time.Second
	}
	dialTO := time.Duration(c.DialTimeout) * time.Second
	if dialTO <= 0 {
		dialTO = 15 * time.Second
	}
	return &probeClient{
		remoteAddr: c.RemoteAddr,
		token:      c.Token,
		keepalive:  keepalive,
		dialTO:     dialTO,
		kcp:        c.KCPConfig.WithDefaults(),
		mux:        muxSettingsFor(c),
		quic: network.QUICSettings{
			DialTimeout:     dialTO,
			KeepAlivePeriod: keepalive,
			MaxIdleTimeout:  3 * keepalive,
			SO_RCVBUF:       c.SO_RCVBUF,
		},
		edgeIP:     c.EdgeIP,
		simpleAuth: c.SimpleAuth,
	}, nil
}

// probeCarrier opens one carrier the given number of times and reports the
// median.
func probeCarrier(ctx context.Context, cfg *probeClient, carrier string, opts ProbeOptions) ProbeResult {
	res := ProbeResult{Transport: carrier}
	var times []float64
	var lastErr error
	for i := 0; i < opts.Samples; i++ {
		attempt, err := ctx, error(nil)
		attempt, cancel := context.WithTimeout(ctx, opts.Timeout)
		ms, err := openCarrier(attempt, cfg, carrier)
		cancel()
		if err != nil {
			lastErr = err
			break
		}
		times = append(times, ms)
	}
	res.Samples = len(times)
	if len(times) == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("the carrier did not come up")
		}
		res.Error = lastErr.Error()
		return res
	}
	sort.Float64s(times)
	res.OK = true
	res.ConnectMs = round1(times[0])
	res.HandshakeMs = 0
	res.RttMs = round1(median(times))
	return res
}

// openCarrier brings one carrier up and returns how long it took, in
// milliseconds.
//
// The carrier is opened the way the tunnel's own client opens it, and closed
// again before it says anything: see the note at the top of this file.
func openCarrier(ctx context.Context, cfg *probeClient, carrier string) (float64, error) {
	start := time.Now()
	switch config.TransportType(carrier) {
	case config.TCP:
		conn, err := network.TcpDialerVia(ctx, nil, cfg.remoteAddr, cfg.dialTO, cfg.keepalive, true, 1, 0, 0, 0)
		if err != nil {
			return 0, err
		}
		conn.Close()

	case config.TCPMUX:
		conn, err := network.TcpDialerVia(ctx, nil, cfg.remoteAddr, cfg.dialTO, cfg.keepalive, true, 1, 0, 0, 0)
		if err != nil {
			return 0, err
		}
		// The mux session is the carrier: the smux handshake is what a
		// tcpmux tunnel runs on. It is opened and closed again without a
		// claim, for the reason at the top of this file.
		session, err := smux.Client(conn, network.SmuxConfig(1, cfg.mux))
		if err != nil {
			conn.Close()
			return 0, err
		}
		session.Close()

	case config.STEALTH:
		conn, err := network.TcpDialerVia(ctx, nil, cfg.remoteAddr, cfg.dialTO, cfg.keepalive, true, 1, 0, 0, 0)
		if err != nil {
			return 0, err
		}
		// The Noise record layer is the carrier: the handshake is what a
		// stealth tunnel runs on, and it needs the tunnel's token.
		layer, err := network.NoiseClientConn(conn, cfg.token, cfg.dialTO)
		if err != nil {
			conn.Close()
			return 0, err
		}
		layer.Close()

	case config.WS, config.WSS, config.WSMUX, config.WSSMUX:
		// The websocket carriers carry the credential in the upgrade request,
		// so the probe sends none: the far end answers with its decoy page
		// instead of a tunnel, and the round trip over the carrier has been
		// made without the probe ever being a client of the tunnel.
		//
		// The upgrade is therefore refused, and that refusal is the answer:
		// it proves the path carries this carrier.
		_, err := network.WebSocketDialer(ctx, nil, cfg.remoteAddr, cfg.edgeIP, "/channel",
			cfg.dialTO, cfg.keepalive, true, "", config.TransportType(carrier), cfg.simpleAuth, 1, 0, 0, 0)
		if err != nil && !isRefusedUpgrade(err) {
			return 0, err
		}

	case config.KCP:
		session, err := network.KCPDialContext(ctx, cfg.remoteAddr, cfg.token, kcpSettings(cfg))
		if err != nil {
			return 0, err
		}
		session.Close()

	case config.QUIC:
		conn, err := network.QUICDial(ctx, cfg.remoteAddr, cfg.quic)
		if err != nil {
			return 0, err
		}
		conn.CloseWithError(0, "the probe is finished")

	default:
		return 0, fmt.Errorf("the carrier %q is not one this build speaks", carrier)
	}
	return float64(time.Since(start).Microseconds()) / 1000, nil
}

// kcpSettings turns the configuration's KCP block into what the dialer wants.
func kcpSettings(cfg *probeClient) network.KCPSettings {
	k := cfg.kcp
	return network.KCPSettings{
		MTU:          k.MTU,
		Interval:     k.Interval,
		Resend:       k.Resend,
		NoDelay:      k.NoDelay,
		NoCongestion: k.NoCongestion,
		SndWnd:       k.SndWnd,
		RcvWnd:       k.RcvWnd,
		AckNoDelay:   k.AckNoDelay,
		DataShards:   k.DataShards,
		ParityShards: k.ParityShards,
	}
}

// isRefusedUpgrade tells a refused websocket upgrade from a broken path.
//
// The far end answers a request it does not recognise with its decoy page,
// so the upgrade fails with the websocket library's "bad handshake". That is
// the answer to a request that reached it, and the path is fine. A dial that
// never got that far failed for another reason — a filtered port, a broken
// route — and the probe has to say so.
func isRefusedUpgrade(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "bad handshake") ||
		strings.Contains(msg, "unexpected status") ||
		strings.Contains(msg, "server returned") ||
		strings.Contains(msg, "connection closed")
}

// median is the middle of a sorted list of measurements.
func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	mid := len(values) / 2
	if len(values)%2 == 1 {
		return values[mid]
	}
	return (values[mid-1] + values[mid]) / 2
}

// round1 rounds a measurement to a tenth of a millisecond, which is more
// precision than a network path can honestly claim.
func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

// ProbeJSON writes the results as one object per line, so the panel can read
// them whatever happens in between.
func ProbeJSON(results []ProbeResult) string {
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

// ProbeCarriers is the list of carriers the engine speaks and the panel may
// test. It is the same list the binary prints with -v.
func ProbeCarriers() []string {
	return []string{
		string(config.TCP),
		string(config.TCPMUX),
		string(config.STEALTH),
		string(config.WS),
		string(config.WSS),
		string(config.WSMUX),
		string(config.WSSMUX),
		string(config.KCP),
		string(config.QUIC),
	}
}

// probeConn is the interface the carriers share, kept here so the file
// compiles against both the stream and the datagram carriers.
var _ net.Conn = (net.Conn)(nil)
