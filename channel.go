package discordgo

// CategoryData represents a category in a guild.
// It contains the category channel and a list of channels that belong to it.
type CategoryData struct {
	Category *Channel
	Channels []*Channel
}

func (d *CategoryData) GetCategoryID() string {
	if d.Category != nil {
		return d.Category.ID
	}
	return ""
}

func (d *CategoryData) GetCategoryName() string {
	if d.Category != nil {
		return d.Category.Name
	}
	return "No Category"
}

// MapCategories takes a slice of channels and returns a slice of CategoryData.
func MapCategories(channels []*Channel) []*CategoryData {
	data := make([]*CategoryData, 0)
	channelsByParentID := make(map[string][]*Channel)
	categories := make([]*Channel, 0)

	for _, channel := range channels {
		if channel.Type == ChannelTypeGuildCategory {
			categories = append(categories, channel)
			continue
		}

		channelsByParentID[channel.ParentID] = append(channelsByParentID[channel.ParentID], channel)
	}

	if orphanChannels, ok := channelsByParentID[""]; ok {
		data = append(data, &CategoryData{
			Category: nil,
			Channels: orphanChannels,
		})
	}

	for _, categoryChannel := range categories {
		category := &CategoryData{
			Category: categoryChannel,
			Channels: channelsByParentID[categoryChannel.ID],
		}
		if category.Channels == nil {
			category.Channels = make([]*Channel, 0)
		}
		data = append(data, category)
	}

	return data
}
