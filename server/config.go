package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

type configuration struct {
	Enabled                       bool
	ExternalAPIURL                string
	AuthorizationType             string
	AuthorizationToken            string
	IncludeMessageText            bool
	MaxMessageTextLength          int
	RequestTimeoutSeconds         int
	MaxRetries                    int
	InitialRetryDelayMilliseconds int
	MaxRetryDelaySeconds          int
	WorkerCount                   int
	QueueSize                     int
	TLSVerify                     bool
	AdditionalHeaders             string
	TestUsernames                 string
}

type runtimeConfig struct {
	Enabled                bool
	ExternalAPIURL         *url.URL
	AuthorizationType      string
	AuthorizationToken     string
	IncludeMessageText     bool
	MaxMessageTextLength   int
	RequestTimeout         time.Duration
	MaxRetries             int
	InitialRetryDelay      time.Duration
	MaxRetryDelay          time.Duration
	WorkerCount            int
	QueueSize              int
	TLSVerify              bool
	AdditionalHeaders      map[string]string
	TestUsernameFilter     map[string]struct{}
	TestModeEnabled        bool
	HTTPClient             *http.Client
	NormalizedEndpointHost string
}

type atomicRuntimeConfig struct {
	value atomic.Pointer[runtimeConfig]
}

func (a *atomicRuntimeConfig) Load() *runtimeConfig {
	return a.value.Load()
}

func (a *atomicRuntimeConfig) Store(cfg *runtimeConfig) {
	a.value.Store(cfg)
}

func parseRuntimeConfig(cfg configuration) (*runtimeConfig, error) {
	rc := &runtimeConfig{
		Enabled:              cfg.Enabled,
		AuthorizationType:    strings.ToLower(strings.TrimSpace(cfg.AuthorizationType)),
		AuthorizationToken:   cfg.AuthorizationToken,
		IncludeMessageText:   cfg.IncludeMessageText,
		MaxMessageTextLength: cfg.MaxMessageTextLength,
		MaxRetries:           cfg.MaxRetries,
		TLSVerify:            true,
	}

	if cfg.RequestTimeoutSeconds <= 0 {
		cfg.RequestTimeoutSeconds = 5
	}
	if cfg.MaxRetries < 0 {
		return nil, fmt.Errorf("MaxRetries must be >= 0")
	}
	if cfg.InitialRetryDelayMilliseconds <= 0 {
		cfg.InitialRetryDelayMilliseconds = 500
	}
	if cfg.MaxRetryDelaySeconds <= 0 {
		cfg.MaxRetryDelaySeconds = 30
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 2
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1024
	}
	if cfg.MaxMessageTextLength < 0 {
		return nil, fmt.Errorf("MaxMessageTextLength must be >= 0")
	}

	rc.RequestTimeout = time.Duration(cfg.RequestTimeoutSeconds) * time.Second
	rc.InitialRetryDelay = time.Duration(cfg.InitialRetryDelayMilliseconds) * time.Millisecond
	rc.MaxRetryDelay = time.Duration(cfg.MaxRetryDelaySeconds) * time.Second
	rc.WorkerCount = cfg.WorkerCount
	rc.QueueSize = cfg.QueueSize
	rc.TLSVerify = cfg.TLSVerify

	if strings.TrimSpace(cfg.ExternalAPIURL) == "" {
		if cfg.Enabled {
			return nil, fmt.Errorf("ExternalAPIURL is required when Enabled=true")
		}
	} else {
		parsedURL, err := url.Parse(strings.TrimSpace(cfg.ExternalAPIURL))
		if err != nil {
			return nil, fmt.Errorf("invalid ExternalAPIURL: %w", err)
		}
		if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
			return nil, fmt.Errorf("ExternalAPIURL must use http or https")
		}
		if parsedURL.Host == "" {
			return nil, fmt.Errorf("ExternalAPIURL host is required")
		}
		rc.ExternalAPIURL = parsedURL
		rc.NormalizedEndpointHost = parsedURL.Host
	}

	if rc.AuthorizationType != "" && rc.AuthorizationType != "bearer" {
		return nil, fmt.Errorf("unsupported AuthorizationType %q", rc.AuthorizationType)
	}
	if rc.AuthorizationType == "bearer" && strings.TrimSpace(rc.AuthorizationToken) == "" {
		return nil, fmt.Errorf("AuthorizationToken is required for bearer auth")
	}

	if strings.TrimSpace(cfg.AdditionalHeaders) == "" {
		cfg.AdditionalHeaders = "{}"
	}
	if err := json.Unmarshal([]byte(cfg.AdditionalHeaders), &rc.AdditionalHeaders); err != nil {
		return nil, fmt.Errorf("AdditionalHeaders must be a JSON object: %w", err)
	}
	if rc.AdditionalHeaders == nil {
		rc.AdditionalHeaders = map[string]string{}
	}
	for k, v := range rc.AdditionalHeaders {
		if strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("AdditionalHeaders contains an empty header name")
		}
		rc.AdditionalHeaders[http.CanonicalHeaderKey(k)] = v
		if http.CanonicalHeaderKey(k) != k {
			delete(rc.AdditionalHeaders, k)
		}
	}

	rc.TestUsernameFilter = parseTestUsernames(cfg.TestUsernames)
	rc.TestModeEnabled = len(rc.TestUsernameFilter) > 0
	rc.HTTPClient = newHTTPClient(rc)

	return rc, nil
}

func parseTestUsernames(raw string) map[string]struct{} {
	allowed := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		allowed[name] = struct{}{}
	}
	return allowed
}

func newHTTPClient(cfg *runtimeConfig) *http.Client {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: !cfg.TLSVerify,
		},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}

	return &http.Client{
		Timeout:   cfg.RequestTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}
