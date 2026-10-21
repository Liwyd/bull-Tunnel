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
