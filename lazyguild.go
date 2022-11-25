package discordgo

import (
	"container/list"
	"fmt"
	"math"
	"time"
)

// Lazy Guilds

// Credits:
//     https://github.com/dolfies/discord.py-self
//     https://arandomnewaccount.gitlab.io/discord-unofficial-docs/lazy_guilds.html
//     https://github.com/Merubokkusu/Discord-S.C.U.M

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

// Requesting members
// Guild must be from GuildWithCounts

type MemberRequest struct {
	Guild      *Guild
	StateGuild *Guild

	Overlap  bool
	Limit    int
	Session  *Session
	OnFinish func([]*Member)
	OnError  func(error)

	// ID => Member
	Members map[string]*Member

	HoistedRoleCount int

	rawRanges [][]int
	// List[ChannelID => RawRange ]
	ranges []map[string][][]int

	removeEventHandler func()
}

func (m *MemberRequest) getReadableChannels(everyone bool) map[string]string {
	// Use a map to prevent duplicate channels
	tempChannels := map[string]string{}

	if m.Guild.RulesChannelID != "" {
		tempChannels[m.Guild.RulesChannelID] = m.Guild.RulesChannelID
	}

	me, _ := m.Session.GuildMember(m.Guild.ID, m.Session.State.User.ID)

	for _, channel := range m.StateGuild.Channels {
		if channel.Type == ChannelTypeGuildStageVoice {
			continue
		}
		if me != nil {
			permissions := MemberPermissions(m.StateGuild, channel, me.User.ID, me.Roles)

			// We can't view the channel
			if permissions&PermissionViewChannel == 0 {
				continue
			}
		}

		// Check if everyone can read the channel
		if everyone {
			for _, overwrite := range channel.PermissionOverwrites {
				if overwrite.Type == PermissionOverwriteTypeRole && overwrite.ID == m.Guild.ID {
					if overwrite.Deny&PermissionReadMessages == 1 {
						continue
					}
				}
			}
		}

		tempChannels[channel.ID] = channel.ID
	}
	return tempChannels
}

func (m *MemberRequest) getChannels() []string {
	readableChannels := m.getReadableChannels(true)
	if len(readableChannels) == 0 {
		// If there are no channels readable by everyone,
		// like if the guild has a muted role, only check if we can read the channel.
		readableChannels = m.getReadableChannels(false)
	}
	if len(readableChannels) == 0 {
		m.OnError(fmt.Errorf("no readable channels"))
	}

	channels := make([]string, 0, len(readableChannels))
	for _, channel := range readableChannels {
		channels = append(channels, channel)
	}
	return channels
}

func (m *MemberRequest) getLimitIgnore() int {
	if m.Guild.ApproximateMemberCount > 1000 {
		return m.Guild.ApproximatePresenceCount
	}
	return m.Guild.ApproximateMemberCount
}

func (m *MemberRequest) getLimit() int {
	limitIgnore := m.getLimitIgnore()
	if m.Limit > 0 && m.Limit < limitIgnore {
		return m.Limit
	}
	return limitIgnore
}

func (m *MemberRequest) getRawRanges() [][]int {
	chunk := 100
	end := 99
	amount := m.getLimit()
	if amount == 0 {
		m.OnError(fmt.Errorf("cannot get ranges for a guild with no member/presence count"))
	}
	ceiling := int(math.Ceil(float64(amount)/float64(chunk))) * chunk
	ranges := make([][]int, ceiling/chunk)
	for i := 0; i < ceiling/chunk; i++ {
		min := i * chunk
		max := min + end
		ranges[i] = []int{min, max}
	}
	return ranges
}

func (m *MemberRequest) getRanges() []map[string][][]int {
	m.rawRanges = m.getRawRanges()

	// Iterate over all ranges with step of 2 if overlap is enabled else 3
	var step int
	if m.Overlap {
		step = 2
	} else {
		step = 3
	}

	channels := m.getChannels()
	ranges := []map[string][][]int{}
	rawRanges := [][][]int{}

	// TODO: do this in getRawRanges
	for i := 0; i < len(m.rawRanges); i = i + step {
		currentRange := [][]int{}
		if len(m.rawRanges)-i > 3 {
			currentRange = append(currentRange, m.rawRanges[i:i+3]...)
		} else {
			currentRange = append(currentRange, m.rawRanges[i:]...)
		}
		rawRanges = append(rawRanges, currentRange)
	}

	requestsNeeded := int(math.Ceil(float64(len(rawRanges)) / float64(len(channels))))

	for i := 0; i < requestsNeeded; i++ {
		ranges = append(ranges, map[string][][]int{})
		for channelIndex, channel := range channels {
			currentIndex := i*len(channels) + channelIndex
			if currentIndex >= len(rawRanges) {
				break
			}
			ranges[i][channel] = rawRanges[currentIndex]
		}
	}
	return ranges
}

func (m *MemberRequest) handleUpdate(ops []*Operator) {
	if m.Members == nil {
		m.Members = make(map[string]*Member, 0)
	}
	for _, op := range ops {
		if op.Op == "SYNC" {
			for _, item := range op.Items {
				if item.Member != nil {
					// Account for overlapping ranges
					m.Members[item.Member.User.ID] = item.Member
				} else if item.Group != nil {
					m.HoistedRoleCount++
				}
			}
			// Check if we are close to the limit
			diff := len(m.Members) - m.getLimit() + m.HoistedRoleCount
			if diff >= -5 && diff <= 5 {
				memberList := m.toMemberList()
				m.done(memberList)
				return
			}
		} else if op.Op == "INSERT" {

		} else if op.Op == "UPDATE" {

		}
	}
}

func (m *MemberRequest) toMemberList() []*Member {
	members := make([]*Member, 0, len(m.Members))
	for _, member := range m.Members {
		members = append(members, member)
	}
	return members
}

func (m *MemberRequest) done(memberList []*Member) {
	if m.removeEventHandler != nil {
		m.removeEventHandler()
	}
	// Add members to the state so we don't have to request them again
	for _, member := range memberList {
		member.GuildID = m.Guild.ID
		m.Session.State.MemberAdd(member)
	}
	go m.OnFinish(memberList)
}

func (m *MemberRequest) Start() {
	go func() {
		// Check if we already have the members
		diff := m.getLimit() - len(m.StateGuild.Members) + m.HoistedRoleCount
		diff2 := m.getLimitIgnore() - len(m.StateGuild.Members) + m.HoistedRoleCount
		if (diff >= -5 && diff <= 5) || (diff2 >= -5 && diff2 <= 5) {
			m.done(m.StateGuild.Members)
			return
		}

		m.ranges = m.getRanges()

		m.removeEventHandler = m.Session.AddHandler(func(s *Session, update *GuildMemberListUpdate) {
			if update.GuildID != m.Guild.ID {
				return
			}
			m.handleUpdate(update.Ops)
		})

		for _, ranges := range m.ranges {
			m.Session.RequestLazyGuild(RequestLazyGuildData{
				GuildID:    m.Guild.ID,
				Channels:   ranges,
				Typing:     true,
				Activities: true,
			})
		}
	}()
}

// Easy function to request members

func (s *Session) LazyRequestMembers(guildID string, limit int, overlap bool) ([]*Member, error) {
	guild, err := s.GuildWithCounts(guildID)
	if err != nil {
		return nil, err
	}
	stateGuild, err := s.State.Guild(guild.ID)
	if err != nil {
		return nil, err
	}

	membersChannel := make(chan []*Member)
	errChannel := make(chan error)

	memberRequest := MemberRequest{
		Guild:      guild,
		StateGuild: stateGuild,
		Overlap:    true,
		Limit:      limit,
		Session:    s,
		OnFinish: func(fetchedMembers []*Member) {
			membersChannel <- fetchedMembers
		},
		OnError: func(err error) {
			errChannel <- err
		},
	}
	memberRequest.Start()

	select {
	case members := <-membersChannel:
		return members, nil
	case err := <-errChannel:
		return stateGuild.Members, err
	case <-time.After(5 * time.Second):
		return stateGuild.Members, fmt.Errorf("timeout reached")
	}
}

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////////// Refactor ///////////////////////////////////

type GuildSubscription struct {
	Session *Session
	GuildID string
	Limit   int

	ranges      *list.List
	guild       *Guild
	channelID   string
	memberCount int
	onlineCount int
}

func (g *GuildSubscription) Subscribe() {
	g.ranges = list.New()

	_guild, err := g.Session.State.Guild(g.GuildID)
	if err != nil {
		// Error
		return
	}
	g.guild = _guild

	g.getChannel(true)
	if g.channelID == "" {
		// Error
		return
	}

	receivedFirstUpdate := make(chan struct{})

	removeHandler := g.Session.AddHandler(func(s *Session, update *GuildMemberListUpdate) {
		if update.GuildID != g.GuildID {
			return
		}
		g.memberCount = update.MemberCount
		g.onlineCount = 0
		for _, group := range update.Groups {
			if group.GroupID != "offline" {
				g.onlineCount += group.MemberCount
			}
		}
		if g.memberCount == 0 || g.onlineCount == 0 {
			// Member list update doesn't have member counts so get them using a separate request
			guild, err := g.Session.GuildWithCounts(g.GuildID)
			if err != nil {
				return
			}
			if g.memberCount == 0 {
				g.memberCount = guild.ApproximateMemberCount
			}
			if g.onlineCount == 0 {
				g.onlineCount = guild.ApproximatePresenceCount
			}
		}

		defer func() {
			recover()
		}()

		close(receivedFirstUpdate)
	})

	// Subscribe to the first 100 members
	g.Session.RequestLazyGuild(RequestLazyGuildData{
		GuildID: g.GuildID,
		Channels: map[string][][]int{
			g.channelID: {{0, 99}},
		},
		Typing:     true,
		Activities: true,
	})

	select {
	case <-receivedFirstUpdate:
		removeHandler()
		g.getRanges()
		g.requestRanges()
	case <-time.After(10 * time.Second):
		// Timeout reached
		removeHandler()
		return
	}
}

func (g *GuildSubscription) requestRanges() {
	for {
		currentRanges := g.getCurrentRanges()
		if len(currentRanges) == 1 {
			break
		}
		err := g.Session.RequestLazyGuild(RequestLazyGuildData{
			GuildID: g.GuildID,
			Channels: map[string][][]int{
				g.channelID: currentRanges,
			},
			Typing:     true,
			Activities: true,
		})
		if err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (g *GuildSubscription) getChannel(readableByEveryone bool) {
	if g.guild.RulesChannelID != "" {
		g.channelID = g.guild.RulesChannelID
		return
	}
	me, _ := g.Session.GuildMember(g.guild.ID, g.Session.State.User.ID)
	for _, channel := range g.guild.Channels {
		if channel.Type == ChannelTypeGuildStageVoice {
			continue
		}
		if me != nil {
			permissions := MemberPermissions(g.guild, channel, me.User.ID, me.Roles)

			// We can't view the channel
			if permissions&PermissionViewChannel == 0 {
				continue
			}
		}

		// Check if everyone can read the channel
		if readableByEveryone {
			for _, overwrite := range channel.PermissionOverwrites {
				if overwrite.Type == PermissionOverwriteTypeRole && overwrite.ID == g.guild.ID {
					if overwrite.Deny&PermissionReadMessages == 1 {
						continue
					}
				}
			}
		}

		g.channelID = channel.ID
		return
	}

	g.getChannel(false)
}

func (g *GuildSubscription) getCurrentRanges() [][]int {
	current := [][]int{{0, 99}}
	for i := 0; i < 2; i++ {
		e := g.ranges.Front()
		if e == nil {
			break
		}
		current = append(current, e.Value.([]int))
		g.ranges.Remove(e)
	}
	return current

}

func (g *GuildSubscription) getLimit(ignore bool) int {
	limit := g.memberCount
	if g.memberCount > 1000 {
		limit = g.onlineCount
	}
	if g.Limit > 0 && g.Limit < limit && !ignore {
		return g.Limit
	}
	return limit
}

func (g *GuildSubscription) getRanges() {
	amount := g.getLimit(false)
	if amount == 0 {
		// error
		return
	}
	ceiling := int(math.Ceil(float64(amount)/100)) * 100
	for i := 1; i < ceiling/100; i++ {
		min := i * 100
		max := min + 99
		g.ranges.PushBack([]int{min, max})
	}
}
