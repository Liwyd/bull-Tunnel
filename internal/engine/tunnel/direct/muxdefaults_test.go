package direct

import "testing"

// A direct tunnel's per-stream window defaults to 1 MiB, not smux's 64 KiB,
// which would cap each forwarded connection at 64 KiB per round trip.
func TestDirectMuxDefaultsLiftTheStreamWindow(t *testing.T) {
	c := Config{Role: RoleOrigin, Addr: "0.0.0.0:7000", Token: "t"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.MaxStreamBuffer != 1<<20 || c.MaxReceiveBuffer != 4<<20 {
		t.Errorf("defaults are stream %d receive %d, want 1 MiB and 4 MiB", c.MaxStreamBuffer, c.MaxReceiveBuffer)
	}

	explicit := Config{Role: RoleOrigin, Addr: "0.0.0.0:7000", Token: "t", MaxStreamBuffer: 65536, MaxReceiveBuffer: 1 << 20}
	if err := explicit.Validate(); err != nil {
		t.Fatal(err)
	}
	if explicit.MaxStreamBuffer != 65536 || explicit.MaxReceiveBuffer != 1<<20 {
		t.Errorf("explicit values were replaced: stream %d receive %d", explicit.MaxStreamBuffer, explicit.MaxReceiveBuffer)
	}
}
