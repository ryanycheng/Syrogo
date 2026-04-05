package provider

import (
	"context"
	"fmt"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
}

type ChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Content string `json:"content"`
}

type Provider interface {
	Name() string
	ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

type MockProvider struct {
	providerName string
}

func NewMock(name string) *MockProvider {
	return &MockProvider{providerName: name}
}

func (p *MockProvider) Name() string {
	return p.providerName
}

func (p *MockProvider) ChatCompletion(_ context.Context, req ChatRequest) (ChatResponse, error) {
	if req.Model == "" {
		return ChatResponse{}, fmt.Errorf("model is required")
	}

	return ChatResponse{
		ID:      "chatcmpl-mock",
		Object:  "chat.completion",
		Model:   req.Model,
		Content: "syrogo mock response",
	}, nil
}
