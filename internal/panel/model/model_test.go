package model

import (
	"encoding/json"
	"testing"
)

// decode is the reading of a model the panel does.
func read(t *testing.T, data string, into any) {
	t.Helper()
	if err := json.Unmarshal([]byte(data), into); err != nil {
		t.Fatalf("cannot read the model: %v", err)
	}
}

// TestTheDefaultsOfALink: a link that is written in the state file without
// the fields the panel added later keeps the values the panel expects.
func TestTheDefaultsOfALink(t *testing.T) {
	var link Link
	read(t, `{"iran_id": "i", "kharej_id": "k", "tunnel_port": 1}`, &link)

	if link.Built {
		t.Error("a link that has never run is not built")
	}
	if link.IranUnit != "" {
		t.Errorf("the unit of the Iran side is %q, it must be empty", link.IranUnit)
	}
	if link.Status != StatusPending {
		t.Errorf("the state of the link is %q, it must be pending", link.Status)
	}
	if link.Priority != 100 {
		t.Errorf("the priority of the link is %d, it must be 100", link.Priority)
	}
	if !link.Managed {
		t.Error("a link the panel knows is managed")
	}
	if !link.Verified {
		t.Error("a link nobody has tested yet is verified")
	}
	if link.ID == "" || link.Token == "" {
		t.Error("the link has no id and no token")
	}
	if link.IranName != "bt-"+link.ID || link.KharejName != "bt-"+link.ID {
		t.Errorf("the names of the link are %q and %q, they must be bt-<id>", link.IranName, link.KharejName)
	}
}

// TestTheDefaultsOfAServer: the same, for a server.
func TestTheDefaultsOfAServer(t *testing.T) {
	var server Server
	read(t, `{"name": "K", "role": "kharej", "ip": "1.2.3.4"}`, &server)

	if server.SSHPort != 22 {
		t.Errorf("the port of SSH is %d, it must be 22", server.SSHPort)
	}
	if server.SSHUser != "root" {
		t.Errorf("the user of SSH is %q, it must be root", server.SSHUser)
	}
	if server.Priority != 100 {
		t.Errorf("the priority of the server is %d, it must be 100", server.Priority)
	}
	if !server.Enabled {
		t.Error("a server that is in the panel is enabled")
	}
	if server.Status != Unknown {
		t.Errorf("the state of the server is %q, it must be unknown", server.Status)
	}
	if server.SSHLatencyMS != nil {
		t.Error("the latency of a server nobody has reached is not a number")
	}
	if server.ProbeAddr() != "127.0.0.1:22" {
		t.Errorf("the probe dials %q, it must dial the server itself", server.ProbeAddr())
	}
}

// TestTheReadingIsTolerant: a file that does not know a field does not
// break the panel, and a field the file does not know is ignored.
func TestTheReadingIsTolerant(t *testing.T) {
	var server Server
	read(t, `{"name": "K", "role": "kharej", "ip": "1.2.3.4", "a_field_of_the_future": 42}`, &server)

	if server.Name != "K" || server.Role != "kharej" || server.IP != "1.2.3.4" {
		t.Errorf("the server is %+v, it must keep what the file says", server)
	}
	if server.SSHPort != 22 {
		t.Errorf("the port of SSH is %d, it must be 22", server.SSHPort)
	}
}

// TestTheSecretsAreMasked: the password and the private key of a server and
// the token of a DNS account never leave the panel in the clear.
func TestTheSecretsAreMasked(t *testing.T) {
	var server Server
	read(t, `{"name": "K", "role": "kharej", "ip": "1.2.3.4", "ssh_password": "secret", "ssh_key": "-----BEGIN"}`, &server)

	public := server.Public()
	if public.SSHPassword == "secret" || public.SSHKey == "-----BEGIN" {
		t.Error("the secrets of the server are not masked")
	}
	if public.SSHPassword == "" || public.SSHKey == "" {
		t.Error("the secrets of the server are not masked at all")
	}
	if server.SSHPassword != "secret" {
		t.Error("the server lost its own password")
	}

	var provider DNSProvider
	read(t, `{"name": "cf", "kind": "cloudflare", "api_token": "secret", "zone": "example.com"}`, &provider)
	if provider.Public().APIToken == "secret" {
		t.Error("the token of the DNS account is not masked")
	}
}

// TestTheCarriersAreRankedByTheLastTest: the working ones first, the
// fastest of them before the slower, then the ones that were never tried,
// then the ones that failed.
func TestTheCarriersAreRankedByTheLastTest(t *testing.T) {
	var link Link
	read(t, `{
		"iran_id": "i", "kharej_id": "k", "tunnel_port": 1,
		"tests": {
			"tcp": {"ok": true, "latency_ms": 30},
			"ws": {"ok": true, "latency_ms": 10},
			"quic": {"ok": false}
		}
	}`, &link)

	ranked := link.RankedTransports([]string{"tcp", "ws", "kcp", "quic"})
	want := []string{"ws", "tcp", "kcp", "quic"}
	if len(ranked) != len(want) {
		t.Fatalf("the carriers are ranked %v, they must be ranked %v", ranked, want)
	}
	for i := range want {
		if ranked[i] != want[i] {
			t.Fatalf("the carriers are ranked %v, they must be ranked %v", ranked, want)
		}
	}
}

// TestTheAllowedCarriersWin: a link that is only allowed to run on some
// carriers never gets another one.
func TestTheAllowedCarriersWin(t *testing.T) {
	var link Link
	read(t, `{"iran_id": "i", "kharej_id": "k", "tunnel_port": 1, "allowed": ["wsmux"]}`, &link)

	ranked := link.RankedTransports([]string{"tcp", "ws", "wsmux"})
	if len(ranked) != 1 || ranked[0] != "wsmux" {
		t.Errorf("the carriers are ranked %v, only wsmux is allowed", ranked)
	}
}

// TestALinkWithoutAnIdentifierGetsOne: a tunnel the panel builds itself
// (the mesh it offers, a tunnel it adopts) is known by its identifier: two
// of them must never share one.
func TestALinkWithoutAnIdentifierGetsOne(t *testing.T) {
	first := &Link{IranID: "i1", KharejID: "k1"}
	second := &Link{IranID: "i2", KharejID: "k2"}
	first.Normalize()
	second.Normalize()

	if first.ID == "" || second.ID == "" {
		t.Fatal("a link without an identifier must get one")
	}
	if first.ID == second.ID {
		t.Errorf("the two links share the identifier %q, they must not", first.ID)
	}
	if first.IranName != "bt-"+first.ID {
		t.Errorf("the name of the link is %q, it must be bt-<id>", first.IranName)
	}
	// an identifier that is already there is kept
	kept := &Link{ID: "l42"}
	kept.Normalize()
	if kept.ID != "l42" {
		t.Errorf("the identifier of the link is %q, it must be l42", kept.ID)
	}
}

// TestAnIdIsOnlyTheLettersTheServersCanHold: an id of the panel becomes a
// file name, a unit name and a word in a command line on the servers, so
// the only shape it may have is the one the engine allows in a name —
// letters, digits, dot, dash, underscore — and an empty string is not an
// id at all (a new one is generated instead).
func TestAnIdIsOnlyTheLettersTheServersCanHold(t *testing.T) {
	for _, id := range []string{"l8224d3", NewID("l"), NewID("s"), "s_sc-1.a", "A.b_c-9", "v1.1.3"} {
		if !ValidID(id) {
			t.Errorf("%q is the shape the panel writes to its servers, and ValidID refuses it", id)
		}
	}
	hostile := []string{
		"", "two words", "a b", `a"b`, "a;b", "a$(id)", "a`id`", "a/b",
		`..\state`, "a\nb", "a\rb", "a|b", "a&b", "a>b", "a\tb",
	}
	for _, id := range hostile {
		if ValidID(id) {
			t.Errorf("%q is not the shape of a file name, and ValidID takes it", id)
		}
	}
}
