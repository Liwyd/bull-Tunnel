package cmd

import (
	"testing"

	"github.com/xtaci/smux"

	"github.com/Liwyd/bull-Tunnel/internal/engine/config"
	"github.com/Liwyd/bull-Tunnel/internal/engine/utils/network"
)

// A tunnel whose file says nothing about the mux gets a 1 MiB stream window, a
// 4 MiB session budget and 64 streams a session.
//
// The 64 KiB stream window these used to be is what capped one connection at
// 64 KiB per round trip (about 3.5 Mbit/s at 150 ms), and the 8 streams a
// session, with the pool's 32-session ceiling, is what refused the 257th
// simultaneous connection. The numbers are asserted so that lowering one has to
// be done on purpose.
func TestTheMuxDefaultsAreTheThroughputOnes(t *testing.T) {
	cfg := &config.Config{}
	applyDefaults(cfg)

	for side, got := range map[string]struct{ recv, stream int }{
		"server": {cfg.Server.MaxReceiveBuffer, cfg.Server.MaxStreamBuffer},
		"client": {cfg.Client.MaxReceiveBuffer, cfg.Client.MaxStreamBuffer},
	} {
		if got.stream != 1<<20 {
			t.Errorf("%s stream window defaults to %d, want 1 MiB", side, got.stream)
		}
		if got.recv != 4<<20 {
			t.Errorf("%s session buffer defaults to %d, want 4 MiB", side, got.recv)
		}
		if got.stream > got.recv {
			t.Errorf("%s stream window %d exceeds the session buffer %d, which smux refuses", side, got.stream, got.recv)
		}
	}
	if cfg.Server.MuxCon != 64 {
		t.Errorf("mux_con defaults to %d, want 64", cfg.Server.MuxCon)
	}

	// What the defaults build has to be a session smux accepts, on both versions.
	for _, version := range []int{1, 2} {
		s := network.MuxSettings{
			MaxFrameSize:     cfg.Server.MaxFrameSize,
			MaxReceiveBuffer: cfg.Server.MaxReceiveBuffer,
			MaxStreamBuffer:  cfg.Server.MaxStreamBuffer,
		}
		if err := smux.VerifyConfig(network.SmuxConfig(version, s)); err != nil {
			t.Errorf("the default mux settings are refused by smux on version %d: %v", version, err)
		}
	}
}

// What the operator wrote is never replaced, and a default never produces a
// pair smux refuses.
func TestExplicitMuxValuesWin(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.MaxStreamBuffer = 262144
	cfg.Server.MaxReceiveBuffer = 2 << 20
	cfg.Server.MuxCon = 12
	cfg.Client.MaxStreamBuffer = 131072 // receive buffer left to default
	applyDefaults(cfg)

	if cfg.Server.MaxStreamBuffer != 262144 || cfg.Server.MaxReceiveBuffer != 2<<20 || cfg.Server.MuxCon != 12 {
		t.Errorf("explicit values were changed: stream %d receive %d mux_con %d",
			cfg.Server.MaxStreamBuffer, cfg.Server.MaxReceiveBuffer, cfg.Server.MuxCon)
	}
	if cfg.Client.MaxStreamBuffer != 131072 || cfg.Client.MaxReceiveBuffer != 4<<20 {
		t.Errorf("a stream window set on its own was changed, or the buffer beside it was not defaulted: stream %d receive %d",
			cfg.Client.MaxStreamBuffer, cfg.Client.MaxReceiveBuffer)
	}

	// A session buffer smaller than the new stream default, set on its own: the
	// stream window follows it down instead of leaving smux a pair it refuses.
	small := &config.Config{}
	small.Server.MaxReceiveBuffer = 256 << 10
	applyDefaults(small)
	if small.Server.MaxReceiveBuffer != 256<<10 || small.Server.MaxStreamBuffer != 256<<10 {
		t.Errorf("a small session buffer set alone gave receive %d stream %d, want both 256 KiB",
			small.Server.MaxReceiveBuffer, small.Server.MaxStreamBuffer)
	}
}
