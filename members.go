package discordgo

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Structs

type Operator struct {
	Op    string      `json:"op"`
	Range []int       `json:"range,omitempty"`
	Items []*SyncItem `json:"items,omitempty"`
	Index int         `json:"index,omitempty"`
	Item  *SyncItem   `json:"item,omitempty"`
}

type SyncItem struct {
	Group  *SyncItemGroup `json:"group,omitempty"`
	Member *Member        `json:"member,omitempty"`
}

type SyncItemGroup struct {
	GroupID     string `json:"id"`
	MemberCount int    `json:"count"`
}

// Websocket opcodes

const (
	OP_REQUEST_MEMBERS = 8
	OP_GUILD_SUBSCRIBE = 14
)

// Websocket payloads

type QueryGuildMembersPayloadData struct {
	GuildID   []string `json:"guild_id"`
	Query     *string  `json:"query"`
	UserIDs   *[]int   `json:"user_ids"`
	Limit     *int     `json:"limit"`
	Nonce     string   `json:"nonce"`
	Presences bool     `json:"presences"`
}

type QueryGuildMembersPayload struct {
	Op   int                          `json:"op"`
	Data QueryGuildMembersPayloadData `json:"d"`
}

// Fetch guild members

type FetchGuildMembersParams struct {
	GuildID       string
	ChannelIDs    []string
	Cache         bool
	ForceScraping bool
	Delay         time.Duration
	Limit         int
}

func NewFetchGuildMembersParams(guildID string) FetchGuildMembersParams {
	return FetchGuildMembersParams{
		GuildID:       guildID,
		ChannelIDs:    make([]string, 0),
		Cache:         true,
		ForceScraping: false,
		Delay:         time.Second,
		Limit:         0,
	}
}

func (s *Session) FetchGuildMembers(params FetchGuildMembersParams) ([]*Member, error) {
	guild, err := s.State.Guild(params.GuildID)
	if err != nil {
		return nil, err
	}

	self, err := s.State.Member(guild.ID, s.State.User.ID)
	if err != nil {
		params := NewQueryGuildMembersParams(guild.ID)

		idInt, _ := strconv.Atoi(s.State.User.ID)
		params.UserIDs = []int{idInt}

		membersList, err := s.QueryGuildMembers(params)
		if err != nil {
			return nil, err
		}

		if len(membersList) == 0 {
			return nil, fmt.Errorf("could not find self in guild")
		}

		self = membersList[0]
	}

	if len(guild.Channels) == 0 {
		return nil, fmt.Errorf("guild has no channels")
	}

	permissions := memberPermissions(guild, guild.Channels[0], self.User.ID, self.Roles)

	kickMembers := permissions&PermissionKickMembers != 0
	banMembers := permissions&PermissionBanMembers != 0
	manageRoles := permissions&PermissionManageRoles != 0

	hasAnyPermission := kickMembers || banMembers || manageRoles

	if !params.ForceScraping && hasAnyPermission {
		queryParams := NewQueryGuildMembersParams(guild.ID)
		queryParams.Limit = params.Limit
		queryParams.Cache = params.Cache
		queryParams.Query = ""

		members, err := s.QueryGuildMembers(queryParams)
		if err != nil {
			return nil, err
		}

		return members, nil
	}

	memberSidebar := MemberSidebar{
		Session: s,
		Guild:   guild,
		Self:    self,
		Delay:   params.Delay,
		Limit:   params.Limit,
	}

	members, err := memberSidebar.GetMembers()
	if err != nil {
		return nil, err
	}

	if params.Cache {
		for _, member := range members {
			member.GuildID = guild.ID
			s.State.MemberAdd(member)
		}
	}

	return members, err
}

func (s *Session) QueryMember(guildID, userID string, reload bool, iteration ...int) (*Member, error) {
	i := 0
	if len(iteration) > 0 {
		i = iteration[0]
	}

	member, err := s.State.Member(guildID, userID)
	if err == nil && !reload {
		return member, nil
	}

	userIDInt, err := strconv.Atoi(userID)
	if err != nil {
		return nil, err
	}

	params := NewQueryGuildMembersParams(guildID)
	params.UserIDs = []int{userIDInt}

	members, err := s.QueryGuildMembers(params)
	if err != nil {
		return nil, err
	}

	if len(members) == 0 && i < 3 {
		// User is not in this server, find mutual servers
		profile, err := s.UserProfile(userID)
		if err != nil {
			return nil, err
		}

		if len(profile.MutualGuilds) == 0 {
			// No mutuals, can't get member
			return nil, errors.New("no mutual guilds")
		}

		return s.QueryMember(profile.MutualGuilds[0].ID, userID, reload, i+1)
	}

	member = members[0]
	if member == nil {
		return nil, errors.New("member not found")
	}

	return member, nil
}

// Query guild members

type QueryGuildMembersParams struct {
	GuildID   string
	Query     string
	Limit     int
	UserIDs   []int
	Presences bool
	Cache     bool
	Subscribe bool
}

func NewQueryGuildMembersParams(guildID string) QueryGuildMembersParams {
	return QueryGuildMembersParams{
		GuildID:   guildID,
		Query:     "empty",
		Limit:     5,
		Presences: true,
		Cache:     true,
		Subscribe: false,
	}
}

func (s *Session) QueryGuildMembers(params QueryGuildMembersParams) ([]*Member, error) {
	nonce := GenerateNonce()

	data := QueryGuildMembersPayloadData{
		GuildID:   []string{params.GuildID},
		Presences: params.Presences,
		Nonce:     nonce,
	}

	if params.Query != "empty" {
		data.Query = &params.Query
	}

	if len(params.UserIDs) > 0 {
		data.UserIDs = &params.UserIDs
	}

	if params.Limit >= 0 {
		data.Limit = &params.Limit
	}

	payload := QueryGuildMembersPayload{Op: OP_REQUEST_MEMBERS, Data: data}

	err := s.SendWsData(payload)
	if err != nil {
		return nil, err
	}

	ch := make(chan *GuildMembersChunk, 1)

	members := []*Member{}

	removeHandler := s.AddHandler(func(_ *Session, event *GuildMembersChunk) {
		if event.Nonce != nonce {
			return
		}

		for _, member := range event.Members {
			for _, presence := range event.Presences {
				if presence.User.ID == member.User.ID {
					member.Presence = presence
				}
			}
		}

		members = append(members, event.Members...)

		if event.ChunkIndex == event.ChunkCount-1 {
			ch <- event
		}
	})

	select {
	case <-ch:
		removeHandler()

		if params.Subscribe {
			// TODO: implement subscribe
			return nil, fmt.Errorf("subscribe is not supported yet")
		}

		if params.Cache {
			for _, member := range members {
				member.GuildID = params.GuildID
				s.State.MemberAdd(member)
			}
		}

		return members, nil
	case <-time.After(10 * time.Second):
		removeHandler()

		return nil, fmt.Errorf("timeout")
	}
}

// Scraping member sidebar

type MemberSidebar struct {
	Session *Session
	Guild   *Guild
	Self    *Member
	Delay   time.Duration

	MemberCount int
	OnlineCount int
	RoleCount   int

	Members            map[string]*Member
	MembersMutex       sync.Mutex
	Ranges             [][]int
	Channels           []string
	SubscribingStarted bool
	SubscribingDone    bool
	LastSync           time.Time
	Safe               bool
	Limit              int
}

func (m *MemberSidebar) GetKnownGoodChannels() []string {
	var channels []string

	if m.Guild.RulesChannelID != "" {
		channels = append(channels, m.Guild.RulesChannelID)
	}

	if m.Guild.SystemChannelID != "" {
		systemChannel, err := m.Session.State.Channel(m.Guild.SystemChannelID)
		if err == nil {
			permissions := memberPermissions(m.Guild, systemChannel, m.Session.State.User.ID, m.Self.Roles)

			if permissions&PermissionReadMessages != 0 {
				channels = append(channels, m.Guild.SystemChannelID)
			}
		}
	}

	return channels
}

func (m *MemberSidebar) GetSelfAndRoleReadableChannels(roleID string) []string {
	var channels []string

	role, err := m.Session.State.Role(m.Guild.ID, roleID)
	if err != nil {
		return channels
	}

	for _, channel := range m.Guild.Channels {
		if channel.Type != ChannelTypeGuildText || channel.ID == m.Guild.RulesChannelID {
			continue
		}

		permissions := memberPermissions(m.Guild, channel, m.Session.State.User.ID, m.Self.Roles)

		if permissions&PermissionReadMessages == 0 {
			continue
		}

		permissions = role.Permissions

		if channel.PermissionOverwrites != nil {
			for _, overwrite := range channel.PermissionOverwrites {
				if overwrite.Type != PermissionOverwriteTypeRole || overwrite.ID != roleID {
					continue
				}

				permissions |= overwrite.Allow
				permissions &^= overwrite.Deny
			}
		}

		if permissions&PermissionReadMessages != 0 {
			channels = append(channels, channel.ID)
		}
	}

	return channels
}

func stringContainsI(s string, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func removeDuplicates(strSlice []string) []string {
	allKeys := make(map[string]bool)
	list := []string{}

	for _, item := range strSlice {
		if _, value := allKeys[item]; !value {
			allKeys[item] = true
			list = append(list, item)
		}
	}

	return list
}

func (m *MemberSidebar) GetChannels() []string {
	channels := m.GetKnownGoodChannels()

	everyoneReadableChannels := m.GetSelfAndRoleReadableChannels(m.Guild.ID)

	if len(everyoneReadableChannels) > 0 {
		channels = append(channels, everyoneReadableChannels...)
	} else {
		// @everyone can't read channels, so we need to find a role that can

		for _, role := range m.Guild.Roles {
			verified := stringContainsI(role.Name, "verified")
			member := stringContainsI(role.Name, "member")
			user := stringContainsI(role.Name, "user")

			if !verified && !member && !user {
				continue
			}

			channels = append(channels, m.GetSelfAndRoleReadableChannels(role.ID)...)
		}
	}

	channels = removeDuplicates(channels)

	channelsByPermission := make(map[int64][]string)

	// Sort channels by permission
	for _, channelID := range channels {
		channel, err := m.Session.State.Channel(channelID)
		if err != nil {
			continue
		}

		permissions := memberPermissions(m.Guild, channel, m.Session.State.User.ID, m.Self.Roles)

		channelsByPermission[permissions] = append(channelsByPermission[permissions], channelID)
	}

	// Get the permission with the most channels
	var maxPermission int64

	for permission, channels := range channelsByPermission {
		if len(channels) > len(channelsByPermission[maxPermission]) {
			maxPermission = permission
		}
	}

	channels = channelsByPermission[maxPermission]

	if len(channels) > 5 {
		channels = channels[:5]
	}

	return channels
}

func (m *MemberSidebar) GetLimit() int {
	if m.MemberCount < 1000 {
		return m.MemberCount
	}

	return m.OnlineCount
}

func (m *MemberSidebar) GetRanges() [][]int {
	var ranges [][]int

	membersPerRequest := 100
	if m.Safe {
		membersPerRequest = 300
	}

	requests := math.Ceil(float64(m.Limit)/float64(membersPerRequest)) - 1

	current := membersPerRequest

	for i := 0; i < int(requests); i++ {
		ranges = append(ranges, []int{current, current + membersPerRequest - 1})

		current += membersPerRequest
	}

	return ranges
}

func (m *MemberSidebar) GetCurrentRanges() map[string][][]int {
	requestsPerChannel := 3

	currentRanges := make(map[string][][]int)

	currentRange := 0

	for i := 0; i < len(m.Channels); i++ {
		channel := m.Channels[i]

		for j := 0; j < requestsPerChannel; j++ {
			if currentRange >= len(m.Ranges) {
				break
			}

			currentRanges[channel] = append(currentRanges[channel], m.Ranges[currentRange])

			currentRange++
		}
	}

	m.Ranges = m.Ranges[currentRange:]

	return currentRanges
}

func (m *MemberSidebar) StartSubscribing() {
	m.SubscribingStarted = true
	m.Ranges = m.GetRanges()

	for {
		if len(m.Ranges) == 0 {
			break
		}

		currentRanges := m.GetCurrentRanges()

		m.Session.RequestLazyGuild(RequestLazyGuildData{
			GuildID:    m.Guild.ID,
			Channels:   currentRanges,
			Typing:     true,
			Threads:    false,
			Activities: true,
		})

		time.Sleep(m.Delay)
	}

	m.SubscribingDone = true
}

func (m *MemberSidebar) HandleMemberListUpdate(event *GuildMemberListUpdate) {
	if event.MemberCount != 0 {
		m.MemberCount = event.MemberCount
	}

	if event.OnlineCount != 0 {
		m.OnlineCount = event.OnlineCount
	}

	if m.Limit == 0 {
		m.Limit = m.GetLimit()

		go func() {
			defer m.Session.ErrorChecker()

			m.StartSubscribing()
		}()
	}

	for _, op := range event.Ops {
		if op.Op != "SYNC" {
			continue
		}

		m.LastSync = time.Now()

		m.MembersMutex.Lock()

		for _, item := range op.Items {
			if item.Member != nil {
				m.Members[item.Member.User.ID] = item.Member
			}

			if item.Group != nil {
				m.RoleCount++
			}
		}

		m.MembersMutex.Unlock()
	}
}

func (m *MemberSidebar) GetMembers() ([]*Member, error) {
	m.Members = make(map[string]*Member)
	m.Channels = m.GetChannels()
	m.Safe = m.Guild.MemberCount < 75000

	if len(m.Channels) == 0 {
		return nil, fmt.Errorf("no channels found")
	}

	initialRanges := [][]int{{0, 99}, {100, 199}, {200, 299}}

	if m.Safe {
		initialRanges = [][]int{{0, 299}, {300, 599}, {600, 899}}
	}

	m.Session.activeGuildSubscriptionsMutex.Lock()
	m.Session.activeGuildSubscriptions[m.Guild.ID] = true
	m.Session.activeGuildSubscriptionsMutex.Unlock()

	removeHandler := m.Session.AddHandler(func(_ *Session, event *GuildMemberListUpdate) {
		if event.GuildID != m.Guild.ID {
			return
		}

		m.HandleMemberListUpdate(event)
	})

	m.Session.RequestLazyGuild(RequestLazyGuildData{
		GuildID: m.Guild.ID,
		Channels: map[string][][]int{
			m.Channels[0]: initialRanges,
		},
		Typing:            true,
		Threads:           false,
		Activities:        true,
		Members:           &[]string{},
		ThreadMemberLists: &[]string{},
	})

	m.LastSync = time.Now()

	for {
		if time.Since(m.LastSync) > time.Second*3 {
			if !m.SubscribingStarted {
				if m.Limit == 0 {
					m.Limit = m.GetLimit()
					if m.Limit == 0 {
						m.Limit = 10000
					}
				}

				go func() {
					defer m.Session.ErrorChecker()

					m.StartSubscribing()
				}()
			}

			if !m.SubscribingDone {
				continue
			}
			break
		}

		// TODO: why the fuck does this happen?
		// guild.Name becomes empty when m.Safe is false
		// guild.Properties.Name is still fine
		// always happens between 200 and 300ms
		if m.Guild.Properties != nil {
			if m.Guild.Name != m.Guild.Properties.Name {
				m.Guild.Name = m.Guild.Properties.Name
			}
		}

		time.Sleep(time.Millisecond * 100)
	}

	removeHandler()

	m.Session.activeGuildSubscriptionsMutex.Lock()
	m.Session.activeGuildSubscriptions[m.Guild.ID] = false
	m.Session.activeGuildSubscriptionsMutex.Unlock()

	members := make([]*Member, 0, len(m.Members))

	for _, member := range m.Members {
		members = append(members, member)
	}

	return members, nil
}
