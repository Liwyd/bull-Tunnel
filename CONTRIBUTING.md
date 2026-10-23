# Contributing

One command verifies a change, one concern belongs in one commit, and the
commit says why.

## The five commands

Run all five before every commit. They are the whole verification; there is
no sixth.

```bash
go vet ./...
go test -race ./... -skip 'TestStartDoesNotReturnUntilThePortsAreFree|TestWriteBatchDoesNotAllocatePerCall|TestAnExplicitServerNameIsNotRewritten'
node --check static/app.js
bash tests/install-check.sh
go run ./cmd/bull-tunnel-panel --demo --data /tmp/panel-demo --port 18080
```

The last one does not assert anything: it is the demo panel, and you look at
it. It connects to no server, and it breaks something every minute so the
failovers can be watched live.

The three tests the race run skips are timing-sensitive tests of the engine's
upstream code. The CI re-runs each of them up to five times; locally, skip
them and let the CI carry them.

## The rules that a change is judged by

1. **Never break a live system.** The panel runs on a server that manages
   live tunnels. Every change is backwards compatible with the state file
   (`state.json`), with the tunnel configs already on the servers, and with
   the API the dashboard calls. A key that is written is not renamed.
2. **Every change has a test, and the test fails first.** A fix without a
   regression test is not finished. Write the test, watch it fail, make the
   change, watch it pass. A test that cannot fail is not a test.
3. **The engine tree is not the panel's to redesign.** `internal/engine/` is
   the AGPL-3.0 tunnel engine, inherited and vendored. It stays the same
   code: anything in that tree which is not part of the engine is removed or
   reduced to the smallest addition with the reason written beside it. The
   panel side — everything outside that tree — is what may be redesigned.
   The panel never imports a package of the engine; they are synchronised by
   contract, not by linking.
4. **When behaviour is in doubt, read the previous working version.**
   `../Tunnelmanager` is a panel that works in production. Doubt is settled
   by reading it, not by guessing.
5. **Small, verifiable steps.** One concern per commit.

## The commits

A subject line is a full sentence in the present tense, saying what the
commit does — not what was wrong with the code before it:

    The installation checks itself
    The feed of events is Go
    One name — the fork's origin leaves the project's own words

The body says **why**, and it is where the reasoning that will be needed
later is written down: what was checked before the change, what turned out
to be wrong with the plan, what a reader might think was a mistake and was
not. If a plan said to do one thing and the code said another, the body
records both and which one won.

One concern per commit. If a commit needs a paragraph to explain what it
does, it is two commits.

## Licence

The panel — everything outside `internal/engine/` — is MIT. The engine tree
is AGPL-3.0 and keeps the notices its licence requires; see
`internal/engine/NOTICE` and the Licences section of the README. A change
that touches both sides keeps that line where it is.
