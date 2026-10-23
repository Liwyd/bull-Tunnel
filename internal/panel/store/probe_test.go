package store

import (
	"strings"
	"testing"
)

// The last measurement of the path between two servers is kept beside the
// state: the drawer of a server asks for the newest one touching it, gets
// a copy of it (the panel may be writing the next one), and a handful are
// remembered — the oldest fall out. The measurement is never written into
// the state file: it is a reading of the moment, not a truth about the
// machines.
func TestTheLastMeasurementIsKeptForTheDrawer(t *testing.T) {
	s := New("")
	if probe := s.ProbeFor("k1"); probe != nil {
		t.Errorf("a panel that has measured nothing remembers %v", probe)
	}

	s.SetProbe(Probe{One: "k1", Two: "i1", TS: 100, OK: true, Best: "ws", LinkID: "l1",
		Rows: []ProbeRow{{Transport: "ws", OK: true, ConnectMS: 12, RTTMS: 41}}})
	s.SetProbe(Probe{One: "k2", Two: "i1", TS: 200, OK: false, Error: "no carrier worked"})

	// the newest measurement that touched a server is the one it asks for
	first := s.ProbeFor("k1")
	if first == nil || first.TS != 100 || first.Best != "ws" || len(first.Rows) != 1 {
		t.Fatalf("the drawer gets %+v, it must get the first measurement", first)
	}
	if newest := s.ProbeFor("i1"); newest == nil || newest.TS != 200 || newest.Error != "no carrier worked" {
		t.Errorf("the drawer gets %+v, it must get the newest measurement of the server", newest)
	}
	if nobody := s.ProbeFor("somebody"); nobody != nil {
		t.Errorf("a server that was never measured remembers %+v", nobody)
	}

	// what the drawer is given is a copy: writing to it must not write into
	// the panel's memory of it
	first.Rows[0].Transport = "changed"
	first.Best = "changed"
	if kept := s.ProbeFor("k1"); kept.Best != "ws" || kept.Rows[0].Transport != "ws" {
		t.Errorf("the drawer changed what the panel remembers: %+v", kept)
	}

	// a handful are remembered: after twenty more, the first is gone
	for i := 0; i < 30; i++ {
		s.SetProbe(Probe{One: "k3", Two: "i1", TS: float64(300 + i)})
	}
	if kept := len(s.probes); kept > maxProbes {
		t.Errorf("the panel remembers %d measurements, it keeps %d", kept, maxProbes)
	}
	if fallen := s.ProbeFor("k1"); fallen != nil {
		t.Errorf("the drawer still gets the measurement of %v, the oldest have fallen out", fallen.TS)
	}
	if newest := s.ProbeFor("k3"); newest == nil || newest.TS != 329 {
		t.Errorf("the newest measurement is %+v", newest)
	}

	// and the state file holds none of it
	data, err := Marshal(s.Raw())
	if err != nil {
		t.Fatalf("the state cannot be written: %v", err)
	}
	if strings.Contains(string(data), "no carrier worked") || strings.Contains(string(data), "probes") {
		t.Error("the state file holds the measurements of the moment")
	}
}
