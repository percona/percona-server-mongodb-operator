package util

import (
	"runtime"
	"sync"
)

type errorGroup struct {
	errs []error
	mu   sync.Mutex

	wg  sync.WaitGroup
	sem chan struct{}
}

func NewErrorGroup(limit int) *errorGroup {
	if limit <= 0 {
		limit = runtime.NumCPU()
	}
	return &errorGroup{sem: make(chan struct{}, limit)}
}

func (g *errorGroup) Wait() []error {
	g.wg.Wait()
	return g.errs
}

func (g *errorGroup) Go(f func() error) {
	g.wg.Add(1)
	go func() {
		g.sem <- struct{}{}

		defer func() {
			<-g.sem
			g.wg.Done()
		}()

		if err := f(); err != nil {
			g.mu.Lock()
			g.errs = append(g.errs, err)
			g.mu.Unlock()
		}
	}()
}
