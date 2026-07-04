package main

import "github.com/mattermost/mattermost/server/public/model"

type mmAPI interface {
	LoadPluginConfiguration(dest any) error
	GetPost(postID string) (*model.Post, *model.AppError)
	GetUser(userID string) (*model.User, *model.AppError)
	GetChannel(channelID string) (*model.Channel, *model.AppError)
	GetTeam(teamID string) (*model.Team, *model.AppError)
	KVSet(key string, value []byte) *model.AppError
	KVGet(key string) ([]byte, *model.AppError)
	KVDelete(key string) *model.AppError
	KVList(page, perPage int) ([]string, *model.AppError)
	KVCompareAndSet(key string, oldValue, newValue []byte) (bool, *model.AppError)
	LogDebug(msg string, keyValuePairs ...any)
	LogInfo(msg string, keyValuePairs ...any)
	LogWarn(msg string, keyValuePairs ...any)
	LogError(msg string, keyValuePairs ...any)
}
