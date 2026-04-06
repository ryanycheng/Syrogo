package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type Listener struct {
	Addr    string
	Handler http.Handler
}

type HTTPServer struct {
	servers []*http.Server
}

func (s *HTTPServer) Listeners() []Listener {
	listeners := make([]Listener, 0, len(s.servers))
	for _, srv := range s.servers {
		listeners = append(listeners, Listener{Addr: srv.Addr, Handler: srv.Handler})
	}
	return listeners
}

func New(listen string, handler http.Handler) *HTTPServer {
	return NewMulti([]string{listen}, handler)
}

func NewMulti(listens []string, handler http.Handler) *HTTPServer {
	listeners := make([]Listener, 0, len(listens))
	for _, listen := range listens {
		listeners = append(listeners, Listener{Addr: listen, Handler: handler})
	}
	return NewListeners(listeners)
}

func NewListeners(listeners []Listener) *HTTPServer {
	servers := make([]*http.Server, 0, len(listeners))
	for _, listener := range listeners {
		servers = append(servers, &http.Server{
			Addr:              listener.Addr,
			Handler:           listener.Handler,
			ReadHeaderTimeout: 5 * time.Second,
		})
	}
	return &HTTPServer{servers: servers}
}

func (s *HTTPServer) Start() error {
	if len(s.servers) == 0 {
		return fmt.Errorf("no http servers configured")
	}
	if len(s.servers) == 1 {
		if err := s.servers[0].ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("listen and serve %s: %w", s.servers[0].Addr, err)
		}
		return nil
	}

	errCh := make(chan error, len(s.servers))
	var wg sync.WaitGroup
	for _, srv := range s.servers {
		wg.Add(1)
		go func(server *http.Server) {
			defer wg.Done()
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("listen and serve %s: %w", server.Addr, err)
			}
		}(srv)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *HTTPServer) Shutdown(ctx context.Context) error {
	var firstErr error
	for _, srv := range s.servers {
		if err := srv.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
