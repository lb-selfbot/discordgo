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
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/gorilla/websocket"
)

// VERSION of DiscordGo, follows Semantic Versioning. (http://semver.org/)
const VERSION = "0.26.5"

var UserAgentMobile = "Discord-Android/221016;RNA"
var UserAgentEmbedded = "Mozilla/5.0 (PlayStation 5/SmartTV) AppleWebKit/605.1.15 (KHTML, like Gecko)"
var UserAgentDesktop = "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) discord/1.0.9037 Chrome/108.0.5359.215 Electron/22.3.26 Safari/537.36"
var UserAgentWeb = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36"

var IdentifyMobile = Identify{
	Properties: IdentifyPropertiesMobile{
		OS:                "Android",
		Browser:           "Discord Android",
		Device:            "Android",
		SystemLocale:      "en-US",
		ClientVersion:     "221.16 - rn",
		ReleaseChannel:    "googleRelease",
		DeviceVendorID:    "7101a8f5-a3cd-4788-ad14-e6ef5295c6a8",
		BrowserUserAgent:  "",
		BrowserVersion:    "",
		OSVersion:         "31",
		ClientBuildNumber: 22101600160222,
		ClientEventSource: nil,
		DesignID:          2,
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
	UserAgent: UserAgentMobile,
}
var IdentifyEmbedded = Identify{
	Properties: IdentifyPropertiesMobile{
		OS:                "Orbis",
		Browser:           "Discord Embedded",
		Device:            "PlayStation 5",
		SystemLocale:      "en-US",
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
	UserAgent: UserAgentEmbedded,
}

var IdentifyDiscordClient = Identify{
	Properties: IdentifyPropertiesDesktop{
		OS:                "Windows",
		Browser:           "Discord Client",
		ReleaseChannel:    "stable",
		ClientVersion:     "1.0.9037",
		OSVersion:         "10.0.22621",
		OSArch:            "x64",
		AppArch:           "ia32",
		SystemLocale:      "en-US",
		BrowserUserAgent:  UserAgentDesktop,
		BrowserVersion:    "22.3.26",
		ClientBuildNumber: 277953,
		NativeBuildNumber: 45369,
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
	UserAgent: UserAgentDesktop,
}

var IdentifyWeb = Identify{
	Properties: IdentifyPropertiesWeb{
		OS:                     "Windows",
		Browser:                "Chrome",
		Device:                 "",
		SystemLocale:           "en-US",
		BrowserUserAgent:       UserAgentWeb,
		BrowserVersion:         "119.0.0.0",
		OSVersion:              "10",
		Referrer:               "",
		ReferringDomain:        "",
		ReferrerCurrent:        "",
		ReferringDomainCurrent: "",
		ReleaseChannel:         "stable",
		ClientBuildNumber:      277953,
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
	UserAgent: UserAgentWeb,
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
		Token:                       token,
		State:                       NewState(),
		Ratelimiter:                 NewRatelimiter(),
		StateEnabled:                true,
		ShouldSubscribeGuilds:       true,
		MaxGuildSubscriptionMembers: 100,
		Compress:                    true,
		ShouldReconnectOnError:      true,
		ShouldRetryOnRateLimit:      true,
		ShardID:                     0,
		ShardCount:                  1,
		MaxRestRetries:              3,
		Dialer:                      websocket.DefaultDialer,
		UserAgent:                   "",
		sequence:                    new(int64),
		LastHeartbeatAck:            time.Now().UTC(),
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
			"x-discord-locale":   "en-US",
			"x-super-properties": "",
		},
		activeGuildSubscriptions: make(map[string]bool),
	}

	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(20),
		tls_client.WithClientProfile(profiles.Chrome_117),
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
	s.UserAgent = i.UserAgent

	s.SuperProperties = s.GetSuperProperties()

	s.Headers["user-agent"] = s.UserAgent
	s.Headers["x-super-properties"] = s.SuperProperties
}

func (s *Session) ErrorChecker() {
	if err := recover(); err != nil {
		s.log(LogError, "Panic recovered: %s", err)

		if s.ErrorHandler != nil {
			s.ErrorHandler(s, err)
		}
	}
}
