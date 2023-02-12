package discordgo

import "math/rand"

// GenerateNonce generates a random nonce string for use in the Discord API.
func GenerateNonce() string {
	alphabet := "0123456789"
	b := make([]byte, 20)
	for i := range b {
		b[i] = alphabet[rand.Intn(len(alphabet))]
	}
	return string(b)
}
