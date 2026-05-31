package tui

import (
	_ "embed"
	"encoding/json"
	"sort"
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

var quickReactionEmoji = []string{"👍", "👎", "😂", "❤️", "💯", "😮", "😢", "🔥"}

var emojiCategoryOrder = []string{
	"Smileys & Emotion",
	"People & Body",
	"Animals & Nature",
	"Food & Drink",
	"Activities",
	"Travel & Places",
	"Objects",
	"Symbols",
	"Flags",
}

func init() {
	json.Unmarshal(emojiData, &allEmoji)
}

// QuickReactions returns the fixed ordered emoji for the quick-react bar.
func QuickReactions() []string {
	return append([]string(nil), quickReactionEmoji...)
}

func quickReactionEntries() []emojiEntry {
	byGlyph := make(map[string]emojiEntry, len(allEmoji))
	for _, entry := range allEmoji {
		byGlyph[entry.Emoji] = entry
	}
	entries := make([]emojiEntry, 0, len(quickReactionEmoji))
	for _, glyph := range quickReactionEmoji {
		entry, ok := byGlyph[glyph]
		if !ok {
			entry = emojiEntry{Emoji: glyph, Name: glyph}
		}
		entries = append(entries, entry)
	}
	return entries
}

func emojiEntriesForGlyphs(glyphs []string) []emojiEntry {
	if len(glyphs) == 0 {
		return nil
	}

	wanted := make(map[string]struct{}, len(glyphs))
	for _, glyph := range glyphs {
		if strings.TrimSpace(glyph) != "" {
			wanted[glyph] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	byGlyph := make(map[string]emojiEntry, len(allEmoji))
	for _, entry := range allEmoji {
		byGlyph[entry.Emoji] = entry
	}

	entries := make([]emojiEntry, 0, len(wanted))
	add := func(glyph string) {
		if _, ok := wanted[glyph]; !ok {
			return
		}
		entry, ok := byGlyph[glyph]
		if !ok {
			entry = emojiEntry{Emoji: glyph, Name: glyph}
		}
		entries = append(entries, entry)
		delete(wanted, glyph)
	}

	for _, glyph := range quickReactionEmoji {
		add(glyph)
	}
	for _, entry := range allEmoji {
		add(entry.Emoji)
	}
	if len(wanted) > 0 {
		remaining := make([]string, 0, len(wanted))
		for glyph := range wanted {
			remaining = append(remaining, glyph)
		}
		sort.Strings(remaining)
		for _, glyph := range remaining {
			add(glyph)
		}
	}

	return entries
}

// AllEmoji returns a copy of the embedded emoji catalog.
func AllEmoji() []emojiEntry {
	return append([]emojiEntry(nil), allEmoji...)
}

// FilterEmoji returns every emoji matching the query without a result cap.
func FilterEmoji(query string) []emojiEntry {
	return SearchEmoji(query, 0)
}

// SearchEmoji returns emoji matching the query (name contains or alias prefix
// match). maxResults <= 0 means uncapped.
func SearchEmoji(query string, maxResults int) []emojiEntry {
	if query == "" {
		if maxResults <= 0 || len(allEmoji) < maxResults {
			return AllEmoji()
		}
		return append([]emojiEntry(nil), allEmoji[:maxResults]...)
	}

	query = strings.ToLower(strings.TrimSpace(query))
	var results []emojiEntry

	for _, e := range allEmoji {
		if maxResults > 0 && len(results) >= maxResults {
			break
		}

		if strings.Contains(strings.ToLower(e.Name), query) {
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

func emojiCategory(entry emojiEntry) string {
	emoji := entry.Emoji
	name := strings.ToLower(entry.Name)
	aliases := strings.ToLower(strings.Join(entry.Aliases, " "))
	text := name + " " + aliases

	switch emoji {
	case "👍", "👎", "👏", "🙏", "🙌", "🤷", "🤦", "🤝", "✌️", "🤙", "👋", "🫶", "💪", "🏃", "🧑‍💻", "💅", "👤", "👥":
		return "People & Body"
	case "🐛", "🐍", "🦀", "🐹", "🐶", "🐱", "🦊", "🐻", "🐧", "🦆", "🌈", "☀️", "🌙", "🌊", "🌸", "🍀", "🌻", "🎄":
		return "Animals & Nature"
	case "☕", "🍺", "🍕", "🌮", "🎂":
		return "Food & Drink"
	case "🎉", "🎯", "🏆", "🎵", "🎮", "🎃", "🎁", "🎗️", "🏅", "🥇", "🥈", "🥉":
		return "Activities"
	case "✈️", "🏠", "🏗️", "🚧":
		return "Travel & Places"
	case "✅", "❌", "⭐", "💯", "⚡", "🌟", "⚠️", "🚨", "🟢", "🔴", "🟡", "⬆️", "⬇️", "➡️", "⬅️", "↩️", "🔄", "➕", "➖", "♻️":
		return "Symbols"
	}

	switch {
	case strings.Contains(text, "face"), strings.Contains(text, "heart"), strings.Contains(text, "laugh"), strings.Contains(text, "cry"), strings.Contains(text, "smile"), strings.Contains(text, "thinking"), strings.Contains(text, "shrug"), strings.Contains(text, "ghost"), strings.Contains(text, "poo"), strings.Contains(text, "skull"):
		return "Smileys & Emotion"
	case strings.Contains(text, "hand"), strings.Contains(text, "person"), strings.Contains(text, "bust"), strings.Contains(text, "people"), strings.Contains(text, "brain"):
		return "People & Body"
	case strings.Contains(text, "dog"), strings.Contains(text, "cat"), strings.Contains(text, "animal"), strings.Contains(text, "flower"), strings.Contains(text, "sun"), strings.Contains(text, "moon"):
		return "Animals & Nature"
	case strings.Contains(text, "food"), strings.Contains(text, "drink"), strings.Contains(text, "cake"), strings.Contains(text, "coffee"), strings.Contains(text, "beer"), strings.Contains(text, "pizza"):
		return "Food & Drink"
	case strings.Contains(text, "game"), strings.Contains(text, "medal"), strings.Contains(text, "trophy"), strings.Contains(text, "party"):
		return "Activities"
	case strings.Contains(text, "airplane"), strings.Contains(text, "house"), strings.Contains(text, "construction"):
		return "Travel & Places"
	case strings.Contains(text, "arrow"), strings.Contains(text, "circle"), strings.Contains(text, "mark"), strings.Contains(text, "symbol"), strings.Contains(text, "warning"):
		return "Symbols"
	default:
		return "Objects"
	}
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
