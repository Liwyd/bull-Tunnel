package demo

import (
	"fmt"
	"math/rand"
	"sync"
)

// SimDNS is the DNS service of the demo: the records live in the memory,
// and the panel can be watched moving them.
type SimDNS struct {
	mu      sync.Mutex
	records map[string]string
	texts   map[string]string
	calls   []string
	// Fail makes the service refuse everything, the way a service that is
	// having a bad day does.
	Fail bool
	// Zone is the zone of the account, said when it is verified.
	Zone string
}

// NewSimDNS returns the DNS service of the demo.
func NewSimDNS(zone string) *SimDNS {
	return &SimDNS{records: map[string]string{}, texts: map[string]string{}, Zone: zone}
}

// GetA is the addresses a name points at.
func (d *SimDNS) GetA(fqdn string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ip, there := d.records[fqdn]; there {
		return []string{ip}, nil
	}
	return []string{}, nil
}

// SetA points a name at an address.
func (d *SimDNS) SetA(fqdn string, ip string, ttl int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Fail {
		return fmt.Errorf("dns api down")
	}
	d.records[fqdn] = ip
	d.calls = append(d.calls, fqdn+"="+ip)
	return nil
}

// GetTXT is the text of the TXT records of a name.
func (d *SimDNS) GetTXT(fqdn string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if text, there := d.texts[fqdn]; there {
		return []string{text}, nil
	}
	return []string{}, nil
}

// SetTXT writes a TXT record.
func (d *SimDNS) SetTXT(fqdn string, text string, ttl int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Fail {
		return fmt.Errorf("dns api down")
	}
	d.texts[fqdn] = text
	return nil
}

// Verify checks the credentials and the zone.
func (d *SimDNS) Verify() (string, error) {
	return "zone " + d.Zone + " (demo)", nil
}

// Records are the records of the demo, for the tests.
func (d *SimDNS) Records() map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]string{}
	for name, ip := range d.records {
		out[name] = ip
	}
	return out
}

// SetRecord puts a record without going through the service, the way the
// demo starts.
func (d *SimDNS) SetRecord(fqdn string, ip string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records[fqdn] = ip
}

// Calls are the records the panel wrote.
func (d *SimDNS) Calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string{}, d.calls...)
}

// Resolver is the public resolver of the demo: it catches up with the record
// a while after it changed.
func (d *SimDNS) Resolver(fqdn string, resolvers []string) map[string][]string {
	d.mu.Lock()
	ip, there := d.records[fqdn]
	d.mu.Unlock()
	out := map[string][]string{}
	for i, resolver := range resolvers {
		if i >= 1 {
			break
		}
		if there && rand.Float64() < 0.5 {
			out[resolver] = []string{ip}
			continue
		}
		out[resolver] = []string{"0.0.0.0"}
	}
	return out
}
