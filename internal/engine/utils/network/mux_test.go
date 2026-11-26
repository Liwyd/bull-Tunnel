package network

import "testing"

// Auto has to resolve to 2, because it is only ever consulted on a handshake
// the peer had to be new to speak — and a new peer does as it is told. An
// explicit choice is left alone.
func TestResolveMuxVersion(t *testing.T) {
	for configured, want := range map[int]int{
		MuxVersionAuto: 2,
		1:              1,
		2:              2,
		// Out of range never reaches here (applyDefaults resets it), but
		// falling through to the negotiated answer is the safe reading.
		7:  2,
		-1: 2,
	} {
		if got := ResolveMuxVersion(configured); got != want {
			t.Errorf("ResolveMuxVersion(%d) = %d, want %d", configured, got, want)
		}
	}
}

// The version has to reach the smux config unchanged. Everything else about
// this scheme rests on the two ends building sessions on the same number, and
// smux tears down any session where they do not.
func TestSmuxConfigCarriesTheVersionAndTuning(t *testing.T) {
	s := MuxSettings{MaxFrameSize: 4096, MaxReceiveBuffer: 1 << 20, MaxStreamBuffer: 1 << 16}

	for _, version := range []int{1, 2} {
		cfg := SmuxConfig(version, s)
		if cfg.Version != version {
			t.Errorf("SmuxConfig(%d) built version %d", version, cfg.Version)
		}
		if cfg.MaxFrameSize != s.MaxFrameSize {
			t.Errorf("frame size %d, want %d", cfg.MaxFrameSize, s.MaxFrameSize)
		}
		if cfg.MaxReceiveBuffer != s.MaxReceiveBuffer {
			t.Errorf("receive buffer %d, want %d", cfg.MaxReceiveBuffer, s.MaxReceiveBuffer)
		}
		if cfg.MaxStreamBuffer != s.MaxStreamBuffer {
			t.Errorf("stream buffer %d, want %d", cfg.MaxStreamBuffer, s.MaxStreamBuffer)
		}
		// smux validates its own config on use; a version it rejects would
		// surface only when a session was built, which is far too late.
		if cfg.KeepAliveTimeout <= cfg.KeepAliveInterval {
			t.Error("keepalive timeout must exceed the interval or smux refuses the config")
		}
	}
}

// KCP and xDi cannot negotiate, so auto must resolve to the version the
// previous release used — 1 — or an upgraded end and a 1.6.5 end disagree and
// smux tears every session down. This is the regression that shipped a working
// tunnel that then failed to create any mux session.
func TestResolveStaticMuxVersion(t *testing.T) {
	for configured, want := range map[int]int{
		MuxVersionAuto: 1, // auto (0) → the historical KCP default, NOT 2
		1:              1,
		2:              2,
		7:              1, // out of range falls back to the safe default
	} {
		if got := ResolveStaticMuxVersion(configured); got != want {
			t.Errorf("ResolveStaticMuxVersion(%d) = %d, want %d", configured, got, want)
		}
	}

	// The value it returns must always be one smux accepts, whatever it is fed.
	for _, in := range []int{-5, 0, 1, 2, 3, 99} {
		if v := ResolveStaticMuxVersion(in); v != 1 && v != 2 {
			t.Fatalf("ResolveStaticMuxVersion(%d) = %d, which smux rejects", in, v)
		}
	}
}

// The buffer defaults are the ones that lift the per-stream throughput cap, and
// they always form a pair smux accepts.
func TestResolveMuxBuffers(t *testing.T) {
	const kib, mib = 1 << 10, 1 << 20
	for _, tc := range []struct {
		name                 string
		receive, stream      int
		wantRecv, wantStream int
	}{
		{"nothing set", 0, 0, 4 * mib, 1 * mib},
		{"negative is unset", -1, -5, 4 * mib, 1 * mib},
		{"both explicit are untouched", 8 * mib, 2 * mib, 8 * mib, 2 * mib},
		{"explicit stream, default receive", 0, 64 * kib, 4 * mib, 64 * kib},
		{"explicit stream above the default receive raises it", 0, 8 * mib, 8 * mib, 8 * mib},
		{"explicit receive, default stream", 16 * mib, 0, 16 * mib, 1 * mib},
		{"explicit small receive holds the default stream down", 256 * kib, 0, 256 * kib, 256 * kib},
		{"an explicit conflict is left for smux to report", 64 * kib, 128 * kib, 64 * kib, 128 * kib},
	} {
		r, s := ResolveMuxBuffers(tc.receive, tc.stream)
		if r != tc.wantRecv || s != tc.wantStream {
			t.Errorf("%s: ResolveMuxBuffers(%d, %d) = (%d, %d), want (%d, %d)",
				tc.name, tc.receive, tc.stream, r, s, tc.wantRecv, tc.wantStream)
		}
	}
	if DefaultMaxStreamBuffer != 1<<20 || DefaultMaxReceiveBuffer != 4<<20 {
		t.Errorf("the defaults moved: stream %d receive %d", DefaultMaxStreamBuffer, DefaultMaxReceiveBuffer)
	}
}
