# Changelog

All the versions of bull-Tunnel. The project follows
[semantic versioning](https://semver.org): the number before the dot is a
change that may break a configuration, the one after it is a change that
only adds something.

## Unreleased

### The network graph follows the type-1 design

The graph of the network view is built from the type-1 design
(`new-ui/type1/graphs.svg`), in the type-2 palette. The shell of the panel
is type 2 — the sidebar, the cards of the pages, the counters, the type
scale — and the graph is the one place where the older, softer shape is
kept, because a graph of moving nodes reads better with soft cards than
with sharp ones.

- **The cards are soft again.** A rounded card with a quiet line of meta
  under the address, the state dot in its corner, and a soft tint on the
  card that is picked and on the ones the path goes through. The rows of
  chips are gone: the meta is text, the way the type-1 card has it.
- **The points where a wire meets a card are drawn**, in the colour of the
  wire, the way the type-1 graph draws them: they say which side of the
  card the path uses.
- **The card of a domain** has the name in the panel's own face, the
  address on the left and the DNS account on the right, and the route the
  traffic really uses in the text colour while the others stay quiet.
- **The labels on the wires are plain text**, in the colour of the state,
  dimmed for the standby tunnels. The wire that is picked gets a pill
  around its label, as in the type-1 design.
- **The zoom tools sit on the canvas**, plain, in a row: a fit, a smaller,
  the size, a bigger, a reset.
- Every wire carries its label now, the healthy ones included — the type-1
  graph names every path it draws.

### bull-Tunnel, and a management tool

The project is **bull-Tunnel** now, and it has a **management command**
the way BackPack has a menu: `bull-tunnel`, installed on the path by the
installer, opens a menu with everything an operator does day to day — the
servers, the tunnels, the domains, the DNS accounts, the tunnel engine on
the servers, the backups, the service, the log and the admin password.

The command runs on the machine the panel runs on and needs no password:
the panel writes a key for it, in a file only the owner of the machine can
read, and the panel accepts that key only from this machine.

```
bull-Tunnel 1.1.0
─────────────────
  panel              http://127.0.0.1:8080
  servers            3 of 3 online
  tunnels            4 up, 0 unstable, 0 down, 1 not built
  domains            2 of 2 healthy
  mode               acting
```

### The installation is one line

```bash
curl -fsSL https://raw.githubusercontent.com/Liwyd/bull-Tunnel/main/deploy/install.sh | sudo bash
```

The script fetches the panel, puts it in place, makes a user for it,
starts the service, opens the management tool and prints the address and
the password. It is safe to run again on a live panel: the code is swapped
while the service is stopped, and the data directory is never touched.

### The engine is bull-Tunnel's own

The tunnel engine was a re-implementation written for this project. It is
now the engine of [BackPack](https://github.com/AminMGMT/BackPack),
vendored under `engine/` with its module path kept as
`github.com/backpack/backpack`, so the copy is byte-identical to upstream
and can be re-synced with a plain `rsync`. See `engine/README.md` for what
was taken, what was left behind and what was added.

**The engine is this project's engine**: the panel builds it, ships it,
versions it and installs it, and the tunnel it brings up is a bull-Tunnel
tunnel. The credit for the design and the code it started from is in the
NOTICE, where the licence asks for it, and nowhere else in the product.

### The licences are split

The control panel stays MIT. The engine is AGPL-3.0 — it is BackPack's
code — and it carries its own `LICENSE` and `NOTICE` under `engine/`, with
the attribution the AGPL's Section 7 asks for, unaltered. The two are
separate programs: the panel drives the engine over SSH and never links a
package of it, so each keeps its own licence.

That brings the carriers the panel always offered but the engine never
had: `kcp` and `quic`, next to `tcp`, `tcpmux`, `stealth`, `ws`, `wss`,
`wsmux` and `wssmux`. The engine says which of them it speaks with `-v`,
and the panel reads that from the server, so it never writes a
configuration the engine would refuse — and never waits a full test
timeout to find out.

Two things are added on top of BackPack, and nothing else:

- **`bull-tunnel-engine probe`** measures the path between two servers, one
  line per carrier. It opens a carrier the way the tunnel's own client
  opens it and closes it again **without claiming the control channel**: a
  claim would make the server take the tunnel away from the client that
  is carrying the traffic. Measuring the path never disturbs a tunnel.
- **`bull-tunnel-engine agent`** is the command channel, which BackPack does
  not have. Its signature is now the panel's, byte for byte, and the TXT
  record it writes is escaped the way Cloudflare wants it. Every command
  used to be refused with "not signed with the right token", because the
  panel left the empty argument list out of the string it signed and the
  agent signed the two braces it found in the record.

### The faults that are fixed

- **One server could stop the whole monitor.** A private key the SSH
  library refuses raised a `ValueError`, which was not an `SSHError`, so
  it escaped the check of the servers and aborted the cycle: no server was
  checked, no tunnel was checked, no domain was evaluated and no failover
  happened — for ever, on every cycle, while the panel looked alive.
- **Deleting a server left its tunnels on the machine.** The cleanup
  looked the servers up in the panel, and the panel had already forgotten
  them: the config, the unit and the running process stayed there.
- **"Make it primary" did nothing** as soon as a domain's group was
  chosen by hand: the group took precedence over the primary, and the
  traffic came back to another server.
- **Saving a server dropped the pinned host key** whenever the host-key
  field was empty — and renaming a server is a save. The key is only
  forgotten when the operator had written a fingerprint and then removed
  it.
- **A state file or a login file that could not be read took the panel
  down** at start-up. The state file is moved aside rather than
  overwritten, the login file is left untouched, and both say so in the
  panel.
- **The engine was never updated.** Its tarball was cached under the name
  "latest" for ever, and a server that had been updated kept the build it
  had. The checksum of what is published now says whether the tarball the
  panel has is still that one.
- **Two mappings that want the same port** were accepted by the panel and
  refused by the engine, which exits and never brings the tunnel up.
- **The health check needed a GNU date.** On a small distribution the
  clock has no nanoseconds, and every tunnel would have read "it did not
  answer" on a system whose clock was perfectly fine.
- **Two backups in the same minute** were the same file, and the second
  threw the first away.
- **A restored login without the key the sessions are signed with** locked
  the panel out.
- **A handover could race with a repair** of the same tunnel, and a
  handover that would have looped was written anyway.
- The panel is faster with many servers: the map of the forwarded ports
  is worked out once and kept until something it depends on changes.
- The connection test says what went wrong instead of failing silently,
  the forwarded-port labels open the drawer of the server the ports leave
  from, and the signed-in user is the one who signed in.

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

The first release of bull-Tunnel: the panel, the tunnel engine and the
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
- `bull-tunnel-engine -c <file> -check` reads a configuration and reports the
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
  `tunnelmanager` becomes `bulltunnel`, `/etc/backpack` becomes
  `/etc/bull-tunnel`, `backpack-<name>.service` becomes
  `bull-tunnel-<name>.service`.
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
  bull-Tunnel, `deploy/remote-migrate.sh` does the same for the servers that
  carry the tunnels. Neither deletes anything.
- The Docker image is published for amd64 and arm64, and a tagged release
  carries the panel and the engine with its checksums.
