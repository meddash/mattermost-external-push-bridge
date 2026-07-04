package main

import "time"

const (
	pluginID                = "com.company.external-push-bridge"
	outboxPrefix            = "outbox:"
	outboxIndexPrefix       = "outbox_index:"
	dedupPrefix             = "dedup:"
	maxResponseBodyBytes    = 16 * 1024
	defaultCacheTTL         = 5 * time.Minute
	eventTypeMessageNotify  = "mattermost_message_notification"
	eventStatusPending      = "pending"
	eventStatusProcessing   = "processing"
	eventStatusDelivered    = "delivered"
	eventStatusFailed       = "failed"
	clusterEventRequeueType = "requeue"
)

type outgoingEvent struct {
	EventID    string             `json:"event_id"`
	EventType  string             `json:"event_type"`
	CreatedAt  string             `json:"created_at"`
	Mattermost mattermostEnvelope `json:"mattermost"`
	Recipient  userEnvelope       `json:"recipient"`
	Sender     userEnvelope       `json:"sender"`
	Channel    channelEnvelope    `json:"channel"`
	Post       postEnvelope       `json:"post"`
}

type mattermostEnvelope struct {
	ServerID               string `json:"server_id,omitempty"`
	NotificationType       string `json:"notification_type,omitempty"`
	NotificationSubtype    string `json:"notification_subtype,omitempty"`
	RawNotificationType    string `json:"raw_notification_type,omitempty"`
	RawNotificationSubtype string `json:"raw_notification_subtype,omitempty"`
	NormalizedReason       string `json:"notification_reason,omitempty"`
}

type userEnvelope struct {
	UserID      string `json:"user_id,omitempty"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	IsBot       bool   `json:"is_bot,omitempty"`
}

type channelEnvelope struct {
	ChannelID   string `json:"channel_id,omitempty"`
	ChannelType string `json:"channel_type,omitempty"`
	Name        string `json:"name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	TeamID      string `json:"team_id,omitempty"`
	TeamName    string `json:"team_name,omitempty"`
}

type postEnvelope struct {
	PostID        string   `json:"post_id,omitempty"`
	RootID        string   `json:"root_id,omitempty"`
	IsThreadReply bool     `json:"is_thread_reply"`
	CreateAt      int64    `json:"create_at,omitempty"`
	CreateAtISO   string   `json:"create_at_iso,omitempty"`
	PostType      string   `json:"post_type,omitempty"`
	Message       *string  `json:"message,omitempty"`
	HasFiles      bool     `json:"has_files"`
	FileIDs       []string `json:"file_ids,omitempty"`
}

type outboxRecord struct {
	Event          outgoingEvent `json:"event"`
	Status         string        `json:"status"`
	AttemptCount   int           `json:"attempt_count"`
	NextAttemptAt  int64         `json:"next_attempt_at,omitempty"`
	LastError      string        `json:"last_error,omitempty"`
	LastHTTPStatus int           `json:"last_http_status,omitempty"`
}

type healthSnapshot struct {
	Enabled          bool   `json:"enabled"`
	TestModeEnabled  bool   `json:"test_mode_enabled"`
	AllowedUserCount int    `json:"allowed_user_count"`
	QueueDepth       int    `json:"queue_depth"`
	EndpointHost     string `json:"endpoint_host,omitempty"`
	WorkerCount      int    `json:"worker_count"`
}
