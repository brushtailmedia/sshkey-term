package tui

// The composer's size rules come from the server: the widget limit follows
// the server's budget (no limit until a hello has set one), the status bar
// counts bytes against it, and a message the client measures as too large
// is refused with the draft kept.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

const (
	budgetSelf  = "usr_aaaaaaaaaaaaaaaaaaaaa"
	budgetGroup = "group_ggggggggggggggggggggg"
)

// smallLimits is a complete block with a 2 KB body cap, so the fixtures
// stay small; the rules are the server's.
func smallLimits() *protocol.ServerLimits {
	return &protocol.ServerLimits{MaxBodyBytes: 2048, WrappedKeyEntryBytes: 160, MaxGroupMembers: 100, RoomTypingMaxMembers: 100, MaxFileIDsPerMessage: 20}
}

// budgetClient is a connected client whose only group member is itself,
// so a send needs no profile keys, with a capturing encoder and the given
// limits.
func budgetClient(t *testing.T, l *protocol.ServerLimits) (*client.Client, *bytes.Buffer) {
	t.Helper()
	c := client.New(client.Config{DeviceID: "dev_ddddddddddddddddddddd"})
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	client.SetPrivateKeyForTesting(c, priv)
	client.SetUserIDForTesting(c, budgetSelf)
	client.SetGroupMembersForTesting(c, budgetGroup, []string{budgetSelf})
	var buf bytes.Buffer
	client.SetEncoderForTesting(c, protocol.NewEncoder(&buf))
	client.SetLimitsForTesting(c, l)
	return c, &buf
}

func TestInput_WidgetLimitFollowsServerBudget(t *testing.T) {
	in := NewInput()
	if in.textInput.CharLimit != 0 || in.ByteBudget() != 0 {
		t.Fatalf("before a hello: CharLimit %d budget %d, want no limit and no budget", in.textInput.CharLimit, in.ByteBudget())
	}
	in, _ = in.Update(keyRune('a'), nil, "", budgetGroup, "")
	if in.textInput.CharLimit != 0 {
		t.Fatal("a nil client must leave the widget unlimited")
	}

	c, _ := budgetClient(t, smallLimits())
	in, _ = in.Update(keyRune('b'), c, "", budgetGroup, "")
	if want := c.ComposeBudget(); want == 0 || in.textInput.CharLimit != want || in.ByteBudget() != want {
		t.Fatalf("CharLimit %d budget %d, want the client's %d", in.textInput.CharLimit, in.ByteBudget(), want)
	}

	// A new hello with other limits takes effect on the next key; a client
	// between handshakes reports no budget and the widget goes unlimited.
	client.SetLimitsForTesting(c, &protocol.ServerLimits{MaxBodyBytes: 16384})
	in, _ = in.Update(keyRune('c'), c, "", budgetGroup, "")
	if in.textInput.CharLimit != 11620 {
		t.Fatalf("CharLimit %d, want 11620 for a 16 KB cap", in.textInput.CharLimit)
	}
	client.SetLimitsForTesting(c, nil)
	in, _ = in.Update(keyRune('d'), c, "", budgetGroup, "")
	if in.textInput.CharLimit != 0 || in.ByteBudget() != 0 {
		t.Fatalf("no handshake: CharLimit %d budget %d, want 0 and 0", in.textInput.CharLimit, in.ByteBudget())
	}
}

func TestInput_BudgetNoticeCountsBytes(t *testing.T) {
	c, _ := budgetClient(t, smallLimits())
	in := NewInput()
	in, _ = in.Update(keyRune('a'), c, "", budgetGroup, "")
	b := in.ByteBudget()
	if b <= 0 {
		t.Fatal("no budget")
	}

	in.textInput.SetValue(strings.Repeat("a", b/2))
	if n := in.BudgetNotice(); n != "" {
		t.Fatalf("half the budget: notice %q, want none", n)
	}
	in.textInput.SetValue(strings.Repeat("a", b-3))
	if n := in.BudgetNotice(); n != "3 bytes left" {
		t.Fatalf("three under: notice %q", n)
	}
	// Bytes, not characters: b two-byte runes pass the rune limit and are
	// b bytes over.
	in.textInput.SetValue(strings.Repeat("é", b))
	if n := in.BudgetNotice(); !strings.HasSuffix(n, "bytes over this server's limit") || !strings.Contains(n, "Message is ") {
		t.Fatalf("multibyte draft: notice %q", n)
	}
	in.textInput.SetValue("")
	if n := in.BudgetNotice(); n != "" {
		t.Fatalf("empty draft: notice %q, want none", n)
	}
}

func TestInput_TooLargeSendKeepsDraft(t *testing.T) {
	c, buf := budgetClient(t, smallLimits())
	in := NewInput()
	in, _ = in.Update(keyRune('a'), c, "", budgetGroup, "")
	in.textInput.SetValue("")
	draft := strings.Repeat("é", in.ByteBudget()) // twice the budget in bytes

	in.textInput.SetValue(draft)
	in.SetReplyContext("msg_mmmmmmmmmmmmmmmmmmmmm", "quoted", "", budgetGroup, "")
	in, _ = in.Update(tea.KeyMsg{Type: tea.KeyEnter}, c, "", budgetGroup, "")

	refusal := in.TakeSendRefusal()
	if !strings.Contains(refusal, "bytes over this server's limit") {
		t.Fatalf("refusal = %q", refusal)
	}
	if in.Value() != draft {
		t.Fatalf("draft after refusal = %d runes, want it kept (%d)", len([]rune(in.Value())), len([]rune(draft)))
	}
	if in.replyTo == "" {
		t.Fatal("reply context must be kept with the draft")
	}
	if in.DidSend() || buf.Len() != 0 || c.SendQueue().Len() != 0 {
		t.Fatalf("refused send: didSend=%v written=%d queued=%d, want nothing", in.DidSend(), buf.Len(), c.SendQueue().Len())
	}
	if in.TakeSendRefusal() != "" {
		t.Fatal("TakeSendRefusal must clear the message")
	}

	in.textInput.SetValue(strings.Repeat("a", 100))
	in, _ = in.Update(tea.KeyMsg{Type: tea.KeyEnter}, c, "", budgetGroup, "")
	if !in.DidSend() || buf.Len() == 0 || in.Value() != "" {
		t.Fatalf("a send within the budget: didSend=%v written=%d value=%q", in.DidSend(), buf.Len(), in.Value())
	}
}

func TestApp_SyncBudgetNotice(t *testing.T) {
	c, _ := budgetClient(t, smallLimits())
	a := minimalAppForServerMsg(t)
	a.input = NewInput()
	a.input, _ = a.input.Update(keyRune('a'), c, "", budgetGroup, "")
	b := a.input.ByteBudget()

	a.input.textInput.SetValue(strings.Repeat("a", b-1))
	a.syncBudgetNotice()
	if a.statusBar.errorMsg != "1 bytes left" {
		t.Fatalf("status = %q, want the counter", a.statusBar.errorMsg)
	}
	a.input.textInput.SetValue("short")
	a.syncBudgetNotice()
	if a.statusBar.errorMsg != "" {
		t.Fatalf("status = %q, want cleared once the draft is back under the budget", a.statusBar.errorMsg)
	}
	// Never clears a status it did not put there.
	a.statusBar.SetError("Edit failed: whatever")
	a.input.textInput.SetValue("")
	a.syncBudgetNotice()
	if a.statusBar.errorMsg != "Edit failed: whatever" {
		t.Fatalf("status = %q, want the unrelated status kept", a.statusBar.errorMsg)
	}
}

func TestApp_GroupSizeAndTypingRuleFromLimits(t *testing.T) {
	a := minimalAppForServerMsg(t)
	if a.groupSizeRefusal(1000) != "" || a.groupWarnThreshold() != 0 || a.typingRuleSuffix() != "" {
		t.Fatal("nil client: no refusal, no hint threshold, no typing rule")
	}
	c := client.New(client.Config{})
	a.client = c
	if a.groupSizeRefusal(1000) != "" || a.groupWarnThreshold() != 0 || a.typingRuleSuffix() != "" {
		t.Fatal("no handshake: no refusal, no hint threshold, no typing rule")
	}
	client.SetConnectedForTesting(c, true)
	client.SetLimitsForTesting(c, &protocol.ServerLimits{MaxGroupMembers: 10, RoomTypingMaxMembers: 100})
	if a.groupSizeRefusal(10) != "" {
		t.Fatal("a group at the cap is allowed")
	}
	if got := a.groupSizeRefusal(11); got != "Groups are limited to 10 members on this server (11 selected)" {
		t.Fatalf("refusal = %q", got)
	}
	if a.groupWarnThreshold() != 5 {
		t.Fatalf("warn threshold = %d, want half the cap", a.groupWarnThreshold())
	}
	if got := a.typingRuleSuffix(); got != "; this server relays them in rooms of up to 100 members" {
		t.Fatalf("typing rule = %q", got)
	}
	client.SetLimitsForTesting(c, &protocol.ServerLimits{MaxGroupMembers: 100, RoomTypingMaxMembers: 0})
	if got := a.typingRuleSuffix(); got != "; this server does not relay them in rooms" {
		t.Fatalf("typing rule with 0 = %q", got)
	}
	if a.groupWarnThreshold() != 50 {
		t.Fatalf("warn threshold at the default cap = %d, want 50", a.groupWarnThreshold())
	}
}

// An edit the client measures as too large stays in the composer, in edit
// mode, with the reason in the status bar.
func TestApp_TooLargeEditStaysInComposer(t *testing.T) {
	a, st := newEditAppHarness(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	client.SetPrivateKeyForTesting(a.client, priv)
	var buf bytes.Buffer
	client.SetEncoderForTesting(a.client, protocol.NewEncoder(&buf))
	client.SetGroupMembersForTesting(a.client, budgetGroup, []string{"usr_alice"})
	client.SetLimitsForTesting(a.client, smallLimits())
	if _, err := st.InsertMessage(store.StoredMessage{ServerOrder: 1, ID: "msg_edit_big", Sender: "usr_alice", Body: "orig", TS: 1, Group: budgetGroup}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	a.messages.SetContext("", budgetGroup, "")
	long := strings.Repeat("é", 900)
	a.input.EnterEditMode("msg_edit_big", long)

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := model.(App)
	if !updated.input.IsEditing() || updated.input.Value() != long {
		t.Fatalf("editing=%v value kept=%v; want the edit left in the composer", updated.input.IsEditing(), updated.input.Value() == long)
	}
	if !strings.Contains(updated.statusBar.errorMsg, "bytes over this server's limit") {
		t.Fatalf("status = %q", updated.statusBar.errorMsg)
	}
	if buf.Len() != 0 {
		t.Fatal("a refused edit must not be written")
	}
}
