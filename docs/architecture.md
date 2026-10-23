# Architecture

## Two planes

bull-Tunnel is a **control plane**. It never carries a user's traffic.

```
   user ──► Iran server ──► tunnel ──► foreign server ──► the real service
             (public IP)   (the data plane, on the servers)

   panel ──SSH──► Iran server        panel ──HTTPS──► Cloudflare / Arvan
   panel ──SSH──► foreign server     panel ──HTTPS──► Telegram
```

* The **data plane** is the tunnel engine (see [engine-schema.md](engine-schema.md)).
  It runs on both ends, it is what the user traffic goes through, and it
  keeps running when the panel is down or switched off.
* The **control plane** is the panel: it renders the configs, starts and
  stops the units, measures the health of every tunnel, and moves the DNS
  records when a path dies.

The separation is deliberate: the panel can be replaced, restarted or
upgraded without touching a single tunnel, and a broken panel cannot take
traffic down.

## How the panel controls tunnels on different servers

Everything the panel does on a server goes through **one SSH connection per
server**, kept open and reused (`internal/panel/ssh`):

| Action | How |
|---|---|
| is the server alive? | `true` over the existing connection, timed |
| which tunnels run there? | one script that lists `/etc/bull-tunnel/*.toml` and asks systemd for the state of the matching unit |
| is a tunnel healthy? | one script per Iran server that reads the unit state, the engine's metrics file, opens the loopback probe and counts the established sockets |
| write a config | SFTP (with a `cat > file` fallback for images without an SFTP subsystem) |
| start / stop a tunnel | `systemctl enable/restart/disable` |
| open the forwarded ports | `ufw allow`, only if ufw is active |
| where does a port finally go? | a read-only script that reads the NAT rules, the listening sockets, the command lines and the live connections |

The panel runs the same script on every server and parses a line-oriented
output, so the amount of traffic between the panel and the servers stays
small and predictable: one round trip per check cycle per server, not per
tunnel.

### SSH details that matter

* **Connection reuse.** One connection per server, kept alive with
  keepalives (15 s, 2 missed). A check cycle of 20 servers is 20 round
  trips, not 20 × (tunnels) handshakes.
* **Reconnect once.** A connection that dies under the panel is dropped and
  rebuilt once, transparently. A second failure is reported as the server
  being down, not as a stack trace.
* **Timeouts everywhere.** Every remote command has a deadline. A server
  that accepts the connection but never answers is treated as offline
  instead of blocking the whole cycle.
* **Host keys are pinned.** The first connection to a server records its
  key, and every later connection must present the same one. A key that
  changed is refused and reported. See the security section.

## The transport stack

### What the engine does on the wire

```
[client] ──── carrier ────► [server]
   │                                │
   │  OPEN  (a TCP connection from  │
   │  the foreign server to the    │
   │  tunnel port of the Iran one) │
   │                                │
   └── multiplexed streams ────────┴──► forwarded to the local ports
```

The tunnel is **reverse**: the side outside Iran dials *in* to the Iran
server. That matters for networks where outgoing connections are the only
ones allowed, and it is why the Iran server needs a public IP and an open
port (or a CDN in front of it).

The carriers the engine can use are listed in
[engine-schema.md](engine-schema.md): `tcp`, `tcpmux`, `ws`, `wss`,
`stealth`, `kcp`, `quic`, `wsmux`, `wssmux`.

### How low-level is it?

Honest answer: **the framing is low-level, and the payload is encrypted**.

* The framing is a length-prefixed stream with a multiplexing layer, and
  the engine speaks it directly on a socket. There is no userspace
  "framework" between the socket and the tunnel: one `read()` gives the
  bytes of one frame, and the engine never copies more than it has to.
* **Every carrier runs inside one session layer that is authenticated and
  encrypted**, whatever the carrier is underneath. The two ends prove they
  hold the same token before a byte of the user's traffic moves, the
  traffic keys come from an ephemeral X25519 exchange stretched with
  HKDF-SHA256 — so a recording of the traffic cannot be read later, even
  by someone who learns the token — and every frame is sealed with
  AES-256-GCM with its type, its flags, its length and its stream id
  authenticated as well. A frame cannot be replayed as another kind of
  frame or as another stream.
* `wss`/`quic` add TLS under the session, so the carrier is encrypted
  twice. It is not needed for the confidentiality of the payload — the
  session already provides it — but it is what makes the tunnel look like
  ordinary web traffic to a middlebox.

### Does it survive filtering?

**Today: only partly.** The panel measures the carriers and keeps the best
one that works, which covers a lot of real cases:

| Situation | What happens today |
|---|---|
| the tunnel port is blocked | every carrier fails, the tunnel is down, the panel moves the domain to another Iran server |
| the TCP handshake is reset by a middlebox | `tcp`/`tcpmux` fail, `ws`/`wss`/`stealth` often survive, because they look like web traffic |
| UDP is throttled or dropped | `kcp`/`quic` fail, the TCP carriers keep working |
| a CDN or an HTTP proxy sits in front | `ws`/`wss` work if the proxy passes the WebSocket upgrade |
| **DNS is the only thing that gets through** | **nothing works** |
| **not even a TCP handshake is possible** | **the control plane is dead too** |

The last two rows are the gap. `dnstt` and its relatives work in exactly
that situation because they do not need a connection at all: they carry
the data inside ordinary DNS queries, which almost every network lets
through, and they can reassemble a stream out of a few hundred small
responses.

**bull-Tunnel does not tunnel the traffic over DNS**, but it does keep the
**control plane** alive there:

```
   panel ──HTTPS──► DNS account ──► TXT record ──DNS──► agent on the server
   panel ◄──HTTPS── DNS account ◄── TXT record ◄──DNS── agent
```

The command is signed with a key only the panel and the agent know, so an
account that can write in the zone still cannot make the agent do
anything. The agent knows two commands (`status` and `restart`), checks
every name twice, and there is no shell anywhere near it. The answer
comes back the same way.

That is the difference between "the tunnel is down" and "the panel can
still see the server and tell it what to do". Turning the *data plane*
over to DNS as well is a much bigger piece of work, and it has not been
taken on.

## The failover policy

For every domain, the panel keeps a *failover group*: the tunnels it may
use, in order of preference. All of them stay connected; the domain's A
record points at the Iran server of the tunnel it currently uses.

1. The current tunnel is down, or its Iran server is down, or it keeps
   flapping → the domain moves to the first healthy tunnel in the group:
   * on another Iran server: the A record is changed (Cloudflare / Arvan),
     and the panel watches the public resolvers to report when the change
     has actually reached the users;
   * on the same Iran server: the forwarded ports are handed over to
     another tunnel, with no DNS change at all.
2. Broken tunnels whose two servers are reachable are rebuilt in the
   background with other carriers.
3. When the first choice has been healthy for `failback_after` seconds
   (10 minutes by default) without a single drop, the traffic goes back.

Nothing moves without a reason: never away from a working tunnel, never
back onto one that dropped recently. A fresh panel starts in *observe
mode*: it watches, it reports what it would have done, and it changes
nothing at all until the operator switches that off.

## Security

What the panel does today:

* One admin account, PBKDF2-HMAC-SHA256 with 200 000 iterations and a
  per-user salt; the session is an HMAC-signed token in an `HttpOnly`,
  `SameSite=Strict` cookie, and the signing secret is rotated when the
  password changes (which logs every session out).
* The data directory is `0700`, the state file `0600`. SSH passwords, DNS
  API tokens and the backup password live there, and in the backups —
  which is why a backup password is not optional in practice.
* **The host keys are pinned.** The first connection to a server records
  its key, and every later connection must present the same one; a key
  that changed is refused and reported. The operator can also write the
  expected fingerprint into the server's settings, and then even the first
  contact is checked against it.
* **The login is rate limited**: eight wrong passwords in five minutes
  block the address for fifteen minutes.
* **The dashboard sends the security headers**: a strict content security
  policy with no inline script, `X-Frame-Options: DENY`, `nosniff`,
  `Referrer-Policy: no-referrer` and HSTS when it is served over TLS.
* The panel runs as an unprivileged user with a hardened systemd unit
  (`ProtectSystem=strict`, `NoNewPrivileges`, `ReadWritePaths` on its own
  directory).
* The panel is not in the traffic path and holds no user data.
* Every remote command is built from validated values: port mappings are
  checked against a strict pattern, tunnel names against
  `^[A-Za-z0-9_.\-]+$`, and every value that reaches a shell is quoted.

What is still missing:

| Issue | Severity | Note |
|---|---|---|
| the audit trail is thin | WARNING | every action is logged to the journal and to Telegram, but there is no separate, tamper-evident log |
| the command channel cannot carry data | WARNING | it carries commands and small answers over DNS, not the traffic of a tunnel: a DNS carrier for the data plane is a much bigger piece of work |
| the panel trusts the DNS account it is given | WARNING | an account that can write TXT records in the production zone could in principle feed a command; the commands are signed, so it cannot, but a zone of its own is the right answer |

## Scale

The panel is built for tens of servers and hundreds of tunnels, not for
millions:

* the state is one JSON file, rewritten atomically; at 100 servers and
  500 tunnels it is a few hundred kilobytes and the rewrite takes
  milliseconds;
* one check cycle touches every server once, in parallel, with a bounded
  number of concurrent SSH commands;
* the panel holds one connection per server, not one per tunnel;
* the event log is capped at 1000 entries, the per-tunnel history at 120
  samples.

What would break first at a much larger scale: the single JSON state file
(a database would be needed around a few thousand servers), the full state
pushed to every browser over the WebSocket on every change, and the fact
that every check cycle re-reads every server (a ticketing system or a
shared cache would be needed around a few hundred servers).

None of that is close today. The design keeps the option open: the state
lives behind a `Store` class with a small interface, so it can be replaced
by a database without touching the orchestrator.
