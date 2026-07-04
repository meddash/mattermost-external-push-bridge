package main

import (
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
)

func buildOutgoingEvent(cfg *runtimeConfig, pushNotification *model.PushNotification, post *model.Post, sender *model.User, recipient *model.User, channel *model.Channel, team *model.Team) outgoingEvent {
	serverID := pushNotification.ServerId
	if serverID == "" {
		serverID = "unknown"
	}
	eventID := makeEventID(serverID, post.Id, recipient.Id, pushNotification.Type)
	createdAt := time.UnixMilli(post.CreateAt).UTC()

	event := outgoingEvent{
		EventID:   eventID,
		EventType: eventTypeMessageNotify,
		CreatedAt: createdAt.Format(time.RFC3339Nano),
		Mattermost: mattermostEnvelope{
			ServerID:               serverID,
			NotificationType:       pushNotification.Type,
			NotificationSubtype:    string(pushNotification.SubType),
			RawNotificationType:    pushNotification.Type,
			RawNotificationSubtype: string(pushNotification.SubType),
			NormalizedReason:       normalizeNotificationReason(channel, post),
		},
		Recipient: userEnvelope{
			UserID:      recipient.Id,
			Username:    recipient.Username,
			DisplayName: recipient.GetDisplayName(model.ShowFullName),
		},
		Sender: userEnvelope{
			UserID:      sender.Id,
			Username:    sender.Username,
			DisplayName: sender.GetDisplayName(model.ShowFullName),
			IsBot:       sender.IsBot,
		},
		Channel: channelEnvelope{
			ChannelID:   channel.Id,
			ChannelType: string(channel.Type),
			Name:        channel.Name,
			DisplayName: channel.DisplayName,
			TeamID:      channel.TeamId,
		},
		Post: postEnvelope{
			PostID:        post.Id,
			RootID:        post.RootId,
			IsThreadReply: post.RootId != "",
			CreateAt:      post.CreateAt,
			CreateAtISO:   createdAt.Format(time.RFC3339Nano),
			PostType:      post.Type,
			HasFiles:      len(post.FileIds) > 0,
			FileIDs:       append([]string(nil), post.FileIds...),
		},
	}
	if team != nil {
		event.Channel.TeamName = team.Name
	}
	if cfg.IncludeMessageText {
		message := truncateUnicode(post.Message, cfg.MaxMessageTextLength)
		event.Post.Message = &message
	}
	return event
}

func truncateUnicode(value string, limit int) string {
	if limit <= 0 {
		return value
	}
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

func normalizeNotificationReason(channel *model.Channel, post *model.Post) string {
	if channel == nil {
		return ""
	}
	switch channel.Type {
	case model.ChannelTypeDirect:
		return "direct_message"
	case model.ChannelTypeGroup:
		return "group_message"
	default:
		if post != nil && post.RootId != "" {
			return "thread_reply"
		}
	}
	return ""
}
