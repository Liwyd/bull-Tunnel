package store

/* The last measurement of the path between two servers, kept in memory: the
   drawer of a server shows it under the connection test, and the panel
   forgets it on a restart. It is deliberately not part of the state file —
   it is a reading of the moment, not a truth about the machines — so it is
   kept beside the state, with a lock of its own and never written to
   Raw(). */

// ProbeRow is what one carrier did in a measurement.
type ProbeRow struct {
	Transport string
	OK        bool
	ConnectMS float64
	SessionMS float64 // the setup of the session (the handshake)
	RTTMS     float64
	Error     string
}

// Probe is one measurement between two servers: the two of them, when it
// was taken, and what every carrier did.
type Probe struct {
	One    string // one of the two servers (either side)
	Two    string // the other one
	TS     float64
	OK     bool
	Best   string
	Error  string
	LinkID string // the tunnel the measurement dialled through
	Rows   []ProbeRow
}

// maxProbes is how many measurements are remembered. The drawer only ever
// asks for the newest one touching a server; a handful is plenty.
const maxProbes = 20

// SetProbe remembers a measurement.
func (s *Store) SetProbe(p Probe) {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	s.probes = append(s.probes, p)
	if len(s.probes) > maxProbes {
		s.probes = s.probes[len(s.probes)-maxProbes:]
	}
}

// ProbeFor is the newest measurement that involved this server, or nil. The
// copy is returned: the caller reads it while the API may write the next
// one.
func (s *Store) ProbeFor(id string) *Probe {
	s.probeMu.RLock()
	defer s.probeMu.RUnlock()
	var best *Probe
	for i := range s.probes {
		if (s.probes[i].One == id || s.probes[i].Two == id) && (best == nil || s.probes[i].TS >= best.TS) {
			best = &s.probes[i]
		}
	}
	if best == nil {
		return nil
	}
	copied := *best
	copied.Rows = append([]ProbeRow{}, best.Rows...)
	return &copied
}
