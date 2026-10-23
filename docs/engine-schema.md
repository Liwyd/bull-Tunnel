# The tunnel engine contract

bull-Tunnel is a **control plane**. The tunnels themselves are carried by a
separate program — the *tunnel engine* — that runs on both ends of every
tunnel. The panel never sits in the traffic path.

The panel does not care which build of the engine runs on a server, as long
as that build obeys this contract. The engine that ships with bull-Tunnel is
its own: one static binary that runs on the servers of the tunnel, wherever
they are. It is an AGPL-3.0 engine, inherited from an open-source project
and vendored unchanged under `internal/engine/`; the credit is in the
NOTICE (`internal/engine/NOTICE`), and the licence of the engine is
the AGPL-3.0 (`internal/engine/LICENSE`).

```
/etc/bull-tunnel/<name>.toml                 config
/etc/bull-tunnel/<name>.metrics.json         written by the engine
/etc/systemd/system/bull-tunnel-<name>.service
```

## Command line

```
bull-tunnel-engine -c /etc/bull-tunnel/name.toml     run the tunnel
bull-tunnel-engine -c name.toml -check            read the file, report the problem, exit
bull-tunnel-engine probe -c name.toml            measure the path, one line per carrier
bull-tunnel-engine agent -c /etc/bull-tunnel/agent.toml   the command channel
bull-tunnel-engine -v                             print the version and the carriers
```

`-v` prints two lines, and the panel reads both:

```
bull-tunnel-engine 1.0.0
transports: tcp,tcpmux,stealth,ws,wss,wsmux,wssmux,kcp,quic
```

The second line is the list of the carriers this build speaks. The panel
reads it from the server and never tests a carrier that is not on it.

The engine must exit non-zero when it cannot start, so the panel sees a
broken configuration instead of a crash loop.

## Config file

One file holds one role. A *server* section is the side that waits for the
other end and publishes the forwarded ports; a *client* section is the side
that dials out. The panel writes one file per side, with the same name on
both servers.

```toml
[server]
bind_addr = "0.0.0.0:3080"     # the tunnel port: the port the far end dials
transport = "tcpmux"           # the carrier, see below
token = "6f1c…(32 hex chars)"  # shared secret: both ends must match
accept_udp = false             # forward UDP as well as TCP
log_level = "info"
ports = [
  "127.0.0.1:61000=127.0.0.1:22",  # the health probe (loopback only)
  "443",                           # this server :443 -> the far end :443
  "8443=2096",                     # this server :8443 -> the far end :2096
  "8443=1.2.3.4:5201",             # this server :8443 -> another host
  "2053-2060",                     # a range, every port to the same number
]

[client]
remote_addr = "1.2.3.4:3080"    # where the tunnel port of the other side is
transport = "tcpmux"
token = "6f1c…(the same one)"
connection_pool = 8
log_level = "info"
```

### Port mapping syntax

| Mapping | Meaning |
|---|---|
| `443` | this server `:443` → the far end `127.0.0.1:443` |
| `8443=2096` | this server `:8443` → the far end `:2096` |
| `8443=1.2.3.4:5201` | this server `:8443` → `1.2.3.4:5201` (the exit is another host) |
| `2053-2060` | a range, every port to the same number on the far end |
| `443-600:5201` | a range, all of it to one port |
| `127.0.0.1:61000=127.0.0.1:22` | a mapping that only listens on the loopback: the health probe |

`accept_udp = true` makes the engine forward datagrams of the same ports as
well. UDP cannot be probed with a TCP probe, so the panel marks such a
tunnel as *not verified* and falls back to the socket table.

### Keys the panel does not own

When the panel rewrites a config it did not write itself, every key it does
not manage is preserved (`mux_con`, `heartbeat`, …). The keys it does manage
are `transport`, `token`, `ports`, `accept_udp`, `bind_addr`/`remote_addr`
and the tunnel port. The engine ignores the keys it does not know.

## Metrics file

The engine writes a small JSON file next to its config, updated once a
second:

```json
{"connected": true, "transports": ["tcp", "tcpmux", "…"], "uptime": 4213,
 "rx_bytes": 918273, "tx_bytes": 8127344, "sessions": 2, "streams": 3}
```

`connected` says whether a client is attached right now. `transports` says
which carriers this build speaks: **the panel never tests a carrier the
engine does not have**, which saves a failing test on every check.

## Health probe

A tunnel the panel creates gets one extra mapping on the *server* side:

```
127.0.0.1:<probe port> = 127.0.0.1:<ssh port of the far end>
```

The probe port is bound on the loopback only, so it is not reachable from
the internet. Reading the SSH banner from it proves the whole path: the
packet leaves the Iran server, travels the tunnel, and comes back from the
SSH daemon of the far end. A tunnel that answers this probe is healthy in
the only sense that matters.

A tunnel that was found on a server (adopted, not created by the panel) has
no probe until the panel rewrites its config for the first time. Until then
its health is inferred from the engine's own report or from the socket
table, and the dashboard marks it as *not verified*.

## The carriers

The carrier is the transport under the session. The session is always
encrypted and authenticated, so a carrier only has to move bytes.

| Transport | Carrier | Notes |
|---|---|---|
| `tcp` | plain TCP | the simplest and the fastest |
| `tcpmux` | TCP | the same carrier: the session is always multiplexed |
| `ws` | WebSocket over HTTP/1.1 | survives proxies that speak HTTP |
| `wsmux` | WebSocket | the same carrier |
| `wss` | WebSocket over TLS | confidentiality on top of the session |
| `wssmux` | WebSocket over TLS | the same carrier |
| `stealth` | a Noise record layer | no TLS fingerprint and no recognisable handshake: on the wire it is indistinguishable from random |
| `kcp` | a reliable protocol over UDP | survives a path where a long-lived TCP flow stalls |
| `quic` | QUIC streams over UDP | brings its own TLS 1.3, multiplexing and loss recovery |

`udp` carries datagrams only and cannot be probed with a TCP probe, so the
panel does not offer it. The experimental carriers of the engine (`xdi`,
`pck`, `spoof`) need raw sockets and root, so they are never offered to
an unattended repair.

The panel measures the carriers and keeps the fastest one that works, but
it never switches away from a working tunnel on its own: a change is made
only after several failed checks in a row, and traffic returns to the first
choice after it has been stable for ten minutes.

## The session

Whatever the carrier, the two ends speak the same protocol on top of it.

```
client                                    server
  |  CLIENT_HELLO  (version, nonce, X25519 public key, tag)   |
  |---------------------------------------------------------->|
  |  SERVER_HELLO  (version, nonce, X25519 public key, tag)   |
  |<----------------------------------------------------------|
  |  encrypted frames: OPEN, DATA, CLOSE, WINDOW, PING       |
  |<------------------------------------------------------->|
```

* **Authentication.** The shared `token` is the key of an HMAC-SHA256 that
  signs the whole handshake. A peer that does not know the token cannot
  produce a valid tag, so the session never opens.
* **Confidentiality and forward secrecy.** The traffic keys come from an
  X25519 exchange (ephemeral on both sides) and are stretched with
  HKDF-SHA256 over the two nonces and the token. A recording of the session
  cannot be read later, even by someone who learns the token.
* **The frames** are encrypted with AES-256-GCM. The type, the flags, the
  length and the stream id are authenticated too, so a frame cannot be
  replayed as another kind of frame or as another stream.
* **The streams.** The server opens a stream for every connection that
  arrives on a published port, and the client connects to the destination
  the server names. The window is 4 MB per session and 64 KB per stream by
  default (both are in the configuration), so that one slow reader cannot
  make the engine buffer without a bound.
* **The keepalive.** A ping every 20 seconds; a session that hears nothing
  for three of them is closed and the client reconnects.

The details are in the source of the engine, which has tests for every
part: `engine/internal/client`, `engine/internal/server` and
`engine/internal/tunnel`.

## Measuring the path

The engine can measure the path to the other end, which is what the ⚡
button of the panel shows:

```
bull-tunnel-engine probe -c name.toml --samples 3
```

It runs on the side that dials out, opens every carrier in turn and writes
one JSON object per carrier:

```json
{"transport":"wss","ok":true,"connect_ms":30.2,"handshake_ms":0,"rtt_ms":21.4,"samples":3}
{"transport":"tcp","ok":false,"connect_ms":0,"handshake_ms":0,"rtt_ms":0,"samples":0,"error":"…"}
```

* `connect_ms` — how long the carrier took to come up (the TCP connection,
  the TLS handshake, the websocket upgrade, the KCP session, the QUIC
  handshake).
* `handshake_ms` — how long the credential exchange took. It is zero,
  because the probe never performs one: a credential exchange on a live
  tunnel is a **control claim**, and the server answers a second claim by
  taking the seat in place — the probe would take the tunnel away from the
  client that is carrying the traffic.
* `rtt_ms` — the median of the samples: the typical time for this carrier
  to come up on this path.

What the probe opens, it opens and closes again without claiming anything,
so measuring the path never disturbs a tunnel that is carrying traffic.

The carrier with the smallest round trip is the best one for that pair,
right now.
