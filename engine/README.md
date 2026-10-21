# The tunnel engine

This is **the BackPack engine**, vendored.

The engine that carries the tunnels is not a Bull-Tunn re-implementation: it is
the engine of [BackPack](https://github.com/AminMGMT/BackPack), the project
this one grew out of. Everything under this directory is that code, and the
module path is kept as `github.com/backpack/backpack` so the copy is
byte-identical to upstream and can be re-synced with a plain copy:

```bash
rsync -a --delete /path/to/BackPack/cmd/            engine/cmd/
rsync -a --delete /path/to/BackPack/config/         engine/config/
rsync -a --delete /path/to/BackPack/internal/…      engine/internal/…
```

## What was taken, and what was left behind

Taken, unchanged:

* `cmd/` — the engine loop: it loads the configuration, applies the defaults,
  validates it, starts the transport and reloads it when the file changes.
* `config/` — the configuration grammar: the `[server]` and `[client]` sections,
  the port mappings, the transports, the tuning.
* `internal/client`, `internal/server` — the two halves of a tunnel.
* `internal/tunnel/*` — the port forwarder, the layer-3 tunnel, the direct
  tunnel, the chains, the limits.
* `internal/utils/*` — the dialers, the carriers' plumbing, the handlers.
* `internal/metrics`, `internal/controlwire`, `internal/enginectl`,
  `internal/optimize`, `internal/quota`, `internal/socks`, `internal/web`,
  `internal/app`, `internal/debugserver`, `internal/snispoof`,
  `internal/testport`.

Left behind, because they are about BackPack's own repository and not about
the engine:

* the tests of the repository (`internal/app/attribution_test.go` and its
  neighbours, `config/reference_test.go`) — they read BackPack's `LICENSE`,
  its `NOTICE`, its `docs/` and its `install.sh`, none of which is part of
  the engine;
* BackPack's own command line, its menu, its web panel, its Telegram bot, its
  scheduler and its management layer — the panel is the management layer here.

## What was added, and why

Three additions, and nothing else:

* **`main.go`** — the `bulltunn-engine` binary. It is BackPack's engine mode
  (`-c <config>`), plus the two entry points the control panel needs and
  BackPack does not have: `probe` (measure the path, one line per carrier)
  and `agent` (the command channel). It also prints the version and the
  carriers with `-v`, which is how the panel learns what this build speaks.
* **`internal/client/transport/probe.go`** — the path measurement. It opens a
  carrier the way the tunnel's own client opens it and closes it again
  without claiming the control channel: a claim would take the tunnel away
  from the client that is carrying the traffic. See the note at the top of
  that file.
* **`internal/agent/`** — the command channel. It is a Bull-Tunn idea, not a
  BackPack one: the panel writes a signed command into a TXT record and the
  agent runs it. Its signature is the panel's, byte for byte, and both sides
  are checked against the same vector.

## The carriers

The engine speaks nine reverse carriers:

```
tcp  tcpmux  stealth  ws  wss  wsmux  wssmux  kcp  quic
```

`udp` carries datagrams only and cannot be probed with a TCP probe, so the
panel does not offer it. The experimental carriers (`xdi`, `pck`, `spoof`)
need raw sockets and root, so they are never offered to an unattended
repair.

The list is printed by `bulltunn-engine -v` and read by the panel, which
therefore never tests a carrier this build does not have.

## Building

```bash
bash engine/build.sh [output directory]
```

The build is hermetic: the dependencies are vendored under `vendor/`, so no
network is needed and the result is the same everywhere. It needs Go 1.26.9
or newer — that is what `go.mod` asks for, because BackPack asks for it.

## The tests

```bash
cd engine && go test ./...
```

The tests of the vendored code are the tests of BackPack: they are the ones
that prove the engine still behaves as it did upstream.
