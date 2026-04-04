package tui

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed emoji.json
var emojiData []byte

type emojiEntry struct {
	Emoji   string   `json:"emoji"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
}

var allEmoji []emojiEntry

func init() {
	json.Unmarshal(emojiData, &allEmoji)
}

// QuickReactions returns the top 8 emoji for the quick-react bar.
func QuickReactions() []string {
	if len(allEmoji) < 8 {
		return nil
	}
	result := make([]string, 8)
	for i := 0; i < 8; i++ {
		result[i] = allEmoji[i].Emoji
	}
	return result
}

// SearchEmoji returns emoji matching the query (name or alias prefix match).
// Returns up to maxResults results.
func SearchEmoji(query string, maxResults int) []emojiEntry {
	if query == "" {
		// Return first maxResults
		if len(allEmoji) < maxResults {
			return allEmoji
		}
		return allEmoji[:maxResults]
	}

	query = strings.ToLower(query)
	var results []emojiEntry

	for _, e := range allEmoji {
		if len(results) >= maxResults {
			break
		}

		if strings.Contains(e.Name, query) {
			results = append(results, e)
			continue
		}

		for _, alias := range e.Aliases {
			if strings.HasPrefix(alias, query) {
				results = append(results, e)
				break
			}
		}
	}

	return results
}

// EmojiByName returns the emoji string for a name or alias.
func EmojiByName(name string) string {
	name = strings.ToLower(name)
	for _, e := range allEmoji {
		if e.Name == name {
			return e.Emoji
		}
		for _, alias := range e.Aliases {
			if alias == name {
				return e.Emoji
			}
		}
	}
	return ""
}
