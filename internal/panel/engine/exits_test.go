package engine

import (
	"strings"
	"testing"
)

// The ports a kharej sends on to another server: found, shown, never acted
// on. The output below was captured from a real run of the script: socat
// with its target on the command line (18443), an iptables DNAT (18444), a
// plain local service (18445) and a socat whose target only shows in its
// connections (18446).
const realExits = `@@NAT
-A OUTPUT -p tcp -m tcp --dport 18444 -j DNAT --to-destination 192.0.2.2:19000
		tcp dport 18444 counter packets 0 bytes 0 dnat to 192.0.2.2:19000
@@PORT 18443
@@PID 646 socat
@@CMD socat TCP-LISTEN:18443,fork,reuseaddr TCP:192.0.2.2:19000
@@OUT
      1 192.0.2.2:19000
@@DISTINCT 1
@@PORT 18444
@@PORT 18445
@@PID 644 python3
@@CMD python3 -m http.server 18445
@@OUT
@@DISTINCT 0
@@PORT 18446
@@PID 648 socat
@@CMD socat TCP-LISTEN:18446,fork,reuseaddr SYSTEM:exec socat - TCP\:192.0.2.2\:19000
@@OUT
      1 192.0.2.2:19000
@@DISTINCT 1
@@REACH
192.0.2.2:19000 ok
192.0.2.2:19999 fail
@@END
`

// TestTheRealOutputIsRead: the exits of a real run are read as they are.
func TestTheRealOutputIsRead(t *testing.T) {
	exits, reach := ParseExits(realExits, []int{18443, 18444, 18445, 18446})

	exit, there := exits[18443]
	if !there {
		t.Fatal("the exit of the first port was not found")
	}
	if exit.Method != "socat" || exit.Via != "socat" || exit.Inferred {
		t.Errorf("the exit of the first port is %+v, it must be the socat of the command line", exit)
	}
	if len(exit.Targets) != 1 || exit.Targets[0] != "192.0.2.2:19000" {
		t.Errorf("the targets of the first port are %v, they must be 192.0.2.2:19000", exit.Targets)
	}

	exit, there = exits[18444]
	if !there {
		t.Fatal("the exit of the second port was not found")
	}
	if exit.Method != "iptables" {
		t.Errorf("the method of the second port is %q, it must be iptables", exit.Method)
	}
	if len(exit.Targets) != 1 || exit.Targets[0] != "192.0.2.2:19000" {
		t.Errorf("the targets of the second port are %v, they must be 192.0.2.2:19000", exit.Targets)
	}

	if _, there := exits[18445]; there {
		t.Error("the third port has an exit, it is served right there")
	}

	exit, there = exits[18446]
	if !there {
		t.Fatal("the exit of the fourth port was not found")
	}
	if !exit.Inferred {
		t.Error("the exit of the fourth port is not marked as inferred")
	}
	if len(exit.Targets) != 1 || exit.Targets[0] != "192.0.2.2:19000" {
		t.Errorf("the targets of the fourth port are %v, they must be 192.0.2.2:19000", exit.Targets)
	}

	if len(reach) != 2 || !reach["192.0.2.2:19000"] || reach["192.0.2.2:19999"] {
		t.Errorf("the targets that answer are %v, they must be the ones of the script", reach)
	}
}

// TestTheOtherForwardersAndServices: the forwarders that say where they
// send the traffic, and the services that do not.
func TestTheOtherForwardersAndServices(t *testing.T) {
	out := `@@NAT
		tcp dport { 80, 443 } dnat ip to 10.9.9.9
-A PREROUTING -p tcp -m multiport --dports 2053:2060 -j DNAT --to-destination 10.8.8.8
@@PORT 443
@@PORT 2055
@@PORT 8443
@@PID 10 gost
@@CMD gost -L tcp://:8443/10.1.1.1:8443 -F relay+tls://u:p@10.2.2.2:443
@@OUT
@@DISTINCT 0
@@PORT 9443
@@PID 11 realm
@@CMD realm -l 0.0.0.0:9443 -r 10.3.3.3:9443
@@OUT
@@DISTINCT 0
@@PORT 4443
@@PID 12 xray
@@CMD xray run -c /etc/xray.json
@@OUT
     40 142.250.1.1:443
     30 104.16.1.1:443
     20 151.101.1.1:443
     10 31.13.1.1:443
      9 17.1.1.1:443
@@DISTINCT 212
@@PORT 5443
@@PID 13 xray
@@CMD xray run -c /etc/relay.json
@@OUT
     55 10.4.4.4:8443
@@DISTINCT 1
@@REACH
@@END
`
	exits, _ := ParseExits(out, []int{443, 2055, 8443, 9443, 4443, 5443})

	exit, there := exits[443]
	if !there {
		t.Fatal("the exit of the first port was not found")
	}
	if exit.Method != "nftables" || exit.Via != "" || exit.Inferred {
		t.Errorf("the exit of the first port is %+v, it must be the rule of nftables", exit)
	}
	if len(exit.Targets) != 1 || exit.Targets[0] != "10.9.9.9:443" {
		t.Errorf("the targets of the first port are %v, they must be 10.9.9.9:443", exit.Targets)
	}

	exit = exits[2055]
	if len(exit.Targets) != 1 || exit.Targets[0] != "10.8.8.8:2055" {
		t.Errorf("the targets of the second port are %v, they must be 10.8.8.8:2055", exit.Targets)
	}

	exit = exits[8443]
	if len(exit.Targets) != 1 || exit.Targets[0] != "10.2.2.2:443" {
		t.Errorf("the targets of the third port are %v, the forwarder itself is the exit", exit.Targets)
	}

	exit = exits[9443]
	if len(exit.Targets) != 1 || exit.Targets[0] != "10.3.3.3:9443" {
		t.Errorf("the targets of the fourth port are %v, they must be 10.3.3.3:9443", exit.Targets)
	}

	if _, there := exits[4443]; there {
		t.Error("the fifth port has an exit, xray browsing the internet for its users is not a forward")
	}

	exit = exits[5443]
	if exit.Method != "xray" || exit.Via != "xray" || !exit.Inferred {
		t.Errorf("the exit of the last port is %+v, it must be the xray of the connections", exit)
	}
	if len(exit.Targets) != 1 || exit.Targets[0] != "10.4.4.4:8443" {
		t.Errorf("the targets of the last port are %v, they must be 10.4.4.4:8443", exit.Targets)
	}
}

// TestTheKharejSideOfAMapping: where a forwarded port is delivered.
func TestTheKharejSideOfAMapping(t *testing.T) {
	cases := []struct {
		mapping string
		host    string
		port    int
	}{
		{"443", "", 443},
		{"8443=2096", "", 2096},
		{"443=1.2.3.4:5201", "1.2.3.4", 5201},
		{"127.0.0.2:443=127.0.0.1:22", "", 22},
		{"443=10.0.0.1:80|10.0.0.2:80", "10.0.0.1", 80},
	}
	for _, c := range cases {
		host, port, ok := KharejDest(c.mapping)
		if !ok {
			t.Errorf("the mapping %q has no destination", c.mapping)
			continue
		}
		if host != c.host || port != c.port {
			t.Errorf("the mapping %q goes to %q:%d, it must go to %q:%d", c.mapping, host, port, c.host, c.port)
		}
	}

	ports := ListenPorts([]string{"443-445"}, 5000)
	if len(ports) != 3 || !ports[443] || !ports[444] || !ports[445] {
		t.Errorf("the ports of the range are %v, they must be 443, 444 and 445", ports)
	}
	ports = ListenPorts([]string{"127.0.0.1:61000=127.0.0.1:22"}, 5000)
	if len(ports) != 1 || !ports[61000] {
		t.Errorf("the ports are %v, there must be only 61000", ports)
	}
}

// TestTheScriptOnlyQuotesValidTargets: a target that is not an address is
// not tried.
func TestTheScriptOnlyQuotesValidTargets(t *testing.T) {
	script := ExitsScript([]int{443}, []string{"1.2.3.4:443", "x;reboot:1", "[2001:db8::1]:443"})
	if !strings.Contains(script, "1.2.3.4:443") {
		t.Error("the script does not have the address it must try")
	}
	if strings.Contains(script, "reboot") {
		t.Error("the script tries an address that is not one")
	}
	if !strings.Contains(script, "[2001:db8::1]:443") {
		t.Error("the script does not have the IPv6 address it must try")
	}
}

// TestTwoMappingsThatWantTheSamePortAreSeen: the engine refuses them, and
// the panel must see it first.
func TestTwoMappingsThatWantTheSamePortAreSeen(t *testing.T) {
	clash := PortsClash([]string{"2053-2060", "2053"})
	if len(clash) != 1 || clash[0] != 2053 {
		t.Errorf("the ports that clash are %v, it must be 2053", clash)
	}
	if len(PortsClash([]string{"443", "8443"})) != 0 {
		t.Error("the ports clash, they must not")
	}
}
