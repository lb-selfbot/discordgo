// Discordgo - Discord bindings for Go
// Available at https://github.com/lb-selfbot/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains functions for interacting with the Discord REST/JSON API
// at the lowest level.

package discordgo

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // For JPEG decoding
	_ "image/png"  // For PNG decoding
	"io"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-json"

	http "github.com/bogdanfinn/fhttp"
	"github.com/lb-selfbot/discordgo/protos"
	"google.golang.org/protobuf/proto"
)

// All error constants
var (
	ErrJSONUnmarshal           = errors.New("json unmarshal")
	ErrStatusOffline           = errors.New("You can't set your Status to offline")
	ErrVerificationLevelBounds = errors.New("VerificationLevel out of bounds, should be between 0 and 3")
	ErrPruneDaysBounds         = errors.New("the number of days should be more than or equal to 1")
	ErrGuildNoIcon             = errors.New("guild does not have an icon set")
	ErrGuildNoSplash           = errors.New("guild does not have a splash set")
	ErrUnauthorized            = errors.New("HTTP request was unauthorized. This could be because the provided token was not a bot token. Please add \"Bot \" to the start of your token. https://discord.com/developers/docs/reference#authentication-example-bot-token-authorization-header")
)

var (
	// Marshal defines function used to encode JSON payloads
	Marshal func(v any) ([]byte, error) = json.Marshal
	// Unmarshal defines function used to decode JSON payloads
	Unmarshal func(src []byte, v any) error = json.Unmarshal
)

// RESTError stores error information about a request with a bad response code.
// Message is not always present, there are cases where api calls can fail
// without returning a json message.
type RESTError struct {
	Request      *http.Request
	Response     *http.Response
	ResponseBody []byte

	Message *APIErrorMessage // Message may be nil.
}

// newRestError returns a new REST API error.
func newRestError(req *http.Request, resp *http.Response, body []byte) *RESTError {
	restErr := &RESTError{
		Request:      req,
		Response:     resp,
		ResponseBody: body,
	}

	// Attempt to decode the error and assume no message was provided if it fails
	var msg *APIErrorMessage
	err := Unmarshal(body, &msg)
	if err == nil {
		restErr.Message = msg
	}

	return restErr
}

// Error returns a Rest API Error with its status code and body.
func (r RESTError) Error() string {
	return "HTTP " + r.Response.Status + ", " + string(r.ResponseBody)
}

// RateLimitError is returned when a request exceeds a rate limit
// and ShouldRetryOnRateLimit is false. The request may be manually
// retried after waiting the duration specified by RetryAfter.
type RateLimitError struct {
	*RateLimit
}

// Error returns a rate limit error with rate limited endpoint and retry time.
func (e RateLimitError) Error() string {
	return "Rate limit exceeded on " + e.URL + ", retry after " + e.RetryAfter.String()
}

// Request is the same as RequestWithBucketID but the bucket id is the same as the urlStr
func (s *Session) Request(method, urlStr string, data any) (response []byte, err error) {
	return s.RequestWithBucketID(method, urlStr, data, strings.SplitN(urlStr, "?", 2)[0])
}

// RequestWithBucketID makes a (GET/POST/...) Requests to Discord REST API with JSON data.
func (s *Session) RequestWithBucketID(method, urlStr string, data any, bucketID string, headers ...map[string]string) (response []byte, err error) {
	var body []byte
	if data != nil {
		body, err = Marshal(data)
		if err != nil {
			return
		}
	}

	return s.request(method, urlStr, "application/json", body, bucketID, 0, headers...)
}

// request makes a (GET/POST/...) Requests to Discord REST API.
// Sequence is the sequence number, if it fails with a 502 it will
// retry with sequence+1 until it either succeeds or sequence >= session.MaxRestRetries
func (s *Session) request(method, urlStr, contentType string, b []byte, bucketID string, sequence int, headers ...map[string]string) (response []byte, err error) {
	if bucketID == "" {
		bucketID = strings.SplitN(urlStr, "?", 2)[0]
	}
	return s.RequestWithLockedBucket(method, urlStr, contentType, b, s.Ratelimiter.LockBucket(bucketID), sequence, headers...)
}

// RequestWithLockedBucket makes a request using a bucket that's already been locked
func (s *Session) RequestWithLockedBucket(method, urlStr, contentType string, b []byte, bucket *Bucket, sequence int, headers ...map[string]string) (response []byte, err error) {
	if s.Debug {
		log.Printf("API REQUEST %8s :: %s\n", method, urlStr)
		log.Printf("API REQUEST  PAYLOAD :: [%s]\n", string(b))
	}

	req, err := http.NewRequest(method, urlStr, bytes.NewBuffer(b))
	if err != nil {
		bucket.Release(nil)
		return
	}

	// Not used on initial login..
	// TODO: Verify if a login, otherwise complain about no-token
	if s.Token != "" {
		req.Header.Set("authorization", s.Token)
	}

	// Discord's API returns a 400 Bad Request is Content-Type is set, but the
	// request body is empty.
	if b != nil {
		req.Header.Set("Content-Type", contentType)
	}

	// TODO: Make a configurable static variable.
	req.Header.Set("User-Agent", s.UserAgent)

	for k, v := range s.Headers {
		req.Header.Set(k, v)
	}

	for _, header := range headers {
		for k, v := range header {
			req.Header.Set(k, v)
		}
	}

	if s.Debug {
		for k, v := range req.Header {
			log.Printf("API REQUEST   HEADER :: [%s] = %+v\n", k, v)
		}
	}

	resp, err := s.Client.Do(req)
	if err != nil {
		bucket.Release(nil)
		return
	}
	defer func() {
		err2 := resp.Body.Close()
		if s.Debug && err2 != nil {
			log.Println("error closing resp body")
		}
	}()

	err = bucket.Release(resp.Header)
	if err != nil {
		return
	}

	response, err = io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	if s.Debug {

		log.Printf("API RESPONSE  STATUS :: %s\n", resp.Status)
		for k, v := range resp.Header {
			log.Printf("API RESPONSE  HEADER :: [%s] = %+v\n", k, v)
		}
		log.Printf("API RESPONSE    BODY :: [%s]\n\n\n", response)
	}

	if sequence > s.MaxRestRetries {
		err = fmt.Errorf("exceeded max retries HTTP %s, %s", resp.Status, response)
		return
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusCreated:
	case http.StatusNoContent:
	case http.StatusBadGateway:
		// Retry sending request
		s.log(LogInformational, "%s Failed (%s), Retrying...", urlStr, resp.Status)
		response, err = s.RequestWithLockedBucket(method, urlStr, contentType, b, s.Ratelimiter.LockBucketObject(bucket), sequence+1)
	case 429: // TOO MANY REQUESTS - Rate limiting
		rl := TooManyRequests{}
		err = Unmarshal(response, &rl)
		if err != nil {
			s.log(LogError, "rate limit unmarshal error, %s", err)
			return
		}

		if s.ShouldRetryOnRateLimit {
			s.log(LogInformational, "Rate Limiting %s, retry in %v", urlStr, rl.RetryAfter)
			s.handleEvent(rateLimitEventType, &RateLimit{TooManyRequests: &rl, URL: urlStr})

			if rl.RetryAfter == 0 {
				err = fmt.Errorf("possible global rate limit! HTTP %s, %s", resp.Status, response)
				return
			}

			fmt.Println("[RATELIMIT] Rate limit reached on URL:", urlStr)
			fmt.Println("[RATELIMIT] Response:", resp.Status, string(response))
			fmt.Println("[RATELIMIT] Retrying after:", rl.RetryAfter)

			time.Sleep(rl.RetryAfter)
			// we can make the above smarter
			// this method can cause longer delays than required

			response, err = s.RequestWithLockedBucket(method, urlStr, contentType, b, s.Ratelimiter.LockBucketObject(bucket), sequence+1)
		} else {
			err = &RateLimitError{&RateLimit{TooManyRequests: &rl, URL: urlStr}}
		}
	case http.StatusUnauthorized:
		if strings.Index(s.Token, "Bot ") != 0 {
			s.log(LogInformational, ErrUnauthorized.Error())
			err = ErrUnauthorized
		}
		fallthrough
	default: // Error condition
		err = newRestError(req, resp, response)
	}

	return
}

func unmarshal(data []byte, v any) error {
	err := Unmarshal(data, v)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrJSONUnmarshal, err)
	}

	return nil
}

// ------------------------------------------------------------------------------------------------
// Functions specific to Discord Users
// ------------------------------------------------------------------------------------------------

// User returns the user details of the given userID
// userID    : A user ID or "@me" which is a shortcut of current user ID
func (s *Session) User(userID string) (st *User, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointUser(userID), nil, EndpointUsers)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// UserWithFallback returns a User from state if available, otherwise
// it will make a request to the API to get the user.
// userID    : A user ID or "@me" which is a shortcut of current user ID
func (s *Session) UserWithFallback(userID string) (*User, error) {
	user, err := s.State.GetUser(userID)
	if err == nil {
		return user, nil
	}

	return s.User(userID)
}

// UserAvatar is deprecated. Please use UserAvatarDecode
// userID    : A user ID or "@me" which is a shortcut of current user ID
func (s *Session) UserAvatar(userID string) (img image.Image, err error) {
	u, err := s.User(userID)
	if err != nil {
		return
	}
	img, err = s.UserAvatarDecode(u)
	return
}

// UserAvatarDecode returns an image.Image of a user's Avatar
// user : The user which avatar should be retrieved
func (s *Session) UserAvatarDecode(u *User) (img image.Image, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointUserAvatar(u.ID, u.Avatar), nil, EndpointUserAvatar("", ""))
	if err != nil {
		return
	}

	img, _, err = image.Decode(bytes.NewReader(body))
	return
}

// UserUpdate updates current user settings.
func (s *Session) UserUpdate(username, avatar string) (st *User, err error) {
	// NOTE: Avatar must be either the hash/id of existing Avatar or
	// data:image/png;base64,BASE64_STRING_OF_NEW_AVATAR_PNG
	// to set a new avatar.
	// If left blank, avatar will be set to null/blank

	data := struct {
		Username string `json:"username,omitempty"`
		Avatar   string `json:"avatar,omitempty"`
	}{username, avatar}

	body, err := s.RequestWithBucketID("PATCH", EndpointUser("@me"), data, EndpointUsers)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// UserSettingsProtoUpdate updates the user's settings with an encoded protobuf
func (s *Session) UserSettingsProtoUpdate(sType, settings string, reqDataVersion *int) (err error) {
	data := struct {
		Settings            string `json:"settings"`
		RequiredDataVersion *int   `json:"required_data_version,omitempty"`
	}{settings, reqDataVersion}

	_, err = s.RequestWithBucketID("PATCH", EndpointUserSettingsProto(sType), data, EndpointUserSettingsProto(sType))

	return
}

// UserFrecencySettings returns the user's frecency settings
func (s *Session) UserFrecencySettings() (settings *protos.FrecencyUserSettings, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointUserSettingsProto("2"), nil, EndpointUserSettingsProto("2"))
	if err != nil {
		return nil, err
	}

	var response struct {
		Settings string `json:"settings"`
	}

	err = unmarshal(body, &response)
	if err != nil {
		return nil, err
	}

	rawSettings, err := base64.StdEncoding.DecodeString(response.Settings)
	if err != nil {
		return nil, err
	}

	settings = &protos.FrecencyUserSettings{}
	err = proto.Unmarshal(rawSettings, settings)
	if err != nil {
		return nil, err
	}

	return
}

// UserSettingsUpdate updates the user's settings
func (s *Session) UserSettingsUpdate(settings *protos.PreloadedUserSettings, requireVersion bool) (err error) {
	rawSettings, err := proto.Marshal(settings)
	if err != nil {
		return
	}

	encodedSettings := base64.StdEncoding.EncodeToString(rawSettings)

	var dataVersion *int

	if requireVersion {
		settingsDataVersion := int(settings.GetVersions().GetDataVersion())
		dataVersion = &settingsDataVersion
	}

	return s.UserSettingsProtoUpdate("1", encodedSettings, dataVersion)
}

// FrecencySettingsUpdate updates the user's frecency settings
func (s *Session) FrecencySettingsUpdate(settings *protos.FrecencyUserSettings, requireVersion bool) (err error) {
	rawSettings, err := proto.Marshal(settings)
	if err != nil {
		return
	}

	encodedSettings := base64.StdEncoding.EncodeToString(rawSettings)

	var dataVersion *int

	if requireVersion {
		settingsDataVersion := int(settings.GetVersions().GetDataVersion())
		dataVersion = &settingsDataVersion
	}

	return s.UserSettingsProtoUpdate("2", encodedSettings, dataVersion)
}

// UserConnections returns the user's connections
func (s *Session) UserConnections() (conn []*UserConnection, err error) {
	response, err := s.RequestWithBucketID("GET", EndpointUserConnections("@me"), nil, EndpointUserConnections("@me"))
	if err != nil {
		return nil, err
	}

	err = unmarshal(response, &conn)
	if err != nil {
		return
	}

	return
}

// UserMutualFriends returns the client's mutual friends with the given user
func (s *Session) UserMutualFriends(userID string) (users []*User, err error) {
	response, err := s.RequestWithBucketID("GET", EndpointUserRelationships(userID), nil, EndpointUserConnections(""))
	if err != nil {
		return nil, err
	}

	err = unmarshal(response, &users)
	if err != nil {
		return
	}

	return
}

// UserProfile returns the user's profile
func (s *Session) UserProfile(userID string) (st *Profile, err error) {
	response, err := s.RequestWithBucketID("GET", EndpointUserProfile(userID)+"?with_mutual_guilds=true", nil, EndpointUserProfile(""))
	if err != nil {
		return nil, err
	}

	err = unmarshal(response, &st)
	if err != nil {
		return
	}

	return
}

// UserChannelCreate creates a new User (Private) Channel with another User
// recipientID : A user ID for the user to which this channel is opened with.
func (s *Session) UserChannelCreate(recipientID string) (st *Channel, err error) {
	data := struct {
		RecipientID string `json:"recipient_id"`
	}{recipientID}

	body, err := s.RequestWithBucketID("POST", EndpointUserChannels("@me"), data, EndpointUserChannels(""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// UserGuildMember returns a guild member object for the current user in the given Guild.
// guildID : ID of the guild
func (s *Session) UserGuildMember(guildID string) (st *Member, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointUserGuildMember("@me", guildID), nil, EndpointUserGuildMember("@me", guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// UserGuilds returns an array of UserGuild structures for all guilds.
// limit     : The number guilds that can be returned. (max 100)
// beforeID  : If provided all guilds returned will be before given ID.
// afterID   : If provided all guilds returned will be after given ID.
func (s *Session) UserGuilds(limit int, beforeID, afterID string) (st []*UserGuild, err error) {
	v := url.Values{}

	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if afterID != "" {
		v.Set("after", afterID)
	}
	if beforeID != "" {
		v.Set("before", beforeID)
	}

	uri := EndpointUserGuilds("@me")

	if len(v) > 0 {
		uri += "?" + v.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointUserGuilds(""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// UserChannelPermissions returns the permission of a user in a channel.
// userID    : The ID of the user to calculate permissions for.
// channelID : The ID of the channel to calculate permission for.
//
// NOTE: This function is now deprecated and will be removed in the future.
// Please see the same function inside state.go
func (s *Session) UserChannelPermissions(userID, channelID string) (apermissions int64, err error) {
	// Try to just get permissions from state.
	apermissions, err = s.State.UserChannelPermissions(userID, channelID)
	if err == nil {
		return
	}

	// Otherwise try get as much data from state as possible, falling back to the network.
	channel, err := s.ChannelWithFallback(channelID)
	if err != nil {
		return
	}

	guild, err := s.GuildWithFallback(channel.GuildID)
	if err != nil {
		return
	}

	if userID == guild.OwnerID {
		apermissions = PermissionAll
		return
	}

	member, err := s.State.Member(guild.ID, userID)
	if err != nil || member == nil {
		member, err = s.GuildMember(guild.ID, userID)
		if err != nil {
			return
		}
	}

	return memberPermissions(guild, channel, userID, member.Roles), nil
}

// Calculates the permissions for a member.
// https://support.discord.com/hc/en-us/articles/206141927-How-is-the-permission-hierarchy-structured-
func memberPermissions(guild *Guild, channel *Channel, userID string, roles []string) (apermissions int64) {
	if userID == guild.OwnerID {
		apermissions = PermissionAll
		return
	}

	for _, role := range guild.Roles {
		if role.ID == guild.ID {
			apermissions |= role.Permissions
			break
		}
	}

	for _, role := range guild.Roles {
		for _, roleID := range roles {
			if role.ID == roleID {
				apermissions |= role.Permissions
				break
			}
		}
	}

	if apermissions&PermissionAdministrator == PermissionAdministrator {
		apermissions |= PermissionAll
	}

	// Apply @everyone overrides from the channel.
	for _, overwrite := range channel.PermissionOverwrites {
		if guild.ID == overwrite.ID {
			apermissions &= ^overwrite.Deny
			apermissions |= overwrite.Allow
			break
		}
	}

	var denies, allows int64
	// Member overwrites can override role overrides, so do two passes
	for _, overwrite := range channel.PermissionOverwrites {
		for _, roleID := range roles {
			if overwrite.Type == PermissionOverwriteTypeRole && roleID == overwrite.ID {
				denies |= overwrite.Deny
				allows |= overwrite.Allow
				break
			}
		}
	}

	apermissions &= ^denies
	apermissions |= allows

	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.Type == PermissionOverwriteTypeMember && overwrite.ID == userID {
			apermissions &= ^overwrite.Deny
			apermissions |= overwrite.Allow
			break
		}
	}

	if apermissions&PermissionAdministrator == PermissionAdministrator {
		apermissions |= PermissionAllChannel
	}

	return apermissions
}

func (s *Session) UserRelationships() (st []*Relationship, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointUserRelationships("@me"), nil, EndpointUserRelationships(""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

func (s *Session) UserRelationshipDelete(userID string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointUserRelationship(userID), nil, EndpointUserRelationships(""), map[string]string{
		"x-context-properties": "eyJsb2NhdGlvbiI6IkNvbnRleHRNZW51In0=",
	})
	return
}

func (s *Session) UserRelationshipCreate(userID string, relType int) (err error) {
	data := map[string]int{}

	if relType != 1 {
		data["type"] = relType
	}

	_, err = s.RequestWithBucketID("PUT", EndpointUserRelationship(userID), data, EndpointUserRelationships(""), map[string]string{
		"x-context-properties": "eyJsb2NhdGlvbiI6IkNvbnRleHRNZW51In0=",
	})
	return
}

func (s *Session) UserSendFriendRequest(username string, discriminator int) (err error) {
	data := map[string]any{
		"username":      username,
		"discriminator": nil,
	}

	if discriminator != 0 {
		data["discriminator"] = discriminator
	}

	_, err = s.RequestWithBucketID("POST", EndpointUserRelationships("@me"), data, EndpointUserRelationships(""), map[string]string{
		"x-context-properties": "eyJsb2NhdGlvbiI6IkFkZCBGcmllbmQifQ==",
	})
	return
}

// ------------------------------------------------------------------------------------------------
// Functions specific to Discord Guilds
// ------------------------------------------------------------------------------------------------

// Guild returns a Guild structure of a specific Guild.
// guildID   : The ID of a Guild
func (s *Session) Guild(guildID string) (st *Guild, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuild(guildID), nil, EndpointGuild(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildWithFallback returns a Guild from state if available, otherwise
// it will make a request to the API to get the guild.
// guildID  :  The ID of a Guild
func (s *Session) GuildWithFallback(guildID string) (*Guild, error) {
	guild, err := s.State.Guild(guildID)
	if err == nil {
		return guild, nil
	}

	return s.Guild(guildID)
}

// GuildWithCounts returns a Guild structure of a specific Guild with approximate member and presence counts.
// guildID    : The ID of a Guild
func (s *Session) GuildWithCounts(guildID string) (st *Guild, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuild(guildID)+"?with_counts=true", nil, EndpointGuild(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildPreview returns a GuildPreview structure of a specific public Guild.
// guildID   : The ID of a Guild
func (s *Session) GuildPreview(guildID string) (st *GuildPreview, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildPreview(guildID), nil, EndpointGuildPreview(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildCreate creates a new Guild
// name      : A name for the Guild (2-100 characters)
func (s *Session) GuildCreate(name string) (st *Guild, err error) {
	data := struct {
		Name string `json:"name"`
	}{name}

	body, err := s.RequestWithBucketID("POST", EndpointGuildCreate, data, EndpointGuildCreate)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildEdit edits a new Guild
// guildID   : The ID of a Guild
// g 		 : A GuildParams struct with the values Name, Region and VerificationLevel defined.
func (s *Session) GuildEdit(guildID string, g *GuildParams) (st *Guild, err error) {
	// Bounds checking for VerificationLevel, interval: [0, 4]
	if g.VerificationLevel != nil {
		val := *g.VerificationLevel
		if val < 0 || val > 4 {
			err = ErrVerificationLevelBounds
			return
		}
	}

	// Bounds checking for regions
	if g.Region != "" {
		isValid := false
		regions, _ := s.VoiceRegions()
		for _, r := range regions {
			if g.Region == r.ID {
				isValid = true
			}
		}
		if !isValid {
			var valid []string
			for _, r := range regions {
				valid = append(valid, r.ID)
			}
			err = fmt.Errorf("region not a valid region (%q)", valid)
			return
		}
	}

	body, err := s.RequestWithBucketID("PATCH", EndpointGuild(guildID), g, EndpointGuild(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildDelete deletes a Guild.
// guildID   : The ID of a Guild
func (s *Session) GuildDelete(guildID string) (st *Guild, err error) {
	body, err := s.RequestWithBucketID("DELETE", EndpointGuild(guildID), nil, EndpointGuild(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildLeave leaves a Guild.
// guildID   : The ID of a Guild
func (s *Session) GuildLeave(guildID string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointUserGuild("@me", guildID), nil, EndpointUserGuild("", guildID))
	return
}

// GuildBans returns an array of GuildBan structures for bans in the given guild.
//
//	guildID   : The ID of a Guild
//	limit     : Max number of bans to return (max 1000)
//	beforeID  : If not empty all returned users will be after the given id
//	afterID   : If not empty all returned users will be before the given id
func (s *Session) GuildBans(guildID string, limit int, beforeID, afterID string) (st []*GuildBan, err error) {
	uri := EndpointGuildBans(guildID)

	v := url.Values{}
	if limit != 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if beforeID != "" {
		v.Set("before", beforeID)
	}
	if afterID != "" {
		v.Set("after", afterID)
	}

	if len(v) > 0 {
		uri += "?" + v.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointGuildBans(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildBanCreate bans the given user from the given guild.
// guildID   : The ID of a Guild.
// userID    : The ID of a User
// days      : The number of days of previous comments to delete.
func (s *Session) GuildBanCreate(guildID, userID string, days int) (err error) {
	return s.GuildBanCreateWithReason(guildID, userID, "", days)
}

// GuildBan finds ban by given guild and user id and returns GuildBan structure
func (s *Session) GuildBan(guildID, userID string) (st *GuildBan, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildBan(guildID, userID), nil, EndpointGuildBan(guildID, userID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildBanCreateWithReason bans the given user from the given guild also providing a reaso.
// guildID   : The ID of a Guild.
// userID    : The ID of a User
// reason    : The reason for this ban
// days      : The number of days of previous comments to delete.
func (s *Session) GuildBanCreateWithReason(guildID, userID, reason string, days int) (err error) {
	uri := EndpointGuildBan(guildID, userID)

	queryParams := url.Values{}
	if days > 0 {
		queryParams.Set("delete_message_days", strconv.Itoa(days))
	}
	if reason != "" {
		queryParams.Set("reason", reason)
	}

	if len(queryParams) > 0 {
		uri += "?" + queryParams.Encode()
	}

	_, err = s.RequestWithBucketID("PUT", uri, nil, EndpointGuildBan(guildID, ""))
	return
}

// GuildBanDelete removes the given user from the guild bans
// guildID   : The ID of a Guild.
// userID    : The ID of a User
func (s *Session) GuildBanDelete(guildID, userID string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointGuildBan(guildID, userID), nil, EndpointGuildBan(guildID, ""))
	return
}

// GuildMembers returns a list of members for a guild.
//
//	guildID  : The ID of a Guild.
//	after    : The id of the member to return members after
//	limit    : max number of members to return (max 1000)
func (s *Session) GuildMembers(guildID string, after string, limit int) (st []*Member, err error) {
	uri := EndpointGuildMembers(guildID)

	v := url.Values{}

	if after != "" {
		v.Set("after", after)
	}

	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}

	if len(v) > 0 {
		uri += "?" + v.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointGuildMembers(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildMembersSearch returns a list of guild member objects whose username or nickname starts with a provided string
// guildID  : The ID of a Guild
// query    : Query string to match username(s) and nickname(s) against
// limit    : Max number of members to return (default 1, min 1, max 1000)
func (s *Session) GuildMembersSearch(guildID, query string, limit int) (st []*Member, err error) {
	uri := EndpointGuildMembersSearch(guildID)

	queryParams := url.Values{}
	queryParams.Set("query", query)
	if limit > 1 {
		queryParams.Set("limit", strconv.Itoa(limit))
	}

	body, err := s.RequestWithBucketID("GET", uri+"?"+queryParams.Encode(), nil, uri)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildMember returns a member of a guild.
//
//	guildID   : The ID of a Guild.
//	userID    : The ID of a User
func (s *Session) GuildMember(guildID, userID string) (st *Member, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildMember(guildID, userID), nil, EndpointGuildMember(guildID, ""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	// The returned object doesn't have the GuildID attribute so we will set it here.
	st.GuildID = guildID
	return
}

// GuildMemberAdd force joins a user to the guild.
//
//	guildID       : The ID of a Guild.
//	userID        : The ID of a User.
//	data          : Parameters of the user to add.
func (s *Session) GuildMemberAdd(guildID, userID string, data *GuildMemberAddParams) (err error) {
	_, err = s.RequestWithBucketID("PUT", EndpointGuildMember(guildID, userID), data, EndpointGuildMember(guildID, ""))
	if err != nil {
		return err
	}

	return err
}

// GuildMemberDelete removes the given user from the given guild.
// guildID   : The ID of a Guild.
// userID    : The ID of a User
func (s *Session) GuildMemberDelete(guildID, userID string) (err error) {
	return s.GuildMemberDeleteWithReason(guildID, userID, "")
}

// GuildMemberDeleteWithReason removes the given user from the given guild.
// guildID   : The ID of a Guild.
// userID    : The ID of a User
// reason    : The reason for the kick
func (s *Session) GuildMemberDeleteWithReason(guildID, userID, reason string) (err error) {
	uri := EndpointGuildMember(guildID, userID)
	if reason != "" {
		uri += "?reason=" + url.QueryEscape(reason)
	}

	_, err = s.RequestWithBucketID("DELETE", uri, nil, EndpointGuildMember(guildID, ""))
	return
}

// GuildMemberEdit edits and returns updated member.
// guildID  : The ID of a Guild.
// userID   : The ID of a User.
// data     : Updated GuildMember data.
func (s *Session) GuildMemberEdit(guildID, userID string, data *GuildMemberParams) (st *Member, err error) {
	var body []byte
	body, err = s.RequestWithBucketID("PATCH", EndpointGuildMember(guildID, userID), data, EndpointGuildMember(guildID, ""))
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &st)
	return
}

// GuildMemberEditComplex edits the nickname and roles of a member.
// NOTE: deprecated, use GuildMemberEdit instead.
// guildID  : The ID of a Guild.
// userID   : The ID of a User.
// data     : A GuildMemberEditData struct with the new nickname and roles
func (s *Session) GuildMemberEditComplex(guildID, userID string, data *GuildMemberParams) (st *Member, err error) {
	return s.GuildMemberEdit(guildID, userID, data)
}

// GuildMemberMove moves a guild member from one voice channel to another/none
//
//	guildID   : The ID of a Guild.
//	userID    : The ID of a User.
//	channelID : The ID of a channel to move user to or nil to remove from voice channel
//
// NOTE : I am not entirely set on the name of this function and it may change
// prior to the final 1.0.0 release of Discordgo
func (s *Session) GuildMemberMove(guildID string, userID string, channelID *string) (err error) {
	data := struct {
		ChannelID *string `json:"channel_id"`
	}{channelID}

	_, err = s.RequestWithBucketID("PATCH", EndpointGuildMember(guildID, userID), data, EndpointGuildMember(guildID, ""))
	return
}

// GuildMemberNickname updates the nickname of a guild member
// guildID   : The ID of a guild
// userID    : The ID of a user
// userID    : The ID of a user or "@me" which is a shortcut of the current user ID
// nickname  : The nickname of the member, "" will reset their nickname
func (s *Session) GuildMemberNickname(guildID, userID, nickname string) (err error) {
	data := struct {
		Nick string `json:"nick"`
	}{nickname}

	if userID == "@me" {
		userID += "/nick"
	}

	_, err = s.RequestWithBucketID("PATCH", EndpointGuildMember(guildID, userID), data, EndpointGuildMember(guildID, ""))
	return
}

// GuildMemberMute server mutes a guild member
//
//	guildID   : The ID of a Guild.
//	userID    : The ID of a User.
//	mute    : boolean value for if the user should be muted
func (s *Session) GuildMemberMute(guildID string, userID string, mute bool) (err error) {
	data := struct {
		Mute bool `json:"mute"`
	}{mute}

	_, err = s.RequestWithBucketID("PATCH", EndpointGuildMember(guildID, userID), data, EndpointGuildMember(guildID, ""))
	return
}

// GuildMemberTimeout times out a guild member
//
//	guildID   : The ID of a Guild.
//	userID    : The ID of a User.
//	until     : The timestamp for how long a member should be timed out.
//	            Set to nil to remove timeout.
func (s *Session) GuildMemberTimeout(guildID string, userID string, until *time.Time) (err error) {
	data := struct {
		CommunicationDisabledUntil *time.Time `json:"communication_disabled_until"`
	}{until}

	_, err = s.RequestWithBucketID("PATCH", EndpointGuildMember(guildID, userID), data, EndpointGuildMember(guildID, ""))
	return
}

// GuildMemberDeafen server deafens a guild member
//
//	guildID   : The ID of a Guild.
//	userID    : The ID of a User.
//	deaf    : boolean value for if the user should be deafened
func (s *Session) GuildMemberDeafen(guildID string, userID string, deaf bool) (err error) {
	data := struct {
		Deaf bool `json:"deaf"`
	}{deaf}

	_, err = s.RequestWithBucketID("PATCH", EndpointGuildMember(guildID, userID), data, EndpointGuildMember(guildID, ""))
	return
}

// GuildMemberRoleAdd adds the specified role to a given member
//
//	guildID   : The ID of a Guild.
//	userID    : The ID of a User.
//	roleID 	  : The ID of a Role to be assigned to the user.
func (s *Session) GuildMemberRoleAdd(guildID, userID, roleID string) (err error) {
	_, err = s.RequestWithBucketID("PUT", EndpointGuildMemberRole(guildID, userID, roleID), nil, EndpointGuildMemberRole(guildID, "", ""))

	return
}

// GuildMemberRoleRemove removes the specified role to a given member
//
//	guildID   : The ID of a Guild.
//	userID    : The ID of a User.
//	roleID 	  : The ID of a Role to be removed from the user.
func (s *Session) GuildMemberRoleRemove(guildID, userID, roleID string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointGuildMemberRole(guildID, userID, roleID), nil, EndpointGuildMemberRole(guildID, "", ""))

	return
}

// GuildChannels returns an array of Channel structures for all channels of a
// given guild.
// guildID   : The ID of a Guild.
func (s *Session) GuildChannels(guildID string) (st []*Channel, err error) {
	body, err := s.request("GET", EndpointGuildChannels(guildID), "", nil, EndpointGuildChannels(guildID), 0)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildChannelCreateData is provided to GuildChannelCreateComplex
type GuildChannelCreateData struct {
	Name                 string                 `json:"name"`
	Type                 ChannelType            `json:"type"`
	Topic                string                 `json:"topic,omitempty"`
	Bitrate              int                    `json:"bitrate,omitempty"`
	UserLimit            int                    `json:"user_limit,omitempty"`
	RateLimitPerUser     int                    `json:"rate_limit_per_user,omitempty"`
	Position             int                    `json:"position,omitempty"`
	PermissionOverwrites []*PermissionOverwrite `json:"permission_overwrites,omitempty"`
	ParentID             string                 `json:"parent_id,omitempty"`
	NSFW                 bool                   `json:"nsfw,omitempty"`
}

// GuildChannelCreateComplex creates a new channel in the given guild
// guildID      : The ID of a Guild
// data         : A data struct describing the new Channel, Name and Type are mandatory, other fields depending on the type
func (s *Session) GuildChannelCreateComplex(guildID string, data GuildChannelCreateData) (st *Channel, err error) {
	body, err := s.RequestWithBucketID("POST", EndpointGuildChannels(guildID), data, EndpointGuildChannels(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildChannelCreate creates a new channel in the given guild
// guildID   : The ID of a Guild.
// name      : Name of the channel (2-100 chars length)
// ctype     : Type of the channel
func (s *Session) GuildChannelCreate(guildID, name string, ctype ChannelType) (st *Channel, err error) {
	return s.GuildChannelCreateComplex(guildID, GuildChannelCreateData{
		Name: name,
		Type: ctype,
	})
}

// GuildChannelsReorder updates the order of channels in a guild
// guildID   : The ID of a Guild.
// channels  : Updated channels.
func (s *Session) GuildChannelsReorder(guildID string, channels []*Channel) (err error) {
	data := make([]struct {
		ID       string `json:"id"`
		Position int    `json:"position"`
	}, len(channels))

	for i, c := range channels {
		data[i].ID = c.ID
		data[i].Position = c.Position
	}

	_, err = s.RequestWithBucketID("PATCH", EndpointGuildChannels(guildID), data, EndpointGuildChannels(guildID))
	return
}

// GuildInvites returns an array of Invite structures for the given guild
// guildID   : The ID of a Guild.
func (s *Session) GuildInvites(guildID string) (st []*Invite, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildInvites(guildID), nil, EndpointGuildInvites(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildRoles returns all roles for a given guild.
// guildID   : The ID of a Guild.
func (s *Session) GuildRoles(guildID string) (st []*Role, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildRoles(guildID), nil, EndpointGuildRoles(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return // TODO return pointer
}

// GuildRoleCreate creates a new Guild Role and returns it.
// guildID : The ID of a Guild.
// data    : New Role parameters.
func (s *Session) GuildRoleCreate(guildID string, data *RoleParams) (st *Role, err error) {
	body, err := s.RequestWithBucketID("POST", EndpointGuildRoles(guildID), data, EndpointGuildRoles(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildRoleEdit updates an existing Guild Role and returns updated Role data.
// guildID   : The ID of a Guild.
// roleID    : The ID of a Role.
// data 		 : Updated Role data.
func (s *Session) GuildRoleEdit(guildID, roleID string, data *RoleParams) (st *Role, err error) {
	// Prevent sending a color int that is too big.
	if data.Color != nil && *data.Color > 0xFFFFFF {
		return nil, fmt.Errorf("color value cannot be larger than 0xFFFFFF")
	}

	body, err := s.RequestWithBucketID("PATCH", EndpointGuildRole(guildID, roleID), data, EndpointGuildRole(guildID, ""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildRoleReorder reoders guild roles
// guildID   : The ID of a Guild.
// roles     : A list of ordered roles.
func (s *Session) GuildRoleReorder(guildID string, roles []*Role) (st []*Role, err error) {
	body, err := s.RequestWithBucketID("PATCH", EndpointGuildRoles(guildID), roles, EndpointGuildRoles(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildRoleDelete deletes an existing role.
// guildID   : The ID of a Guild.
// roleID    : The ID of a Role.
func (s *Session) GuildRoleDelete(guildID, roleID string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointGuildRole(guildID, roleID), nil, EndpointGuildRole(guildID, ""))

	return
}

// GuildPruneCount Returns the number of members that would be removed in a prune operation.
// Requires 'KICK_MEMBER' permission.
// guildID	: The ID of a Guild.
// days		: The number of days to count prune for (1 or more).
func (s *Session) GuildPruneCount(guildID string, days uint32) (count uint32, err error) {
	count = 0

	if days <= 0 {
		err = ErrPruneDaysBounds
		return
	}

	p := struct {
		Pruned uint32 `json:"pruned"`
	}{}

	uri := EndpointGuildPrune(guildID) + "?days=" + strconv.FormatUint(uint64(days), 10)
	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointGuildPrune(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &p)
	if err != nil {
		return
	}

	count = p.Pruned

	return
}

// GuildPrune Begin as prune operation. Requires the 'KICK_MEMBERS' permission.
// Returns an object with one 'pruned' key indicating the number of members that were removed in the prune operation.
// guildID	: The ID of a Guild.
// days		: The number of days to count prune for (1 or more).
func (s *Session) GuildPrune(guildID string, days uint32) (count uint32, err error) {
	count = 0

	if days <= 0 {
		err = ErrPruneDaysBounds
		return
	}

	data := struct {
		days uint32
	}{days}

	p := struct {
		Pruned uint32 `json:"pruned"`
	}{}

	body, err := s.RequestWithBucketID("POST", EndpointGuildPrune(guildID), data, EndpointGuildPrune(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &p)
	if err != nil {
		return
	}

	count = p.Pruned

	return
}

// GuildIntegrations returns an array of Integrations for a guild.
// guildID   : The ID of a Guild.
func (s *Session) GuildIntegrations(guildID string) (st []*Integration, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildIntegrations(guildID), nil, EndpointGuildIntegrations(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildIntegrationCreate creates a Guild Integration.
// guildID          : The ID of a Guild.
// integrationType  : The Integration type.
// integrationID    : The ID of an integration.
func (s *Session) GuildIntegrationCreate(guildID, integrationType, integrationID string) (err error) {
	data := struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}{integrationType, integrationID}

	_, err = s.RequestWithBucketID("POST", EndpointGuildIntegrations(guildID), data, EndpointGuildIntegrations(guildID))
	return
}

// GuildIntegrationEdit edits a Guild Integration.
// guildID              : The ID of a Guild.
// integrationType      : The Integration type.
// integrationID        : The ID of an integration.
// expireBehavior	      : The behavior when an integration subscription lapses (see the integration object documentation).
// expireGracePeriod    : Period (in seconds) where the integration will ignore lapsed subscriptions.
// enableEmoticons	    : Whether emoticons should be synced for this integration (twitch only currently).
func (s *Session) GuildIntegrationEdit(guildID, integrationID string, expireBehavior, expireGracePeriod int, enableEmoticons bool) (err error) {
	data := struct {
		ExpireBehavior    int  `json:"expire_behavior"`
		ExpireGracePeriod int  `json:"expire_grace_period"`
		EnableEmoticons   bool `json:"enable_emoticons"`
	}{expireBehavior, expireGracePeriod, enableEmoticons}

	_, err = s.RequestWithBucketID("PATCH", EndpointGuildIntegration(guildID, integrationID), data, EndpointGuildIntegration(guildID, ""))
	return
}

// GuildIntegrationDelete removes the given integration from the Guild.
// guildID          : The ID of a Guild.
// integrationID    : The ID of an integration.
func (s *Session) GuildIntegrationDelete(guildID, integrationID string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointGuildIntegration(guildID, integrationID), nil, EndpointGuildIntegration(guildID, ""))
	return
}

// GuildIcon returns an image.Image of a guild icon.
// guildID   : The ID of a Guild.
func (s *Session) GuildIcon(guildID string) (img image.Image, err error) {
	g, err := s.Guild(guildID)
	if err != nil {
		return
	}

	if g.Icon == "" {
		err = ErrGuildNoIcon
		return
	}

	body, err := s.RequestWithBucketID("GET", EndpointGuildIcon(guildID, g.Icon), nil, EndpointGuildIcon(guildID, ""))
	if err != nil {
		return
	}

	img, _, err = image.Decode(bytes.NewReader(body))
	return
}

// GuildSplash returns an image.Image of a guild splash image.
// guildID   : The ID of a Guild.
func (s *Session) GuildSplash(guildID string) (img image.Image, err error) {
	g, err := s.Guild(guildID)
	if err != nil {
		return
	}

	if g.Splash == "" {
		err = ErrGuildNoSplash
		return
	}

	body, err := s.RequestWithBucketID("GET", EndpointGuildSplash(guildID, g.Splash), nil, EndpointGuildSplash(guildID, ""))
	if err != nil {
		return
	}

	img, _, err = image.Decode(bytes.NewReader(body))
	return
}

// GuildEmbed returns the embed for a Guild.
// guildID   : The ID of a Guild.
func (s *Session) GuildEmbed(guildID string) (st *GuildEmbed, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildEmbed(guildID), nil, EndpointGuildEmbed(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildEmbedEdit edits the embed of a Guild.
// guildID   : The ID of a Guild.
// data      : New GuildEmbed data.
func (s *Session) GuildEmbedEdit(guildID string, data *GuildEmbed) (err error) {
	_, err = s.RequestWithBucketID("PATCH", EndpointGuildEmbed(guildID), data, EndpointGuildEmbed(guildID))
	return
}

// GuildAuditLog returns the audit log for a Guild.
// guildID     : The ID of a Guild.
// userID      : If provided the log will be filtered for the given ID.
// beforeID    : If provided all log entries returned will be before the given ID.
// actionType  : If provided the log will be filtered for the given Action Type.
// limit       : The number messages that can be returned. (default 50, min 1, max 100)
func (s *Session) GuildAuditLog(guildID, userID, beforeID string, actionType, limit int) (st *GuildAuditLog, err error) {
	uri := EndpointGuildAuditLogs(guildID)

	v := url.Values{}
	if userID != "" {
		v.Set("user_id", userID)
	}
	if beforeID != "" {
		v.Set("before", beforeID)
	}
	if actionType > 0 {
		v.Set("action_type", strconv.Itoa(actionType))
	}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if len(v) > 0 {
		uri = fmt.Sprintf("%s?%s", uri, v.Encode())
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointGuildAuditLogs(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildEmojis returns all emoji
// guildID : The ID of a Guild.
func (s *Session) GuildEmojis(guildID string) (emoji []*Emoji, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildEmojis(guildID), nil, EndpointGuildEmojis(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &emoji)
	return
}

// GuildEmoji returns specified emoji.
// guildID : The ID of a Guild
// emojiID : The ID of an Emoji to retrieve
func (s *Session) GuildEmoji(guildID, emojiID string) (emoji *Emoji, err error) {
	var body []byte
	body, err = s.RequestWithBucketID("GET", EndpointGuildEmoji(guildID, emojiID), nil, EndpointGuildEmoji(guildID, emojiID))
	if err != nil {
		return
	}

	err = unmarshal(body, &emoji)
	return
}

// GuildEmojiCreate creates a new Emoji.
// guildID : The ID of a Guild.
// data    : New Emoji data.
func (s *Session) GuildEmojiCreate(guildID string, data *EmojiParams) (emoji *Emoji, err error) {
	body, err := s.RequestWithBucketID("POST", EndpointGuildEmojis(guildID), data, EndpointGuildEmojis(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &emoji)
	return
}

// GuildEmojiEdit modifies and returns updated Emoji.
// guildID : The ID of a Guild.
// emojiID : The ID of an Emoji.
// data    : Updated Emoji data.
func (s *Session) GuildEmojiEdit(guildID, emojiID string, data *EmojiParams) (emoji *Emoji, err error) {
	body, err := s.RequestWithBucketID("PATCH", EndpointGuildEmoji(guildID, emojiID), data, EndpointGuildEmojis(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &emoji)
	return
}

// GuildEmojiDelete deletes an Emoji.
// guildID : The ID of a Guild.
// emojiID : The ID of an Emoji.
func (s *Session) GuildEmojiDelete(guildID, emojiID string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointGuildEmoji(guildID, emojiID), nil, EndpointGuildEmojis(guildID))
	return
}

// GuildTemplate returns a GuildTemplate for the given code
// templateCode: The Code of a GuildTemplate
func (s *Session) GuildTemplate(templateCode string) (st *GuildTemplate, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildTemplate(templateCode), nil, EndpointGuildTemplate(templateCode))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildCreateWithTemplate creates a guild based on a GuildTemplate
// templateCode: The Code of a GuildTemplate
// name: The name of the guild (2-100) characters
// icon: base64 encoded 128x128 image for the guild icon
func (s *Session) GuildCreateWithTemplate(templateCode, name, icon string) (st *Guild, err error) {
	data := struct {
		Name string `json:"name"`
		Icon string `json:"icon"`
	}{name, icon}

	body, err := s.RequestWithBucketID("POST", EndpointGuildTemplate(templateCode), data, EndpointGuildTemplate(templateCode))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildTemplates returns all of GuildTemplates
// guildID: The ID of the guild
func (s *Session) GuildTemplates(guildID string) (st []*GuildTemplate, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildTemplates(guildID), nil, EndpointGuildTemplates(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildTemplateCreate creates a template for the guild
// guildID : The ID of the guild
// data    : Template metadata
func (s *Session) GuildTemplateCreate(guildID string, data *GuildTemplateParams) (st *GuildTemplate) {
	body, err := s.RequestWithBucketID("POST", EndpointGuildTemplates(guildID), data, EndpointGuildTemplates(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildTemplateSync syncs the template to the guild's current state
// guildID: The ID of the guild
// templateCode: The code of the template
func (s *Session) GuildTemplateSync(guildID, templateCode string) (err error) {
	_, err = s.RequestWithBucketID("PUT", EndpointGuildTemplateSync(guildID, templateCode), nil, EndpointGuildTemplateSync(guildID, ""))
	return
}

// GuildTemplateEdit modifies the template's metadata
// guildID      : The ID of the guild
// templateCode : The code of the template
// data         : New template metadata
func (s *Session) GuildTemplateEdit(guildID, templateCode string, data *GuildTemplateParams) (st *GuildTemplate, err error) {
	body, err := s.RequestWithBucketID("PATCH", EndpointGuildTemplateSync(guildID, templateCode), data, EndpointGuildTemplateSync(guildID, ""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildTemplateDelete deletes the template
// guildID: The ID of the guild
// templateCode: The code of the template
func (s *Session) GuildTemplateDelete(guildID, templateCode string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointGuildTemplateSync(guildID, templateCode), nil, EndpointGuildTemplateSync(guildID, ""))
	return
}

// GuildAck acknowledges a guild
func (s *Session) GuildAck(guildID string) (err error) {
	_, err = s.RequestWithBucketID("POST", EndpointGuildAck(guildID), struct{}{}, EndpointGuildAck(guildID))
	return
}

// GuildApplicationCommandIndex gets the application command index of a guild
func (s *Session) GuildApplicationCommandIndex(guildID string) ([]*ApplicationCommand, []*ApplicationCommandApplication, error) {
	var data []byte
	data, err := s.RequestWithBucketID("GET", EndpointGuildApplicationCommandIndex(guildID), nil, EndpointGuildApplicationCommandIndex(""))
	if err != nil {
		return nil, nil, err
	}

	var response struct {
		ApplicationCommands []*ApplicationCommand            `json:"application_commands"`
		Applications        []*ApplicationCommandApplication `json:"applications"`
	}

	err = unmarshal(data, &response)
	if err != nil {
		return nil, nil, err
	}

	return response.ApplicationCommands, response.Applications, nil
}

// ------------------------------------------------------------------------------------------------
// Functions specific to Discord Channels
// ------------------------------------------------------------------------------------------------

// Channel returns a Channel structure of a specific Channel.
// channelID  : The ID of the Channel you want returned.
func (s *Session) Channel(channelID string) (st *Channel, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointChannel(channelID), nil, EndpointChannel(channelID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelWithFallback returns a Channel from state if available, otherwise
// it will make a request to the API to get the channel.
// channelID  : The ID of the Channel you want returned.
func (s *Session) ChannelWithFallback(channelID string) (*Channel, error) {
	channel, err := s.State.Channel(channelID)
	if err == nil {
		return channel, nil
	}

	return s.Channel(channelID)
}

// ChannelEdit edits the given channel and returns the updated Channel data.
// channelID  : The ID of a Channel.
// data       : New Channel data.
func (s *Session) ChannelEdit(channelID string, data *ChannelEdit) (st *Channel, err error) {
	body, err := s.RequestWithBucketID("PATCH", EndpointChannel(channelID), data, EndpointChannel(channelID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelEditComplex edits an existing channel, replacing the parameters entirely with ChannelEdit struct
// NOTE: deprecated, use ChannelEdit instead
// channelID     : The ID of a Channel
// data          : The channel struct to send
func (s *Session) ChannelEditComplex(channelID string, data *ChannelEdit) (st *Channel, err error) {
	return s.ChannelEdit(channelID, data)
}

// ChannelDelete deletes the given channel
// channelID  : The ID of a Channel
// silent     : If deleting the channel should be done silently (for group DMs)
func (s *Session) ChannelDelete(channelID string, silent ...bool) (st *Channel, err error) {
	uri := EndpointChannel(channelID)

	if len(silent) > 0 {
		v := url.Values{}
		v.Set("silent", strconv.FormatBool(silent[0]))
		uri += "?" + v.Encode()
	}

	body, err := s.RequestWithBucketID("DELETE", uri, nil, EndpointChannel(channelID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelTyping broadcasts to all members that authenticated user is typing in
// the given channel.
// channelID  : The ID of a Channel
func (s *Session) ChannelTyping(channelID string) (err error) {
	_, err = s.RequestWithBucketID("POST", EndpointChannelTyping(channelID), nil, EndpointChannelTyping(channelID))
	return
}

// ChannelTypingDuration broadcasts to all members that authenticated user is typing in
// the given channel for a specific duration.
// channelID  : The ID of a Channel
// duration   : The duration to broadcast typing for
func (s *Session) ChannelTypingDuration(channelID string, duration time.Duration) (err error) {
	const interval = 5 * time.Second

	iterations := int(duration/interval) + 1
	for range iterations {
		if err := s.ChannelTyping(channelID); err != nil {
			return err
		}

		time.Sleep(interval)
	}

	return nil
}

// ChannelMessages returns an array of Message structures for messages within
// a given channel.
// channelID : The ID of a Channel.
// limit     : The number messages that can be returned. (max 100)
// beforeID  : If provided all messages returned will be before given ID.
// afterID   : If provided all messages returned will be after given ID.
// aroundID  : If provided all messages returned will be around given ID.
func (s *Session) ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string) (st []*Message, err error) {
	uri := EndpointChannelMessages(channelID)

	v := url.Values{}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if afterID != "" {
		v.Set("after", afterID)
	}
	if beforeID != "" {
		v.Set("before", beforeID)
	}
	if aroundID != "" {
		v.Set("around", aroundID)
	}
	if len(v) > 0 {
		uri += "?" + v.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointChannelMessages(channelID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelMessage gets a single message by ID from a given channel.
// channeld  : The ID of a Channel
// messageID : the ID of a Message
func (s *Session) ChannelMessage(channelID, messageID string) (st *Message, err error) {
	response, err := s.RequestWithBucketID("GET", EndpointChannelMessage(channelID, messageID), nil, EndpointChannelMessage(channelID, ""))
	if err != nil {
		return
	}

	err = unmarshal(response, &st)
	return
}

// ChannelMessageSend sends a message to the given channel.
// channelID : The ID of a Channel.
// content   : The message to send.
func (s *Session) ChannelMessageSend(channelID string, content string) (*Message, error) {
	return s.ChannelMessageSendComplex(channelID, &MessageSend{
		Content: content,
	})
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

// ChannelMessageSendComplex sends a message to the given channel.
// channelID : The ID of a Channel.
// data      : The message struct to send.
func (s *Session) ChannelMessageSendComplex(channelID string, data *MessageSend) (st *Message, err error) {
	// TODO: Remove this when compatibility is not required.
	if data.Embed != nil {
		if data.Embeds == nil {
			data.Embeds = []*MessageEmbed{data.Embed}
		} else {
			err = fmt.Errorf("cannot specify both Embed and Embeds")
			return
		}
	}

	for _, embed := range data.Embeds {
		if embed.Type == "" {
			embed.Type = "rich"
		}
	}
	endpoint := EndpointChannelMessages(channelID)

	// TODO: Remove this when compatibility is not required.
	files := data.Files
	if data.File != nil {
		if files == nil {
			files = []*File{data.File}
		} else {
			err = fmt.Errorf("cannot specify both File and Files")
			return
		}
	}

	var response []byte
	if len(files) > 0 {
		contentType, body, encodeErr := MultipartBodyWithJSON(data, files)
		if encodeErr != nil {
			return st, encodeErr
		}

		response, err = s.request("POST", endpoint, contentType, body, endpoint, 0)
	} else {
		response, err = s.RequestWithBucketID("POST", endpoint, data, endpoint)
	}
	if err != nil {
		return
	}

	err = unmarshal(response, &st)
	return
}

// ChannelMessageSendTTS sends a message to the given channel with Text to Speech.
// channelID : The ID of a Channel.
// content   : The message to send.
func (s *Session) ChannelMessageSendTTS(channelID string, content string) (*Message, error) {
	return s.ChannelMessageSendComplex(channelID, &MessageSend{
		Content: content,
		TTS:     true,
	})
}

// ChannelMessageSendEmbed sends a message to the given channel with embedded data.
// channelID : The ID of a Channel.
// embed     : The embed data to send.
func (s *Session) ChannelMessageSendEmbed(channelID string, embed *MessageEmbed) (*Message, error) {
	return s.ChannelMessageSendEmbeds(channelID, []*MessageEmbed{embed})
}

// ChannelMessageSendEmbeds sends a message to the given channel with multiple embedded data.
// channelID : The ID of a Channel.
// embeds    : The embeds data to send.
func (s *Session) ChannelMessageSendEmbeds(channelID string, embeds []*MessageEmbed) (*Message, error) {
	return s.ChannelMessageSendComplex(channelID, &MessageSend{
		Embeds: embeds,
	})
}

// ChannelMessageSendReply sends a message to the given channel with reference data.
// channelID : The ID of a Channel.
// content   : The message to send.
// reference : The message reference to send.
func (s *Session) ChannelMessageSendReply(channelID string, content string, reference *MessageReference) (*Message, error) {
	if reference == nil {
		return nil, fmt.Errorf("reply attempted with nil message reference")
	}
	return s.ChannelMessageSendComplex(channelID, &MessageSend{
		Content:   content,
		Reference: reference,
	})
}

// ChannelMessageSendEmbedReply sends a message to the given channel with reference data and embedded data.
// channelID : The ID of a Channel.
// embed   : The embed data to send.
// reference : The message reference to send.
func (s *Session) ChannelMessageSendEmbedReply(channelID string, embed *MessageEmbed, reference *MessageReference) (*Message, error) {
	return s.ChannelMessageSendEmbedsReply(channelID, []*MessageEmbed{embed}, reference)
}

// ChannelMessageSendEmbedsReply sends a message to the given channel with reference data and multiple embedded data.
// channelID : The ID of a Channel.
// embeds    : The embeds data to send.
// reference : The message reference to send.
func (s *Session) ChannelMessageSendEmbedsReply(channelID string, embeds []*MessageEmbed, reference *MessageReference) (*Message, error) {
	if reference == nil {
		return nil, fmt.Errorf("reply attempted with nil message reference")
	}
	return s.ChannelMessageSendComplex(channelID, &MessageSend{
		Embeds:    embeds,
		Reference: reference,
	})
}

// ChannelMessageEdit edits an existing message, replacing it entirely with
// the given content.
// channelID  : The ID of a Channel
// messageID  : The ID of a Message
// content    : The contents of the message
func (s *Session) ChannelMessageEdit(channelID, messageID, content string) (*Message, error) {
	return s.ChannelMessageEditComplex(NewMessageEdit(channelID, messageID).SetContent(content))
}

// ChannelMessageEditComplex edits an existing message, replacing it entirely with
// the given MessageEdit struct
func (s *Session) ChannelMessageEditComplex(m *MessageEdit) (st *Message, err error) {
	// TODO: Remove this when compatibility is not required.
	if m.Embed != nil {
		if m.Embeds == nil {
			m.Embeds = []*MessageEmbed{m.Embed}
		} else {
			err = fmt.Errorf("cannot specify both Embed and Embeds")
			return
		}
	}

	for _, embed := range m.Embeds {
		if embed.Type == "" {
			embed.Type = "rich"
		}
	}

	endpoint := EndpointChannelMessage(m.Channel, m.ID)

	var response []byte
	if len(m.Files) > 0 {
		contentType, body, encodeErr := MultipartBodyWithJSON(m, m.Files)
		if encodeErr != nil {
			return st, encodeErr
		}
		response, err = s.request("PATCH", endpoint, contentType, body, EndpointChannelMessage(m.Channel, ""), 0)
	} else {
		response, err = s.RequestWithBucketID("PATCH", endpoint, m, EndpointChannelMessage(m.Channel, ""))
	}
	if err != nil {
		return
	}

	err = unmarshal(response, &st)
	return
}

// ChannelMessageEditEmbed edits an existing message with embedded data.
// channelID : The ID of a Channel
// messageID : The ID of a Message
// embed     : The embed data to send
func (s *Session) ChannelMessageEditEmbed(channelID, messageID string, embed *MessageEmbed) (*Message, error) {
	return s.ChannelMessageEditEmbeds(channelID, messageID, []*MessageEmbed{embed})
}

// ChannelMessageEditEmbeds edits an existing message with multiple embedded data.
// channelID : The ID of a Channel
// messageID : The ID of a Message
// embeds    : The embeds data to send
func (s *Session) ChannelMessageEditEmbeds(channelID, messageID string, embeds []*MessageEmbed) (*Message, error) {
	return s.ChannelMessageEditComplex(NewMessageEdit(channelID, messageID).SetEmbeds(embeds))
}

// ChannelMessageDelete deletes a message from the Channel.
func (s *Session) ChannelMessageDelete(channelID, messageID string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointChannelMessage(channelID, messageID), nil, EndpointChannelMessage(channelID, ""))
	return
}

// ChannelMessagesBulkDelete bulk deletes the messages from the channel for the provided messageIDs.
// If only one messageID is in the slice call channelMessageDelete function.
// If the slice is empty do nothing.
// channelID : The ID of the channel for the messages to delete.
// messages  : The IDs of the messages to be deleted. A slice of string IDs. A maximum of 100 messages.
func (s *Session) ChannelMessagesBulkDelete(channelID string, messages []string) (err error) {
	if len(messages) == 0 {
		return
	}

	if len(messages) == 1 {
		err = s.ChannelMessageDelete(channelID, messages[0])
		return
	}

	if len(messages) > 100 {
		messages = messages[:100]
	}

	data := struct {
		Messages []string `json:"messages"`
	}{messages}

	_, err = s.RequestWithBucketID("POST", EndpointChannelMessagesBulkDelete(channelID), data, EndpointChannelMessagesBulkDelete(channelID))
	return
}

// ChannelMessagePin pins a message within a given channel.
// channelID: The ID of a channel.
// messageID: The ID of a message.
func (s *Session) ChannelMessagePin(channelID, messageID string) (err error) {
	_, err = s.RequestWithBucketID("PUT", EndpointChannelMessagePin(channelID, messageID), nil, EndpointChannelMessagePin(channelID, ""))
	return
}

// ChannelMessageUnpin unpins a message within a given channel.
// channelID: The ID of a channel.
// messageID: The ID of a message.
func (s *Session) ChannelMessageUnpin(channelID, messageID string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointChannelMessagePin(channelID, messageID), nil, EndpointChannelMessagePin(channelID, ""))
	return
}

// ChannelMessagesPinned returns an array of Message structures for pinned messages
// within a given channel
// channelID : The ID of a Channel.
func (s *Session) ChannelMessagesPinned(channelID string) (st []*Message, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointChannelMessagesPins(channelID), nil, EndpointChannelMessagesPins(channelID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelFileSend sends a file to the given channel.
// channelID : The ID of a Channel.
// name: The name of the file.
// io.Reader : A reader for the file contents.
func (s *Session) ChannelFileSend(channelID, name string, r io.Reader) (*Message, error) {
	return s.ChannelMessageSendComplex(channelID, &MessageSend{File: &File{Name: name, Reader: r}})
}

// ChannelFileSendWithMessage sends a file to the given channel with an message.
// DEPRECATED. Use ChannelMessageSendComplex instead.
// channelID : The ID of a Channel.
// content: Optional Message content.
// name: The name of the file.
// io.Reader : A reader for the file contents.
func (s *Session) ChannelFileSendWithMessage(channelID, content string, name string, r io.Reader) (*Message, error) {
	return s.ChannelMessageSendComplex(channelID, &MessageSend{File: &File{Name: name, Reader: r}, Content: content})
}

// ChannelInvites returns an array of Invite structures for the given channel
// channelID   : The ID of a Channel
func (s *Session) ChannelInvites(channelID string) (st []*Invite, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointChannelInvites(channelID), nil, EndpointChannelInvites(channelID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelInviteCreate creates a new invite for the given channel.
// channelID   : The ID of a Channel
// maxAge	   : Duration of invite in seconds before expiry, or 0 for never.
// maxUses	   : Maximum number of uses or 0 for unlimited.
// temp		   : Whether this invite only grants temporary membership.
func (s *Session) ChannelInviteCreate(channelID string, maxAge, maxUses int, temp bool) (st *Invite, err error) {
	data := struct {
		Flags      int  `json:"flags"`
		MaxAge     int  `json:"max_age"`
		MaxUses    int  `json:"max_uses"`
		TargetType any  `json:"target_type"`
		Temporary  bool `json:"temporary"`
	}{0, maxAge, maxUses, nil, temp}

	body, err := s.RequestWithBucketID("POST", EndpointChannelInvites(channelID), data, EndpointChannelInvites(channelID), map[string]string{
		"x-context-properties": "eyJsb2NhdGlvbiI6Ikd1aWxkIEhlYWRlciJ9",
	})
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelPermissionSet creates a Permission Override for the given channel.
// NOTE: This func name may changed.  Using Set instead of Create because
// you can both create a new override or update an override with this function.
func (s *Session) ChannelPermissionSet(channelID, targetID string, targetType PermissionOverwriteType, allow, deny int64) (err error) {
	data := struct {
		ID    string                  `json:"id"`
		Type  PermissionOverwriteType `json:"type"`
		Allow int64                   `json:"allow,string"`
		Deny  int64                   `json:"deny,string"`
	}{targetID, targetType, allow, deny}

	_, err = s.RequestWithBucketID("PUT", EndpointChannelPermission(channelID, targetID), data, EndpointChannelPermission(channelID, ""))
	return
}

// ChannelPermissionDelete deletes a specific permission override for the given channel.
// NOTE: Name of this func may change.
func (s *Session) ChannelPermissionDelete(channelID, targetID string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointChannelPermission(channelID, targetID), nil, EndpointChannelPermission(channelID, ""))
	return
}

// ChannelMessageCrosspost cross posts a message in a news channel to followers
// of the channel
// channelID   : The ID of a Channel
// messageID   : The ID of a Message
func (s *Session) ChannelMessageCrosspost(channelID, messageID string) (st *Message, err error) {
	endpoint := EndpointChannelMessageCrosspost(channelID, messageID)

	body, err := s.RequestWithBucketID("POST", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelNewsFollow follows a news channel in the targetID
// channelID   : The ID of a News Channel
// targetID    : The ID of a Channel where the News Channel should post to
func (s *Session) ChannelNewsFollow(channelID, targetID string) (st *ChannelFollow, err error) {
	endpoint := EndpointChannelFollow(channelID)

	data := struct {
		WebhookChannelID string `json:"webhook_channel_id"`
	}{targetID}

	body, err := s.RequestWithBucketID("POST", endpoint, data, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelRecipientAdd adds a recipient to a GroupDM
// channelID   : The ID of a GroupDM Channel
// userID      : The ID of a User
func (s *Session) ChannelRecipientAdd(channelID, userID string) (err error) {
	_, err = s.RequestWithBucketID("PUT", EndpointChannelRecipient(channelID, userID), nil, EndpointChannelRecipient(channelID, ""))
	return
}

// ChannelRecipientRemove removes a recipient from a GroupDM
// channelID   : The ID of a GroupDM Channel
// userID      : The ID of a User
func (s *Session) ChannelRecipientRemove(channelID, userID string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointChannelRecipient(channelID, userID), nil, EndpointChannelRecipient(channelID, ""))
	return
}

type ApplicationCommandsSearchParams struct {
	// The ID of the channel to search for commands in
	ChannelID string

	// The type of search to use (1)
	Type int

	// The limit of commands to search for (10)
	Limit int

	// The query to search for
	Query string

	// The cursor
	Cursor string

	// The IDs of the commands to search for
	CommandIDs []string

	// The ID of the application to search for
	ApplicationID string

	// Whether to include applications in the response
	IncludeApplications bool
}

func (s *Session) ChannelApplicationCommandsSearch(searchParams *ApplicationCommandsSearchParams) (commands []*ApplicationCommand, applications []*Application, err error) {
	params := url.Values{}
	params.Set("type", strconv.Itoa(searchParams.Type))

	if searchParams.IncludeApplications {
		params.Set("include_applications", "true")
	} else {
		params.Set("include_applications", "false")
	}

	if searchParams.Limit > 0 {
		params.Set("limit", strconv.Itoa(searchParams.Limit))
	}

	if searchParams.Query != "" {
		params.Set("query", searchParams.Query)
	}

	if searchParams.Cursor != "" {
		params.Set("cursor", searchParams.Cursor)
	}

	if len(searchParams.CommandIDs) > 0 {
		params.Set("command_ids", strings.Join(searchParams.CommandIDs, ","))
	}

	if searchParams.ApplicationID != "" {
		params.Set("application_id", searchParams.ApplicationID)
	}

	var response struct {
		ApplicationCommands []*ApplicationCommand `json:"application_commands"`
		Applications        []*Application        `json:"applications"`
	}

	endpoint := EndpointChannelApplicationCommandsSearch(searchParams.ChannelID)

	var data []byte
	data, err = s.RequestWithBucketID("GET", endpoint+"?"+params.Encode(), nil, EndpointChannelApplicationCommandsSearch(""))
	if err != nil {
		return
	}

	fmt.Println(string(data))

	err = unmarshal(data, &response)
	if err != nil {
		return
	}

	commands = response.ApplicationCommands
	applications = response.Applications

	for _, application := range applications {
		for _, command := range commands {
			if command.ApplicationID == application.ID {
				command.BotUserID = application.Bot.ID
			}
		}
	}

	return
}

// ------------------------------------------------------------------------------------------------
// Functions specific to Discord Invites
// ------------------------------------------------------------------------------------------------

// Invite returns an Invite structure of the given invite
// inviteID : The invite code
func (s *Session) Invite(inviteID string) (st *Invite, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointInvite(inviteID), nil, EndpointInvite(""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// InviteWithCounts returns an Invite structure of the given invite including approximate member counts
// inviteID : The invite code
func (s *Session) InviteWithCounts(inviteID string) (st *Invite, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointInvite(inviteID)+"?with_counts=true", nil, EndpointInvite(""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// InviteComplex returns an Invite structure of the given invite including specified fields.
//
//	inviteID                  : The invite code
//	guildScheduledEventID     : If specified, includes specified guild scheduled event.
//	withCounts                : Whether to include approximate member counts or not
//	withExpiration            : Whether to include expiration time or not
func (s *Session) InviteComplex(inviteID, guildScheduledEventID, inputValue string, withCounts, withExpiration bool) (st *Invite, err error) {
	endpoint := EndpointInvite(inviteID)
	v := url.Values{}
	if guildScheduledEventID != "" {
		v.Set("guild_scheduled_event_id", guildScheduledEventID)
	}
	if inputValue != "" {
		v.Set("inputValue", inputValue)
	}
	if withCounts {
		v.Set("with_counts", "true")
	}
	if withExpiration {
		v.Set("with_expiration", "true")
	}

	if len(v) != 0 {
		endpoint += "?" + v.Encode()
	}

	body, err := s.RequestWithBucketID("GET", endpoint, nil, EndpointInvite(""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// InviteDelete deletes an existing invite
// inviteID   : the code of an invite
func (s *Session) InviteDelete(inviteID string) (st *Invite, err error) {
	body, err := s.RequestWithBucketID("DELETE", EndpointInvite(inviteID), nil, EndpointInvite(""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// InviteAccept accepts an Invite to a Guild or Channel
// inviteID : The invite code
func (s *Session) InviteAccept(inviteID, guildID, channelID string, channelType int) (st *Invite, err error) {
	contextProperties := "{\"location\":\"Join Guild\",\"location_guild_id\":\"%s\",\"location_channel_id\":\"%s\",\"location_channel_type\":%d}"

	contextProperties = fmt.Sprintf(contextProperties, guildID, channelID, channelType)

	headers := map[string]string{
		"x-context-properties": base64.StdEncoding.EncodeToString([]byte(contextProperties)),
	}

	data := map[string]string{
		"session_id": s.State.SessionID,
	}

	body, err := s.RequestWithBucketID("POST", EndpointInvite(inviteID), data, EndpointInvite(""), headers)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// CreateFriendInvite creates a friend invite for the given user.
func (s *Session) CreateFriendInvite(userID string) (st *Invite, err error) {
	body, err := s.RequestWithBucketID("POST", EndpointUserInvites(userID), map[string]string{}, EndpointUserInvites(""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ------------------------------------------------------------------------------------------------
// Functions specific to Discord Voice
// ------------------------------------------------------------------------------------------------

// VoiceRegions returns the voice server regions
func (s *Session) VoiceRegions() (st []*VoiceRegion, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointVoiceRegions, nil, EndpointVoiceRegions)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ------------------------------------------------------------------------------------------------
// Functions specific to Discord Websockets
// ------------------------------------------------------------------------------------------------

// Gateway returns the websocket Gateway address
func (s *Session) Gateway() (gateway string, err error) {
	response, err := s.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway)
	if err != nil {
		return
	}

	temp := struct {
		URL string `json:"url"`
	}{}

	err = unmarshal(response, &temp)
	if err != nil {
		return
	}

	gateway = temp.URL

	// Ensure the gateway always has a trailing slash.
	// MacOS will fail to connect if we add query params without a trailing slash on the base domain.
	if !strings.HasSuffix(gateway, "/") {
		gateway += "/"
	}

	return
}

// GatewayBot returns the websocket Gateway address and the recommended number of shards
func (s *Session) GatewayBot() (st *GatewayBotResponse, err error) {
	response, err := s.RequestWithBucketID("GET", EndpointGatewayBot, nil, EndpointGatewayBot)
	if err != nil {
		return
	}

	err = unmarshal(response, &st)
	if err != nil {
		return
	}

	// Ensure the gateway always has a trailing slash.
	// MacOS will fail to connect if we add query params without a trailing slash on the base domain.
	if !strings.HasSuffix(st.URL, "/") {
		st.URL += "/"
	}

	return
}

// Functions specific to Webhooks

// WebhookCreate returns a new Webhook.
// channelID: The ID of a Channel.
// name     : The name of the webhook.
// avatar   : The avatar of the webhook.
func (s *Session) WebhookCreate(channelID, name, avatar string) (st *Webhook, err error) {
	data := struct {
		Name   string `json:"name"`
		Avatar string `json:"avatar,omitempty"`
	}{name, avatar}

	body, err := s.RequestWithBucketID("POST", EndpointChannelWebhooks(channelID), data, EndpointChannelWebhooks(channelID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// ChannelWebhooks returns all webhooks for a given channel.
// channelID: The ID of a channel.
func (s *Session) ChannelWebhooks(channelID string) (st []*Webhook, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointChannelWebhooks(channelID), nil, EndpointChannelWebhooks(channelID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildWebhooks returns all webhooks for a given guild.
// guildID: The ID of a Guild.
func (s *Session) GuildWebhooks(guildID string) (st []*Webhook, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildWebhooks(guildID), nil, EndpointGuildWebhooks(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// Webhook returns a webhook for a given ID
// webhookID: The ID of a webhook.
func (s *Session) Webhook(webhookID string) (st *Webhook, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointWebhook(webhookID), nil, EndpointWebhooks)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// WebhookWithToken returns a webhook for a given ID
// webhookID: The ID of a webhook.
// token    : The auth token for the webhook.
func (s *Session) WebhookWithToken(webhookID, token string) (st *Webhook, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointWebhookToken(webhookID, token), nil, EndpointWebhookToken("", ""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// WebhookEdit updates an existing Webhook.
// webhookID: The ID of a webhook.
// name     : The name of the webhook.
// avatar   : The avatar of the webhook.
func (s *Session) WebhookEdit(webhookID, name, avatar, channelID string) (st *Role, err error) {
	data := struct {
		Name      string `json:"name,omitempty"`
		Avatar    string `json:"avatar,omitempty"`
		ChannelID string `json:"channel_id,omitempty"`
	}{name, avatar, channelID}

	body, err := s.RequestWithBucketID("PATCH", EndpointWebhook(webhookID), data, EndpointWebhooks)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// WebhookEditWithToken updates an existing Webhook with an auth token.
// webhookID: The ID of a webhook.
// token    : The auth token for the webhook.
// name     : The name of the webhook.
// avatar   : The avatar of the webhook.
func (s *Session) WebhookEditWithToken(webhookID, token, name, avatar string) (st *Role, err error) {
	data := struct {
		Name   string `json:"name,omitempty"`
		Avatar string `json:"avatar,omitempty"`
	}{name, avatar}

	body, err := s.RequestWithBucketID("PATCH", EndpointWebhookToken(webhookID, token), data, EndpointWebhookToken("", ""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// WebhookDelete deletes a webhook for a given ID
// webhookID: The ID of a webhook.
func (s *Session) WebhookDelete(webhookID string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointWebhook(webhookID), nil, EndpointWebhooks)

	return
}

// WebhookDeleteWithToken deletes a webhook for a given ID with an auth token.
// webhookID: The ID of a webhook.
// token    : The auth token for the webhook.
func (s *Session) WebhookDeleteWithToken(webhookID, token string) (st *Webhook, err error) {
	body, err := s.RequestWithBucketID("DELETE", EndpointWebhookToken(webhookID, token), nil, EndpointWebhookToken("", ""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

func (s *Session) webhookExecute(webhookID, token string, wait bool, threadID string, data *WebhookParams) (st *Message, err error) {
	uri := EndpointWebhookToken(webhookID, token)

	v := url.Values{}
	if wait {
		v.Set("wait", "true")
	}

	if threadID != "" {
		v.Set("thread_id", threadID)
	}
	if len(v) != 0 {
		uri += "?" + v.Encode()
	}

	headers := map[string]string{
		"Origin":        uri,
		"Authorization": "",
	}

	var response []byte
	if len(data.Files) > 0 {
		contentType, body, encodeErr := MultipartBodyWithJSON(data, data.Files)
		if encodeErr != nil {
			return st, encodeErr
		}

		response, err = s.request("POST", uri, contentType, body, uri, 0, headers)
	} else {
		response, err = s.RequestWithBucketID("POST", uri, data, uri, headers)
	}
	if !wait || err != nil {
		return
	}

	err = unmarshal(response, &st)
	return
}

// WebhookExecute executes a webhook.
// webhookID: The ID of a webhook.
// token    : The auth token for the webhook
// wait     : Waits for server confirmation of message send and ensures that the return struct is populated (it is nil otherwise)
func (s *Session) WebhookExecute(webhookID, token string, wait bool, data *WebhookParams) (st *Message, err error) {
	return s.webhookExecute(webhookID, token, wait, "", data)
}

// WebhookThreadExecute executes a webhook in a thread.
// webhookID: The ID of a webhook.
// token    : The auth token for the webhook
// wait     : Waits for server confirmation of message send and ensures that the return struct is populated (it is nil otherwise)
// threadID :	Sends a message to the specified thread within a webhook's channel. The thread will automatically be unarchived.
func (s *Session) WebhookThreadExecute(webhookID, token string, wait bool, threadID string, data *WebhookParams) (st *Message, err error) {
	return s.webhookExecute(webhookID, token, wait, threadID, data)
}

// WebhookMessage gets a webhook message.
// webhookID : The ID of a webhook
// token     : The auth token for the webhook
// messageID : The ID of message to get
func (s *Session) WebhookMessage(webhookID, token, messageID string) (message *Message, err error) {
	uri := EndpointWebhookMessage(webhookID, token, messageID)

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointWebhookToken("", ""))
	if err != nil {
		return
	}

	err = Unmarshal(body, &message)

	return
}

// WebhookMessageEdit edits a webhook message and returns a new one.
// webhookID : The ID of a webhook
// token     : The auth token for the webhook
// messageID : The ID of message to edit
func (s *Session) WebhookMessageEdit(webhookID, token, messageID string, data *WebhookEdit) (st *Message, err error) {
	uri := EndpointWebhookMessage(webhookID, token, messageID)

	var response []byte
	if len(data.Files) > 0 {
		contentType, body, err := MultipartBodyWithJSON(data, data.Files)
		if err != nil {
			return nil, err
		}

		response, err = s.request("PATCH", uri, contentType, body, uri, 0)
		if err != nil {
			return nil, err
		}
	} else {
		response, err = s.RequestWithBucketID("PATCH", uri, data, EndpointWebhookToken("", ""))
		if err != nil {
			return nil, err
		}
	}

	err = unmarshal(response, &st)
	return
}

// WebhookMessageDelete deletes a webhook message.
// webhookID : The ID of a webhook
// token     : The auth token for the webhook
// messageID : The ID of a message to edit
func (s *Session) WebhookMessageDelete(webhookID, token, messageID string) (err error) {
	uri := EndpointWebhookMessage(webhookID, token, messageID)

	_, err = s.RequestWithBucketID("DELETE", uri, nil, EndpointWebhookToken("", ""))
	return
}

// MessageReactionAdd creates an emoji reaction to a message.
// channelID : The channel ID.
// messageID : The message ID.
// emojiID   : Either the unicode emoji for the reaction, or a guild emoji identifier in name:id format (e.g. "hello:1234567654321")
func (s *Session) MessageReactionAdd(channelID, messageID, emojiID string) error {
	// emoji such as  #⃣ need to have # escaped
	emojiID = strings.Replace(emojiID, "#", "%23", -1)
	_, err := s.RequestWithBucketID("PUT", EndpointMessageReaction(channelID, messageID, emojiID, "@me"), nil, EndpointMessageReaction(channelID, "", "", ""))

	return err
}

// MessageReactionRemove deletes an emoji reaction to a message.
// channelID : The channel ID.
// messageID : The message ID.
// emojiID   : Either the unicode emoji for the reaction, or a guild emoji identifier.
// userID	 : @me or ID of the user to delete the reaction for.
func (s *Session) MessageReactionRemove(channelID, messageID, emojiID, userID string) error {
	// emoji such as  #⃣ need to have # escaped
	emojiID = strings.Replace(emojiID, "#", "%23", -1)
	_, err := s.RequestWithBucketID("DELETE", EndpointMessageReaction(channelID, messageID, emojiID, userID), nil, EndpointMessageReaction(channelID, "", "", ""))

	return err
}

// MessageReactionsRemoveAll deletes all reactions from a message
// channelID : The channel ID
// messageID : The message ID.
func (s *Session) MessageReactionsRemoveAll(channelID, messageID string) error {
	_, err := s.RequestWithBucketID("DELETE", EndpointMessageReactionsAll(channelID, messageID), nil, EndpointMessageReactionsAll(channelID, messageID))

	return err
}

// MessageReactionsRemoveEmoji deletes all reactions of a certain emoji from a message
// channelID : The channel ID
// messageID : The message ID
// emojiID   : The emoji ID
func (s *Session) MessageReactionsRemoveEmoji(channelID, messageID, emojiID string) error {
	// emoji such as  #⃣ need to have # escaped
	emojiID = strings.Replace(emojiID, "#", "%23", -1)
	_, err := s.RequestWithBucketID("DELETE", EndpointMessageReactions(channelID, messageID, emojiID), nil, EndpointMessageReactions(channelID, messageID, emojiID))

	return err
}

// MessageReactions gets all the users reactions for a specific emoji.
// channelID : The channel ID.
// messageID : The message ID.
// emojiID   : Either the unicode emoji for the reaction, or a guild emoji identifier.
// limit    : max number of users to return (max 100)
// beforeID  : If provided all reactions returned will be before given ID.
// afterID   : If provided all reactions returned will be after given ID.
func (s *Session) MessageReactions(channelID, messageID, emojiID string, limit int, beforeID, afterID string) (st []*User, err error) {
	// emoji such as  #⃣ need to have # escaped
	emojiID = strings.Replace(emojiID, "#", "%23", -1)
	uri := EndpointMessageReactions(channelID, messageID, emojiID)

	v := url.Values{}

	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}

	if afterID != "" {
		v.Set("after", afterID)
	}
	if beforeID != "" {
		v.Set("before", beforeID)
	}

	if len(v) > 0 {
		uri += "?" + v.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointMessageReaction(channelID, "", "", ""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// MessageAck acknowledges a message.
func (s *Session) MessageAck(channelID, messageID string) error {
	data := map[string]any{"token": nil}

	_, err := s.RequestWithBucketID("POST", EndpointMessageAck(channelID, messageID), data, EndpointMessageAck(channelID, ""))
	return err
}

// MessageUnack unacknowledges a message.
func (s *Session) MessageUnack(channelID, messageID string, mentionCount int) error {
	data := map[string]any{"manual": true, "mention_count": mentionCount}

	_, err := s.RequestWithBucketID("POST", EndpointMessageAck(channelID, messageID), data, EndpointMessageAck(channelID, ""))
	return err
}

// ------------------------------------------------------------------------------------------------
// Functions specific to threads
// ------------------------------------------------------------------------------------------------

// MessageThreadStartComplex creates a new thread from an existing message.
// channelID : Channel to create thread in
// messageID : Message to start thread from
// data : Parameters of the thread
func (s *Session) MessageThreadStartComplex(channelID, messageID string, data *ThreadStart) (ch *Channel, err error) {
	endpoint := EndpointChannelMessageThread(channelID, messageID)
	var body []byte
	body, err = s.RequestWithBucketID("POST", endpoint, data, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &ch)
	return
}

// MessageThreadStart creates a new thread from an existing message.
// channelID       : Channel to create thread in
// messageID       : Message to start thread from
// name            : Name of the thread
// archiveDuration : Auto archive duration (in minutes)
func (s *Session) MessageThreadStart(channelID, messageID string, name string, archiveDuration int) (ch *Channel, err error) {
	return s.MessageThreadStartComplex(channelID, messageID, &ThreadStart{
		Name:                name,
		AutoArchiveDuration: archiveDuration,
	})
}

// ThreadStartComplex creates a new thread.
// channelID : Channel to create thread in
// data : Parameters of the thread
func (s *Session) ThreadStartComplex(channelID string, data *ThreadStart) (ch *Channel, err error) {
	endpoint := EndpointChannelThreads(channelID)
	var body []byte
	body, err = s.RequestWithBucketID("POST", endpoint, data, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &ch)
	return
}

// ThreadStart creates a new thread.
// channelID       : Channel to create thread in
// name            : Name of the thread
// archiveDuration : Auto archive duration (in minutes)
func (s *Session) ThreadStart(channelID, name string, typ ChannelType, archiveDuration int) (ch *Channel, err error) {
	return s.ThreadStartComplex(channelID, &ThreadStart{
		Name:                name,
		Type:                typ,
		AutoArchiveDuration: archiveDuration,
	})
}

// ForumThreadStartComplex starts a new thread (creates a post) in a forum channel.
// channelID   : Channel to create thread in.
// threadData  : Parameters of the thread.
// messageData : Parameters of the starting message.
func (s *Session) ForumThreadStartComplex(channelID string, threadData *ThreadStart, messageData *MessageSend) (th *Channel, err error) {
	endpoint := EndpointChannelThreads(channelID)

	// TODO: Remove this when compatibility is not required.
	if messageData.Embed != nil {
		if messageData.Embeds == nil {
			messageData.Embeds = []*MessageEmbed{messageData.Embed}
		} else {
			err = fmt.Errorf("cannot specify both Embed and Embeds")
			return
		}
	}

	for _, embed := range messageData.Embeds {
		if embed.Type == "" {
			embed.Type = "rich"
		}
	}

	// TODO: Remove this when compatibility is not required.
	files := messageData.Files
	if messageData.File != nil {
		if files == nil {
			files = []*File{messageData.File}
		} else {
			err = fmt.Errorf("cannot specify both File and Files")
			return
		}
	}

	data := struct {
		*ThreadStart
		Message *MessageSend `json:"message"`
	}{ThreadStart: threadData, Message: messageData}

	var response []byte
	if len(files) > 0 {
		contentType, body, encodeErr := MultipartBodyWithJSON(data, files)
		if encodeErr != nil {
			return th, encodeErr
		}

		response, err = s.request("POST", endpoint, contentType, body, endpoint, 0)
	} else {
		response, err = s.RequestWithBucketID("POST", endpoint, data, endpoint)
	}
	if err != nil {
		return
	}

	err = unmarshal(response, &th)
	return
}

// ForumThreadStart starts a new thread (post) in a forum channel.
// channelID       : Channel to create thread in.
// name            : Name of the thread.
// archiveDuration : Auto archive duration.
// content         : Content of the starting message.
func (s *Session) ForumThreadStart(channelID, name string, archiveDuration int, content string) (th *Channel, err error) {
	return s.ForumThreadStartComplex(channelID, &ThreadStart{
		Name:                name,
		AutoArchiveDuration: archiveDuration,
	}, &MessageSend{Content: content})
}

// ForumThreadStartEmbed starts a new thread (post) in a forum channel.
// channelID       : Channel to create thread in.
// name            : Name of the thread.
// archiveDuration : Auto archive duration.
// embed           : Embed data of the starting message.
func (s *Session) ForumThreadStartEmbed(channelID, name string, archiveDuration int, embed *MessageEmbed) (th *Channel, err error) {
	return s.ForumThreadStartComplex(channelID, &ThreadStart{
		Name:                name,
		AutoArchiveDuration: archiveDuration,
	}, &MessageSend{Embeds: []*MessageEmbed{embed}})
}

// ForumThreadStartEmbeds starts a new thread (post) in a forum channel.
// channelID       : Channel to create thread in.
// name            : Name of the thread.
// archiveDuration : Auto archive duration.
// embeds          : Embeds data of the starting message.
func (s *Session) ForumThreadStartEmbeds(channelID, name string, archiveDuration int, embeds []*MessageEmbed) (th *Channel, err error) {
	return s.ForumThreadStartComplex(channelID, &ThreadStart{
		Name:                name,
		AutoArchiveDuration: archiveDuration,
	}, &MessageSend{Embeds: embeds})
}

// ThreadJoin adds current user to a thread
func (s *Session) ThreadJoin(id string) error {
	endpoint := EndpointThreadMember(id, "@me")
	_, err := s.RequestWithBucketID("PUT", endpoint, nil, endpoint)
	return err
}

// ThreadLeave removes current user to a thread
func (s *Session) ThreadLeave(id string) error {
	endpoint := EndpointThreadMember(id, "@me")
	_, err := s.RequestWithBucketID("DELETE", endpoint, nil, endpoint)
	return err
}

// ThreadMemberAdd adds another member to a thread
func (s *Session) ThreadMemberAdd(threadID, memberID string) error {
	endpoint := EndpointThreadMember(threadID, memberID)
	_, err := s.RequestWithBucketID("PUT", endpoint, nil, endpoint)
	return err
}

// ThreadMemberRemove removes another member from a thread
func (s *Session) ThreadMemberRemove(threadID, memberID string) error {
	endpoint := EndpointThreadMember(threadID, memberID)
	_, err := s.RequestWithBucketID("DELETE", endpoint, nil, endpoint)
	return err
}

// ThreadMember returns thread member object for the specified member of a thread
func (s *Session) ThreadMember(threadID, memberID string) (member *ThreadMember, err error) {
	endpoint := EndpointThreadMember(threadID, memberID)
	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &member)
	return
}

// ThreadMembers returns all members of specified thread.
func (s *Session) ThreadMembers(threadID string) (members []*ThreadMember, err error) {
	var body []byte
	body, err = s.RequestWithBucketID("GET", EndpointThreadMembers(threadID), nil, EndpointThreadMembers(threadID))
	if err != nil {
		return
	}

	err = unmarshal(body, &members)
	return
}

// ThreadsActive returns all active threads for specified channel.
func (s *Session) ThreadsActive(channelID string) (threads *ThreadsList, err error) {
	var body []byte
	body, err = s.RequestWithBucketID("GET", EndpointChannelActiveThreads(channelID), nil, EndpointChannelActiveThreads(channelID))
	if err != nil {
		return
	}

	err = unmarshal(body, &threads)
	return
}

// GuildThreadsActive returns all active threads for specified guild.
func (s *Session) GuildThreadsActive(guildID string) (threads *ThreadsList, err error) {
	var body []byte
	body, err = s.RequestWithBucketID("GET", EndpointGuildActiveThreads(guildID), nil, EndpointGuildActiveThreads(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &threads)
	return
}

// ThreadsArchived returns archived threads for specified channel.
// before : If specified returns only threads before the timestamp
// limit  : Optional maximum amount of threads to return.
func (s *Session) ThreadsArchived(channelID string, before *time.Time, limit int) (threads *ThreadsList, err error) {
	endpoint := EndpointChannelPublicArchivedThreads(channelID)
	v := url.Values{}
	if before != nil {
		v.Set("before", before.Format(time.RFC3339))
	}

	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}

	if len(v) > 0 {
		endpoint += "?" + v.Encode()
	}

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &threads)
	return
}

// ThreadsPrivateArchived returns archived private threads for specified channel.
// before : If specified returns only threads before the timestamp
// limit  : Optional maximum amount of threads to return.
func (s *Session) ThreadsPrivateArchived(channelID string, before *time.Time, limit int) (threads *ThreadsList, err error) {
	endpoint := EndpointChannelPrivateArchivedThreads(channelID)
	v := url.Values{}
	if before != nil {
		v.Set("before", before.Format(time.RFC3339))
	}

	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}

	if len(v) > 0 {
		endpoint += "?" + v.Encode()
	}
	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &threads)
	return
}

// ThreadsPrivateJoinedArchived returns archived joined private threads for specified channel.
// before : If specified returns only threads before the timestamp
// limit  : Optional maximum amount of threads to return.
func (s *Session) ThreadsPrivateJoinedArchived(channelID string, before *time.Time, limit int) (threads *ThreadsList, err error) {
	endpoint := EndpointChannelJoinedPrivateArchivedThreads(channelID)
	v := url.Values{}
	if before != nil {
		v.Set("before", before.Format(time.RFC3339))
	}

	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}

	if len(v) > 0 {
		endpoint += "?" + v.Encode()
	}
	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &threads)
	return
}

// ------------------------------------------------------------------------------------------------
// Functions specific to application (slash) commands
// ------------------------------------------------------------------------------------------------

// ApplicationCommandCreate creates a global application command and returns it.
// appID       : The application ID.
// guildID     : Guild ID to create guild-specific application command. If empty - creates global application command.
// cmd         : New application command data.
func (s *Session) ApplicationCommandCreate(appID string, guildID string, cmd *ApplicationCommand) (ccmd *ApplicationCommand, err error) {
	endpoint := EndpointApplicationGlobalCommands(appID)
	if guildID != "" {
		endpoint = EndpointApplicationGuildCommands(appID, guildID)
	}

	body, err := s.RequestWithBucketID("POST", endpoint, *cmd, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &ccmd)

	return
}

// ApplicationCommandEdit edits application command and returns new command data.
// appID       : The application ID.
// cmdID       : Application command ID to edit.
// guildID     : Guild ID to edit guild-specific application command. If empty - edits global application command.
// cmd         : Updated application command data.
func (s *Session) ApplicationCommandEdit(appID, guildID, cmdID string, cmd *ApplicationCommand) (updated *ApplicationCommand, err error) {
	endpoint := EndpointApplicationGlobalCommand(appID, cmdID)
	if guildID != "" {
		endpoint = EndpointApplicationGuildCommand(appID, guildID, cmdID)
	}

	body, err := s.RequestWithBucketID("PATCH", endpoint, *cmd, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &updated)

	return
}

// ApplicationCommandBulkOverwrite Creates commands overwriting existing commands. Returns a list of commands.
// appID    : The application ID.
// commands : The commands to create.
func (s *Session) ApplicationCommandBulkOverwrite(appID string, guildID string, commands []*ApplicationCommand) (createdCommands []*ApplicationCommand, err error) {
	endpoint := EndpointApplicationGlobalCommands(appID)
	if guildID != "" {
		endpoint = EndpointApplicationGuildCommands(appID, guildID)
	}

	body, err := s.RequestWithBucketID("PUT", endpoint, commands, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &createdCommands)

	return
}

// ApplicationCommandDelete deletes application command by ID.
// appID       : The application ID.
// cmdID       : Application command ID to delete.
// guildID     : Guild ID to delete guild-specific application command. If empty - deletes global application command.
func (s *Session) ApplicationCommandDelete(appID, guildID, cmdID string) error {
	endpoint := EndpointApplicationGlobalCommand(appID, cmdID)
	if guildID != "" {
		endpoint = EndpointApplicationGuildCommand(appID, guildID, cmdID)
	}

	_, err := s.RequestWithBucketID("DELETE", endpoint, nil, endpoint)

	return err
}

// ApplicationCommand retrieves an application command by given ID.
// appID       : The application ID.
// cmdID       : Application command ID.
// guildID     : Guild ID to retrieve guild-specific application command. If empty - retrieves global application command.
func (s *Session) ApplicationCommand(appID, guildID, cmdID string) (cmd *ApplicationCommand, err error) {
	endpoint := EndpointApplicationGlobalCommand(appID, cmdID)
	if guildID != "" {
		endpoint = EndpointApplicationGuildCommand(appID, guildID, cmdID)
	}

	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &cmd)

	return
}

// ApplicationCommands retrieves all commands in application.
// appID       : The application ID.
// guildID     : Guild ID to retrieve all guild-specific application commands. If empty - retrieves global application commands.
func (s *Session) ApplicationCommands(appID, guildID string) (cmd []*ApplicationCommand, err error) {
	endpoint := EndpointApplicationGlobalCommands(appID)
	if guildID != "" {
		endpoint = EndpointApplicationGuildCommands(appID, guildID)
	}

	body, err := s.RequestWithBucketID("GET", endpoint+"?with_localizations=true", nil, "GET "+endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &cmd)

	return
}

// GuildApplicationCommandsPermissions returns permissions for application commands in a guild.
// appID       : The application ID
// guildID     : Guild ID to retrieve application commands permissions for.
func (s *Session) GuildApplicationCommandsPermissions(appID, guildID string) (permissions []*GuildApplicationCommandPermissions, err error) {
	endpoint := EndpointApplicationCommandsGuildPermissions(appID, guildID)

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &permissions)
	return
}

// ApplicationCommandPermissions returns all permissions of an application command
// appID       : The Application ID
// guildID     : The guild ID containing the application command
// cmdID       : The command ID to retrieve the permissions of
func (s *Session) ApplicationCommandPermissions(appID, guildID, cmdID string) (permissions *GuildApplicationCommandPermissions, err error) {
	endpoint := EndpointApplicationCommandPermissions(appID, guildID, cmdID)

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &permissions)
	return
}

// ApplicationCommandPermissionsEdit edits the permissions of an application command
// appID       : The Application ID
// guildID     : The guild ID containing the application command
// cmdID       : The command ID to edit the permissions of
// permissions : An object containing a list of permissions for the application command
//
// NOTE: Requires OAuth2 token with applications.commands.permissions.update scope
func (s *Session) ApplicationCommandPermissionsEdit(appID, guildID, cmdID string, permissions *ApplicationCommandPermissionsList) (err error) {
	endpoint := EndpointApplicationCommandPermissions(appID, guildID, cmdID)

	_, err = s.RequestWithBucketID("PUT", endpoint, permissions, endpoint)
	return
}

// ApplicationCommandPermissionsBatchEdit edits the permissions of a batch of commands
// appID       : The Application ID
// guildID     : The guild ID to batch edit commands of
// permissions : A list of permissions paired with a command ID, guild ID, and application ID per application command
//
// NOTE: This endpoint has been disabled with updates to command permissions (Permissions v2). Please use ApplicationCommandPermissionsEdit instead.
func (s *Session) ApplicationCommandPermissionsBatchEdit(appID, guildID string, permissions []*GuildApplicationCommandPermissions) (err error) {
	endpoint := EndpointApplicationCommandsGuildPermissions(appID, guildID)

	_, err = s.RequestWithBucketID("PUT", endpoint, permissions, endpoint)
	return
}

// InteractionRespond creates the response to an interaction.
// interaction : Interaction instance.
// resp        : Response message data.
func (s *Session) InteractionRespond(interaction *Interaction, resp *InteractionResponse) error {
	endpoint := EndpointInteractionResponse(interaction.ID, interaction.Token)

	if resp.Data != nil && len(resp.Data.Files) > 0 {
		contentType, body, err := MultipartBodyWithJSON(resp, resp.Data.Files)
		if err != nil {
			return err
		}

		_, err = s.request("POST", endpoint, contentType, body, endpoint, 0)
		return err
	}

	_, err := s.RequestWithBucketID("POST", endpoint, *resp, endpoint)
	return err
}

// InteractionResponse gets the response to an interaction.
// interaction : Interaction instance.
func (s *Session) InteractionResponse(interaction *Interaction) (*Message, error) {
	return s.WebhookMessage(interaction.AppID, interaction.Token, "@original")
}

// InteractionResponseEdit edits the response to an interaction.
// interaction : Interaction instance.
// newresp     : Updated response message data.
func (s *Session) InteractionResponseEdit(interaction *Interaction, newresp *WebhookEdit) (*Message, error) {
	return s.WebhookMessageEdit(interaction.AppID, interaction.Token, "@original", newresp)
}

// InteractionResponseDelete deletes the response to an interaction.
// interaction : Interaction instance.
func (s *Session) InteractionResponseDelete(interaction *Interaction) error {
	endpoint := EndpointInteractionResponseActions(interaction.AppID, interaction.Token)

	_, err := s.RequestWithBucketID("DELETE", endpoint, nil, endpoint)

	return err
}

// FollowupMessageCreate creates the followup message for an interaction.
// interaction : Interaction instance.
// wait        : Waits for server confirmation of message send and ensures that the return struct is populated (it is nil otherwise)
// data        : Data of the message to send.
func (s *Session) FollowupMessageCreate(interaction *Interaction, wait bool, data *WebhookParams) (*Message, error) {
	return s.WebhookExecute(interaction.AppID, interaction.Token, wait, data)
}

// FollowupMessageEdit edits a followup message of an interaction.
// interaction : Interaction instance.
// messageID   : The followup message ID.
// data        : Data to update the message
func (s *Session) FollowupMessageEdit(interaction *Interaction, messageID string, data *WebhookEdit) (*Message, error) {
	return s.WebhookMessageEdit(interaction.AppID, interaction.Token, messageID, data)
}

// FollowupMessageDelete deletes a followup message of an interaction.
// interaction : Interaction instance.
// messageID   : The followup message ID.
func (s *Session) FollowupMessageDelete(interaction *Interaction, messageID string) error {
	return s.WebhookMessageDelete(interaction.AppID, interaction.Token, messageID)
}

type InteractData struct {
	// The type of interaction.
	Type int

	// The application ID
	ApplicationID string

	// The guild ID
	GuildID string

	// The channel ID
	ChannelID string

	// The message of the interaction.
	Message *Message

	// The data to send with the interaction.
	Data any

	// Whether the interaction is a slash command.
	IsSlash bool
}

// Interact creates a new interaction.
func (s *Session) Interact(interactData *InteractData) error {
	payload := map[string]any{
		"application_id": interactData.ApplicationID,
		"channel_id":     interactData.ChannelID,
		"data":           interactData.Data,
		"nonce":          GenerateNonce(),
		"session_id":     s.State.SessionID,
		"type":           interactData.Type,
	}

	if interactData.GuildID != "" {
		payload["guild_id"] = interactData.GuildID
	}

	if interactData.Message != nil {
		msg := interactData.Message
		payload["application_id"] = msg.Author.ID
		payload["channel_id"] = msg.ChannelID
		payload["message_flags"] = int(msg.Flags)
		payload["message_id"] = msg.ID

		if msg.GuildID != "" {
			payload["guild_id"] = msg.GuildID
		}
	}

	if interactData.IsSlash {
		payload["analytics_location"] = "slash_ui"
	}

	_, err := s.RequestWithBucketID("POST", EndpointInteractions, payload, EndpointInteractions)
	return err
}

// InteractionClick sends a click interaction.
// DEPRECATED: Use Interact instead.
func (s *Session) InteractionClick(button *Button, message *Message) error {
	appID := message.Author.ID

	if message.ApplicationID != "" {
		appID = message.ApplicationID
	}

	data := InteractionClick{
		ApplicationID: appID,
		ChannelID:     message.ChannelID,
		Data: InteractionClickData{
			ComponentType: 2,
			CustomID:      button.CustomID,
		},
		GuildID:      message.GuildID,
		MessageFlags: 0,
		MessageID:    message.ID,
		Nonce:        GenerateNonce(),
		SessionID:    s.State.SessionID,
		Type:         3,
	}

	_, err := s.RequestWithBucketID("POST", EndpointInteractions, data, EndpointInteractions)
	return err
}

// DEPRECATED: Use Interact instead.
func (s *Session) InteractionSubmitModal(message *Message, modal *InteractionModalCreate, customID, value string) error {
	data := ModalSubmit{
		Type:          InteractionModalSubmit,
		ApplicationID: message.Author.ID,
		ChannelID:     message.ChannelID,
		GuildID:       message.GuildID,
		Data: ModalSubmitData{
			ID:       modal.ID,
			CustomID: modal.CustomID,
			Components: []ActionsRow{
				{
					Components: []MessageComponent{
						TextInput{
							CustomID: customID,
							Value:    value,
						},
					},
				},
			},
		},
		SessionID: s.State.SessionID,
		Nonce:     GenerateNonce(),
	}

	_, err := s.RequestWithBucketID("POST", EndpointInteractions, data, EndpointInteractions)
	return err
}

// ------------------------------------------------------------------------------------------------
// Functions specific to stage instances
// ------------------------------------------------------------------------------------------------

// StageInstanceCreate creates and returns a new Stage instance associated to a Stage channel.
// data : Parameters needed to create a stage instance.
// data : The data of the Stage instance to create
func (s *Session) StageInstanceCreate(data *StageInstanceParams) (si *StageInstance, err error) {
	body, err := s.RequestWithBucketID("POST", EndpointStageInstances, data, EndpointStageInstances)
	if err != nil {
		return
	}

	err = unmarshal(body, &si)
	return
}

// StageInstance will retrieve a Stage instance by ID of the Stage channel.
// channelID : The ID of the Stage channel
func (s *Session) StageInstance(channelID string) (si *StageInstance, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointStageInstance(channelID), nil, EndpointStageInstance(channelID))
	if err != nil {
		return
	}

	err = unmarshal(body, &si)
	return
}

// StageInstanceEdit will edit a Stage instance by ID of the Stage channel.
// channelID : The ID of the Stage channel
// data : The data to edit the Stage instance
func (s *Session) StageInstanceEdit(channelID string, data *StageInstanceParams) (si *StageInstance, err error) {
	body, err := s.RequestWithBucketID("PATCH", EndpointStageInstance(channelID), data, EndpointStageInstance(channelID))
	if err != nil {
		return
	}

	err = unmarshal(body, &si)
	return
}

// StageInstanceDelete will delete a Stage instance by ID of the Stage channel.
// channelID : The ID of the Stage channel
func (s *Session) StageInstanceDelete(channelID string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointStageInstance(channelID), nil, EndpointStageInstance(channelID))
	return
}

// ------------------------------------------------------------------------------------------------
// Functions specific to guilds scheduled events
// ------------------------------------------------------------------------------------------------

// GuildScheduledEvents returns an array of GuildScheduledEvent for a guild
// guildID        : The ID of a Guild
// userCount      : Whether to include the user count in the response
func (s *Session) GuildScheduledEvents(guildID string, userCount bool) (st []*GuildScheduledEvent, err error) {
	uri := EndpointGuildScheduledEvents(guildID)
	if userCount {
		uri += "?with_user_count=true"
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointGuildScheduledEvents(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildScheduledEvent returns a specific GuildScheduledEvent in a guild
// guildID        : The ID of a Guild
// eventID        : The ID of the event
// userCount      : Whether to include the user count in the response
func (s *Session) GuildScheduledEvent(guildID, eventID string, userCount bool) (st *GuildScheduledEvent, err error) {
	uri := EndpointGuildScheduledEvent(guildID, eventID)
	if userCount {
		uri += "?with_user_count=true"
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointGuildScheduledEvent(guildID, eventID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildScheduledEventCreate creates a GuildScheduledEvent for a guild and returns it
// guildID   : The ID of a Guild
// eventID   : The ID of the event
func (s *Session) GuildScheduledEventCreate(guildID string, event *GuildScheduledEventParams) (st *GuildScheduledEvent, err error) {
	body, err := s.RequestWithBucketID("POST", EndpointGuildScheduledEvents(guildID), event, EndpointGuildScheduledEvents(guildID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildScheduledEventEdit updates a specific event for a guild and returns it.
// guildID   : The ID of a Guild
// eventID   : The ID of the event
func (s *Session) GuildScheduledEventEdit(guildID, eventID string, event *GuildScheduledEventParams) (st *GuildScheduledEvent, err error) {
	body, err := s.RequestWithBucketID("PATCH", EndpointGuildScheduledEvent(guildID, eventID), event, EndpointGuildScheduledEvent(guildID, eventID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildScheduledEventDelete deletes a specific GuildScheduledEvent in a guild
// guildID   : The ID of a Guild
// eventID   : The ID of the event
func (s *Session) GuildScheduledEventDelete(guildID, eventID string) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointGuildScheduledEvent(guildID, eventID), nil, EndpointGuildScheduledEvent(guildID, eventID))
	return
}

// GuildScheduledEventUsers returns an array of GuildScheduledEventUser for a particular event in a guild
// guildID    : The ID of a Guild
// eventID    : The ID of the event
// limit      : The maximum number of users to return (Max 100)
// withMember : Whether to include the member object in the response
// beforeID   : If is not empty all returned users entries will be before the given ID
// afterID    : If is not empty all returned users entries will be after the given ID
func (s *Session) GuildScheduledEventUsers(guildID, eventID string, limit int, withMember bool, beforeID, afterID string) (st []*GuildScheduledEventUser, err error) {
	uri := EndpointGuildScheduledEventUsers(guildID, eventID)

	queryParams := url.Values{}
	if withMember {
		queryParams.Set("with_member", "true")
	}
	if limit > 0 {
		queryParams.Set("limit", strconv.Itoa(limit))
	}
	if beforeID != "" {
		queryParams.Set("before", beforeID)
	}
	if afterID != "" {
		queryParams.Set("after", afterID)
	}

	if len(queryParams) > 0 {
		uri += "?" + queryParams.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointGuildScheduledEventUsers(guildID, eventID))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ----------------------------------------------------------------------
// Functions specific to auto moderation
// ----------------------------------------------------------------------

// AutoModerationRules returns a list of auto moderation rules.
// guildID : ID of the guild
func (s *Session) AutoModerationRules(guildID string) (st []*AutoModerationRule, err error) {
	endpoint := EndpointGuildAutoModerationRules(guildID)

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// AutoModerationRule returns an auto moderation rule.
// guildID : ID of the guild
// ruleID  : ID of the auto moderation rule
func (s *Session) AutoModerationRule(guildID, ruleID string) (st *AutoModerationRule, err error) {
	endpoint := EndpointGuildAutoModerationRule(guildID, ruleID)

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// AutoModerationRuleCreate creates an auto moderation rule with the given data and returns it.
// guildID : ID of the guild
// rule    : Rule data
func (s *Session) AutoModerationRuleCreate(guildID string, rule *AutoModerationRule) (st *AutoModerationRule, err error) {
	endpoint := EndpointGuildAutoModerationRules(guildID)

	var body []byte
	body, err = s.RequestWithBucketID("POST", endpoint, rule, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// AutoModerationRuleEdit edits and returns the updated auto moderation rule.
// guildID : ID of the guild
// ruleID  : ID of the auto moderation rule
// rule    : New rule data
func (s *Session) AutoModerationRuleEdit(guildID, ruleID string, rule *AutoModerationRule) (st *AutoModerationRule, err error) {
	endpoint := EndpointGuildAutoModerationRule(guildID, ruleID)

	var body []byte
	body, err = s.RequestWithBucketID("PATCH", endpoint, rule, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// AutoModerationRuleDelete deletes an auto moderation rule.
// guildID : ID of the guild
// ruleID  : ID of the auto moderation rule
func (s *Session) AutoModerationRuleDelete(guildID, ruleID string) (err error) {
	endpoint := EndpointGuildAutoModerationRule(guildID, ruleID)
	_, err = s.RequestWithBucketID("DELETE", endpoint, nil, endpoint)
	return
}

// ApplicationRoleConnectionMetadata returns application role connection metadata.
// appID : ID of the application
func (s *Session) ApplicationRoleConnectionMetadata(appID string) (st []*ApplicationRoleConnectionMetadata, err error) {
	endpoint := EndpointApplicationRoleConnectionMetadata(appID)
	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ApplicationRoleConnectionMetadataUpdate updates and returns application role connection metadata.
// appID    : ID of the application
// metadata : New metadata
func (s *Session) ApplicationRoleConnectionMetadataUpdate(appID string, metadata []*ApplicationRoleConnectionMetadata) (st []*ApplicationRoleConnectionMetadata, err error) {
	endpoint := EndpointApplicationRoleConnectionMetadata(appID)
	var body []byte
	body, err = s.RequestWithBucketID("PUT", endpoint, metadata, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// UserApplicationRoleConnection returns user role connection to the specified application.
// appID : ID of the application
func (s *Session) UserApplicationRoleConnection(appID string) (st *ApplicationRoleConnection, err error) {
	endpoint := EndpointUserApplicationRoleConnection(appID)
	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// UserApplicationRoleConnectionUpdate updates and returns user role connection to the specified application.
// appID      : ID of the application
// connection : New ApplicationRoleConnection data
func (s *Session) UserApplicationRoleConnectionUpdate(appID string, rconn *ApplicationRoleConnection) (st *ApplicationRoleConnection, err error) {
	endpoint := EndpointUserApplicationRoleConnection(appID)
	var body []byte
	body, err = s.RequestWithBucketID("PUT", endpoint, rconn, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ----------------------------------------------------------------------
// Functions specific to authentication
// ----------------------------------------------------------------------

// LoginSessions returns an array of currently logged in sessions for the current user.
func (s *Session) LoginSessions() (sessions []*LoginSession, err error) {
	var sessionData struct {
		UserSessions []*LoginSession `json:"user_sessions"`
	}

	var body []byte
	body, err = s.RequestWithBucketID("GET", EndpointSessions, nil, EndpointSessions)
	if err != nil {
		return
	}

	err = unmarshal(body, &sessionData)
	if err != nil {
		return
	}

	sessions = sessionData.UserSessions
	return
}
