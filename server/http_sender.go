package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type deliveryResult struct {
	retryable   bool
	httpStatus  int
	retryAfter  time.Duration
	errCategory string
	err         error
}

func sendEvent(ctx context.Context, cfg *runtimeConfig, event outgoingEvent) deliveryResult {
	payload, err := json.Marshal(event)
	if err != nil {
		return deliveryResult{errCategory: "marshal", err: err}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.ExternalAPIURL.String(), bytes.NewReader(payload))
	if err != nil {
		return deliveryResult{errCategory: "request_build", err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", event.EventID)
	for k, v := range cfg.AdditionalHeaders {
		req.Header.Set(k, v)
	}
	if cfg.AuthorizationType == "bearer" {
		req.Header.Set("Authorization", "Bearer "+cfg.AuthorizationToken)
	}

	start := time.Now()
	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		if isRetryableNetworkError(err) {
			return deliveryResult{
				retryable:   true,
				errCategory: "network",
				err:         err,
			}
		}
		return deliveryResult{errCategory: "network", err: err}
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBodyBytes))
	_ = start

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return deliveryResult{httpStatus: resp.StatusCode}
	}

	result := deliveryResult{
		httpStatus:  resp.StatusCode,
		errCategory: "http_status",
		err:         fmt.Errorf("unexpected status %d", resp.StatusCode),
	}
	if retryAfter := parseRetryAfter(resp.Header.Get("Retry-After")); retryAfter > 0 {
		result.retryAfter = retryAfter
	}
	switch {
	case resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode == http.StatusTooEarly,
		resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500:
		result.retryable = true
	}
	return result
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

func isRetryableNetworkError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func computeBackoff(cfg *runtimeConfig, attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	delay := cfg.InitialRetryDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay > cfg.MaxRetryDelay {
			delay = cfg.MaxRetryDelay
			break
		}
	}
	jitter := time.Duration(rand.Int63n(int64(delay / 2)))
	delay += jitter
	if delay > cfg.MaxRetryDelay {
		delay = cfg.MaxRetryDelay
	}
	return delay
}
