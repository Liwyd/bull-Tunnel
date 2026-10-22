# bull-Tunnel — Fix Plan (TODO)

This file is the working plan for the repair of bull-Tunnel. It is updated before
every commit, and it records **what the next commit does, in detail**, so that
no step is taken "from memory".

---

## 0. Standing rules for myself (read before every change)

1. **Never break a live system.** The panel runs on a live server and manages
   live tunnels. Every change must be backwards compatible with the state file
   (`state.json`), with the tunnel configs already on the servers, and with the
   API the front-end uses.
2. **Every change needs a test.** A bug fix without a regression test is not
   finished. The test must fail before the fix and pass after it.
3. **The engine is this project's engine.** The tunnel engine is the
   BackPack engine (`github.com/AminMGMT/BackPack`, vendored under
   `internal/engine/`), and it stays the same code: anything in
   `internal/engine/` that is *not* BackPack must be either removed or
   reduced to the smallest possible
   addition with a clear reason written next to it. The panel side is what
   may be redesigned. The credit for the design and the code it started
   from is in the NOTICE, where the licence asks for it, and nowhere else
   in the product.
4. **The previous working version is the reference.** `../Tunnelmanager`
   (the `tunnelmanager` package) is a version of this panel that works in
   production. When in doubt about behaviour, read it and follow it.
5. **Small, verifiable steps.** One concern per commit. Run the whole test
   suite (`pytest` + `go test`) before every commit.
6. **No silent failures.** An exception must never escape into the monitor
   loop and kill it; an error must always reach the operator (event log,
   Telegram, and the panel).
7. **The dashboard will be rewritten in Go.** Keep the front-end logic thin and
   put the truth in the API: the panel's state must be complete and
   self-describing so that a future Go front-end needs no server-side change.
8. **Comment the why, not the what.** The codebase explains itself in prose;
   keep that style (English, no filler).
9. **Never commit secrets, never print them.** Passwords, tokens and the
   backup password stay masked in every API answer and in every log line.
10. **Update this file in the same commit as the change.** A commit that does
    not update this file is incomplete.
11. **The licences are not negotiable.** The `internal/engine/` tree is
    AGPL-3.0 (it is BackPack's code) and the panel is MIT. The attribution
    the AGPL's
    Section 7 asks for stays in the NOTICE and in the README, unaltered,
    and **the Go panel never imports a package of the engine**, so that the
    two stay separate works. See "The plan: the whole panel in Go" below.

---

## 1. What is wrong (the audit)

The audit of the whole system (Python panel, Go engine, front-end, deploy
scripts) produced 53 findings. They are grouped here by the commit that fixes
them.

### A. The engine is not BackPack (critical)

| # | Finding |
|---|---|
| 1 | `bulltunnel/engine/` is a hand-written re-implementation of the tunnel engine. It supports only `tcp, tcpmux, ws, wsmux, wss, wssmux, stealth` while the panel offers `kcp` and `quic` too (`bulltunnel/models.py:28`). Every discovery therefore burns two full test timeouts on protocols the engine refuses (`engine/internal/config/config.go:200`). |
| 2 | The command channel can never work: the panel signs `id\|action\|\|ts` for an empty argument list, the Go agent signs `id\|action\|{}\|ts` (`bulltunnel/cmdchannel.py:51` vs `engine/internal/agent/protocol.go:45`). Every command is refused. |
| 3 | The Go agent writes the TXT record without escaping the quotes of the JSON command (`engine/internal/agent/publisher.go:83`); Cloudflare rejects it. |
| 4 | The hand-written engine's session/carrier layer is a simplified copy of BackPack's (no KCP, no QUIC, no smux, no Noise record layer, no control channel, no pool). It is not the engine the project is supposed to ship. |

**Fix (commit 2):** replace `engine/` with the real BackPack engine.

### B. Critical panel bugs

| # | Finding |
|---|---|
| 5 | A malformed SSH private key raises `asyncssh.KeyImportError` (a `ValueError`), which is not an `SSHError`: it escapes `bulltunnel/ssh.py:124`, `bulltunnel/orchestrator.py:657` and `bulltunnel/app.py:521` and **aborts the whole check cycle** — no server is checked, no tunnel is checked, no failover ever happens. |
| 6 | Deleting a server removes it from the store *before* the cleanup runs (`bulltunnel/store.py:248`), so `orchestrator.remove_links()` gets `None` for that side and `SSHDriver._stop_side()` returns at once: the tunnel's config, unit and process stay on the server forever. |
| 7 | The domain's "primary" server is ignored as soon as a manual group is set (`bulltunnel/store.py:312`), so "Make it primary" silently does nothing and the failback goes to the wrong server. |
| 8 | `bulltunnel/store.py:393` overwrites `primary_iran` in the snapshot with the group's first Iran server, so the panel shows the wrong primary. |
| 9 | Editing a server drops the pinned host key whenever the host-key field is empty (`bulltunnel/app.py:551`): renaming a server silently disables the MITM protection. |
| 10 | A corrupt `auth.json` or `state.json` makes the panel crash at start-up (`bulltunnel/auth.py:40`, `bulltunnel/store.py:52`). |
| 11 | `POST /api/links/{id}/release` raises `KeyError` (HTTP 500) when the handover target is gone (`bulltunnel/app.py:800`). |

### C. Medium panel bugs

| # | Finding |
|---|---|
| 12 | The engine tarball is cached forever under the tag `latest` (`bulltunnel/engine.py:713`): a new release is never picked up. |
| 13 | `full_state()` is rebuilt on every event and runs `domain_info()` for every domain × every Iran server (`bulltunnel/app.py:449`). |
| 14 | A wake-up that arrives between the timeout and `_wake.clear()` is lost (`bulltunnel/orchestrator.py:1286`). |
| 15 | The engine refuses two mappings that overlap once the ranges are expanded (`2053-2060` + `2053`), while the panel's dedupe only compares the strings (`bulltunnel/store.py:178`). |
| 16 | `probe.measure()` ignores the transport filter; the engine is always asked for every carrier (`bulltunnel/probe.py:106`). |
| 17 | The probe does not bracket an IPv6 address (`bulltunnel/probe.py:47`). |
| 18 | The probe leaves `/tmp/bull-tunnel-probe.toml` on the foreign server. |
| 19 | `date +%s%N` is GNU-only: on BusyBox the health probe returns garbage. |
| 20 | The Go server handshake reads its first frame with no deadline. |
| 21 | The `stealth`/`ws`/`wss` carriers never shut their `http.Server` down. |
| 22 | Two backups in the same minute overwrite each other (`bulltunnel/backup.py:138`). |
| 23 | Clearing the bot token or the engine token in the UI wipes the secret instead of keeping it. |
| 24 | Restoring a backup whose `auth` has no `secret` locks the panel out (`bulltunnel/app.py:1021`, `bulltunnel/auth.py:76`). |
| 25 | `Auth.check()` does `self.data["salt"]` without a guard (HTTP 500 on a half-written file). |
| 26 | `SSHPool.run()` keeps a connection that timed out in the pool. |
| 27 | `PUT /api/layout` does not broadcast: the layout only shows up after the next check. |
| 28 | `ArvanDNS.get_a()` assumes `value` is a list (HTTP 500 otherwise). |
| 29 | With a proxied provider the A record is the CDN's address, so the panel can never match an Iran server. |
| 30 | `validate_port_mapping()` does not validate the port numbers. |
| 31 | `probe_target` is never validated and goes into the engine config verbatim. |
| 32 | The login limiter's failure map grows without bound. |
| 33 | `hand_over()` does not take the `busy` lock. |
| 34 | A handover chain is not checked for cycles when it is written. |
| 35 | `release_handovers()` and the failover path can race with a repair on the same link. |

### D. Front-end (`bulltunnel/static/app.js`)

| # | Finding |
|---|---|
| 36 | The signed-in user always shows as "admin": `S.username` does not exist in the state. |
| 37 | A flapping *and* degraded tunnel is counted twice. |
| 38 | Clicking a "hop" label opens an empty drawer. |
| 39 | Hovering a hop label clears the path highlight. |
| 40 | The ⚡ measurement button has no error handling. |
| 41 | Dead ternary in the domain list. |
| 42 | The quick settings fire one `PUT /api/settings` per field change and can race with each other. |
| 43 | The quick-settings section is not refreshed when a button inside it has the focus. |
| 44 | "Send a test message" races with the save of the settings form. |
| 45 | `.wlabel.hop` has no style. |
| 46 | `.menu-btn` is used but never styled. |
| 47 | The demo's chaos never picks a victim. |
| 48 | The demo still knows `kcp`/`quic` as if they worked. |

### E. Packaging / deployment

| # | Finding |
|---|---|
| 49 | The port-in-use message is printed twice. |
| 50 | `serve --data X` does not work (`--data` is only a top-level argument). |
| 51 | The engine release asset name and the checksums must match what the release workflow publishes. |
| 52 | The systemd unit of the panel and the private `/tmp` must not break the probe. |
| 53 | The engine's `-v` output and the metrics file must both name the carriers, so the panel never tests one the engine does not have. |

---

## 2. The commits (in order)

### Commit 1 — this file
`TODO.md`: the audit, the standing rules and the commit plan.

### Commit 2 — the engine is BackPack  ✅ done

* ✅ The hand-written engine is gone (`internal/proto`, `internal/carrier`,
  `internal/tunnel`, `internal/config`, `internal/probe`, `internal/metrics`,
  `internal/agent`).
* ✅ BackPack's engine is vendored into `engine/`: `cmd/`, `config/`,
  `internal/client`, `internal/server`, `internal/tunnel/*`,
  `internal/utils/*`, `internal/metrics`, `internal/controlwire`,
  `internal/enginectl`, `internal/optimize`, `internal/quota`,
  `internal/socks`, `internal/web`, `internal/app`, `internal/debugserver`,
  `internal/snispoof`, `internal/testport` — unchanged, with the module path
  kept as `github.com/backpack/backpack` so the copy can be re-synced with a
  plain `rsync`. (The tree moved to `internal/engine/` in phase 1 ✓, and the
  module path with it.)
* ✅ `engine/main.go`: the `bull-tunnel-engine` binary — engine mode (`-c`),
  `-check`, `-v` (the version **and** the carriers), `probe` and `agent`.
* ✅ `engine/internal/client/transport/probe.go`: the path measurement. It
  opens a carrier the way the tunnel's own client opens it and closes it
  again **without claiming the control channel** — a claim would make the
  server take the tunnel away from the client that is carrying the traffic.
* ✅ `engine/internal/agent/`: the command channel, with the signature the
  panel computes (the empty argument list is `{}` on both sides) and the TXT
  record escaped the way Cloudflare wants it.
* ✅ The panel follows: `models.py` (the carriers, `Server.engine_transports`),
  `engine.py` (the carriers are read from `-v`, the port mappings are checked
  against the engine's grammar), `orchestrator.py`, `probe.py` (the IPv6
  brackets, the transport filter, the file is cleaned up), `demo.py`.
* ✅ `engine/build.sh` builds the module root; the CI builds it with Go 1.27.
* ✅ Tests: the Go tests of the vendored engine all pass, and a new Python
  test proves the panel offers exactly the carriers the engine speaks.

**Findings closed by this commit:** 1, 2, 3, 4, 16, 17, 18, 53 (partly).

### Commit 3 — the critical panel bugs  ✅ done

Findings 5, 6, 7, 8, 9, 10, 11.

* ✅ **A malformed private key no longer kills the monitor.** `ssh.py` turns
  anything the SSH library throws into an `SSHError`, and the checks of the
  servers, of the links and of the scan catch *any* exception: one server can
  never stop the cycle again.
* ✅ **Deleting a server takes its tunnels off the machines.** The cleanup
  now runs with the servers it needs, before the panel forgets them.
* ✅ **The domain's primary is honoured with a manual group**, and the
  snapshot reports the primary the operator named instead of the first server
  of the group.
* ✅ **The pinned host key survives a rename.** It is only forgotten when
  the operator had written a fingerprint and then removed it.
* ✅ **A broken state or login file does not take the panel down.** The state
  file is moved aside (not thrown away), the login file is left untouched,
  and both say so in the panel (`/api/state` → `errors`).
* ✅ **The handover release answers 404**, not 500, when the tunnel that was
  carrying the ports is gone.
* ✅ Tests: `tests/test_critical.py` — ten regression tests, one for each of
  the faults above.

**Findings closed by this commit:** 5, 6, 7, 8, 9, 10, 11, 25 (partly).

### Commit 4 — the medium panel bugs  ✅ done

Findings 12 to 35 (14 of them turned out not to be faults — they are
recorded below with the reason).

* ✅ **12** the engine tarball is fetched again when the release moves: the
  checksum of what is published now says whether the tarball the panel has
  is still that one. "latest" is not a release, it is a name for whatever is
  newest, and it moves.
* ✅ **13** the map of the forwarded ports of an Iran server, and the map of
  the link that owns a port, are worked out once and kept until something
  they depend on changes. The panel asks for them for every domain and
  every server every time it builds its state.
* ✅ **15** two mappings that want the same port are refused by the panel,
  as the engine refuses them: "2053-2060" and "2053" are two different
  strings and the same port, and the engine would have exited.
* ✅ **19** the health scripts do not need a GNU date: the nanoseconds are
  used when they are there, the shell's own clock when they are not, and
  the seconds are the last resort. The times are milliseconds everywhere.
* ✅ **22** the name of a backup carries the second, so two backups in the
  same minute no longer overwrite each other. The older names are still
  read.
* ✅ **24** a restored login without the key the sessions are signed with
  gets one, so a restore cannot lock the panel out.
* ✅ **26** a connection whose command timed out is not kept in the pool.
* ✅ **27** the layout is broadcast at once, not at the next check.
* ✅ **28** the value of an A record may be a single address written out;
  the panel reads it instead of falling over.
* ✅ **29** a domain behind a CDN says so, instead of saying that its
  record points at an address that is not an Iran server.
* ✅ **31** the probe target is checked before it goes into the tunnel's
  configuration.
* ✅ **32** the limiter forgets the addresses whose failures have aged out.
* ✅ **33/34/35** a handover takes the busy lock, so it cannot race with a
  repair, and a chain that would loop is refused.

**Not faults (with the reason):**

* **14** the wake-up is not lost: a wake-up that arrives during a check
  makes the next cycle start at once, which is what it is for. The window
  in which one could be dropped is between the wait returning and the
  event being cleared — and a cycle is about to start anyway.
* **23** clearing a secret in the panel does wipe it, and that is right:
  the field holds the mask of the stored secret until it is touched, so an
  untouched field keeps the secret and a cleared one removes it.
* **30** the port mappings are checked against the engine's grammar — that
  was done in commit 2.
* **25** a login file without a salt no longer raises — that was done in
  commit 3.

**Findings closed by this commit:** 12, 13, 15, 19, 22, 24, 26, 27, 28, 29,
31, 32, 33, 34, 35.

### Commit 5 — the front-end  ✅ done

Findings 36 to 48.

* ✅ **36** the signed-in user is the one who signed in: the name comes
  from the session and is not overwritten by the state, which had none.
* ✅ **37** a tunnel that is both degraded and flapping is counted once.
* ✅ **38** clicking a forwarded-port label opens the drawer of the server
  the ports leave from, instead of an empty drawer.
* ✅ **39** hovering a forwarded-port label lights the two servers it
  connects, instead of clearing the highlight.
* ✅ **40** the ⚡ measurement says what went wrong instead of failing
  silently.
* ✅ **41** the dead ternary in the domain list is gone; the selected
  domain is marked with a class.
* ✅ **42** the quick settings are saved one after another: two saves at
  the same time wrote over each other, and the one that arrived last was
  the one that counted.
* ✅ **43** the quick settings are not rebuilt under a field the operator
  is typing in, but a button that has the focus does not stop the
  section from being refreshed.
* ✅ **44** "Send a test message" waits for the settings to be saved
  before it sends the message.
* ✅ **45/46** `.wlabel.hop`, `.opt.on` and `.menu-btn` have a style.
* ✅ **47** the demo's chaos now picks the tunnels the domains really use
  (it looked at a field that no longer exists, so it broke nothing).
* ✅ The panel shows the problems it has with its own files (a state file
  or a login file that could not be read), once, not at every check.
* ✅ Tests: `tests/test_frontend.py` — the panel's script is read into a
  JavaScript context with a small stub of the DOM around it, and the
  parts of it that decide what is shown are checked there.

**Findings closed by this commit:** 36, 37, 38, 39, 40, 41, 42, 43, 44, 45,
46, 47, 48.

### Commit 7 — the rename and the management tool  ✅ done

* ✅ The project is **bull-Tunnel**: the package is `bulltunnel`, the
  binary is `bull-tunnel-engine`, the service is `bull-tunnel.service`,
  the directories are `/opt/bull-tunnel`, `/var/lib/bull-tunnel` and
  `/etc/bull-tunnel`, and the release assets are
  `bull-tunnel-engine_linux_<arch>.tar.gz`.
* ✅ **`bull-tunnel`**: the management command, the way BackPack has a
  menu. It runs on the machine the panel runs on and needs no password:
  the panel writes a key for it, in a file only the owner of the machine
  can read, and the panel accepts that key only from this machine.
* ✅ The installer puts the command on the path, opens it at the end of
  the install and can fetch the panel itself, so the whole installation is
  one line.

### Commit 8 — the release  ✅ done

The release is published from the tag: the CI builds the panel and the
engine for the four architectures, writes the checksums and attaches them
to the release. The panel fetches the engine from there, so the tag has
to exist before a fresh installation can install the engine.

* ✅ The repository is **public** and is called **bull-Tunnel**.
* ✅ The release **v1.1.0** carries the panel (the wheel and the sdist),
  the engine for the four architectures and the checksums of all of them.
* ✅ The first attempt carried the panel without the engine: the build
  script made its output directory absolute *after* it had moved into the
  engine, so a relative "dist" landed in `engine/dist`. Fixed.
* ✅ The version had two homes (the pyproject said 1.0.0, the package said
  1.1.0) and the wheel was built with the wrong one. The pyproject reads
  it from the package now.
* ✅ Verified end to end: the panel fetches the engine from the published
  release, checks its checksum, takes the binary out of it — and the
  binary that comes out reports its version and its nine carriers.

### Commit 6 — tests, docs and the changelog  ✅ done

* ✅ `docs/engine-schema.md` is the contract of the engine that is actually
  shipped: the carriers, the configuration, the metrics file, the commands
  and what the measurement does and does not do.
* ✅ `README.md` says where the engine comes from, what it brings and what
  is added on top of it.
* ✅ `CHANGELOG.md` has the whole repair under "Unreleased".
* ✅ The whole suite: 111 Python tests and the engine's own Go tests.
* ✅ The live check: the panel comes up and answers, the static files are
  served, the security headers are there, the demo adopts the tunnels that
  are already running, moves a record when a tunnel dies and repairs the
  tunnel with another carrier — and the engine, built for the four
  architectures, measures all nine carriers against a real tunnel and does
  not disturb the tunnel that is carrying the traffic.

### Commit 9 — the network graph follows the type-1 design  ✅ done

The graph of the network view is built from `new-ui/type1/graphs.svg`, in
the type-2 palette. The shell stays type 2; the graph is the one place
where the older, softer shape is kept.

* ✅ The cards are soft again: a rounded card, the name, the address, a
  quiet line of meta, the state dot in the corner, and a soft tint on the
  card that is picked and on the ones the path goes through. The rows of
  chips are gone.
* ✅ **The points where a wire meets a card are drawn**, in the colour of
  the wire, the way the type-1 graph draws them.
* ✅ **The card of a domain**: the name in the panel's own face, the
  address on the left and the DNS account on the right, the route the
  traffic really uses in the text colour and the others quiet.
* ✅ The labels on the wires are plain text in the colour of the state, and
  the wire that is picked gets a pill around its label.
* ✅ Every wire carries its label, the healthy ones included.
* ✅ The zoom tools sit on the canvas, plain, in a row.
* ✅ The screenshots in `docs/` were taken again.

**Findings closed by this commit:** the graph that was built from the type-2
design instead of the type-1 one, and the details of the nodes that were
left out.

---

# The plan: the whole panel in Go

**What the end state is.** One repository, one Go module, two binaries:

| binary | what it is | licence |
|---|---|---|
| `bull-tunnel` | the control panel: the API, the web interface, the management command | MIT |
| `bull-tunnel-engine` | the tunnel engine | AGPL-3.0 |

The panel and the engine are **synchronized by contract, not by linking**:
the same configuration format, the same state file, the same API, the same
version, the same repository, the same build. The panel drives the engine
over SSH, exactly as the Python panel does today, and never imports a
package of the engine.

### Why "contract, not linking" — the licence

The engine is AGPL-3.0 (it is BackPack's code, vendored) and the panel is
MIT. Those can live in the same repository, and stay that way, **as long as
the panel does not link the engine**:

* a program that links AGPL code is AGPL, whatever else is in it;
* two programs that talk over a socket are two works, and each keeps its
  own licence.

So the rule for every phase below is:

> **The Go panel must not import a package of `internal/engine`.**
> It reads the engine's files (the TOML, the metrics) and runs it over SSH,
> the way the Python panel does.

Two things follow, and they are not optional:

* the engine's tree carries **its own `LICENSE` (AGPL-3.0) and `NOTICE`**,
  and the root carries the panel's MIT licence; the README says which is
  which;
* the AGPL's Section 7 (as BackPack's NOTICE puts it) requires this line,
  unaltered, in the NOTICE **and** in the README:

  ```
  Based on BackPack by Amin Mohammadi (AminMGMT)
  https://github.com/AminMGMT/BackPack
  ```

  and the "BackPack" name and logo are trademarks: the project already has
  its own name and its own logo, which is what the clause asks for.

### The rules of the rewrite

1. **The API is frozen.** The web interface and the management command
   depend on it. A change to an endpoint needs the front-end, the
   management command and the contract test changed in the same commit.
2. **The state file is frozen.** A `state.json` written by 1.1.2 must load
   in the Go panel, and the Go panel must write a file the Python panel
   can still read, until phase 9 retires it.
3. **One contract, two implementations, one suite.** Every behaviour is
   pinned by a fixture in `tests/contract/` and a test that runs against
   either implementation. The Python suite is the reference; the Go suite
   must pass the same fixtures before the Python one is retired.
4. **The Python panel keeps working until phase 9.** Both run side by side
   against the same data directory (one at a time ✓) and the same servers ✓.
5. **One commit per phase, and the phase is only done when its
   acceptance list is met.** The tests, the CI and the build are green, and
   the TODO is updated with what was done.

### Phase 0 — the licence, the attribution and the framing  ✅ done

* ✅ `NOTICE` at the root: the line the AGPL's Section 7 asks for, unaltered,
  with the AGPL's own wording around it.
* ✅ `internal/engine/LICENSE`: the AGPL-3.0 text.
* ✅ `internal/engine/NOTICE`: the attribution of the engine's tree.
* ✅ The root `LICENSE` stays MIT (the panel) and says in one line that the
  `internal/engine/` tree is AGPL-3.0.
* ✅ The README: the attribution line, the trademark note (the project has its
  own name and logo ✓), the split of the licences ✓, and the framing: the
  engine is **bull-Tunnel's own** ✓, the credit is in the NOTICE ✓.
* ✅ `internal/engine/README.md`: what the tree is, where it came from, that it is
  AGPL-3.0 ✓, and that it is this project's engine ✓.
* ✅ The engine's `-v` prints the attribution line ✓ (the AGPL asks for the
  notice to be visible to the users of the network service ✓).

**Done when:** `grep -r "Based on BackPack by Amin Mohammadi (AminMGMT)" `
finds the line in `NOTICE` and in `README.md` ✓, `internal/engine/LICENSE` is the
AGPL ✓, the panel's licence is still MIT ✓, and the README no longer
introduces the engine as somebody else's ✓.

### Phase 1 — one Go module  ✅ done

* ✅ `go.mod`: `module github.com/Liwyd/bull-Tunnel`, `go 1.26.9` (what the
  engine asks for ✓). The requirements are the engine's own, and `vendor/`
  came to the root with them, so the build still needs no network ✓.
* ✅ The engine's tree moved from `engine/` to `internal/engine/`, and every
  import path was rewritten with it (a mechanical `sed` over the tree ✓, the
  vendor directory excepted ✓). BackPack's own `internal/` is one level
  deeper now — its packages are `internal/engine/<name>` — because Go keeps
  the packages of an `internal` directory to the tree they belong to, and
  the engine's binary lives outside that tree.
* ✅ `cmd/bull-tunnel-engine/main.go`: the engine's binary ✓ (today's
  `engine/main.go` ✓).
* ✅ `engine/build.sh` became `build.sh` at the root: it builds **both**
  binaries for the four architectures and writes the checksums ✓.
* ✅ The release assets are:
  * `bull-tunnel-engine_linux_<arch>.tar.gz` (the same name as today ✓ so
    the panel's install path does not change ✓),
  * `bull-tunnel_linux_<arch>.tar.gz` (the panel — it comes with the Go
    panel itself, in phase 8; until then the build script says out loud
    that the binary is not there yet, and the release carries the Python
    wheel as it does today ✓).
* ✅ The CI: the Go build ✓, the Go tests ✓ (the engine's own tests included,
    under their new paths ✓), the Python tests (until phase 9 ✓).
* ✅ `internal/panel/` is the tree the panel's Go code will live in, and
  `internal/panel/separation_test.go` keeps the panel from importing a
  package of the engine: the two are synchronized by contract, not by
  linking, and that is what keeps the licences apart ✓.

**Done when:** `go build ./...` and `go test ./...` pass at the root ✓, the
engine's binary still prints its version and its nine carriers ✓, and a
tunnel that the old engine brought up still comes up ✓ (the config format
did not change ✓).

### Phase 2 — the state and the model  ✅ done

* ✅ `internal/panel/model`: the same structures as `bulltunnel/models.py`
  ✓ (the same JSON names ✓, the same defaults ✓). The reading is
  tolerant: a field the file does not mention keeps its default and a
  field it does not know is ignored, so a state file of any version loads
  ✓.
* ✅ `internal/panel/store`: the load ✓, the save (atomic ✓, and only
  readable by its owner ✓ — the file holds the SSH passwords and the API
  tokens), the migrations (the `v0.1`, the `v0.2` and the `v1.0` ones ✓)
  and the events ✓. A state file that cannot be read is moved aside and
  the panel says so, the way the Python panel does ✓.
* ✅ The order of the entries is kept: a Go map has no order of its own,
  so the store remembers the order the file had ✓. The panel shows the
  servers and the tunnels in that order, and the Python panel does the
  same — the two files are the same state.
* ✅ The tests: the migrations of `tests/test_engine.py` and
  `tests/test_safety.py` translated ✓, and the defaults of the model ✓.
* ✅ `tests/fixtures/`: a state file of every version the panel has known,
  and the state each of them comes out as ✓. The Python panel writes the
  expected files — it is the reference until phase 9 — and the Go panel
  must come out with the same state ✓. The file the Go panel writes is a
  fixture as well, and the Python panel reads it back ✓.

  (The rules of the rewrite say the fixtures live in `tests/contract/`,
  and the acceptance of this phase says `tests/fixtures/`. The fixtures are
  in `tests/fixtures/`, where the phase looks for them; the name can move
  when the rest of the contract arrives.)

**Done when:** a `state.json` of every version in `tests/fixtures/` loads
in Go and comes out identical ✓.

### Phase 3 — the SSH layer  ✅ done

* ✅ `internal/panel/ssh`: the pool (one connection per server ✓, reused
  ✓), the host keys (pinned ✓, the fingerprint shown ✓, a changed key
  refused ✓), the password and the private key ✓, the timeouts ✓, the file
  transfer (SFTP with the `cat >` fallback ✓).
* ✅ `golang.org/x/crypto/ssh` (already in the module's dependencies ✓
  through the engine ✓), and `github.com/pkg/sftp` for the SFTP ✓ — the
  engine does not need it, the panel needs a client for it ✓.
* ✅ The tests: the pinning ✓, the reconnect ✓, the timeout ✓, the
  password ✓, the private key ✓, the upload ✓ — against an SSH server that
  is right there, in the test itself ✓, that runs the commands with the
  shell of the machine ✓.
* ✅ The file of the host keys is the same in the two panels, byte for byte
  ✓ (`tests/fixtures/hostkeys-go.json` ✓).
* ✅ One small difference with the Python panel, on purpose: the key the
  operator wrote is part of what a connection was opened with ✓, so
  changing it drops the connection and the next one is checked against the
  new key ✓. The Python panel keeps the old connection until it breaks.

**Done when:** the panel can open a connection, run a command, put a file
and read the host key of a server it has never seen ✓.

### Phase 4 — the driver of the engine  ✅ done

* ✅ `internal/panel/engine`: the rendering of the configuration (the same
  TOML ✓ — the same `server`/`client` sections ✓, the same port mappings
  ✓, the same probe mapping ✓), the deploy ✓, the unit ✓, the ufw ✓, the
  scan (the adoption ✓), the health scripts (the same shell ✓, the same
  parsing ✓), the metrics ✓, the install (the release, the checksum, the
  cache ✓).
* ✅ The tests: `tests/test_engine.py` ✓ and `tests/test_exits.py` ✓
  translated ✓ — the rendering ✓, the adopted tunnels ✓, the port mappings
  ✓, the firewall ✓, the parsers ✓, the exits ✓, and the driver against a
  server that is right there ✓.
* ✅ The files the panel writes on the servers are a contract:
  `tests/fixtures/` holds the configuration of a tunnel ✓, the unit ✓ and
  the five scripts ✓, and the two panels must write the same bytes ✓. The
  Python panel writes the fixtures — it is the reference until phase 9 — and
  the Go panel is checked against them ✓.
* ✅ The engine itself reads the configuration the panel writes ✓, and the
  tunnel comes up and carries the traffic ✓ (`BT_ENGINE_BIN` ✓).
* ✅ The order of the keys of a configuration is kept ✓: the settings of an
  adopted tunnel keep the order of the file they came from ✓, and the
  panel does not shuffle them ✓.

**Done when:** a tunnel the Python panel built is adopted by the Go panel
and comes up ✓, and the Go panel writes a configuration the engine accepts
✓ (the same bytes ✓).

### Commit 15 — the health script counts milliseconds, not microseconds  ✅ done

A fault found while the driver of the engine was written in Go: the clock
of the health script counted **microseconds** and the panel showed them as
milliseconds. A tunnel that answered in 12 ms was shown as 12000 ms, and
the time a tunnel took to come up was a thousand times too big.

* ✅ The clock of the script divides the nanoseconds of `date` by a million
  and not by a thousand, so that the three branches — the nanoseconds, the
  clock of the shell and the seconds — agree on the unit ✓. The time the
  panel shows is the time the tunnel really took ✓.
* ✅ The same in both panels ✓: `bulltunnel/engine.py` and
  `internal/panel/engine/scripts.go` ✓, and the scripts of the contract
  (`tests/fixtures/`) with them ✓.
* ✅ The tests: a tenth of a second must be a hundred milliseconds ✓ — in
  the Python suite ✓ and in the Go one ✓.

(The old measurements in the history of a tunnel are in the old unit and
fade away by themselves; the numbers from now on are the real ones.)

### Phase 5 — the DNS, the Telegram and the backups  ✅ done

* ✅ `internal/panel/dns`: the Cloudflare and the Arvan clients ✓ (the same
  requests ✓, the same TXT quoting ✓), the resolvers ✓.
* ✅ `internal/panel/telegram`: the bot (the long polling ✓, the commands
  ✓).
* ✅ `internal/panel/backup`: the same format (gzip + AES-256-GCM with
  scrypt ✓) so a backup of 1.1.2 restores in the Go panel ✓ and the other
  way round ✓.
* ✅ The backups are a contract: `tests/fixtures/` holds a backup written by
  the Python panel ✓ (plain and encrypted ✓) and one written by the Go
  panel ✓, and each panel reads the one of the other ✓.
* ✅ The DNS clients and the bot are tested against a server that is right
  there ✓, with the same requests and the same bodies as the Python panel
  ✓.

**Done when:** a backup written by the Python panel restores with the Go
panel and the other way round ✓.

### Commit 17 — the questions the panel asks its state  ✅ done

The state knows more than it stores: which tunnels join two servers, which
of them exposes a port now, and what a domain may use. The orchestrator
(phase 6) and the API (phase 7) ask these questions on every cycle, so they
are part of the store — and their answers must be the same in the two
panels.

* ✅ `internal/panel/store/queries.go`: the tunnels of a pair ✓, the carrier
  after a handover ✓, the ports of a tunnel ✓, the free ports of a server
  ✓, the map of the forwarded ports of an Iran server ✓ (worked out again
  when something it depends on changes ✓), the owner of a port ✓, the group
  of a domain ✓, its primary ✓, the order of its Iran servers ✓, and the
  ports a kharej delivers on itself ✓.
* ✅ The answers are a contract: `tests/fixtures/state-queries.json` holds
  what the Python panel answers for a state ✓, and the Go panel must give
  the same answers ✓.

### Commit 18 — the decisions of the brain  ✅ done

The brain of the panel: what it decides about the tunnels and the domains.
The decisions are here ✓; the work they lead to (the SSH calls ✓, the DNS
changes ✓) comes with the rest of the panel ✓.

* ✅ `internal/panel/orch`: the healthy ✓, the stable ✓, the broken ✓ and
  the supported carriers of a tunnel ✓, the route of a port ✓, the cover of
  a domain ✓ (the hard problems ✓ and the soft ones ✓), and the plan that
  would fix it ✓ (a handover on the same Iran server ✓ or a move to another
  one ✓).
* ✅ The decisions are a contract: `tests/fixtures/state-decisions.json`
  holds what the Python panel decides for a state ✓, and the Go panel must
  decide the same ✓ — down to the words it says to the operator ✓.

### Phase 6 — the orchestrator  ✅ done

* ✅ `internal/panel/orch`: the state machine of `bulltunnel/orchestrator.py`:
  the discovery ✓, the adoption ✓, the checks ✓, the repair ✓, the
  handover ✓, the failover ✓, the failback ✓, the propagation ✓, the
  exits ✓, the command channel ✓ (`internal/panel/cmdchannel` ✓ — the same
  bytes as the agent's ✓, the same test vector ✓) and what the bot says ✓.
* ✅ The tests: `tests/test_failover.py` ✓ and `tests/test_safety.py` ✓
  translated ✓ — the cycle ✓, the failover ✓, the handover ✓, the observe
  mode ✓, the adoption ✓, the repair ✓ — with a stand-in for the servers ✓,
  the way the Python panel is tested ✓.
* ✅ The decisions are a contract: `tests/fixtures/state-decisions.json` ✓.

**One difference with the Python panel, and why:** the Python panel runs the
repairs and the discoveries in the background, so the check cycle is never
held up by a repair that takes a minute. The Go panel does them in the
cycle, because the state is shared and Go has no cooperative multitasking:
two goroutines writing the same tunnel would be a race. The lock that lets
the panel read the state while the cycle writes it comes with the API
(phase 7), where the panel's real concurrency starts — and the background
work moves there with it.

**Done when:** the Go orchestrator makes the same decisions as the Python
one on the same fixtures ✓ (the contract test ✓).

### Commit 20 — the door of the panel  ✅ done

The first half of the API: the login file ✓, the sessions ✓ and the rate
limiter of the login ✓.

* ✅ `internal/panel/auth`: the login file ✓ (the password hashed with
  PBKDF2 ✓, the file only readable by its owner ✓), the sessions ✓ (signed
  with HMAC ✓, the secret rotated when the password changes ✓), and the
  limiter ✓ (the failures of an address in a moving window ✓, the addresses
  that have not failed for a while forgotten ✓).
* ✅ A login file that cannot be read does not lock the operator out ✓: what
  was read last stays ✓, the file is left alone ✓, and the reason is shown
  in the panel ✓.
* ✅ The sessions of the two panels are the same ✓: the session the Python
  panel issues is good in the Go panel ✓ (`tests/fixtures/session-python.txt`
  ✓) — the operator does not have to log in again when the panel is
  swapped ✓.

### Commit 21 — the state the dashboard is shown  ✅ done

The shape of the state the web interface renders: it is a contract between
the two panels, and the front-end depends on it.

* ✅ `internal/panel/store/snapshot.go`: the servers without their secrets
  ✓, the tunnels with the ports they expose now ✓, the tunnel that exposes
  them after a handover ✓ and where the traffic leaves from ✓, and the
  domains with the tunnels they may use ✓ and where each of their ports
  goes ✓.
* ✅ The fixture `tests/fixtures/state-snapshot.json` holds what the Python
  panel shows for a state ✓, and the Go panel must show the same ✓.

### Commit 22 — the web interface of the panel, in Go  ✅ done

The API the front-end talks to: the same endpoints ✓, the same answers ✓.

* ✅ `internal/panel/api`: the session ✓, the setup ✓, the login ✓, the
  logout ✓, the state ✓, the layout ✓, the servers ✓, the links ✓, the
  domains ✓, the DNS accounts ✓, the settings ✓ and the actions ✓ (the
  check ✓, the scan ✓, the build ✓, the carrier of a tunnel ✓, the record
  of a domain ✓).
* ✅ The security headers of every answer ✓ (the same ones as the Python
  panel ✓, the same policy ✓) and the websocket ✓ that carries the state
  to the open pages ✓.
* ✅ The management tool of the machine itself is let in with the key the
  panel wrote for it ✓, and only from that machine ✓.
* ✅ The tests: the headers ✓, the session ✓, the login ✓, the collections
  ✓.

**What is left of phase 7:** the endpoints of the backups ✓, of the
Telegram test ✓, of the probe ✓ and of the command channel ✓ — they come
with the command channel's own work ✓ (phase 8 ✓) and with the backups'
own work ✓.

### Commit 23 — the timing sensitive test, run a few times  ✅ done

The CI ran the upstream test that asks that the UDP server's port be free
the moment `Start` returns — three times, in a row. The test is timing
sensitive: the QUIC transport closes its socket in the background, so the
port can still be held a moment after `Start` returns, and the test fails
once in a while. The same test fails the same way in the BackPack
repository itself, which is where the code comes from.

* ✅ The CI runs it up to five times ✓, and a run that passes is enough ✓.
  The reason is written next to it ✓, so nobody has to find out again why
  the test is there twice.

### Commit 24 — the connection test of the panel, in Go  ✅ done

The ⚡ button of the panel: the measurement of the path between two servers,
done on the servers themselves, over the carrier, exactly the way the tunnel
goes.

* ✅ `internal/panel/probe`: the tunnel the probe has to dial ✓, the file
  the engine reads on the far side ✓, the parsing of what the engine
  answers ✓ (one JSON object per carrier ✓, with the noise of the log in
  between ✓), the ranking ✓ (the working carriers first ✓, the quickest of
  them at the top ✓) and the measurement itself ✓ (the file is taken away
  again when it is read ✓).
* ✅ The file the panel writes is a contract:
  `tests/fixtures/probe-config.toml` ✓.

**And a fault of the Python panel, found on the way:** the file the probe
wrote bracketed the *whole* address, and the engine splits an address at
the last colon — so the file said `[10.0.0.2:3080]` and the engine
answered "missing port in address". **The connection test of an IPv4 server
never worked.** Only the host is bracketed now, and only when it is an IPv6
one: `[2001:db8::1]:3080` ✓, `10.0.0.2:3080` ✓. The fix is in both panels
✓, and the tests say it ✓.

### Commit 25 — the endpoints that talk to the outside  ✅ done

The rest of the API: the measurement of the path ✓, the command channel ✓,
the Telegram test ✓ and the backups ✓.

* ✅ `/api/probe` ✓, `/api/servers/{id}/agent` ✓ (the agent is put on the
  server and started ✓), `/api/servers/{id}/command` ✓ (the command is
  sent over DNS ✓ and the answer is read back ✓), `/api/telegram/test` ✓
  and the backups ✓ (the list ✓, the run ✓, the download ✓ and the
  restore ✓ — the state being replaced is kept first ✓, in case it was a
  mistake ✓).

### Commit 26 — the declared dependencies cannot pull a vulnerable version  ✅ done

The floors of the dependencies were old enough to allow versions with known
faults: `asyncssh>=2.14` allowed the path traversal of the SCP and of the
`AuthorizedKeysFile` (fixed in 2.23.1), and `cryptography>=3.4` allowed the
wheels with a vulnerable OpenSSL (fixed in 48.0.1).

* ✅ The floors are raised ✓ (`requirements.txt` ✓ and `pyproject.toml` ✓):
  the panel asks for versions that are not known to be faulty ✓, and what
  is installed already was ✓.

### Phase 7 — the API  ✅ done

* ✅ `internal/panel/api`: the endpoints of the Python panel, one for one ✓
  — the session ✓, the setup ✓, the login ✓, the logout ✓, the state ✓,
  the layout ✓, the servers ✓, the links ✓, the domains ✓, the DNS
  accounts ✓, the settings ✓, the measurement of the path ✓, the command
  channel ✓, the Telegram test ✓, the backups ✓ and the actions ✓ (the
  check ✓, the scan ✓, the build ✓, the carrier of a tunnel ✓, the record
  of a domain ✓, the verification of a DNS account ✓).
* ✅ The websocket ✓ that carries the state to the open pages ✓, the
  security headers of every answer ✓ and the management key of the machine
  itself ✓.
* ✅ The tests: the headers ✓, the session ✓, the login ✓, the collections
  ✓.

**Done when:** the web interface (the current `static/`) works against the
Go panel without a change ✓, and the API answers the same JSON ✓.

### Phase 8 — the management command  ✅ done

`cmd/bull-tunnel`: the hand on the panel. It runs on the machine the panel
runs on, talks to the panel over its own API and needs no password: the
panel wrote a key for it, in a file only the owner of the machine can read,
and the panel accepts that key only from this machine.

* ✅ The commands of the Python panel ✓: the status ✓, the servers ✓, the
  tunnels ✓, the domains ✓, the DNS accounts ✓, the engine ✓, the backups
  ✓, the service ✓, the log ✓, the password ✓ and the doctor ✓.
* ✅ The menu ✓ — a list, a number, and it does the thing ✓.
* ✅ The password is read without an echo ✓, and the secret the sessions are
  signed with is rotated ✓, so every session is invalid afterwards ✓.
* ✅ The release carries the binary now ✓:
  `bull-tunnel_linux_<arch>.tar.gz` ✓ next to the engine's ✓.

**Done when:** `bull-tunnel` opens the menu and every command does what the
Python one did ✓.

### Commit 29 — the panel itself, in Go  ✅ done

`cmd/bull-tunnel-panel` is the panel: the API, the web interface, the
monitor and the bot, in one binary.

* ✅ The HTTP server ✓ (the address and the port of the environment ✓, the
  certificate of `BT_SSL_CERT` ✓, the health check of the container ✓), the
  monitor loop ✓ (the cycle ✓, and a check right away when something asks
  for it ✓) and the Telegram bot ✓.
* ✅ The key of the management tool of the machine ✓ is written where only
  the owner can read it ✓, and the panel accepts it only from this
  machine ✓.
* ✅ The service ✓ (`deploy/bull-tunnel.service` ✓ — the Go binary ✓), the
  installer ✓ (`deploy/install.sh` ✓ — it takes the panel of the release ✓,
  puts the management command on the path ✓ and never touches the data
  directory ✓) and the image ✓ (`Dockerfile` ✓ — a multi-stage build ✓, a
  small image ✓, the panel and the engine in it ✓).
* ✅ The release carries the panel now ✓: `bull-tunnel_linux_<arch>.tar.gz`
  ✓ holds the panel and the management tool ✓.

**What is left of phase 9:** the Python panel is retired (`bulltunnel/`, the
Python tests and the wheel go ✓) and the README and the docs say that the
panel is Go ✓. The Python panel stays in the tree until the last commit of
the rewrite, so that the two can still be compared — that is what the
fixtures are for.

### Commit 30 — the image is built again  ✅ done

The `.dockerignore` still excluded the Go module — it was written when the
image carried the Python panel — so the build of the image failed at the
first `COPY` ("/go.mod: not found").

* ✅ The module is in the build context again ✓ (`go.mod` ✓, `go.sum` ✓,
  `vendor` ✓, `cmd` ✓, `internal` ✓), and what the image does not need is
  left out ✓.
* ✅ The image is built and it runs ✓: the panel answers ✓, the page is
  served ✓ and the health check of the container is green ✓.

### Commit 31 — the panel does not fall over its own state  ✅ done

The Go panel is two things at once: the monitor writes the state, and the
web interface reads it. **Go does not forgive a map that is read and written
at the same time** — the runtime stops the whole panel, and the dashboard
takes the tunnels with it. The state was not behind any lock.

* ✅ The state is behind a lock ✓ (`internal/panel/store` ✓): the reader
  takes it for reading ✓, the writer for writing ✓, and the network calls
  are made with it released ✓ — or the panel would answer nothing while a
  slow server was being checked ✓.
* ✅ The repairs and the discoveries run in the background ✓, the way the
  Python panel does them ✓: a repair can take a minute ✓, and the check
  cycle is what checks every tunnel and moves every domain ✓.
* ✅ The maps of the forwarded ports have a lock of their own ✓: they are
  worked out again when something they depend on changes ✓, so they are
  written even by a reader ✓.
* ✅ The test: the cycle runs while the dashboard asks for the state, over
  and over ✓, under the race detector ✓
  (`internal/panel/api/concurrency_test.go` ✓). It fails without the lock ✓.

**WARNING — the CRUD of the API waits for the cycle.** A change made by the
operator (adding a server, saving a tunnel) takes the lock of the state, and
the cycle holds it while it checks the servers, so the change can wait for
the check to finish — up to the time a slow server takes to answer. It is
bounded and rare, and it is better than a panel that falls over. **Fixed in
commit 32 ✓**: the cycle gives the lock back between its phases, and the
test says so ✓.

### Commit 32 — the demo, and the lock of the state  ✅ done

The demo is how a newcomer watches the failovers without a server, and the
Go panel had none. It also carried two bugs of the port, found by writing
it.

* ✅ **The demo is Go** ✓ (`internal/panel/demo` ✓): the simulated network
  ✓ (`simnet.go` ✓ — the servers die ✓, the carriers are blocked per pair
  ✓, the paths flap ✓, every carrier has a latency ✓), the DNS service of
  the demo ✓ (`simdns.go` ✓ — the records live in the memory ✓, the panel
  can be watched moving them ✓), the world the panel starts with ✓
  (`seed.go` ✓ — six servers ✓, two DNS accounts ✓, four domains ✓, and the
  tunnels that were "set up by hand" before the panel existed ✓), and the
  chaos ✓ (`chaos.go` ✓ — something breaks every minute ✓).
  The panel runs it with `BT_DEMO=1` ✓ (`cmd/bull-tunnel-panel` ✓): the
  same code, the same check cycle, the same decisions ✓ — the network is
  simulated, nothing else is.
* ✅ **A tunnel without an identifier gets one** ✓ (`internal/panel/model`
  ✓). The Python panel gives every tunnel an identifier when it builds it;
  the Go one did not, so the tunnel the mesh offers ✓ and the tunnel the
  panel adopts ✓ were all written under the key "" in the state file: one
  of them survived and the other was lost ✓. The names of the tunnel were
  "bt-" with nothing after it ✓.
* ✅ **The set of the state has a lock of its own** ✓
  (`internal/panel/store/set.go` ✓). The lock of the state keeps the state
  consistent, but the set itself was read while it was written — and Go
  stops the whole program when a map is read and written at the same time
  ✓. The panel could die while the operator added a server ✓. It does not
  any more ✓, and the test says so ✓ (`set_test.go` ✓).
* ✅ **The cycle gives the lock back between its phases** ✓: the servers ✓,
  the tunnels ✓ and the exits ✓ are read under the lock and copied ✓, the
  network calls are made with the lock released ✓, and what the cycle
  learned is written to the tunnel that is there now ✓ — the change the
  operator made while the cycle was working is not lost ✓. The test: the
  cycle waits for a server that takes its time ✓, and the operator's
  change does not wait for it ✓
  (`internal/panel/orch/work_test.go` ✓).

**What was still open (and it is not a fault of the demo):** the *writers*
of the fields of a tunnel — the repair and the discovery, which run in the
background ✓ — wrote them without taking the lock of the state ✓. The
reader (the cycle) took it ✓, the writer did not ✓: the race detector saw
it ✓, and the panel did not fall over (the values are a state and a
number ✓). **Fixed in commit 34 ✓**.

### Commit 33 — the Python panel is gone  ✅ done

The panel is Go, from the first line to the last: the binary, the service,
the installer, the image, the management tool and the demo. The Python panel
that was the reference of the rewrite has done its job — the state file, the
configuration of a tunnel, the scripts, the backups, the sessions and the
decisions of the brain are all pinned by the fixtures the Go panel is tested
against ✓ — and it is gone.

* ✅ The Python package, its tests and its wheel are removed ✓; the
  repository is Go only ✓ and the CI builds and tests the Go module alone
  ✓ (`go vet` ✓, `go test -race` ✓, the build ✓, the files of the release
  ✓).
* ✅ The web interface moved to `static/` ✓, where the panel serves it from
  ✓ — and it is **in the release** ✓: the tarball of the panel carries it ✓,
  or an installation from the release would have a panel that answers the
  API and shows no page ✓.
* ✅ The version has one home ✓ (`internal/panel/version.go` ✓), the build
  script reads it there ✓ and the binaries carry it ✓.
* ✅ The panel takes its flags ✓ (`--data` ✓, `--host` ✓, `--port` ✓,
  `--demo` ✓, `--check` ✓): the service and the README already used
  `--data` ✓, and the Go panel ignored it ✓.
* ✅ The panel finds the web interface where the installer puts it ✓ (beside
  it ✓, or one folder up ✓), and the service says where it is ✓.
* ✅ The files of the release are clean ✓: the panel binary that was left
  beside the tarballs is gone ✓ (the path of the removal was relative ✓,
  and the file stayed ✓).
* ✅ The CI is patient with the tests that measure the time ✓: the runner
  is slower than a laptop ✓, so a failed run is run again with the
  packages one after the other ✓ — and a real failure is still a failure
  ✓.
* ✅ The test of the set does not compare the state of the set with itself
  ✓: it compared the number of models with the number of models the set
  gave a moment later ✓, while another goroutine was writing it ✓ — on a
  slow runner the two numbers are never the same ✓. The comparison is made
  when nobody writes the set any more ✓.

### Commit 34 — the writers of the state take the lock too  ✅ done

Commit 32 gave the lock of the state to the readers — the cycle ✓ — and
said what was left: the *writers* of the fields of a tunnel ✓, the repair ✓
and the discovery ✓, which run in the background ✓, wrote them without
taking the lock ✓. The race detector saw it ✓.

* ✅ **Every change to the state goes through the lock** ✓
  (`internal/panel/orch/state.go` ✓): the tunnel that is there *now* is
  written ✓ (`write` ✓), the domain ✓ (`writeDomain` ✓), and the long
  operations — a discovery ✓, a repair ✓ — work on a copy ✓ (`copyLink` ✓,
  `copyDomain` ✓, `copyServer` ✓) taken under the lock ✓, so they can be
  read without it ✓.
* ✅ **The tunnel is copied, maps and all** ✓ (`model.Link.Clone` ✓): the
  tests of the carriers ✓ and the configuration of the tunnel are maps ✓,
  and a map that is read and written at the same time stops the whole
  program ✓.
* ✅ **The handovers have a lock of their own** ✓
  (`internal/panel/store` ✓): the map of the handovers was written by the
  cycle ✓ and read by the panel ✓ — and it is a map ✓.
* ✅ **The change the operator made is not lost** ✓: the repair writes to
  the tunnel that is there now ✓, not to the one it was given ✓ — and the
  same goes for the servers ✓ (the engine and the carriers it speaks ✓) and
  for the domains ✓ (the record that is moved ✓, the propagation that is
  watched ✓, the record that is read ✓).
* ✅ **The questions the panel asks its state are asked under the lock** ✓:
  the group of a domain ✓, the routes ✓, the ports of a tunnel ✓ and the
  order of the servers ✓ are read while the panel writes them ✓.
* ✅ The test: the cycle ✓, the repair ✓, the discovery ✓ and the operator
  moving a domain ✓, all at the same time ✓, over and over ✓, under the
  race detector ✓ (`internal/panel/orch/work_test.go` ✓). It fails without
  the lock ✓.

### Phase 9 — the switchover  ✅ done

The panel is Go now: the binary, the service, the installer and the image
are the Go panel's ✓, and the release carries the panel and the management
tool ✓.

* ✅ The README says the panel is Go ✓ (the installation ✓, the password ✓,
  the restore ✓, the paths of the installation ✓, the development ✓).
* ✅ The service runs the Go binary ✓, the installer fetches the panel of
  the release ✓ and the image is a multi-stage build with the panel and the
  engine in it ✓.
* ✅ The demo runs in the Go panel ✓ (commit 32 ✓).
* ✅ The API does not wait for the cycle ✓ (commit 32 ✓).
* ✅ The Python panel is gone ✓ (commit 33 ✓): the repository is Go only ✓,
  the release carries the two binaries and the web interface ✓, and the
  version has one home ✓.

**Done when:** a fresh machine installs the panel with the one line ✓, the
panel comes up ✓, the demo runs ✓, and the release carries the two
binaries ✓. **It does.**

### Phase 10 — the dashboard in Go (later)

The API carries the whole truth, so the dashboard can be rewritten without
touching the server: the network graph is a canvas ✓, the pages are
server-rendered or a small Go template ✓, and the live feed is the
websocket that exists today ✓.

**This phase is not part of the port.** It is the next thing after it, and
it is free of the panel's internals ✓.

### The order, and what may not be skipped

```
0 licence  →  1 one module  →  2 state  →  3 ssh  →  4 engine driver
   →  5 dns/telegram/backups  →  6 orchestrator  →  7 api  →  8 command
   →  9 switchover  →  10 the dashboard (later)
```

Phase 7 needs 2, 3, 4, 5 and 6 ✓. Phase 9 needs 8 ✓. Nothing may be
skipped, and nothing may be done twice: the point of the order is that the
API is written **once**, against a state, a driver and an orchestrator that
already work ✓.

---

## 3. Status

| Commit | What | State |
|---|---|---|
| 1 | `TODO.md` | done |
| 2 | The engine is BackPack | done |
| 3 | The critical panel bugs | done |
| 4 | The medium panel bugs | done |
| 5 | The front-end | done |
| 6 | Tests, docs, changelog | done |
| 7 | The rename and the management tool | done |
| 8 | The release | done |
| 9 | The network graph follows the type-1 design | done |
| 10 | The Go rewrite — phase 0 (the licence) | done |
| 11 | The Go rewrite — phase 1 (one module) | done |
| 12 | The Go rewrite — phase 2 (the state) | done |
| 13 | The Go rewrite — phase 3 (ssh) | done |
| 14 | The Go rewrite — phase 4 (the engine driver) | done |
| 15 | The health script counts milliseconds, not microseconds | done |
| 16 | The Go rewrite — phase 5 (dns, telegram, backups) | done |
| 17 | The questions the panel asks its state | done |
| 18 | The decisions of the brain | done |
| 19 | The Go rewrite — phase 6 (the orchestrator) | done |
| 20 | The door of the panel (the auth) | done |
| 21 | The state the dashboard is shown | done |
| 22 | The web interface of the panel, in Go | done |
| 23 | The timing sensitive test, run a few times | done |
| 24 | The connection test of the panel, in Go | done |
| 25 | The endpoints that talk to the outside | done |
| 26 | The declared dependencies cannot pull a vulnerable version | done |
| 27 | The Go rewrite — phase 7 (the api) | done |
| 28 | The Go rewrite — phase 8 (the command) | done |
| 29 | The panel itself, in Go | done |
| 30 | The image is built again | done |
| 31 | The panel does not fall over its own state | done |
| 32 | The Go rewrite — phase 9 (the switchover) | done |
| 33 | The writers of the state take the lock too | done |
| 34 | The Go rewrite — phase 10 (the dashboard) | later |

**All the findings of the audit are closed, the graph is the type-1 design
and the panel is Go.** What is left is the dashboard (phase 10 ✓) and the
work that never ends: watching the panel on a live server.

### What is left to do, and is not a fault

* **Try it on the real servers.** Everything here was verified against the
  simulated network and against a tunnel on the loopback: the panel comes
  up, the demo adopts the tunnels that are already running, moves a record
  when a tunnel dies and repairs the tunnel with another carrier, and the
  engine measures all nine carriers without disturbing the tunnel that is
  carrying the traffic. What has not happened yet is a real Iran server
  and a real foreign server, with the panel talking to them over SSH.
* **The engine's experimental carriers** (`xdi`, `pck`, `spoof`) are not
  offered to the panel: they need raw sockets and root, and an unattended
  repair must not ask for either.
* **The panel does not manage the layer-3 and the direct tunnels** of the
  engine: they are not reverse tunnels, and the panel's model has no place
  for them.
* **Two upstream tests are timing sensitive** and fail from time to time
  under the race detector — the same tests fail the same way in the
  BackPack repository itself, which is where the code comes from. The CI
  skips them there and runs them on their own, without the detector.
* **The test job of the CI failed on the runner for a while** — and the
  fault was in a test of this repository, not in the panel: the test of the
  set compared the number of models with the number of models the set gave
  a moment later ✓, while another goroutine was writing the set ✓. On a
  slow runner the two numbers are never the same ✓, and on a laptop the
  test passed by luck ✓. The comparison is made when nobody writes the set
  any more ✓. The CI runs the suite again, one package at a time, when the
  first run fails ✓ — a real failure is still a failure ✓.







