package sessions

import (
	"context"
	"sync"
	"time"
)

const DefaultSweepInterval = 5 * time.Second

type Reaper struct {
	store     *Store
	ticker    *time.Ticker
	ticks     <-chan time.Time
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func NewReaper(store *Store) *Reaper {
	ticker := time.NewTicker(DefaultSweepInterval)
	return newReaper(store, ticker.C, ticker)
}

func newReaper(store *Store, ticks <-chan time.Time, ticker *time.Ticker) *Reaper {
	reaper := &Reaper{store: store, ticker: ticker, ticks: ticks, done: make(chan struct{})}
	reaper.wg.Add(1)
	go reaper.run()
	return reaper
}

func (r *Reaper) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.ticker != nil {
			r.ticker.Stop()
		}
		close(r.done)
	})
	closed := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(closed)
	}()
	select {
	case <-closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Reaper) run() {
	defer r.wg.Done()
	for {
		select {
		case <-r.ticks:
			r.store.Sweep(DefaultStoppedRetention, DefaultTransientTTL)
		case <-r.done:
			return
		}
	}
}
