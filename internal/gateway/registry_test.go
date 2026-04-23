package gateway

import (
	"bytes"
	"log/slog"
	"net/http"
	"testing"

	"github.com/ryanycheng/Syrogo/internal/config"
)

type stubInboundCodec struct{}

func (stubInboundCodec) Handle(_ *Handler, _ http.ResponseWriter, _ *http.Request, _ config.InboundSpec, _ config.ClientSpec, _ *slog.Logger) {
}

func TestInboundRegistryGetReturnsRegisteredCodec(t *testing.T) {
	registry := NewInboundRegistry()
	if err := registry.Register("stub", stubInboundCodec{}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	codec, ok := registry.Get("stub")
	if !ok || codec == nil {
		t.Fatalf("Get() = (%v, %v), want registered codec", codec, ok)
	}
}

func TestDefaultInboundRegistryRegistersCoreProtocols(t *testing.T) {
	registry := DefaultInboundRegistry()
	want := []string{"anthropic_messages", "openai_chat", "openai_responses"}
	if got := registry.Protocols(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("Protocols() = %#v, want %#v", got, want)
	}
	for _, protocol := range want {
		codec, ok := registry.Get(protocol)
		if !ok || codec == nil {
			t.Fatalf("Get(%q) = (%v, %v), want registered codec", protocol, codec, ok)
		}
	}
}

func TestInboundRegistryRejectsDuplicateRegister(t *testing.T) {
	registry := NewInboundRegistry()
	if err := registry.Register("stub", stubInboundCodec{}); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}

	err := registry.Register("stub", stubInboundCodec{})
	if err == nil || err.Error() != "inbound codec \"stub\" already registered" {
		t.Fatalf("second Register() error = %v, want duplicate register error", err)
	}
}

func TestHandlerHandleByCodecRejectsUnknownProtocol(t *testing.T) {
	h := &Handler{registry: NewInboundRegistry(), logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}
	w := loggingResponseWriter{ResponseWriter: newTestResponseWriter()}
	ok := h.handleByCodec(&w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "token", nil), config.InboundSpec{Protocol: "missing"}, config.ClientSpec{}, h.logger)
	if ok {
		t.Fatal("handleByCodec() ok = true, want false")
	}
}

type testResponseWriter struct {
	header http.Header
	status int
}

func newTestResponseWriter() *testResponseWriter {
	return &testResponseWriter{header: make(http.Header)}
}

func (w *testResponseWriter) Header() http.Header            { return w.header }
func (w *testResponseWriter) Write(data []byte) (int, error) { return len(data), nil }
func (w *testResponseWriter) WriteHeader(statusCode int)     { w.status = statusCode }
