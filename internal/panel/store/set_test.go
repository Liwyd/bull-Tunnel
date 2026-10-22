package store

import (
	"sync"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// TestTheSetCanBeReadWhileItIsWritten: the panel reads the state while it
// writes it, and Go stops the whole program when a map is read and written
// at the same time. The set has a lock of its own for that.
func TestTheSetCanBeReadWhileItIsWritten(t *testing.T) {
	set := NewSet[model.Server]()

	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			server := &model.Server{ID: model.NewID("s"), Name: "S", Role: model.Kharej}
			set.Put(server.ID, server)
			if i%2 == 0 {
				set.Remove(server.ID)
			}
		}
	}()

	for i := 0; i < 500; i++ {
		for _, server := range set.List() {
			if server.Name == "" {
				t.Error("the set gave a server without a name")
			}
		}
		if set.Len() != len(set.List()) {
			t.Error("the set does not know how many models it holds")
		}
	}
	close(stop)
	writer.Wait()
}

// TestTheSetKeepsTheOrderTheModelsCameIn: the order is not a detail: the
// state file is written in it.
func TestTheSetKeepsTheOrderTheModelsCameIn(t *testing.T) {
	set := NewSet[model.Server]()
	for _, name := range []string{"one", "two", "three"} {
		server := &model.Server{ID: name, Name: name}
		set.Put(server.ID, server)
	}
	// a model that is already there keeps its place
	set.Put("two", &model.Server{ID: "two", Name: "the second one"})

	names := []string{}
	for _, server := range set.List() {
		names = append(names, server.ID)
	}
	want := []string{"one", "two", "three"}
	if len(names) != len(want) {
		t.Fatalf("the set holds %v, it must hold %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("the set holds %v, it must hold %v", names, want)
		}
	}
	if set.Get("two").Name != "the second one" {
		t.Error("the model that was put again is not the new one")
	}
	if !set.Has("one") || set.Has("four") {
		t.Error("the set does not know what it holds")
	}
	set.Remove("two")
	if set.Has("two") || set.Len() != 2 {
		t.Error("the model was not taken out of the set")
	}
}
