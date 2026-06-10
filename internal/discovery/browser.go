package discovery

import "context"

// Browser emits discovered services until the context is cancelled. The mDNS
// implementation is the production browser; tests use a fake that replays a
// fixed set of services, so the recorder and the confirmation flow can be
// exercised without a network.
type Browser interface {
	Browse(ctx context.Context) (<-chan Service, error)
}

// SliceBrowser is a Browser that emits a fixed list of services, for tests.
type SliceBrowser struct {
	Services []Service
}

// Browse sends the configured services and then waits for cancellation.
func (b SliceBrowser) Browse(ctx context.Context) (<-chan Service, error) {
	ch := make(chan Service)
	go func() {
		defer close(ch)
		for _, s := range b.Services {
			select {
			case ch <- s:
			case <-ctx.Done():
				return
			}
		}
		<-ctx.Done()
	}()
	return ch, nil
}
