package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

type fakeAPI struct {
	mu           sync.Mutex
	cfg          configuration
	posts        map[string]*model.Post
	users        map[string]*model.User
	channels     map[string]*model.Channel
	teams        map[string]*model.Team
	kv           map[string][]byte
	getPostCalls int
	getUserCalls int
	getChanCalls int
	getTeamCalls int
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		cfg: configuration{
			Enabled:                       true,
			ExternalAPIURL:                "http://example.invalid",
			RequestTimeoutSeconds:         1,
			MaxRetries:                    2,
			InitialRetryDelayMilliseconds: 10,
			MaxRetryDelaySeconds:          1,
			WorkerCount:                   1,
			QueueSize:                     32,
			TLSVerify:                     true,
			AdditionalHeaders:             "{}",
		},
		posts:    map[string]*model.Post{},
		users:    map[string]*model.User{},
		channels: map[string]*model.Channel{},
		teams:    map[string]*model.Team{},
		kv:       map[string][]byte{},
	}
}

func (f *fakeAPI) LoadPluginConfiguration(dest any) error {
	raw, _ := json.Marshal(f.cfg)
	return json.Unmarshal(raw, dest)
}
func (f *fakeAPI) GetPost(postID string) (*model.Post, *model.AppError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getPostCalls++
	return f.posts[postID], nil
}
func (f *fakeAPI) GetUser(userID string) (*model.User, *model.AppError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getUserCalls++
	return f.users[userID], nil
}
func (f *fakeAPI) GetChannel(channelID string) (*model.Channel, *model.AppError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getChanCalls++
	return f.channels[channelID], nil
}
func (f *fakeAPI) GetTeam(teamID string) (*model.Team, *model.AppError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getTeamCalls++
	return f.teams[teamID], nil
}
func (f *fakeAPI) KVSet(key string, value []byte) *model.AppError {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kv[key] = append([]byte(nil), value...)
	return nil
}
func (f *fakeAPI) KVGet(key string) ([]byte, *model.AppError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.kv[key]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), value...), nil
}
func (f *fakeAPI) KVDelete(key string) *model.AppError {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.kv, key)
	return nil
}
func (f *fakeAPI) KVList(page, perPage int) ([]string, *model.AppError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(f.kv))
	for key := range f.kv {
		keys = append(keys, key)
	}
	start := page * perPage
	if start >= len(keys) {
		return nil, nil
	}
	end := start + perPage
	if end > len(keys) {
		end = len(keys)
	}
	return keys[start:end], nil
}
func (f *fakeAPI) KVCompareAndSet(key string, oldValue, newValue []byte) (bool, *model.AppError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	current, ok := f.kv[key]
	switch {
	case !ok && oldValue == nil:
		f.kv[key] = append([]byte(nil), newValue...)
		return true, nil
	case ok && bytes.Equal(current, oldValue):
		f.kv[key] = append([]byte(nil), newValue...)
		return true, nil
	default:
		return false, nil
	}
}
func (f *fakeAPI) LogDebug(string, ...any) {}
func (f *fakeAPI) LogInfo(string, ...any)  {}
func (f *fakeAPI) LogWarn(string, ...any)  {}
func (f *fakeAPI) LogError(string, ...any) {}

func newTestPlugin(t *testing.T, api *fakeAPI) *Plugin {
	t.Helper()
	p := &Plugin{api: api}
	if err := p.OnActivate(); err != nil {
		t.Fatalf("OnActivate: %v", err)
	}
	t.Cleanup(func() { _ = p.OnDeactivate() })
	return p
}

func seedCommonEntities(api *fakeAPI) {
	api.users["sender"] = &model.User{Id: "sender", Username: "sender", FirstName: "Send", LastName: "Er"}
	api.users["recipient"] = &model.User{Id: "recipient", Username: "recipient", FirstName: "Re", LastName: "Cipient"}
	api.users["other"] = &model.User{Id: "other", Username: "other"}
	api.channels["channel"] = &model.Channel{Id: "channel", Type: model.ChannelTypeOpen, Name: "town-square", DisplayName: "Town Square", TeamId: "team"}
	api.channels["dm"] = &model.Channel{Id: "dm", Type: model.ChannelTypeDirect, Name: "sender__recipient", DisplayName: "Direct"}
	api.channels["gm"] = &model.Channel{Id: "gm", Type: model.ChannelTypeGroup, Name: "group", DisplayName: "Group"}
	api.teams["team"] = &model.Team{Id: "team", Name: "team-name"}
}

func TestBuildOutgoingEventIncludeMessageToggle(t *testing.T) {
	api := newFakeAPI()
	seedCommonEntities(api)
	post := &model.Post{Id: "post", UserId: "sender", ChannelId: "channel", Message: "hello", CreateAt: time.Now().UnixMilli()}
	push := &model.PushNotification{ServerId: "srv", Type: model.PushTypeMessage}
	withTextCfg, _ := parseRuntimeConfig(api.cfg)
	withTextCfg.IncludeMessageText = true
	event := buildOutgoingEvent(withTextCfg, push, post, api.users["sender"], api.users["recipient"], api.channels["channel"], api.teams["team"])
	if event.Post.Message == nil || *event.Post.Message != "hello" {
		t.Fatalf("expected message to be present")
	}
	noTextCfg, _ := parseRuntimeConfig(api.cfg)
	noTextCfg.IncludeMessageText = false
	event = buildOutgoingEvent(noTextCfg, push, post, api.users["sender"], api.users["recipient"], api.channels["channel"], api.teams["team"])
	if event.Post.Message != nil {
		t.Fatalf("expected message to be omitted")
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	postMap, ok := decoded["post"].(map[string]any)
	if !ok {
		t.Fatalf("expected post object in payload")
	}
	if _, ok := postMap["message"]; ok {
		t.Fatalf("message field must be omitted from JSON")
	}
}

func TestUnicodeTruncation(t *testing.T) {
	if got := truncateUnicode("привет🙂", 4); got != "прив" {
		t.Fatalf("unexpected truncation result: %q", got)
	}
}

func TestRecipientFilteringByTestUsernames(t *testing.T) {
	api := newFakeAPI()
	api.cfg.TestUsernames = " Recipient , OTHER, recipient "
	seedCommonEntities(api)
	p := newTestPlugin(t, api)
	push := &model.PushNotification{ServerId: "srv", Type: model.PushTypeMessage, PostId: "post"}
	api.posts["post"] = &model.Post{Id: "post", UserId: "sender", ChannelId: "channel", Message: "hi"}

	p.NotificationWillBePushed(push, "recipient")
	if p.metrics.hookEnqueued.Load() != 1 {
		t.Fatalf("expected recipient to pass filter")
	}

	p.NotificationWillBePushed(&model.PushNotification{ServerId: "srv", Type: model.PushTypeMessage, PostId: "post-2"}, "sidorova")
	if api.getPostCalls != 1 {
		t.Fatalf("excluded recipient should not trigger post lookup")
	}
}

func TestExcludedRecipientDoesNotPersistOutboxRecord(t *testing.T) {
	api := newFakeAPI()
	api.cfg.TestUsernames = "recipient"
	seedCommonEntities(api)
	api.users["blocked"] = &model.User{Id: "blocked", Username: "blocked"}
	api.posts["post"] = &model.Post{Id: "post", UserId: "sender", ChannelId: "channel", Message: "hi"}
	p := newTestPlugin(t, api)

	replacement, reason := p.NotificationWillBePushed(&model.PushNotification{ServerId: "srv", Type: model.PushTypeMessage, PostId: "post"}, "blocked")
	if replacement != nil || reason != "" {
		t.Fatalf("hook must always return nil, empty string")
	}
	for key := range api.kv {
		if strings.HasPrefix(key, outboxPrefix) {
			t.Fatalf("excluded recipient must not create outbox records")
		}
	}
}

func TestNotificationDeduplicatesAcrossRepeatedPushes(t *testing.T) {
	api := newFakeAPI()
	seedCommonEntities(api)
	api.posts["post"] = &model.Post{Id: "post", UserId: "sender", ChannelId: "dm", Message: "hello"}
	p := newTestPlugin(t, api)
	push := &model.PushNotification{ServerId: "srv", Type: model.PushTypeMessage, PostId: "post"}

	p.NotificationWillBePushed(push, "recipient")
	p.NotificationWillBePushed(push, "recipient")

	if p.metrics.hookEnqueued.Load() != 1 {
		t.Fatalf("expected one enqueued event, got %d", p.metrics.hookEnqueued.Load())
	}
	if p.metrics.hookDeduplicated.Load() != 1 {
		t.Fatalf("expected one deduplicated event, got %d", p.metrics.hookDeduplicated.Load())
	}
}

func TestHookAlwaysReturnsUnmodifiedNotification(t *testing.T) {
	api := newFakeAPI()
	seedCommonEntities(api)
	api.posts["post"] = &model.Post{Id: "post", UserId: "sender", ChannelId: "dm", Message: "hello"}
	p := newTestPlugin(t, api)
	replacement, reason := p.NotificationWillBePushed(&model.PushNotification{ServerId: "srv", Type: model.PushTypeMessage, PostId: "post"}, "recipient")
	if replacement != nil || reason != "" {
		t.Fatalf("expected nil replacement and empty reason")
	}
}

func TestDifferentRecipientsProduceDifferentEvents(t *testing.T) {
	api := newFakeAPI()
	seedCommonEntities(api)
	api.users["recipient2"] = &model.User{Id: "recipient2", Username: "recipient2"}
	api.posts["post"] = &model.Post{Id: "post", UserId: "sender", ChannelId: "channel", Message: "@recipient @recipient2"}
	p := newTestPlugin(t, api)
	push := &model.PushNotification{ServerId: "srv", Type: model.PushTypeMessage, PostId: "post"}

	p.NotificationWillBePushed(push, "recipient")
	p.NotificationWillBePushed(push, "recipient2")

	if p.metrics.hookEnqueued.Load() != 2 {
		t.Fatalf("expected two distinct events")
	}
}

func TestDispatcherRetriesAndPermanentFailureRules(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch requests {
		case 1:
			w.WriteHeader(http.StatusInternalServerError)
		case 2:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		case 3:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	cfg, _ := parseRuntimeConfig(configuration{
		Enabled:                       true,
		ExternalAPIURL:                server.URL,
		RequestTimeoutSeconds:         1,
		MaxRetries:                    3,
		InitialRetryDelayMilliseconds: 10,
		MaxRetryDelaySeconds:          1,
		WorkerCount:                   1,
		QueueSize:                     8,
		TLSVerify:                     true,
		AdditionalHeaders:             "{}",
	})
	api := newFakeAPI()
	store := newKVEventStore(api)
	record := outboxRecord{
		Event: outgoingEvent{
			EventID:   "event-1",
			EventType: eventTypeMessageNotify,
			Recipient: userEnvelope{UserID: "recipient"},
			Post:      postEnvelope{PostID: "post"},
		},
		Status: eventStatusPending,
	}
	if _, err := store.InsertPending(record); err != nil {
		t.Fatalf("InsertPending: %v", err)
	}
	m := &metrics{}
	d := newDispatcher(api, store, m, cfg)
	d.updateConfigGetter(func() *runtimeConfig { return cfg })
	defer d.Stop(time.Second)
	if !d.Enqueue("event-1") {
		t.Fatalf("enqueue failed")
	}

	requireEventually(t, 2*time.Second, func() bool {
		record, _ := store.Get("event-1")
		return record != nil && record.Status == eventStatusDelivered
	})
}

func TestNoRetryOnHTTP400(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	cfg, _ := parseRuntimeConfig(configuration{
		Enabled:                       true,
		ExternalAPIURL:                server.URL,
		RequestTimeoutSeconds:         1,
		MaxRetries:                    3,
		InitialRetryDelayMilliseconds: 10,
		MaxRetryDelaySeconds:          1,
		WorkerCount:                   1,
		QueueSize:                     8,
		TLSVerify:                     true,
		AdditionalHeaders:             "{}",
	})
	result := sendEvent(context.Background(), cfg, outgoingEvent{EventID: "e1"})
	if result.retryable {
		t.Fatalf("400 must not be retryable")
	}
}

func TestTimeoutIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	cfg, _ := parseRuntimeConfig(configuration{
		Enabled:                       true,
		ExternalAPIURL:                server.URL,
		RequestTimeoutSeconds:         0,
		MaxRetries:                    1,
		InitialRetryDelayMilliseconds: 10,
		MaxRetryDelaySeconds:          1,
		WorkerCount:                   1,
		QueueSize:                     8,
		TLSVerify:                     true,
		AdditionalHeaders:             "{}",
	})
	cfg.RequestTimeout = 50 * time.Millisecond
	cfg.HTTPClient = newHTTPClient(cfg)
	result := sendEvent(context.Background(), cfg, outgoingEvent{EventID: "e1"})
	if !result.retryable {
		t.Fatalf("timeout must be retryable")
	}
}

func TestRecoverPendingAfterRestart(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	api := newFakeAPI()
	api.cfg.ExternalAPIURL = server.URL
	seedCommonEntities(api)
	api.posts["post"] = &model.Post{Id: "post", UserId: "sender", ChannelId: "dm", Message: "hello"}

	p1 := newTestPlugin(t, api)
	push := &model.PushNotification{ServerId: "srv", Type: model.PushTypeMessage, PostId: "post"}
	p1.NotificationWillBePushed(push, "recipient")
	_ = p1.OnDeactivate()

	p2 := newTestPlugin(t, api)
	requireEventually(t, 2*time.Second, func() bool { return p2.metrics.delivered.Load() == 1 })
}

func TestConfigurationChangeAppliesNewTestUsernamesWithoutRestart(t *testing.T) {
	api := newFakeAPI()
	api.cfg.TestUsernames = "recipient"
	seedCommonEntities(api)
	api.users["blocked"] = &model.User{Id: "blocked", Username: "blocked"}
	api.posts["post"] = &model.Post{Id: "post", UserId: "sender", ChannelId: "dm", Message: "hello"}
	p := newTestPlugin(t, api)

	p.NotificationWillBePushed(&model.PushNotification{ServerId: "srv", Type: model.PushTypeMessage, PostId: "post"}, "blocked")
	if p.metrics.hookEnqueued.Load() != 0 {
		t.Fatalf("blocked recipient should not pass initial filter")
	}

	api.cfg.TestUsernames = "blocked"
	if err := p.OnConfigurationChange(); err != nil {
		t.Fatalf("OnConfigurationChange: %v", err)
	}
	p.NotificationWillBePushed(&model.PushNotification{ServerId: "srv", Type: model.PushTypeMessage, PostId: "post"}, "blocked")
	if p.metrics.hookEnqueued.Load() != 1 {
		t.Fatalf("updated filter should apply without restart")
	}
}

func TestServeHealth(t *testing.T) {
	api := newFakeAPI()
	seedCommonEntities(api)
	p := newTestPlugin(t, api)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(nil, rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if !bytes.Contains(body, []byte(`"enabled":true`)) {
		t.Fatalf("unexpected health payload: %s", string(body))
	}
}

func TestInvalidConfigurationRejected(t *testing.T) {
	_, err := parseRuntimeConfig(configuration{
		Enabled:           true,
		ExternalAPIURL:    "ftp://invalid",
		AdditionalHeaders: "{}",
		TLSVerify:         true,
	})
	if err == nil {
		t.Fatalf("expected invalid config to fail")
	}
}

func TestDMMentionAndThreadReasonNormalization(t *testing.T) {
	api := newFakeAPI()
	seedCommonEntities(api)
	push := &model.PushNotification{ServerId: "srv", Type: model.PushTypeMessage}
	post := &model.Post{Id: "post", UserId: "sender", ChannelId: "dm", RootId: "root"}
	event := buildOutgoingEvent(mustConfig(t), push, post, api.users["sender"], api.users["recipient"], api.channels["dm"], nil)
	if event.Mattermost.NormalizedReason != "direct_message" {
		t.Fatalf("expected DM reason")
	}
	event = buildOutgoingEvent(mustConfig(t), push, &model.Post{Id: "post", UserId: "sender", ChannelId: "channel", RootId: "root"}, api.users["sender"], api.users["recipient"], api.channels["channel"], api.teams["team"])
	if event.Mattermost.NormalizedReason != "thread_reply" {
		t.Fatalf("expected thread reply reason")
	}
}

func TestAdditionalHeadersAndIdempotencyKey(t *testing.T) {
	var auth, idem, header string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		idem = r.Header.Get("Idempotency-Key")
		header = r.Header.Get("X-Test")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	cfg, _ := parseRuntimeConfig(configuration{
		Enabled:                       true,
		ExternalAPIURL:                server.URL,
		AuthorizationType:             "bearer",
		AuthorizationToken:            "secret",
		RequestTimeoutSeconds:         1,
		MaxRetries:                    1,
		InitialRetryDelayMilliseconds: 10,
		MaxRetryDelaySeconds:          1,
		WorkerCount:                   1,
		QueueSize:                     8,
		TLSVerify:                     true,
		AdditionalHeaders:             `{"X-Test":"ok"}`,
	})
	event := outgoingEvent{EventID: "event-1"}
	result := sendEvent(context.Background(), cfg, event)
	if result.err != nil {
		t.Fatalf("sendEvent failed: %v", result.err)
	}
	if auth != "Bearer secret" || idem != "event-1" || header != "ok" {
		t.Fatalf("unexpected headers auth=%q idem=%q header=%q", auth, idem, header)
	}
}

func mustConfig(t *testing.T) *runtimeConfig {
	t.Helper()
	cfg, err := parseRuntimeConfig(configuration{
		Enabled:                       true,
		ExternalAPIURL:                "http://example.invalid",
		RequestTimeoutSeconds:         1,
		MaxRetries:                    1,
		InitialRetryDelayMilliseconds: 10,
		MaxRetryDelaySeconds:          1,
		WorkerCount:                   1,
		QueueSize:                     8,
		TLSVerify:                     true,
		AdditionalHeaders:             "{}",
	})
	if err != nil {
		t.Fatalf("parseRuntimeConfig: %v", err)
	}
	return cfg
}

func requireEventually(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
