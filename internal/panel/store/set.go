package store

import "sync"

// Set is a set of models that remembers the order they came in.
//
// The order is not a detail: the state file is written in it, and the panel
// shows the servers and the tunnels in it. A Go map has no order of its own,
// so the order is kept beside it — the way a Python dictionary keeps the
// order of its keys.
//
// The set has a lock of its own. The lock of the state (the store's) keeps
// the state consistent — the servers, the tunnels and the domains seen
// together — but the set itself must never be read while it is written, not
// even by a reader that only wanted a list: Go stops the whole program when
// a map is read and written at the same time, and the panel takes the
// tunnels with it.
type Set[T any] struct {
	mu    sync.RWMutex
	items map[string]*T
	order []string
}

// NewSet returns an empty set.
func NewSet[T any]() *Set[T] {
	return &Set[T]{items: map[string]*T{}}
}

// Put adds a model to the set. A model that is already there keeps its
// place and takes the new value.
func (s *Set[T]) Put(id string, item *T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, there := s.items[id]; !there {
		s.order = append(s.order, id)
	}
	s.items[id] = item
}

// Get returns the model with this id, or nothing.
func (s *Set[T]) Get(id string) *T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.items[id]
}

// Has tells whether the set holds a model with this id.
func (s *Set[T]) Has(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, there := s.items[id]
	return there
}

// Remove takes a model out of the set.
func (s *Set[T]) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, there := s.items[id]; !there {
		return
	}
	delete(s.items, id)
	for i, other := range s.order {
		if other == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

// List returns the models, in the order they came in.
func (s *Set[T]) List() []*T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*T, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.items[id])
	}
	return out
}

// Len is how many models the set holds.
func (s *Set[T]) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.order)
}
