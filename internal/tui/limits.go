package tui

import "fmt"

// Size rules come from the server's hello, through the client. There are
// no client-side defaults: before a handshake the client reports no cap,
// no typing limit and no budget, and nothing here invents one.

// groupSizeRefusal is the status-bar message refusing a group of total
// members (the caller included) when total exceeds the server's cap;
// empty otherwise, and empty before a handshake (no group can be created
// then).
func (a *App) groupSizeRefusal(total int) string {
	if a.client == nil {
		return ""
	}
	limit := a.client.GroupMemberCap()
	if limit <= 0 || total <= limit {
		return ""
	}
	return fmt.Sprintf("Groups are limited to %d members on this server (%d selected)", limit, total)
}

// groupWarnThreshold is the member count from which group creation shows
// the "consider a room" hint: half the server's cap, 50 at the default
// 100. Zero before a handshake, which callers treat as "no hint".
func (a *App) groupWarnThreshold() int {
	if a.client == nil {
		return 0
	}
	return a.client.GroupMemberCap() / 2
}

// typingRuleSuffix states the connected server's typing rule for the
// /typing status line; empty when no server is connected.
func (a *App) typingRuleSuffix() string {
	if a.client == nil || !a.client.IsConnected() {
		return ""
	}
	limit := a.client.RoomTypingLimit()
	if limit <= 0 {
		return "; this server does not relay them in rooms"
	}
	return fmt.Sprintf("; this server relays them in rooms of up to %d members", limit)
}

// syncBudgetNotice mirrors the composer's live byte counter into the
// status bar: shown when the draft is within the last tenth of the
// server's compose budget or over it, cleared when the draft drops back
// under — and only ever clearing its own notice, never a status something
// else put there.
func (a *App) syncBudgetNotice() {
	n := a.input.BudgetNotice()
	if n == a.lastBudgetNotice {
		return
	}
	if n != "" {
		a.statusBar.SetError(n)
	} else if a.statusBar.errorMsg == a.lastBudgetNotice {
		a.statusBar.ClearError()
	}
	a.lastBudgetNotice = n
}
