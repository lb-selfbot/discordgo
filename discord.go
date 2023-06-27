// Discordgo - Discord bindings for Go
// Available at https://github.com/LightningDev1/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains high level helper functions and easy entry points for the
// entire discordgo package.  These functions are being developed and are very
// experimental at this point.  They will most likely change so please use the
// low level functions if that's a problem.

// Package discordgo provides Discord binding for Go
package discordgo

import (
	"time"

	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/gorilla/websocket"
)

// VERSION of DiscordGo, follows Semantic Versioning. (http://semver.org/)
const VERSION = "0.26.5"

var UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/111.0.5563.147 Safari/537.36"
var BrowserVersion = "111.0.5563.147"

var IdentifyMobile = Identify{
	Properties: IdentifyProperties{
		OS:                     "Android",
		Browser:                "Discord Android",
		Device:                 "Android",
		SystemLocale:           "en-US",
		BrowserUserAgent:       UserAgent,
		BrowserVersion:         BrowserVersion,
		OSVersion:              "10",
		Referrer:               "https://www.google.com/",
		ReferringDomain:        "google.com",
		ReferrerCurrent:        "",
		ReferringDomainCurrent: "",
		ReleaseChannel:         "stable",
		ClientBuildNumber:      0,
		ClientEventSource:      nil,
	},
	Compress:     true,
	Capabilities: 8189,
	ClientState: ClientState{
		GuildHashes:              map[string]string{},
		HighestLastMessageID:     "0",
		ReadStateVersion:         0,
		UserGuildSettingsVersion: -1,
	},
	Presence: GatewayStatusUpdate{
		Since:      0,
		Status:     "unknown",
		Activities: []*Activity{},
		AFK:        true,
	},
}

var IdentifyDiscordClient = Identify{
	Properties: IdentifyProperties{
		OS:                "Windows",
		Browser:           "Discord Client",
		ReleaseChannel:    "stable",
		ClientVersion:     "1.0.9012",
		OSVersion:         "10.0.22621",
		OSArch:            "x64",
		SystemLocale:      "en-US",
		ClientBuildNumber: 0,
		NativeBuildNumber: 32020,
		ClientEventSource: nil,
	},
	Compress:     true,
	Capabilities: 8189,
	ClientState: ClientState{
		GuildHashes:              map[string]string{},
		HighestLastMessageID:     "0",
		ReadStateVersion:         0,
		UserGuildSettingsVersion: -1,
		UserSettingsVersion:      -1,
		PrivateChannelsVersion:   "0",
		APICodeVersion:           0,
	},
	Presence: GatewayStatusUpdate{
		Since:      0,
		Status:     "unknown",
		Activities: []*Activity{},
		AFK:        true,
	},
}

var IdentifyWeb = Identify{
	Properties: IdentifyProperties{
		OS:                     "Windows",
		Browser:                "Chrome",
		Device:                 "",
		SystemLocale:           "en-US",
		BrowserUserAgent:       UserAgent,
		BrowserVersion:         BrowserVersion,
		OSVersion:              "10",
		Referrer:               "",
		ReferringDomain:        "",
		ReferrerCurrent:        "",
		ReferringDomainCurrent: "",
		ReleaseChannel:         "stable",
		ClientBuildNumber:      0,
		ClientEventSource:      nil,
		DesignID:               0,
	},
	Compress:     true,
	Capabilities: 8189,
	ClientState: ClientState{
		GuildHashes:              map[string]string{},
		HighestLastMessageID:     "0",
		ReadStateVersion:         0,
		UserGuildSettingsVersion: -1,
	},
	Presence: GatewayStatusUpdate{
		Since:      0,
		Status:     "unknown",
		Activities: []*Activity{},
		AFK:        true,
	},
}

// New creates a new Discord session with provided token.
// If the token is for a bot, it must be prefixed with "Bot "
//
//	e.g. "Bot ..."
//
// Or if it is an OAuth2 token, it must be prefixed with "Bearer "
//
//	e.g. "Bearer ..."
func New(token string) (s *Session, err error) {

	// Create an empty Session interface.
	s = &Session{
		Token:                  token,
		State:                  NewState(),
		Ratelimiter:            NewRatelimiter(),
		StateEnabled:           true,
		ShouldSubscribeGuilds:  true,
		Compress:               true,
		ShouldReconnectOnError: true,
		ShouldRetryOnRateLimit: true,
		ShardID:                0,
		ShardCount:             1,
		MaxRestRetries:         3,
		Dialer:                 websocket.DefaultDialer,
		UserAgent:              "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/108.0.0.0 Safari/537.36",
		sequence:               new(int64),
		LastHeartbeatAck:       time.Now().UTC(),
		Headers: map[string]string{
			"accept":             "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,image/apng,*/*;q=0.8",
			"accept-encoding":    "",
			"accept-language":    "en-US,en;q=0.5",
			"referer":            "https://discord.com/",
			"origin":             "https://discord.com",
			"sec-fetch-dest":     "empty",
			"sec-fetch-mode":     "cors",
			"sec-fetch-site":     "same-origin",
			"user-agent":         "",
			"x-debug-options":    "bugReporterEnabled",
			"x-discord-locale":   "",
			"x-super-properties": "",
		},
	}

	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(20),
		tls_client.WithClientProfile(tls_client.Chrome_111),
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
		tls_client.WithRandomTLSExtensionOrder(),
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, err
	}

	s.Client = client

	return
}

func (s *Session) SetIdentify(i Identify) {
	s.Identify = i
	s.Identify.Token = s.Token
	s.Identify.Properties.ClientBuildNumber = s.GetBuildNumber()

	s.SuperProperties = s.GetSuperProperties()

	s.Headers["user-agent"] = s.UserAgent
	s.Headers["x-discord-locale"] = s.Identify.Properties.SystemLocale
	s.Headers["x-super-properties"] = s.SuperProperties
}
