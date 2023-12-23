package discordgo

import (
	"encoding/base64"
	"github.com/goccy/go-json"
)

// Get super properties
func (s *Session) GetSuperProperties() string {
	superPropertiesJson, err := json.Marshal(s.Identify.Properties)
	if err != nil {
		return ""
	}

	return base64.StdEncoding.EncodeToString(superPropertiesJson)
}
