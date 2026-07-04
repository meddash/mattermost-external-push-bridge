package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type Plugin struct {
	plugin.MattermostPlugin

	api          mmAPI
	config       atomicRuntimeConfig
	store        eventStore
	dispatcher   *dispatcher
	metrics      *metrics
	userCache    *ttlCache[*model.User]
	channelCache *ttlCache[*model.Channel]
	teamCache    *ttlCache[*model.Team]
	mu           sync.Mutex
}

func (p *Plugin) OnActivate() error {
	if p.api == nil {
		p.api = p.API
	}
	p.metrics = &metrics{}
	p.userCache = newTTLCache[*model.User](defaultCacheTTL)
	p.channelCache = newTTLCache[*model.Channel](defaultCacheTTL)
	p.teamCache = newTTLCache[*model.Team](defaultCacheTTL)

	if err := p.reloadConfiguration(); err != nil {
		return err
	}
	p.store = newKVEventStore(p.api)
	p.startDispatcher()
	return nil
}

func (p *Plugin) OnDeactivate() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dispatcher != nil {
		p.dispatcher.Stop(10 * time.Second)
		p.dispatcher = nil
	}
	return nil
}

func (p *Plugin) OnConfigurationChange() error {
	return p.reloadConfiguration()
}

func (p *Plugin) reloadConfiguration() error {
	var cfg configuration
	if err := p.api.LoadPluginConfiguration(&cfg); err != nil {
		return err
	}
	runtimeCfg, err := parseRuntimeConfig(cfg)
	if err != nil {
		p.api.LogError("Invalid plugin configuration", "error", err.Error())
		return err
	}

	p.config.Store(runtimeCfg)
	if runtimeCfg.TestModeEnabled {
		p.api.LogInfo("External push bridge test mode enabled", "allowed_user_count", len(runtimeCfg.TestUsernameFilter))
	} else {
		p.api.LogInfo("External push bridge test mode disabled")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.store == nil {
		return nil
	}
	if p.dispatcher != nil {
		p.dispatcher.Stop(10 * time.Second)
	}
	p.startDispatcher()
	return nil
}

func (p *Plugin) startDispatcher() {
	cfg := p.getConfig()
	p.dispatcher = newDispatcher(p.api, p.store, p.metrics, cfg)
	p.dispatcher.updateConfigGetter(p.getConfig)
	if err := p.dispatcher.RecoverPending(); err != nil {
		p.api.LogError("Failed to recover pending outbox events", "error", err.Error())
	}
}

func (p *Plugin) getConfig() *runtimeConfig {
	return p.config.Load()
}

func (p *Plugin) NotificationWillBePushed(pushNotification *model.PushNotification, userID string) (*model.PushNotification, string) {
	p.metrics.hookReceived.Add(1)

	cfg := p.getConfig()
	if cfg == nil || !cfg.Enabled || pushNotification == nil {
		return nil, ""
	}
	if pushNotification.Type != model.PushTypeMessage || pushNotification.PostId == "" {
		return nil, ""
	}
	if pushNotification.SubType == model.PushSubTypeCalls {
		return nil, ""
	}

	recipient, err := p.getUser(userID)
	if err != nil || recipient == nil {
		p.api.LogWarn("Failed to load recipient user", "recipient_user_id", userID, "error", errorString(err))
		return nil, ""
	}
	if cfg.TestModeEnabled {
		if _, ok := cfg.TestUsernameFilter[strings.ToLower(recipient.Username)]; !ok {
			p.api.LogDebug("Notification skipped because recipient is not included in TestUsernames", "recipient_user_id", userID)
			return nil, ""
		}
	}

	post, err := p.getPost(pushNotification.PostId)
	if err != nil || post == nil {
		p.api.LogWarn("Failed to load post for push notification", "post_id", pushNotification.PostId, "recipient_user_id", userID, "error", errorString(err))
		return nil, ""
	}
	if post.Type != "" {
		return nil, ""
	}
	if post.UserId == userID {
		return nil, ""
	}

	sender, err := p.getUser(post.UserId)
	if err != nil || sender == nil {
		p.api.LogWarn("Failed to load sender user", "post_id", post.Id, "sender_user_id", post.UserId, "error", errorString(err))
		return nil, ""
	}
	channel, err := p.getChannel(post.ChannelId)
	if err != nil || channel == nil {
		p.api.LogWarn("Failed to load channel", "post_id", post.Id, "channel_id", post.ChannelId, "error", errorString(err))
		return nil, ""
	}
	var team *model.Team
	if channel.TeamId != "" {
		team, _ = p.getTeam(channel.TeamId)
	}

	event := buildOutgoingEvent(cfg, pushNotification, post, sender, recipient, channel, team)
	record := outboxRecord{
		Event:  event,
		Status: eventStatusPending,
	}
	inserted, err := p.store.InsertPending(record)
	if err != nil {
		p.api.LogError("Failed to persist pending outbox record", "event_id", event.EventID, "error", err.Error())
		return nil, ""
	}
	if !inserted {
		p.metrics.hookDeduplicated.Add(1)
		return nil, ""
	}

	if p.dispatcher == nil || !p.dispatcher.Enqueue(event.EventID) {
		p.api.LogWarn("Event queued in durable outbox but not added to in-memory queue", "event_id", event.EventID)
		return nil, ""
	}
	p.metrics.hookEnqueued.Add(1)
	return nil, ""
}

func (p *Plugin) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/health" {
		http.NotFound(w, r)
		return
	}
	cfg := p.getConfig()
	queueDepth := 0
	if p.dispatcher != nil {
		queueDepth = p.dispatcher.QueueDepth()
	}
	snapshot := healthSnapshot{
		Enabled:          cfg != nil && cfg.Enabled,
		TestModeEnabled:  cfg != nil && cfg.TestModeEnabled,
		AllowedUserCount: len(cfg.TestUsernameFilter),
		QueueDepth:       queueDepth,
		WorkerCount:      cfg.WorkerCount,
	}
	if cfg != nil {
		snapshot.EndpointHost = cfg.NormalizedEndpointHost
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (p *Plugin) ServeMetrics(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	queueDepth := 0
	if p.dispatcher != nil {
		queueDepth = p.dispatcher.QueueDepth()
	}
	writeTextResponse(w, http.StatusOK, p.metrics.render(queueDepth))
}

func (p *Plugin) getPost(postID string) (*model.Post, error) {
	post, appErr := p.api.GetPost(postID)
	return post, wrapAppErr(appErr)
}

func (p *Plugin) getUser(userID string) (*model.User, error) {
	if cached, ok := p.userCache.Get(userID); ok {
		return cached, nil
	}
	user, appErr := p.api.GetUser(userID)
	if appErr != nil {
		return nil, wrapAppErr(appErr)
	}
	if user != nil {
		p.userCache.Set(userID, user)
	}
	return user, nil
}

func (p *Plugin) getChannel(channelID string) (*model.Channel, error) {
	if cached, ok := p.channelCache.Get(channelID); ok {
		return cached, nil
	}
	channel, appErr := p.api.GetChannel(channelID)
	if appErr != nil {
		return nil, wrapAppErr(appErr)
	}
	if channel != nil {
		p.channelCache.Set(channelID, channel)
	}
	return channel, nil
}

func (p *Plugin) getTeam(teamID string) (*model.Team, error) {
	if cached, ok := p.teamCache.Get(teamID); ok {
		return cached, nil
	}
	team, appErr := p.api.GetTeam(teamID)
	if appErr != nil {
		return nil, wrapAppErr(appErr)
	}
	if team != nil {
		p.teamCache.Set(teamID, team)
	}
	return team, nil
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
