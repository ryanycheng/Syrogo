package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ryanycheng/Syrogo/internal/runtime"
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
		if err := validateOpenAIResponsesRequest(req, p.responsesCompat); err != nil {
			return runtime.Response{}, err
		}
		payload = encodeOpenAIResponsesRequest(req, p.responsesCompat)
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
	if p.mode == openAIProtocolModeResponses {
		resp, err := p.ChatCompletion(ctx, req)
		if err != nil {
			return nil, err
		}
		return streamResponse(resp), nil
	}
	if req.Model == "" {
		return nil, fmt.Errorf("model is required")
	}
	if len(p.apiKeys) == 0 {
		return nil, fmt.Errorf("api key is required")
	}

	payload := encodeOpenAIChatRequest(req)
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	streamPayload := append([]byte(nil), encodedPayload...)
	streamPayload, err = enableStreamOnPayload(streamPayload)
	if err != nil {
		return nil, err
	}

	start := p.currentAPIKeyIndex()
	lastErr := error(nil)
	for offset := range p.apiKeys {
		keyIx := (start + offset) % len(p.apiKeys)
		events, err := p.streamWithAPIKey(ctx, streamPayload, p.apiKeys[keyIx])
		if err == nil {
			p.markNextAPIKeyAfter(keyIx)
			return events, nil
		}
		lastErr = err
		if NormalizeError(err) != ErrorKindQuotaExceeded || offset == len(p.apiKeys)-1 {
			break
		}
		p.setNextAPIKey((keyIx + 1) % len(p.apiKeys))
	}

	resp, err := p.ChatCompletion(ctx, req)
	if err == nil {
		return streamResponse(resp), nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, err
}

func enableStreamOnPayload(payload []byte) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, fmt.Errorf("decode stream payload: %w", err)
	}
	body["stream"] = true
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode stream payload: %w", err)
	}
	return encoded, nil
}

func (p *OpenAICompatibleProvider) streamWithAPIKey(ctx context.Context, payload []byte, apiKey string) (<-chan runtime.StreamEvent, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+p.path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	trace := providerTraceSnapshot{
		RequestID: requestIDFromContext(ctx),
		Provider:  p.providerName,
		Protocol:  "openai_chat",
		Method:    http.MethodPost,
		URL:       httpReq.URL.String(),
		Headers: redactHeaders(map[string]string{
			"Content-Type":  httpReq.Header.Get("Content-Type"),
			"Authorization": httpReq.Header.Get("Authorization"),
			"Accept":        httpReq.Header.Get("Accept"),
		}),
		Request:   append(json.RawMessage(nil), payload...),
		CreatedAt: time.Now().Format(time.RFC3339Nano),
	}

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		trace.Error = err.Error()
		appendProviderTraceSnapshot(trace)
		return nil, NewRetryableError(fmt.Errorf("send stream request: %w", err))
	}

	if httpResp.StatusCode == http.StatusTooManyRequests {
		defer httpResp.Body.Close()
		responseBody, _ := io.ReadAll(httpResp.Body)
		trace.Status = httpResp.StatusCode
		trace.Response = append(json.RawMessage(nil), responseBody...)
		appendProviderTraceSnapshot(trace)
		return nil, NewQuotaExceededError(fmt.Errorf("upstream quota exceeded: %s", previewResponseBody(responseBody)))
	}
	if httpResp.StatusCode >= http.StatusBadRequest {
		defer httpResp.Body.Close()
		responseBody, _ := io.ReadAll(httpResp.Body)
		trace.Status = httpResp.StatusCode
		trace.Response = append(json.RawMessage(nil), responseBody...)
		appendProviderTraceSnapshot(trace)
		if httpResp.StatusCode >= http.StatusInternalServerError {
			return nil, NewRetryableError(fmt.Errorf("upstream server error: %s body=%s", httpResp.Status, previewResponseBody(responseBody)))
		}
		return nil, NewFatalError(fmt.Errorf("upstream request failed: %s body=%s", httpResp.Status, previewResponseBody(responseBody)))
	}

	trace.Status = httpResp.StatusCode
	appendProviderTraceSnapshot(trace)
	contentType := httpResp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/event-stream") {
		defer httpResp.Body.Close()
		responseBody, err := io.ReadAll(httpResp.Body)
		if err != nil {
			return nil, NewRetryableError(fmt.Errorf("read fallback response body: %w", err))
		}
		trace.Response = append(json.RawMessage(nil), responseBody...)
		appendProviderTraceSnapshot(trace)
		var resp openAIChatResponseEnvelope
		if err := json.Unmarshal(responseBody, &resp); err != nil {
			return nil, NewRetryableError(fmt.Errorf("decode fallback response: %w", err))
		}
		decoded, err := decodeOpenAIChatResponse(resp)
		if err != nil {
			return nil, err
		}
		return streamResponse(decoded), nil
	}

	traceWriter := newProviderTraceWriter(trace.RequestID, trace.Provider, trace.Protocol, "stream")
	streamBody := io.Reader(httpResp.Body)
	if traceWriter != nil {
		streamBody = io.TeeReader(httpResp.Body, traceWriter)
	}
	events, err := decodeOpenAIChatStream(streamBody)
	if err != nil {
		if traceWriter != nil {
			_ = traceWriter.Close()
		}
		_ = httpResp.Body.Close()
		return nil, NewRetryableError(err)
	}
	out := make(chan runtime.StreamEvent)
	go func() {
		defer close(out)
		defer httpResp.Body.Close()
		defer func() {
			if traceWriter != nil {
				_ = traceWriter.Close()
			}
		}()
		for event := range events {
			out <- event
		}
	}()
	return out, nil
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
