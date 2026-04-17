package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"syrogo/internal/runtime"
)

func (p *OpenAICompatibleProvider) ChatCompletion(ctx context.Context, req runtime.Request) (runtime.Response, error) {
	if req.Model == "" {
		return runtime.Response{}, fmt.Errorf("model is required")
	}
	if len(p.apiKeys) == 0 {
		return runtime.Response{}, fmt.Errorf("api key is required")
	}

	var payload any
	switch p.mode {
	case openAIProtocolModeResponses:
		payload = encodeOpenAIResponsesRequest(req)
	default:
		payload = encodeOpenAIChatRequest(req)
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return runtime.Response{}, fmt.Errorf("marshal request: %w", err)
	}

	start := p.currentAPIKeyIndex()
	lastErr := error(nil)
	for offset := range p.apiKeys {
		keyIx := (start + offset) % len(p.apiKeys)
		resp, err := p.completionWithAPIKey(ctx, encodedPayload, p.apiKeys[keyIx])
		if err == nil {
			p.markNextAPIKeyAfter(keyIx)
			return resp, nil
		}

		lastErr = err
		if NormalizeError(err) != ErrorKindQuotaExceeded || offset == len(p.apiKeys)-1 {
			return runtime.Response{}, err
		}

		p.setNextAPIKey((keyIx + 1) % len(p.apiKeys))
	}

	return runtime.Response{}, lastErr
}

func (p *OpenAICompatibleProvider) StreamCompletion(ctx context.Context, req runtime.Request) (<-chan runtime.StreamEvent, error) {
	resp, err := p.ChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}
	return streamResponse(resp), nil
}

func (p *OpenAICompatibleProvider) completionWithAPIKey(ctx context.Context, payload []byte, apiKey string) (runtime.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+p.path, bytes.NewReader(payload))
	if err != nil {
		return runtime.Response{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	protocol := "openai_chat"
	if p.mode == openAIProtocolModeResponses {
		protocol = "openai_responses"
	}
	trace := providerTraceSnapshot{
		RequestID: requestIDFromContext(ctx),
		Provider:  p.providerName,
		Protocol:  protocol,
		Method:    http.MethodPost,
		URL:       httpReq.URL.String(),
		Headers: redactHeaders(map[string]string{
			"Content-Type":  httpReq.Header.Get("Content-Type"),
			"Authorization": httpReq.Header.Get("Authorization"),
		}),
		Request:   append(json.RawMessage(nil), payload...),
		CreatedAt: time.Now().Format(time.RFC3339Nano),
	}

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		trace.Error = err.Error()
		appendProviderTraceSnapshot(trace)
		return runtime.Response{}, NewRetryableError(fmt.Errorf("send request: %w", err))
	}
	defer httpResp.Body.Close()

	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		trace.Status = httpResp.StatusCode
		trace.Error = err.Error()
		appendProviderTraceSnapshot(trace)
		return runtime.Response{}, NewRetryableError(fmt.Errorf("read response body: %w", err))
	}
	trace.Status = httpResp.StatusCode
	trace.Response = append(json.RawMessage(nil), responseBody...)
	appendProviderTraceSnapshot(trace)

	if httpResp.StatusCode == http.StatusTooManyRequests {
		return runtime.Response{}, NewQuotaExceededError(fmt.Errorf("upstream quota exceeded: %s", previewResponseBody(responseBody)))
	}
	if httpResp.StatusCode >= http.StatusInternalServerError {
		return runtime.Response{}, NewRetryableError(fmt.Errorf("upstream server error: %s body=%s", httpResp.Status, previewResponseBody(responseBody)))
	}
	if httpResp.StatusCode >= http.StatusBadRequest {
		return runtime.Response{}, NewFatalError(fmt.Errorf("upstream request failed: %s body=%s", httpResp.Status, previewResponseBody(responseBody)))
	}

	switch p.mode {
	case openAIProtocolModeResponses:
		var resp openAIResponsesEnvelope
		if err := json.Unmarshal(responseBody, &resp); err != nil {
			return runtime.Response{}, NewRetryableError(fmt.Errorf("decode response: %w", err))
		}
		return decodeOpenAIResponsesResponse(resp)
	default:
		var resp openAIChatResponseEnvelope
		if err := json.Unmarshal(responseBody, &resp); err != nil {
			return runtime.Response{}, NewRetryableError(fmt.Errorf("decode response: %w", err))
		}
		return decodeOpenAIChatResponse(resp)
	}
}
