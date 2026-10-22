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

	// The set is read while it is written: what the test says is that the
	// program does not stop — Go throws "concurrent map read and map write"
	// when a map is read and written at the same time, and the race
	// detector says so too. The number of models is not compared: the set
	// changes between two calls, and the count is not a thing here.
	for i := 0; i < 500; i++ {
		for _, server := range set.List() {
			if server.Name == "" {
				t.Error("the set gave a server without a name")
			}
		}
	}
	close(stop)
	writer.Wait()

	// what the set holds when nobody writes it any more
	if set.Len() != len(set.List()) {
		t.Errorf("the set says it holds %d models and gives %d, they must be the same", set.Len(), len(set.List()))
	}
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
