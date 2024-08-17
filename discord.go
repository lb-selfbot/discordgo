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

var UserAgentMobile = "Discord-Android/242020;RNA"
var UserAgentDesktop = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) discord/1.0.9158 Chrome/124.0.6367.243 Electron/30.2.0 Safari/537.36"
var UserAgentWeb = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36"

var IdentifyMobile = Identify{
	Properties: IdentifyPropertiesMobile{
		OS:                "Android",
		Browser:           "Discord Android",
		Device:            "vbox86p",
		SystemLocale:      "en-US",
		ClientVersion:     "242.20 - rn",
		ReleaseChannel:    "googleRelease",
		DeviceVendorID:    "91de7152-0a7e-45f7-9ca9-7b33290b391a",
		BrowserUserAgent:  "",
		BrowserVersion:    "",
		OSVersion:         "33",
		ClientBuildNumber: 242020,
		ClientEventSource: nil,
		DesignID:          2,
	},
	Compress:     true,
	Capabilities: 30719,
	ClientState: ClientState{
		GuildVersions: map[string]string{},
	},
	Presence: GatewayStatusUpdate{
		Since:      0,
		Status:     "unknown",
		Activities: []*Activity{},
		AFK:        true,
	},
	UserAgent: UserAgentMobile,
	Headers:   map[string]string{},
}

var IdentifyEmbedded = Identify{
	Properties: IdentifyPropertiesDesktop{
		OS:                "Windows",
		Browser:           "Discord Embedded",
		ReleaseChannel:    "stable",
		ClientVersion:     "1.0.9158",
		OSVersion:         "10.0.22631",
		OSArch:            "x64",
		AppArch:           "x64",
		SystemLocale:      "en-US",
		BrowserUserAgent:  UserAgentDesktop,
		BrowserVersion:    "30.2.0",
		ClientBuildNumber: 318966,
		NativeBuildNumber: 50841,
		ClientEventSource: nil,
	},
	Compress:     true,
	Capabilities: 30717,
	ClientState: ClientState{
		GuildVersions: map[string]string{},
	},
	Presence: GatewayStatusUpdate{
		Since:      0,
		Status:     "unknown",
		Activities: []*Activity{},
		AFK:        true,
	},
	UserAgent: UserAgentDesktop,
	Headers: map[string]string{
		"Accept":         "*/*",
		"Referer":        "https://discord.com/",
		"Origin":         "https://discord.com",
		"Sec-Fetch-Dest": "empty",
		"Sec-Fetch-Mode": "cors",
		"Sec-Fetch-Site": "same-origin",
	},
}

var IdentifyDiscordClient = Identify{
	Properties: IdentifyPropertiesDesktop{
		OS:                "Windows",
		Browser:           "Discord Client",
		ReleaseChannel:    "stable",
		ClientVersion:     "1.0.9158",
		OSVersion:         "10.0.22631",
		OSArch:            "x64",
		AppArch:           "x64",
		SystemLocale:      "en-US",
		BrowserUserAgent:  UserAgentDesktop,
		BrowserVersion:    "30.2.0",
		ClientBuildNumber: 318966,
		NativeBuildNumber: 50841,
		ClientEventSource: nil,
	},
	Compress:     true,
	Capabilities: 30717,
	ClientState: ClientState{
		GuildVersions: map[string]string{},
	},
	Presence: GatewayStatusUpdate{
		Since:      0,
		Status:     "unknown",
		Activities: []*Activity{},
		AFK:        true,
	},
	UserAgent: UserAgentDesktop,
	Headers: map[string]string{
		"Accept":         "*/*",
		"Referer":        "https://discord.com/",
		"Origin":         "https://discord.com",
		"Sec-Fetch-Dest": "empty",
		"Sec-Fetch-Mode": "cors",
		"Sec-Fetch-Site": "same-origin",
	},
}

var IdentifyWeb = Identify{
	Properties: IdentifyPropertiesWeb{
		OS:                     "Windows",
		Browser:                "Chrome",
		Device:                 "",
		SystemLocale:           "en-US",
		BrowserUserAgent:       UserAgentWeb,
		BrowserVersion:         "127.0.0.0",
		OSVersion:              "10",
		Referrer:               "",
		ReferringDomain:        "",
		ReferrerCurrent:        "",
		ReferringDomainCurrent: "",
		ReleaseChannel:         "stable",
		ClientBuildNumber:      318966,
		ClientEventSource:      nil,
	},
	Compress:     true,
	Capabilities: 30717,
	ClientState: ClientState{
		GuildVersions: map[string]string{},
	},
	Presence: GatewayStatusUpdate{
		Since:      0,
		Status:     "unknown",
		Activities: []*Activity{},
		AFK:        true,
	},
	UserAgent: UserAgentWeb,
	Headers: map[string]string{
		"Accept":         "*/*",
		"Referer":        "https://discord.com/",
		"Origin":         "https://discord.com",
		"Sec-Fetch-Dest": "empty",
		"Sec-Fetch-Mode": "cors",
		"Sec-Fetch-Site": "same-origin",
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
			"Accept-Language":    "en-US",
			"X-Debug-Options":    "bugReporterEnabled",
			"X-Discord-Locale":   "en-US",
			"X-Discord-Timezone": "GMT",
		},
		activeGuildSubscriptions: make(map[string]bool),
	}

	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(20),
		tls_client.WithClientProfile(profiles.Chrome_124),
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

	s.Headers["User-Agent"] = s.UserAgent
	s.Headers["X-Super-Properties"] = s.SuperProperties

	for k, v := range i.Headers {
		s.Headers[k] = v
	}
}

func (s *Session) ErrorChecker() {
	if err := recover(); err != nil {
		s.log(LogError, "Panic recovered: %s", err)

		if s.ErrorHandler != nil {
			s.ErrorHandler(s, err)
		}
	}
}
