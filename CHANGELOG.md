# Changelog

All the versions of Bull-Tunn. The project follows
[semantic versioning](https://semver.org): the number before the dot is a
change that may break a configuration, the one after it is a change that
only adds something.

## Unreleased

### A new interface for the panel

The panel was redesigned from the ground up. The screens, the colours, the
type and the way the network is drawn follow one system, and the panel is
now a small application with a sidebar and five pages instead of one long
screen.

- **The design system.** The palette is exact: a charcoal dark theme
  (`#171A1B`, `#1E2223`, `#272C2D`, `#3B4243`, `#F1F0E9`, `#A1ABAB`,
  `#E23B32`) and a mineral-paper light theme (`#EDEEE8`, `#F8F8F2`,
  `#E3E5DE`, `#C3C8C0`, `#202726`, `#5E6B66`, `#BE2F28`), plus one colour
  per state: healthy, unstable, down, testing and forwarded. The corners
  are square, the lines are 1px, and every number, protocol and address is
  written in `IBM Plex Mono`.
- **The shell.** A sidebar with the brand, the pages and the state of the
  network, a breadcrumb bar, a counter strip under every title and a
  footer. The pages are: **Network view**, **Tunnels & protocols**,
  **Domains**, **Infrastructure** and **Settings**.
- **The network graph** keeps the softer structure of the earlier design —
  three columns of cards, the pills on the wires, the zoom tools and the
  legend — inside the new palette, and the shape of a line now says the
  state of a tunnel even without a colour: solid, a long dash, a short
  dash, a dash and a dot, a directed dash for a forwarded port and a
  directed dotted line for the route of a domain. A selection is a red
  edge, never a different colour.
- **Everything is still reachable.** The panels of a server, a tunnel and a
  domain are still there, with the same actions; the forms, the probes,
  the command channel, the backups and the restore did not change.
- **The logo** is the mark of the new design, and the mark in the sidebar
  takes the colour of the theme.
- The static files are served with `Cache-Control: no-cache`, so a browser
  never mixes the stylesheet of the old version with the page of the new
  one.

### The whole project is in English now

The panel, the dashboard, the Telegram bot, the command line, the install
scripts, the documentation and the tests were translated from Persian to
English. Nothing else changed:

- The dashboard is left-to-right now and uses Inter instead of the
  Persian typeface.
- Every message the panel shows, writes to Telegram or returns in an API
  error is in English.
- The screenshots in `docs/` were taken again with the English dashboard.

## 1.0.0 — 2026-10-20

The first release of Bull-Tunn: the panel, the tunnel engine and the
command channel, with nothing left over from the project it grew out of.

### The tunnel engine

The engine is now part of this project. It is a single static Go binary
with no dependency but the standard library, so nothing has to be
installed on the servers that carry the tunnels.

- A reverse tunnel: the client dials in, the server publishes the ports.
- The carriers `tcp`, `ws` (a websocket over HTTP), `wss` (a websocket over
  TLS) and `stealth` (an HTTP `CONNECT` tunnel that looks like ordinary
  proxy traffic). The `mux` variants share the carrier of their base
  transport.
- One session under every carrier: the token authenticates the handshake
  with HMAC-SHA256, the traffic keys come from an ephemeral X25519
  exchange stretched with HKDF-SHA256, and every frame is sealed with
  AES-256-GCM. The type, the flags, the length and the stream id of a
  frame are authenticated too.
- Several streams per session, with a window of 256 KiB each, a ping every
  20 seconds and a reconnect with backoff.
- A metrics file with the state of the tunnel and the carriers the build
  speaks, so the panel never tests a carrier the engine does not have.
- `bulltunn-engine -c <file> -check` reads a configuration and reports the
  problem instead of crash looping.

### The command channel

When not even a TCP handshake is possible, the panel can still talk to a
server.

- The panel writes a signed command into a TXT record; the agent on the
  server polls it over ordinary DNS, checks the signature, runs the
  command and writes the answer into another TXT record.
- The commands are signed with a key only the panel and the agent know, so
  an account that can write in the zone still cannot make the agent do
  anything.
- The agent knows two commands (`status` and `restart`), checks every
  tunnel name twice, and there is no shell anywhere near it.

### The control plane

- The package, the service, the data directory, the environment variables
  and the config directory on the managed servers are all renamed:
  `tunnelmanager` becomes `bulltunn`, `/etc/backpack` becomes
  `/etc/bulltunn`, `backpack-<name>.service` becomes
  `bulltunn-<name>.service`.
- The engine is installed from the release of this project, with the
  SHA-256 of the file checked, or from a mirror, or from a tarball dropped
  in the cache by hand.
- The panel runs as an unprivileged user with a hardened systemd unit, and
  the installer swaps the code atomically and checks that the panel really
  answers before it says it is done.
- The SSH host keys are pinned: the first connection records the key, a key
  that changed is refused and reported, and the operator can write the
  expected fingerprint into the server's settings.
- The login is rate limited, and the dashboard sends a strict content
  security policy with no inline script.
- `deploy/migrate.sh` moves an installation of the previous version to
  Bull-Tunn, `deploy/remote-migrate.sh` does the same for the servers that
  carry the tunnels. Neither deletes anything.
- The Docker image is published for amd64 and arm64, and a tagged release
  carries the panel and the engine with its checksums.
