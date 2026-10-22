package orch

import (
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The state of the panel is behind a lock: the monitor writes it, the web
// interface reads it, and the repairs and the discoveries run in the
// background. Every one of them takes the lock — the reader for reading,
// the writer for writing — and the network calls are made with it released,
// or the panel would answer nothing while a slow server was being checked.
//
// The helpers here are how the orchestrator does it. They also write to the
// tunnel that is there *now*: the operator may have changed it while the
// panel was talking to a server, and the change must not be lost.

// copyLink is the tunnel as it is now, copied: a long operation — a
// discovery, a repair — works on the copy, and reads it without the lock.
func (o *Orchestrator) copyLink(link *model.Link) *model.Link {
	if link == nil {
		return nil
	}
	o.store.RLock()
	defer o.store.RUnlock()
	return o.store.Links.Get(link.ID).Clone()
}

// copyServer is the server as it is now, copied.
func (o *Orchestrator) copyServer(server *model.Server) *model.Server {
	if server == nil {
		return nil
	}
	o.store.RLock()
	defer o.store.RUnlock()
	current := o.store.Servers.Get(server.ID)
	if current == nil {
		return nil
	}
	copied := *current
	return &copied
}

// copyDomain is the domain as it is now, copied.
func (o *Orchestrator) copyDomain(domain *model.Domain) *model.Domain {
	if domain == nil {
		return nil
	}
	o.store.RLock()
	defer o.store.RUnlock()
	current := o.store.Domains.Get(domain.ID)
	if current == nil {
		return nil
	}
	copied := *current
	return &copied
}

// write changes a tunnel under the lock of the state. It says whether the
// tunnel is still in the panel.
func (o *Orchestrator) write(link *model.Link, change func(*model.Link)) bool {
	if link == nil {
		return false
	}
	o.store.Lock()
	defer o.store.Unlock()
	current := o.store.Links.Get(link.ID)
	if current == nil {
		return false
	}
	change(current)
	return true
}

// writeDomain changes a domain under the lock of the state. It says whether
// the domain is still in the panel.
func (o *Orchestrator) writeDomain(domain *model.Domain, change func(*model.Domain)) bool {
	if domain == nil {
		return false
	}
	o.store.Lock()
	defer o.store.Unlock()
	current := o.store.Domains.Get(domain.ID)
	if current == nil {
		return false
	}
	change(current)
	return true
}

// read runs what with the lock of the state held for reading: the questions
// the panel asks its state read the fields of the tunnels and of the
// servers, and those are written while they are read.
func (o *Orchestrator) read(what func()) {
	o.store.RLock()
	defer o.store.RUnlock()
	what()
}
