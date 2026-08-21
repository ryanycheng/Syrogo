package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

const codexConsumerBaseURL = "https://chatgpt.com/backend-api/codex"

type CodexConsumerProvider struct {
	providerName string
	source       CredentialSource
	httpClient   *http.Client
}

func NewCodexConsumerProvider(name string, source CredentialSource, httpClient *http.Client) *CodexConsumerProvider {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &CodexConsumerProvider{providerName: name, source: source, httpClient: httpClient}
}

func (p *CodexConsumerProvider) Name() string { return p.providerName }

func (p *CodexConsumerProvider) ChatCompletion(ctx context.Context, req runtime.Request) (runtime.Response, error) {
	if p.source == nil {
		return runtime.Response{}, fmt.Errorf("Codex OAuth credential is required")
	}
	credential, err := p.source.Credential(ctx)
	if err != nil {
		return runtime.Response{}, err
	}
	delegate := NewOpenAIResponsesCompatible(p.providerName, codexConsumerBaseURL, []string{credential.Value}, config.OutboundCapabilities{}, p.httpClient)
	response, err := delegate.ChatCompletion(ctx, req)
	if NormalizeError(err) != ErrorKindAuthFailed {
		return response, err
	}
	p.source.Invalidate()
	credential, refreshErr := p.source.Credential(ctx)
	if refreshErr != nil {
		return runtime.Response{}, refreshErr
	}
	delegate = NewOpenAIResponsesCompatible(p.providerName, codexConsumerBaseURL, []string{credential.Value}, config.OutboundCapabilities{}, p.httpClient)
	return delegate.ChatCompletion(ctx, req)
}

func (p *CodexConsumerProvider) StreamCompletion(ctx context.Context, req runtime.Request) (<-chan runtime.StreamEvent, error) {
	return nil, fmt.Errorf("Codex OAuth streaming is not supported")
}
