package tui

import (
	"crypto/ed25519"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/crypto"
)

// VerifyModel manages the safety numbers verification dialog.
type VerifyModel struct {
	visible      bool
	user         string
	safetyNumber string
	verified     bool
}

// VerifyActionMsg is sent when the user marks someone as verified.
type VerifyActionMsg struct {
	User string
}

func (v *VerifyModel) Show(targetUser string, c *client.Client) {
	v.visible = true
	v.user = targetUser
	v.verified = false
	v.safetyNumber = ""

	if c == nil {
		return
	}

	// Get both public keys
	targetProfile := c.Profile(targetUser)
	if targetProfile == nil {
		return
	}

	targetPub, err := crypto.ParseSSHPubKey(targetProfile.PubKey)
	if err != nil {
		return
	}

	myProfile := c.Profile(c.Username())
	if myProfile == nil {
		return
	}

	myPub, err := crypto.ParseSSHPubKey(myProfile.PubKey)
	if err != nil {
		return
	}

	v.safetyNumber = crypto.SafetyNumber(ed25519.PublicKey(myPub), ed25519.PublicKey(targetPub))

	// Check if already verified
	if store := c.Store(); store != nil {
		_, verified, err := store.GetPinnedKey(targetUser)
		if err == nil {
			v.verified = verified
		}
	}
}

func (v *VerifyModel) Hide() {
	v.visible = false
}

func (v *VerifyModel) IsVisible() bool {
	return v.visible
}

func (v VerifyModel) Update(msg tea.KeyMsg) (VerifyModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		v.Hide()
		return v, nil
	case "enter", "v":
		if !v.verified {
			user := v.user
			v.verified = true
			return v, func() tea.Msg {
				return VerifyActionMsg{User: user}
			}
		}
	}
	return v, nil
}

func (v VerifyModel) View(width int) string {
	if !v.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render(" Verify: " + v.user))
	b.WriteString("\n\n")
	b.WriteString("  Safety number:\n\n")

	if v.safetyNumber != "" {
		parts := strings.Fields(v.safetyNumber)
		if len(parts) == 6 {
			b.WriteString("     " + searchHeaderStyle.Render(parts[0]+"  "+parts[1]+"  "+parts[2]) + "\n")
			b.WriteString("     " + searchHeaderStyle.Render(parts[3]+"  "+parts[4]+"  "+parts[5]) + "\n")
		}
	} else {
		b.WriteString("     (unable to compute)\n")
	}

	b.WriteString("\n")
	b.WriteString("  Compare this number with " + v.user + "\n")
	b.WriteString("  via phone or in person.\n\n")

	if v.verified {
		b.WriteString("  " + checkStyle.Render("✓ Verified") + "\n")
	} else {
		b.WriteString("  [Mark as verified]  (press Enter or v)\n")
	}

	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render("  Esc=close"))

	return dialogStyle.Width(width - 4).Render(b.String())
}
