package client

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// Shadow-device transparency, Tier 1 (client half). The server pushes a
// device_added the moment a brand-new device registers under this identity,
// and the client also reconciles the authoritative device_list against a
// locally-remembered set on every connect. Together they ensure a stolen-key
// device using a fresh device_id surfaces to the user — pushed live when
// another session is online, caught on next connect otherwise. The UI layer
// drives display (via OnMessage, mirroring device_revoked / device_list); this
// file owns only the persisted known-set and the new-vs-seen decision. See
// sshkey-chat/docs/planning/open/device-identity-transparency.md.

// knownDevicesStateKey is the state-kv key holding the set of device_ids this
// client has already seen for this identity (JSON array of ids).
const knownDevicesStateKey = "known_devices"

// loadKnownDevices reads the remembered device-id set. seeded is false when
// the key has never been written (first run) — the caller seeds silently then
// rather than alerting on every pre-existing device. Any read/parse failure is
// treated as not-seeded (re-seed) — safer than alerting on the whole list.
func (c *Client) loadKnownDevices() (set map[string]bool, seeded bool) {
	if c.store == nil {
		return map[string]bool{}, false
	}
	val, err := c.store.GetState(knownDevicesStateKey)
	if errors.Is(err, sql.ErrNoRows) || err != nil || val == "" {
		return map[string]bool{}, false
	}
	var ids []string
	if json.Unmarshal([]byte(val), &ids) != nil {
		return map[string]bool{}, false
	}
	set = make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set, true
}

// saveKnownDevices persists the device-id set as a sorted JSON array.
func (c *Client) saveKnownDevices(set map[string]bool) {
	if c.store == nil {
		return
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	blob, err := json.Marshal(ids)
	if err != nil {
		return
	}
	if err := c.store.SetState(knownDevicesStateKey, string(blob)); err != nil {
		c.logger.Warn("persist known devices", "error", err)
	}
}

// ReconcileDevices compares the authoritative device_list against the
// locally-remembered set and returns the devices that are new to us (excluding
// our own), updating the remembered set. On a fresh client (set never written)
// it seeds silently and returns nil so an existing multi-device account does
// not alarm on first connect. The UI calls this from its device_list handler
// (connect-time auto-fetch + manual refresh) and alerts on each returned
// device. Idempotent after the first sighting of each device.
func (c *Client) ReconcileDevices(devices []protocol.DeviceInfo) []protocol.DeviceInfo {
	if c.store == nil {
		return nil
	}
	known, seeded := c.loadKnownDevices()
	if !seeded {
		set := make(map[string]bool, len(devices))
		for _, d := range devices {
			set[d.DeviceID] = true
		}
		c.saveKnownDevices(set)
		return nil
	}

	var fresh []protocol.DeviceInfo
	changed := false
	for _, d := range devices {
		if known[d.DeviceID] {
			continue
		}
		known[d.DeviceID] = true
		changed = true
		if !d.Current { // never alert for our own device
			fresh = append(fresh, d)
		}
	}
	if changed {
		c.saveKnownDevices(known)
	}
	return fresh
}

// NoteAddedDevice records a live device_added push and reports whether it is
// newly-seen (and thus worth alerting). The server only pushes this for a
// genuinely-new device, so a not-yet-seeded client still returns true (alert),
// but leaves seeding to ReconcileDevices — writing a partial set here would
// make the next reconcile alarm on every other pre-existing device.
func (c *Client) NoteAddedDevice(deviceID string) bool {
	if deviceID == "" {
		return false
	}
	if c.store == nil {
		return true // no local memory; surface the server's new-device signal
	}
	known, seeded := c.loadKnownDevices()
	if !seeded {
		return true // alert; ReconcileDevices will seed (silently incl. this one)
	}
	if known[deviceID] {
		return false // already surfaced via an earlier reconcile — no double-alert
	}
	known[deviceID] = true
	c.saveKnownDevices(known)
	return true
}
