package store

// Set is a set of models that remembers the order they came in.
//
// The order is not a detail: the state file is written in it, and the panel
// shows the servers and the tunnels in it. A Go map has no order of its own,
// so the order is kept beside it — the way a Python dictionary keeps the
// order of its keys.
type Set[T any] struct {
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
	if _, there := s.items[id]; !there {
		s.order = append(s.order, id)
	}
	s.items[id] = item
}

// Get returns the model with this id, or nothing.
func (s *Set[T]) Get(id string) *T {
	return s.items[id]
}

// Has tells whether the set holds a model with this id.
func (s *Set[T]) Has(id string) bool {
	_, there := s.items[id]
	return there
}

// Remove takes a model out of the set.
func (s *Set[T]) Remove(id string) {
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
	out := make([]*T, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.items[id])
	}
	return out
}

// Len is how many models the set holds.
func (s *Set[T]) Len() int { return len(s.order) }
