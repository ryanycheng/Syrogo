package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewMultiCreatesOneServerPerListenAddress(t *testing.T) {
	s := NewMulti([]string{":8080", ":8081"}, http.NewServeMux())
	if s == nil {
		t.Fatal("NewMulti() returned nil")
	}
	if len(s.servers) != 2 {
		t.Fatalf("len(servers) = %d, want 2", len(s.servers))
	}
	if s.servers[0].Addr != ":8080" || s.servers[1].Addr != ":8081" {
		t.Fatalf("server addrs = [%s %s], want [:8080 :8081]", s.servers[0].Addr, s.servers[1].Addr)
	}
}

func TestNewListenersUsesPerListenerHandlers(t *testing.T) {
	h1 := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	h2 := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	s := NewListeners([]Listener{{Addr: ":8080", Handler: h1}, {Addr: ":8081", Handler: h2}})
	if s == nil {
		t.Fatal("NewListeners() returned nil")
	}
	if len(s.servers) != 2 {
		t.Fatalf("len(servers) = %d, want 2", len(s.servers))
	}

	w1 := httptest.NewRecorder()
	s.servers[0].Handler.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w1.Code != http.StatusCreated {
		t.Fatalf("servers[0] handler status = %d, want 201", w1.Code)
	}

	w2 := httptest.NewRecorder()
	s.servers[1].Handler.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w2.Code != http.StatusAccepted {
		t.Fatalf("servers[1] handler status = %d, want 202", w2.Code)
	}
}
