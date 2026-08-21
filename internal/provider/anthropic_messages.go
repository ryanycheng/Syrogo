package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ryanycheng/Syrogo/internal/latency"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

func (p *AnthropicMessagesProvider) ChatCompletion(ctx context.Context, req runtime.Request) (runtime.Response, error) {
	if req.Model == "" {
		return runtime.Response{}, fmt.Errorf("model is required")
	}
	if len(p.apiKeys) == 0 && p.credentialSource == nil {
		return runtime.Response{}, fmt.Errorf("api key is required")
	}

	payload := encodeAnthropicMessagesRequest(req)
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return runtime.Response{}, fmt.Errorf("marshal request: %w", err)
	}

	if p.credentialSource != nil {
		credential, err := p.credentialSource.Credential(ctx)
		if err != nil {
			return runtime.Response{}, err
		}
		response, err := p.completionWithCredential(ctx, req, encodedPayload, credential.Value, true)
		if NormalizeError(err) != ErrorKindAuthFailed {
			return response, err
		}
		p.credentialSource.Invalidate()
		credential, refreshErr := p.credentialSource.Credential(ctx)
		if refreshErr != nil {
			return runtime.Response{}, refreshErr
		}
		return p.completionWithCredential(ctx, req, encodedPayload, credential.Value, true)
	}
	return p.completionWithCredential(ctx, req, encodedPayload, p.apiKeys[0], false)
}

func (p *AnthropicMessagesProvider) StreamCompletion(ctx context.Context, req runtime.Request) (<-chan runtime.StreamEvent, error) {
	streamReq := req
	streamReq.Stream = false
	resp, err := p.ChatCompletion(ctx, streamReq)
	if err != nil {
		return nil, err
	}
	return streamResponse(resp), nil
}

func (p *AnthropicMessagesProvider) completionWithCredential(ctx context.Context, req runtime.Request, payload []byte, credential string, oauth bool) (runtime.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/messages", bytes.NewReader(payload))
	if err != nil {
		return runtime.Response{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if oauth {
		httpReq.Header.Set("Authorization", "Bearer "+credential)
	} else {
		httpReq.Header.Set("x-api-key", credential)
	}
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	trace := providerTraceSnapshot{
		RequestID: requestIDFromContext(ctx),
		Provider:  p.providerName,
		Protocol:  "anthropic_messages",
		Method:    http.MethodPost,
		URL:       httpReq.URL.String(),
		Headers: redactHeaders(map[string]string{
			"Content-Type":      httpReq.Header.Get("Content-Type"),
			"x-api-key":         httpReq.Header.Get("x-api-key"),
			"anthropic-version": httpReq.Header.Get("anthropic-version"),
		}),
		Request:   append(json.RawMessage(nil), payload...),
		CreatedAt: time.Now().Format(time.RFC3339Nano),
	}

	upstreamStartedAt := time.Now()
	httpResp, err := p.httpClient.Do(httpReq)
	latency.RecordSpan(ctx, "upstream_round_trip", upstreamStartedAt, map[string]string{
		"provider": p.providerName,
		"protocol": "anthropic_messages",
	})
	if err != nil {
		trace.Error = err.Error()
		appendProviderTraceSnapshot(trace)
		return runtime.Response{}, NewTransientError(fmt.Errorf("send request: %w", err))
	}
	defer func() {
		_ = httpResp.Body.Close()
	}()

	readStartedAt := time.Now()
	responseBody, err := io.ReadAll(httpResp.Body)
	latency.RecordSpan(ctx, "upstream_read", readStartedAt, map[string]string{
		"provider": p.providerName,
		"protocol": "anthropic_messages",
	})
	if err != nil {
		trace.Status = httpResp.StatusCode
		trace.Error = err.Error()
		appendProviderTraceSnapshot(trace)
		return runtime.Response{}, NewTransientError(fmt.Errorf("read response body: %w", err))
	}
	trace.Status = httpResp.StatusCode
	trace.Response = append(json.RawMessage(nil), responseBody...)
	appendProviderTraceSnapshot(trace)

	if httpResp.StatusCode == http.StatusTooManyRequests {
		return runtime.Response{}, withResponseMetadata(NewQuotaExceededError(fmt.Errorf("upstream quota exceeded: %s", previewResponseBody(responseBody))), httpResp)
	}
	if httpResp.StatusCode >= http.StatusInternalServerError {
		return runtime.Response{}, withResponseMetadata(NewUpstreamServerError(fmt.Errorf("upstream server error: %s body=%s", httpResp.Status, previewResponseBody(responseBody))), httpResp)
	}
	if httpResp.StatusCode == http.StatusUnauthorized || httpResp.StatusCode == http.StatusForbidden {
		return runtime.Response{}, withResponseMetadata(NewAuthFailedError(fmt.Errorf("upstream auth failed: %s body=%s", httpResp.Status, previewResponseBody(responseBody))), httpResp)
	}
	if httpResp.StatusCode >= http.StatusBadRequest {
		return runtime.Response{}, withResponseMetadata(NewFatalError(fmt.Errorf("upstream request failed: %s body=%s", httpResp.Status, previewResponseBody(responseBody))), httpResp)
	}

	var resp anthropicMessagesEnvelope
	if err := json.Unmarshal(responseBody, &resp); err != nil {
		return runtime.Response{}, NewTransientError(fmt.Errorf("decode response: %w", err))
	}
	decoded, err := decodeAnthropicMessagesResponse(resp)
	if err != nil {
		return runtime.Response{}, err
	}
	if decoded.Usage == nil && p.usageEstimation.heuristicEnabled() {
		decoded.Usage = estimateUsageHeuristically(req, decoded)
	}
	return decoded, nil
}
