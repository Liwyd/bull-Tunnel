# Changelog

All the versions of bull-Tunnel. The project follows
[semantic versioning](https://semver.org): the number before the dot is a
change that may break a configuration, the one after it is a change that
only adds something.

## Unreleased

- **The setup cannot take over a broken login** (the security review's
  F3): when the login file exists but cannot be read, `/api/setup`
  answers 500 and leaves the file alone instead of handing the panel to
  whoever reaches the port first; setup attempts are now rate-limited
  like the login's; and the session endpoint tells an unauthenticated
  caller only that the login file could not be read — the path and the
  system's reason go to the log of the service and to the state shown
  signed in.
- **An id can no longer reach the shell or the configuration** (the
  security review's F2): the API refuses an id outside the panel's own
  shape — letters, digits, dot, dash and underscore — for tunnels,
  servers, domains and DNS accounts; the health script escapes the id it
  echoes back, so even an id from a restored state file round-trips as
  those letters and is never run; and a name carrying a line break in a
  configuration's header line stays one line of comment instead of
  becoming a key of the tunnel's TOML.

## 1.4.0 — 2026-10-25

The dashboard tells the operator what it knows. The page of a domain
answers the two numbers it never carried — how many of its ports a
healthy tunnel answers, on every server the record may move to, and when
the record comes back to the primary — the tunnels gained the two knobs
the engine honours (`heartbeat`, `mss`), and the connection test takes
only the panel's own carriers where it builds a command line for the
servers. The compression library was upgraded to the patched release
Dependabot flagged.

### The carriers a measurement may try are the panel's own

A carrier name is written into the command the panel runs over SSH on the
foreign server. The panel now takes its own carriers — the engine's list,
`tcpmux` … `wss` — and refuses anything else, in three places: the API of
the connection test, the settings' list of carriers to try, and the
measurement itself, which is the last gate before the shell. A name of
anyone's own making can no longer become part of a command.

### A heartbeat per tunnel

The control channel of a tunnel can beat on demand now: `heartbeat` is
how often the Iran server sends a liveness byte down it, so a client
notices a server that has gone away without closing the socket — and the
beat holds the path open while nothing is being carried.

- `model.Link` carries `heartbeat`; the API sends and validates it. The
  range is `0` or 1–10 seconds: the engine clamps anything above ten
  seconds to ten (that is the longest its control channel goes without a
  beat), so a bigger number would be a setting the engine does not
  honour.
- The renderer writes it into the `[server]` section only — the Iran side
  is the one that beats, and the client's config has no such key. `0`
  writes no line and leaves the engine's own rhythm.
- The drawer's settings carry the field and its hint; a change is applied
  the way the MSS is (both configurations written again, and a busy
  tunnel waits for its rebuild).
- The state file, the fixtures, `docs/engine-schema.md` and this
  changelog carry the field.

### The page of a domain says what it covers and when it comes back

The two numbers the dashboard never carried are on the domain page now:

- The routes panel carries the coverage of the server the record points
  at: how many of the domain's ports a healthy tunnel answers — green
  when all of them are up, amber for a part of them, red for none.
- The failover panel lists every Iran server the record may move to, in
  the order the panel tries them, each with its own coverage — so the
  server the note promises ("the first with all the ports healthy") is a
  row with a number, not a promise.
- The countdown back to the primary runs under that list while the record
  is on a fallback, the way it does in the drawer. The countdown spans
  are named by their attribute now, not by an id: the drawer and the
  page are in the DOM at the same time, and an id must be unique.

### The maximum segment size of a tunnel

Some paths connect and then stall on the first large transfer because they
cannot carry full-sized packets. The engine has an `mss` clamp for that
case, and the panel offers it now per tunnel, in the drawer's settings:

- `model.Link` carries `mss`, the API sends and validates it, and the
  renderer writes it into both the `[server]` and the `[client]` section.
- `0` writes no line and leaves what the tunnel already has — the kernel's
  own value for a tunnel the panel built, the value in its file for one it
  adopted. The API accepts `0` or a size between 536 and 65495.
- Changing it rewrites and restarts both ends, because the engine reads it
  at start. A tunnel that is busy being built or repaired is left to that
  operation, and the event log says the saved value waits for its next
  rebuild.
- The state file gains the field; the fixtures and `docs/engine-schema.md`
  carry it.

### A high-severity vulnerability in a dependency

Dependabot reported `github.com/klauspost/compress` ≤ v1.18.6
(GHSA-259r-337f-4rfw, CVE-2026-113727, high): a crafted S2 dictionary can
overflow the repeat index and crash the process with an out-of-bounds
write. The module is an indirect dependency of `utls` (which uses its zstd
package), and this project never calls `s2.NewDict`; the vulnerable package
was not even vendored. The upgrade is still taken, so the alert does not
stay open and the next use of the module cannot inherit the fault.

- `go.mod` and `go.sum` pin `github.com/klauspost/compress` at v1.18.7.
- `vendor/` is rebuilt from it.
- The whole Go suite passes, under the race detector as the CI runs it.

## 1.3.0 — 2026-10-22

The dashboard is served by Go, screen by screen: the shell, the network
view, the graph, the other views and the forms are drawn from the Go side
now, and the browser draws what it is given. The state got its lock in
order first — every writer takes it, and every question the panel asks
its state is asked under it — because the race detector was the one that
said so.

- The dashboard's shell, the network view and the graph are rendered by
  the panel itself: `GET /api/view` carries the facts and the parts of
  the page, and the page is drawn from them.
- A form is the API body, drawn: `GET /api/form` returns the HTML of the
  form of a server, a link, a provider, a domain, the settings or a
  restore, and the browser only collects what the form shows and sends
  it. The six form builders in JavaScript went with it.
- The state takes the lock: the writers, the servers, the records and
  the questions of the panel are all under it, and the TODO says what the
  race detector found.
- The CI keeps the release that runs up to date itself: every push to
  main that passes the tests publishes the binaries as the release
  `build-main`, marked latest — the one `releases/latest` answers with,
  and the panel installs the engine from there — in the place of the one
  before it. The tagged releases are the versions an operator pins.
- The copyright line of the licence and the notice names the author.
- The origin of the inherited engine leaves the project's own words: the
  README, the changelog, the docs, the comments, the installer and the
  engine's version output no longer carry it. The attribution the
  engine's AGPL-3.0 Section 7 requires stays where the licence puts it —
  the engine's `NOTICE`, the engine's `README.md` and the README of the
  distribution — and a test in `internal/panel/licence_test.go` holds it
  there. The engine's `NOTICE` pointed at a `TRADEMARK.md` that did not
  exist; it does now.

## 1.2.0 — 2026-10-22

The panel is Go, from the first line to the last: the binary, the service,
the installer, the image, the management tool and the demo. The Python panel
that was the reference of the rewrite has done its job — the state file, the
configuration of a tunnel, the scripts, the backups, the sessions and the
decisions of the brain are all pinned by the fixtures the Go panel is tested
against — and it is gone.

- The repository is Go only: the Python package, its tests and its wheel are
  removed, and the CI builds and tests the Go module alone.
- The web interface moved to `static/`, where the panel serves it from.
- The version has one home: the panel's own package (`internal/panel`), and
  the build script reads it there.
- The release carries the two binaries of the panel and the engine, and
  nothing else.

### The demo, in Go

The demo is how a newcomer watches the failovers without a server, and it
runs the panel itself: the same code, the same check cycle, the same
decisions — only the network is simulated.

- The simulated network: the servers die, the carriers are blocked per pair
  of servers, the paths flap, and every carrier has a latency.
- The DNS service of the demo: the records live in the memory, and the panel
  can be watched moving them.
- The world the panel starts with: six servers, two DNS accounts, four
  domains, and the tunnels that were "set up by hand" before the panel
  existed — the first scan adopts them.
- The chaos: something breaks every minute, so the failover can be watched.

```bash
BT_DEMO=1 go run ./cmd/bull-tunnel-panel
```

### A tunnel without an identifier gets one

The Python panel gives every tunnel an identifier when it builds it; the Go
one did not. The tunnel the mesh offers and the tunnel the panel adopts
were both written under the key "" in the state file: one of them survived
and the other was lost. Their names were "bt-" with nothing after it.

### The set of the state has a lock of its own

The lock of the state keeps the state consistent, but the set itself was
read while it was written — and Go stops the whole program when a map is
read and written at the same time. The panel could die while the operator
added a server.

### The operator's change does not wait for the cycle

The cycle gives the lock of the state back between its phases: the servers,
the tunnels and the exits are read under the lock and copied, the network
calls are made with it released, and what the cycle learned is written to
the tunnel that is there now — the change the operator made while the cycle
was working is not lost.

### The panel does not fall over its own state

The Go panel is two things at once: the monitor writes the state, and the
web interface reads it. **Go does not forgive a map that is read and
written at the same time** — the runtime stops the whole panel, and the
dashboard takes the tunnels with it. The state was not behind any lock.

- The state is behind a lock now: the reader takes it for reading, the
  writer for writing, and the network calls are made with it released — or
  the panel would answer nothing while a slow server was being checked.
- The repairs and the discoveries run in the background, the way the Python
  panel does them: a repair can take a minute, and the check cycle is what
  checks every tunnel and moves every domain.
- The test: the cycle runs while the dashboard asks for the state, over and
  over, under the race detector. It fails without the lock.

### The image is built again

The `.dockerignore` still excluded the Go module — it was written when the
image carried the Python panel — so the build of the image failed at the
first `COPY`. The module is in the build context again, and what the image
does not need is left out.

The image is built and it runs: the panel answers, the page is served and
the health check of the container is green.

### The panel itself, in Go

`bull-tunnel-panel` is the panel: the API, the web interface, the monitor
and the bot, in one binary.

- The service, the installer and the image are the panel's now: the
  installer takes the panel of the release, puts the management command on
  the path and never touches the data directory; the image is a multi-stage
  build with the panel and the engine in it.
- The panel writes the key of the management tool of the machine where only
  the owner can read it, and accepts it only from this machine.
- The release carries the panel now: `bull-tunnel_linux_<arch>.tar.gz` holds
  the panel and the management tool.

### The management tool of the panel, in Go

`bull-tunnel` is the hand on the panel: it runs on the machine the panel
runs on, talks to the panel over its own API and needs no password — the
panel wrote a key for it, in a file only the owner of the machine can read,
and the panel accepts that key only from this machine.

Every command of the Python tool is there — the status, the servers, the
tunnels, the domains, the DNS accounts, the engine, the backups, the
service, the log, the password and the doctor — and so is the menu: a list, a
number, and it does the thing.

The password is read without an echo, and the secret the sessions are
signed with is rotated, so every session is invalid afterwards.

The release carries the binary now, next to the engine's:
`bull-tunnel_linux_<arch>.tar.gz`.

### The API of the panel is complete

Every endpoint of the Python panel is there, one for one, with the same
answers: the session, the setup, the login, the logout, the state, the
layout, the servers, the links, the domains, the DNS accounts, the settings,
the measurement of the path, the command channel, the Telegram test and the
backups — and the actions: the check, the scan, the build, the carrier of a
tunnel, the record of a domain and the verification of a DNS account.

The websocket carries the state to the open pages, every answer carries the
security headers, and the management tool of the machine itself is let in
with the key the panel wrote for it, and only from that machine.

### The declared dependencies cannot pull a vulnerable version

The floors of the dependencies were old enough to allow versions with known
faults: `asyncssh>=2.14` allowed the path traversal of the SCP and of the
`AuthorizedKeysFile` (fixed in 2.23.1), and `cryptography>=3.4` allowed the
wheels with a vulnerable OpenSSL (fixed in 48.0.1). The floors are raised:
the panel now asks for versions that are not known to be faulty, and what
is installed already was.

### The connection test of an IPv4 server never worked

The file the connection test (the ⚡ button) writes on the far side
bracketed the **whole** address, and the engine splits an address at the
last colon — so the file said `[10.0.0.2:3080]` and the engine answered
"missing port in address". The measurement of the path between two servers
was therefore broken for every IPv4 server, which is most of them.

Only the host is bracketed now, and only when it is an IPv6 one:
`[2001:db8::1]:3080` and `10.0.0.2:3080`. The fix is in both panels, and
the tests say it.

### The web interface of the panel, in Go

`internal/panel/api` is the API the front-end talks to: the same endpoints
and the same answers as the Python panel, so the interface changes nothing.

- **The security headers of every answer** — the same ones, and the same
  content security policy — and the websocket that carries the state to the
  open pages.
- **The management tool of the machine itself** is let in with the key the
  panel wrote for it, and only from that machine: an operator can run
  `bull-tunnel` on the panel's own server without knowing the admin's
  password.
- **The secrets are masked on the way out** and the mask is not taken for a
  new secret on the way in: a panel that is given the mask of the password
  keeps the password it had.

The endpoints of the backups, of the Telegram test, of the probe and of the
command channel come with their own work.

### The brain of the panel, in Go

`internal/panel/orch` is the state machine of the panel: the check cycle,
the discovery, the repair, the adoption, the failover, the failback, the
handovers, the exits and the propagation — and what the bot says about all
of it.

- **The failover policy is the one of the Python panel**: a domain that
  cannot be served where it is moves to the first healthy tunnel of its
  group — to another Iran server (the record moves) or to a spare tunnel on
  the same one (the ports are handed over) — and the traffic comes back to
  the primary once it has been stable, with not a single drop.
- **The observe mode changes nothing**: the panel says what it would have
  done, once, and does nothing.
- **The command channel** is the panel's side of the contract with the
  agent: the same bytes, the same signature, the same test vector as the
  engine and the Python panel.

The work runs in the check cycle, not in the background: the state is
shared, and Go has no cooperative multitasking — two goroutines writing the
same tunnel would be a race. The lock that lets the panel read the state
while the cycle writes it comes with the API.

### The decisions of the brain

The brain of the panel now decides in Go: whether a tunnel is healthy,
whether it is stable enough to take the traffic back, why it must not carry
it, which carriers the engine on the Iran side really speaks, which tunnel
carries a port, whether an Iran server can carry every port of a domain, and
what would fix the domain if it could not — a handover of the ports on the
same server, or a move of the record to another one.

The decisions are a contract: the Python panel and the Go panel must decide
the same for the same state, down to the words they say to the operator.

### The questions the panel asks its state

The state knows more than it stores: which tunnels join two servers, which
of them exposes a port now, and what a domain may use. The orchestrator and
the API ask these questions on every cycle, so they live in the store — and
their answers are the same in the two panels, down to the order of the
servers and of the tunnels.

### The DNS, the Telegram and the backups, in Go

- **`internal/panel/dns`** is the panel's access to Cloudflare and
  ArvanCloud: the same requests, the same bodies and the same way of
  quoting the text of a TXT record as the Python panel — the command
  channel lives in one, and the panel must not split it.
- **`internal/panel/telegram`** is the bot: it pushes every event to the
  configured chats and answers the commands of the chats that are linked to
  the panel.
- **`internal/panel/backup`** writes and reads the backups of the panel.
  The format is the one the Python panel writes — gzipped JSON, encrypted
  with AES-256-GCM and a key from scrypt — so a backup written by one
  panel is restored by the other. The files in `tests/fixtures/` are the
  contract: each panel reads the backup of the other.

### The time of a tunnel is the time it really took

The health check of a tunnel measured the time in **microseconds** and the
panel showed it as **milliseconds**: a tunnel that answered in 12 ms was
shown as 12000 ms, and the time a tunnel took to come up was a thousand
times too big.

The clock of the script now counts milliseconds in all three of its
branches — the nanoseconds of `date`, the clock of the shell and the plain
seconds — so the number in the panel is the number the tunnel really took.

### The engine driver of the panel, in Go

`internal/panel/engine` is everything the panel does with the tunnel engine
on the servers.

- **The configuration of a tunnel is written by hand**, key by key, the way
  the Python panel writes it: the same `[server]` and `[client]` sections,
  the same port mappings, the same probe. A tunnel the panel found already
  running keeps the settings the panel does not manage — and the order of
  the keys, so the panel does not shuffle them.
- **The files the panel writes on the servers are a contract**: the
  configuration, the unit and the five scripts are in `tests/fixtures/`,
  and the two panels must write the same bytes. The Python panel writes the
  fixtures — it is the reference until the rewrite is finished — and the Go
  panel is checked against them.
- **The health of a tunnel is read from the answer of the script**: the
  state of the unit, the engine's own view, an end-to-end probe where the
  tunnel has one, and the socket table as the last resort.
- **The exits are only looked at**: the DNAT rules, who listens on a port
  and where that process's connections go. The panel changes nothing.
- **The engine is put on a server from the release**: the tarball is fetched
  once, its checksum is checked against the one the release publishes, and
  the binary is pushed over SSH — the servers in Iran often cannot reach
  the artifact host.

The tests run against a real engine when there is one (`BT_ENGINE_BIN`):
the configuration the panel writes is read by the engine, and the tunnel
comes up and carries the traffic.

### The SSH layer of the panel, in Go

`internal/panel/ssh` is the panel's access to the servers it manages: one
connection per server, kept and reused, with the host keys pinned.

- **The host key of a server is pinned.** The first connection records it,
  and every later connection must present the same one — a key that changed
  means something is in the middle, and the connection is refused. The
  operator can also write the expected fingerprint into the settings of the
  server, and then even the first contact is checked.
- **A command that does not finish in time is not run again.** The
  connection is dropped, because it may be in a state nobody can predict.
  A connection that died under the panel is opened again, and the command
  is run once more.
- **A private key that cannot be read is the operator's mistake, not a
  fault of the network** — but it is an error of the SSH layer and nothing
  else, so one bad server cannot stop the check cycle.
- **A file is written with SFTP**, and with the shell on a server that has
  no SFTP subsystem, which is what the smallest images have.
- **The file of the host keys is the same in the two panels**, byte for
  byte: `tests/fixtures/hostkeys-go.json` is the file the Go panel writes
  and the Python panel reads it back.

The tests run against an SSH server that is right there, in the test
itself, that runs the commands with the shell of the machine — the panel is
tested the way it works in production: over a socket, with a host key, with
a password and with a private key.

### The state of the panel, in Go

The first half of the panel's Go rewrite: the model and the state file.

- **`internal/panel/model`** is the data model of the panel: the same
  structures the Python panel writes in its state file, with the same names
  and the same defaults. The reading is tolerant — a field the file does
  not mention keeps its default, a field it does not know is ignored — so
  a state file of any version loads.
- **`internal/panel/store`** reads the state file, writes it back (the
  write is atomic and the file is only readable by its owner: it holds the
  SSH passwords and the API tokens), and brings the old files forward: the
  `v0.1` ports that were on the kharej server, the `v0.2` domains that
  followed one tunnel and the `v1.0` names of the engine settings.
- **The order of the entries is kept.** A Go map has no order of its own,
  so the store remembers the order the file had: the panel shows the
  servers and the tunnels in that order, the way it always did.
- **The state files of every version are a contract.** `tests/fixtures/`
  holds a state file of every version the panel has known, and the state
  each of them comes out as. The Python panel writes the expected files —
  it is the reference until the rewrite is finished — and the Go panel
  must come out with the same state. The file the Go panel writes is a
  fixture too, and the Python panel reads it back.

### One Go module

The panel and the engine are one Go module now —
`github.com/Liwyd/bull-Tunnel` — with two binaries and one vendor directory:

```
cmd/bull-tunnel-engine/   the engine's binary
cmd/bull-tunnel/           the panel's binary (with phase 8 of the rewrite)
internal/engine/           the engine
internal/panel/            the panel
vendor/                    the dependencies: the build needs no network
```

The engine's tree moved from `engine/` to `internal/engine/` and its import
paths were rewritten with it (`github.com/backpack/backpack/config` became
`github.com/Liwyd/bull-Tunnel/internal/engine/config`). Nothing else of it
changed: the same code, the same tests, and a binary that prints the same
version and the same nine carriers.

- **The build script is at the root** (`bash build.sh`) and builds both
  binaries for the four architectures and writes the checksums next to them.
  The engine's file keeps its name —
  `bull-tunnel-engine_linux_<arch>.tar.gz` — so the panel installs it
  exactly as before, with the same checksum and the same path.
- **The dependencies are vendored at the root of the module**, so the build
  needs no network and gives the same result everywhere.
- **The engine stays AGPL-3.0 and the panel stays MIT.** They are two
  programs that talk over SSH, and a test
  (`internal/panel/separation_test.go`) keeps the panel from ever importing
  a package of the engine: a program that links AGPL code is AGPL, whatever
  else is in it.

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

The project is **bull-Tunnel** now, and it has a **management command**:
`bull-tunnel`, installed on the path by the
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
now an established AGPL-3.0 tunnel engine,
vendored under `engine/` with its module path kept as
`github.com/backpack/backpack`, so the copy is byte-identical to the
origin and can be re-synced with a plain `rsync`. See `engine/README.md` for what
was taken, what was left behind and what was added. The origin and the
attribution the licence requires are in the NOTICE.

**The engine is this project's engine**: the panel builds it, ships it,
versions it and installs it, and the tunnel it brings up is a bull-Tunnel
tunnel. The credit for the design and the code it started from is in the
NOTICE, where the licence asks for it, and nowhere else in the product.

### The licences are split

The control panel stays MIT. The engine is AGPL-3.0 — inherited
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

Two things are added on top of the inherited engine, and nothing else:

- **`bull-tunnel-engine probe`** measures the path between two servers, one
  line per carrier. It opens a carrier the way the tunnel's own client
  opens it and closes it again **without claiming the control channel**: a
  claim would make the server take the tunnel away from the client that
  is carrying the traffic. Measuring the path never disturbs a tunnel.
- **`bull-tunnel-engine agent`** is the command channel, which the inherited
  engine does
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
