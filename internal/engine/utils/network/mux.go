package network

import (
	"time"

	"github.com/xtaci/smux"
)

// Agreeing on a mux version, because smux will not agree on one itself.
//
// smux has no version negotiation whatsoever. Each end is configured with a
// number, every frame carries it, and the first frame whose number does not
// match what the reader expects tears the session down with "invalid protocol".
// There is no fallback and no error the operator ever sees on the tunnel: the
// control channel is not muxed, so it comes up perfectly, and then no data
// passes. A tunnel that reports itself connected and carries nothing is the
// hardest failure in this whole system to diagnose.
//
// That is why the default could not simply be raised to 2, which is otherwise
// worth doing — version 1 has no per-stream flow control at all, so
// MaxStreamBuffer is silently ignored and one heavy stream can take the whole
// session's credit while the others wait. Raising it would have broken every
// mux tunnel at the moment one side was upgraded and the other was not.
//
// So the version is settled on the control channel instead, before any mux
// session exists. The server decides and says so in its handshake answer; the
// client uses what it is told, whatever its own file says. One end deciding is
// what makes disagreement impossible — two ends each applying their own
// configuration is exactly how the sessions end up mismatched.
//
// A client too old to be told anything speaks the older handshake, which the
// server answers without a version, and both stay on 1 as they always did.

// MuxVersionAuto is the configured value that means "negotiate it". It is the
// zero value, so a config that never mentions mux_version negotiates.
const MuxVersionAuto = 0

// ResolveMuxVersion turns a configured value into the version the server will
// impose. Auto becomes 2: the question is only ever asked on a handshake the
// client had to be new to speak, and a new client uses what it is told.
func ResolveMuxVersion(configured int) int {
	if configured == 1 || configured == 2 {
		return configured
	}
	return 2
}

// ResolveStaticMuxVersion is the choice for every mux transport that cannot
// negotiate — KCP and xDi (whose control channel is itself a smux session, so
// there is no muxless channel to agree over) and the websocket-mux transports
// (which were simply never given a negotiation handshake). Both ends resolve
// independently and must land on the same number from the same config, which
// they do because the resolution is deterministic.
//
// Auto becomes 1, not 2, and the difference is the whole point: these transports
// used version 1 on the previous release, so a 1.7.0 end meeting a 1.6.5 end has
// to choose 1 or the two disagree and smux tears every session down. An operator
// who wants 2 sets it explicitly on both ends. (smux rejects any version that is
// not 1 or 2 outright — "unsupported protocol version" — which is what a bare
// auto value of 0 produced before this existed, and is the regression this
// prevents.)
//
// This is deliberately not ResolveMuxVersion: that one answers auto with 2,
// because it is only ever consulted on the tcp/tcpmux negotiation handshake,
// where the server imposes the version and a new client obeys. Here there is no
// one to impose it, so backward compatibility decides.
func ResolveStaticMuxVersion(configured int) int {
	if configured == 1 || configured == 2 {
		return configured
	}
	return 1
}

// The mux buffer defaults, written once for every place that builds a session.
//
// smux version 2 gives every stream a receive window of MaxStreamBuffer, and a
// sender may have no more than that many bytes unacknowledged on the stream.
// So one stream's throughput is capped at window / round-trip time whatever the
// link can carry: with the 64 KiB that used to be the default, a 150 ms path
// gave about 3.5 Mbit/s per connection, and a single download or speed test
// through the tunnel never got near the bandwidth the machines have. 1 MiB
// lifts that to about 56 Mbit/s per stream on the same path, which is what a
// user expects from one connection.
//
// What it costs: the window is a ceiling on how much one stream may have
// buffered unread, not memory held up front, and the session's receive buffer
// (DefaultMaxReceiveBuffer) caps the unread data of the whole session whatever
// the streams' windows add up to. So the worst case for a session stays
// DefaultMaxReceiveBuffer, and the stream window changes only how many bytes
// of that one stream may take. Four streams that are all backed up fill a
// session; a session with more of them simply stops reading until they drain.
// 4 MiB per session is kept (not raised) because a small VPS holds pool-size
// sessions of it, and 4 MiB is already four times the largest window.
//
// Wire compatibility: each end announces its own window to the peer, and each
// sender obeys the window its receiver announced, so an upgraded end opens a
// larger window to an old peer without the old peer having to understand
// anything; only the direction an old receiver is on stays at 64 KiB.
const (
	// DefaultMaxReceiveBuffer is the per-session receive budget.
	DefaultMaxReceiveBuffer = 4 << 20
	// DefaultMaxStreamBuffer is the per-stream receive window.
	DefaultMaxStreamBuffer = 1 << 20
)

// ResolveMuxBuffers fills in whichever of the two buffers a configuration left
// unset (zero or less) and returns the pair to use. A value that is set is
// never touched.
//
// smux refuses a session whose stream window is larger than its receive
// buffer, so the defaults are derived to always satisfy that: a stream window
// that was only defaulted is held to the session buffer, and a session buffer
// that was only defaulted is raised to a stream window the operator chose
// explicitly. A pair the operator set in conflict is returned as is, and smux
// reports it when the session is built, exactly as it did before.
func ResolveMuxBuffers(receive, stream int) (int, int) {
	switch {
	case receive <= 0 && stream <= 0:
		return DefaultMaxReceiveBuffer, DefaultMaxStreamBuffer
	case receive <= 0:
		return max(DefaultMaxReceiveBuffer, stream), stream
	case stream <= 0:
		return receive, min(DefaultMaxStreamBuffer, receive)
	}
	return receive, stream
}

// MuxSettings is the tuning both ends apply once they agree on a version.
type MuxSettings struct {
	MaxFrameSize     int
	MaxReceiveBuffer int
	MaxStreamBuffer  int
}

// SmuxConfig builds a session configuration for a settled version.
func SmuxConfig(version int, s MuxSettings) *smux.Config {
	return &smux.Config{
		Version:           version,
		KeepAliveInterval: 20 * time.Second,
		KeepAliveTimeout:  40 * time.Second,
		MaxFrameSize:      s.MaxFrameSize,
		MaxReceiveBuffer:  s.MaxReceiveBuffer,
		MaxStreamBuffer:   s.MaxStreamBuffer,
	}
}
