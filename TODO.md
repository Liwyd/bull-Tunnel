# Bull-Tunn — Fix Plan (TODO)

This file is the working plan for the repair of Bull-Tunn. It is updated before
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
3. **The engine is BackPack.** The tunnel engine is not a rewrite: it is the
   BackPack engine (`github.com/AminMGMT/BackPack`, vendored under
   `engine/`). Anything in `engine/` that is *not* BackPack must be either
   removed or reduced to the smallest possible addition with a clear reason
   written next to it. The panel side is what may be redesigned.
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

---

## 1. What is wrong (the audit)

The audit of the whole system (Python panel, Go engine, front-end, deploy
scripts) produced 53 findings. They are grouped here by the commit that fixes
them.

### A. The engine is not BackPack (critical)

| # | Finding |
|---|---|
| 1 | `bulltunn/engine/` is a hand-written re-implementation of the tunnel engine. It supports only `tcp, tcpmux, ws, wsmux, wss, wssmux, stealth` while the panel offers `kcp` and `quic` too (`bulltunn/models.py:28`). Every discovery therefore burns two full test timeouts on protocols the engine refuses (`engine/internal/config/config.go:200`). |
| 2 | The command channel can never work: the panel signs `id\|action\|\|ts` for an empty argument list, the Go agent signs `id\|action\|{}\|ts` (`bulltunn/cmdchannel.py:51` vs `engine/internal/agent/protocol.go:45`). Every command is refused. |
| 3 | The Go agent writes the TXT record without escaping the quotes of the JSON command (`engine/internal/agent/publisher.go:83`); Cloudflare rejects it. |
| 4 | The hand-written engine's session/carrier layer is a simplified copy of BackPack's (no KCP, no QUIC, no smux, no Noise record layer, no control channel, no pool). It is not the engine the project is supposed to ship. |

**Fix (commit 2):** replace `engine/` with the real BackPack engine.

### B. Critical panel bugs

| # | Finding |
|---|---|
| 5 | A malformed SSH private key raises `asyncssh.KeyImportError` (a `ValueError`), which is not an `SSHError`: it escapes `bulltunn/ssh.py:124`, `bulltunn/orchestrator.py:657` and `bulltunn/app.py:521` and **aborts the whole check cycle** — no server is checked, no tunnel is checked, no failover ever happens. |
| 6 | Deleting a server removes it from the store *before* the cleanup runs (`bulltunn/store.py:248`), so `orchestrator.remove_links()` gets `None` for that side and `SSHDriver._stop_side()` returns at once: the tunnel's config, unit and process stay on the server forever. |
| 7 | The domain's "primary" server is ignored as soon as a manual group is set (`bulltunn/store.py:312`), so "Make it primary" silently does nothing and the failback goes to the wrong server. |
| 8 | `bulltunn/store.py:393` overwrites `primary_iran` in the snapshot with the group's first Iran server, so the panel shows the wrong primary. |
| 9 | Editing a server drops the pinned host key whenever the host-key field is empty (`bulltunn/app.py:551`): renaming a server silently disables the MITM protection. |
| 10 | A corrupt `auth.json` or `state.json` makes the panel crash at start-up (`bulltunn/auth.py:40`, `bulltunn/store.py:52`). |
| 11 | `POST /api/links/{id}/release` raises `KeyError` (HTTP 500) when the handover target is gone (`bulltunn/app.py:800`). |

### C. Medium panel bugs

| # | Finding |
|---|---|
| 12 | The engine tarball is cached forever under the tag `latest` (`bulltunn/engine.py:713`): a new release is never picked up. |
| 13 | `full_state()` is rebuilt on every event and runs `domain_info()` for every domain × every Iran server (`bulltunn/app.py:449`). |
| 14 | A wake-up that arrives between the timeout and `_wake.clear()` is lost (`bulltunn/orchestrator.py:1286`). |
| 15 | The engine refuses two mappings that overlap once the ranges are expanded (`2053-2060` + `2053`), while the panel's dedupe only compares the strings (`bulltunn/store.py:178`). |
| 16 | `probe.measure()` ignores the transport filter; the engine is always asked for every carrier (`bulltunn/probe.py:106`). |
| 17 | The probe does not bracket an IPv6 address (`bulltunn/probe.py:47`). |
| 18 | The probe leaves `/tmp/bulltunn-probe.toml` on the foreign server. |
| 19 | `date +%s%N` is GNU-only: on BusyBox the health probe returns garbage. |
| 20 | The Go server handshake reads its first frame with no deadline. |
| 21 | The `stealth`/`ws`/`wss` carriers never shut their `http.Server` down. |
| 22 | Two backups in the same minute overwrite each other (`bulltunn/backup.py:138`). |
| 23 | Clearing the bot token or the engine token in the UI wipes the secret instead of keeping it. |
| 24 | Restoring a backup whose `auth` has no `secret` locks the panel out (`bulltunn/app.py:1021`, `bulltunn/auth.py:76`). |
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

### D. Front-end (`bulltunn/static/app.js`)

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
  plain `rsync`.
* ✅ `engine/main.go`: the `bulltunn-engine` binary — engine mode (`-c`),
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

### Commit 3 — the critical panel bugs
Findings 5, 6, 7, 8, 9, 10, 11.
* One place that turns anything into an `SSHError`, so no exception can ever
  kill the check cycle again.
* The cleanup of a deleted server runs before the server leaves the store.
* The domain's primary is honoured with a manual group, and the snapshot no
  longer lies about it.
* The host key is only forgotten when the operator really cleared it.
* A corrupt state/auth file is reported instead of crashing the panel.
* The release endpoint answers 404 instead of 500.

### Commit 4 — the medium panel bugs
Findings 12 to 35.

### Commit 5 — the front-end
Findings 36 to 48.

### Commit 6 — tests, docs and the changelog
The whole suite, the README, the CHANGELOG and `docs/engine-schema.md`.

---

## 3. Status

| Commit | What | State |
|---|---|---|
| 1 | `TODO.md` | done |
| 2 | The engine is BackPack | done |
| 3 | The critical panel bugs | pending |
| 4 | The medium panel bugs | pending |
| 5 | The front-end | pending |
| 6 | Tests, docs, changelog | pending |

**Next up (commit 3):** the critical panel bugs.

1. `bulltunn/ssh.py`: a malformed private key must become an `SSHError`, and
   the check cycle must survive any exception a server can throw.
2. `bulltunn/store.py` + `bulltunn/orchestrator.py`: the cleanup of a deleted
   server must run while the server is still known.
3. `bulltunn/store.py`: the domain's primary must be honoured with a manual
   group, and the snapshot must not overwrite it.
4. `bulltunn/app.py`: the host key must only be forgotten when the operator
   really cleared it.
5. `bulltunn/auth.py` + `bulltunn/store.py`: a corrupt file must be reported,
   not crash the panel.
6. `bulltunn/app.py`: the handover release must answer 404, not 500.

