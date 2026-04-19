package gateway

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"

	"github.com/ryanycheng/Syrogo/internal/config"
)

type InboundCodec interface {
	Handle(h *Handler, w http.ResponseWriter, r *http.Request, inbound config.InboundSpec, client config.ClientSpec, logger *slog.Logger)
}

type InboundRegistry struct {
	mu     sync.RWMutex
	codecs map[string]InboundCodec
}

func NewInboundRegistry() *InboundRegistry {
	return &InboundRegistry{codecs: make(map[string]InboundCodec)}
}

func (r *InboundRegistry) Register(protocol string, codec InboundCodec) error {
	if protocol == "" {
		return fmt.Errorf("inbound protocol is required")
	}
	if codec == nil {
		return fmt.Errorf("inbound codec %q is nil", protocol)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.codecs[protocol]; exists {
		return fmt.Errorf("inbound codec %q already registered", protocol)
	}
	if r.codecs == nil {
		r.codecs = make(map[string]InboundCodec)
	}
	r.codecs[protocol] = codec
	return nil
}

func (r *InboundRegistry) MustRegister(protocol string, codec InboundCodec) {
	if err := r.Register(protocol, codec); err != nil {
		panic(err)
	}
}

func (r *InboundRegistry) Get(protocol string) (InboundCodec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	codec, ok := r.codecs[protocol]
	return codec, ok
}

func (r *InboundRegistry) Has(protocol string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.codecs[protocol]
	return ok
}

func (r *InboundRegistry) Protocols() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	protocols := make([]string, 0, len(r.codecs))
	for protocol := range r.codecs {
		protocols = append(protocols, protocol)
	}
	sort.Strings(protocols)
	return protocols
}

var defaultInboundRegistry = newDefaultInboundRegistry()

func DefaultInboundRegistry() *InboundRegistry {
	return defaultInboundRegistry
}

func newDefaultInboundRegistry() *InboundRegistry {
	registry := NewInboundRegistry()
	registry.MustRegister("openai_chat", openAIChatCodec{})
	registry.MustRegister("openai_responses", openAIResponsesCodec{})
	registry.MustRegister("anthropic_messages", anthropicMessagesCodec{})
	return registry
}
