package tunnel

import (
	"io"
	"sync"
)

// pipe copies bytes in both directions until one side is done. A half close
// is used when the other side supports it, so the far end sees the end of
// the request before the answer.
func pipe(a io.ReadWriteCloser, b io.ReadWriteCloser) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(b, a)
		if c, ok := b.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		} else {
			b.Close()
		}
	}()
	go func() {
		defer wg.Done()
		io.Copy(a, b)
		a.Close()
	}()
	wg.Wait()
}
