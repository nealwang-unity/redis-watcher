package rediswatcher

import (
	rds "github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

type WatcherOptions struct {
	rds.Options
	SubClient              *rds.ClusterClient
	PubClient              *rds.ClusterClient
	Channel                string
	IgnoreSelf             bool
	LocalID                string
	Addresses              []string
	Namespace              string
	MaxConnections         uint
	UseSentinel            bool
	MasterName             string
	OptionalUpdateCallback func(string)
}

func initConfig(option *WatcherOptions) {
	if option.LocalID == "" {
		option.LocalID = uuid.New().String()
	}
	if option.Channel == "" {
		option.Channel = "/casbin"
	}
}
