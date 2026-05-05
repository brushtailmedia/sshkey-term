// Package client implements the SSH client connection and protocol handling.
package client

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

const (
	ClientName    = "sshkey-chat"
	ClientVersion = "0.1.0"
)

// Config holds connection parameters.
type Config struct {
	Host     string
	Port     int
	KeyPath  string
	DeviceID string
	DataDir  string // per-server data directory (e.g., ~/.sshkey-term/chat.example.com/)

	// Callbacks
	OnMessage    func(msgType string, raw json.RawMessage)
	OnError      func(err error)
	OnPassphrase PassphraseFunc // called if key is passphrase-protected

	// OnKeyWarning fires when StoreProfile detects a mismatch between
	// a user's currently-pinned fingerprint and the fingerprint on an
	// incoming profile broadcast. Under the no-rotation protocol
	// invariant (PROTOCOL.md "Keys as Identities"), this event only
	// fires on anomalous inputs — a compromised server substituting
	// a key, a server bug emitting a corrupted fingerprint, or local
	// DB tampering. The callback runs on the client's readLoop
	// goroutine; receivers should push to a channel and return
	// promptly rather than blocking. Phase 21 F3 closure 2026-04-19.
	OnKeyWarning func(user, oldFingerprint, newFingerprint string)

	// OnAttachmentReady fires when an auto-previewed image attachment
	// finishes downloading to the local file cache. The callback
	// carries the file_id so the TUI can re-render the matching
	// message — the render path looks up the cache file on disk by
	// file_id, so no path is needed in the callback. Runs on the
	// auto-download goroutine; receivers should push to a channel and
	// return promptly.
	OnAttachmentReady func(fileID string)

	// ImageAutoPreviewMaxBytes is the size cap for auto-downloading
	// image attachments on message receive. Images at or below this
	// threshold are fetched in the background so they can render
	// inline; larger images still require an explicit `o` keypress.
	// Zero or negative disables auto-preview entirely.
	ImageAutoPreviewMaxBytes int64

	Logger *slog.Logger
}

// Client manages the SSH connection and protocol session.
type Client struct {
	cfg            Config
	conn           *ssh.Client
	channel        ssh.Channel
	downloadChan   ssh.Channel // Channel 2: client reads file bytes from here
	downloadChanMu sync.Mutex  // serializes concurrent downloads (request+read atomic)
	uploadChan     ssh.Channel // Channel 3: client writes file bytes here
	uploadChanMu   sync.Mutex  // serializes concurrent uploads (frame writes must not interleave)
	enc            *protocol.Encoder
	dec            *protocol.Decoder
	logger         *slog.Logger
	store          *store.Store

	mu           sync.RWMutex
	userID       string // nanoid (usr_ prefix) — immutable identity
	displayName  string
	admin        bool
	rooms        []string
	groups       []string
	capabilities []string
	profiles     map[string]*protocol.Profile
	groupMembers map[string][]string // group DM ID -> member userIDs
	// Phase 14: in-memory admin set per group. Sourced from the
	// group_list catchup on connect (protocol.GroupInfo.Admins) and
	// updated by live group_event{promote,demote} + sync replay. Not
	// persisted — other members' admin state is authoritative on the
	// server and the client refetches on reconnect. The LOCAL user's
	// admin flag IS persisted (groups.is_admin column) for pre-check
	// survival across restart.
	groupAdmins     map[string]map[string]bool  // group DM ID -> set of admin userIDs
	dms             map[string][2]string        // 1:1 DM ID -> [userA, userB]
	retired         map[string]string           // retired userID -> retired_at timestamp
	epochKeys       map[string]map[int64][]byte // room -> epoch -> unwrapped key
	currentEpoch    map[string]int64            // room -> current epoch number
	seqCounters     map[string]int64            // "room:x" or "group:x" or "dm:x" -> next seq
	hasPendingKeys  bool                        // true when admin_notify arrived (cleared on list refresh)
	pendingKeys     []protocol.PendingKeyEntry  // populated by pending_keys_list
	roomMembersRoom string                      // room for latest room_members_list
	roomMembers     []string                    // member usernames from room_members_list
	privKey         ed25519.PrivateKey
	signer          ssh.Signer
	lastSynced      string

	// sendQueue (Phase 17c Step 5) tracks in-flight outbound requests
	// by their client-generated corr_id. Replaces fire-and-forget.
	// Server echoes corr_id on success broadcasts (message/
	// group_message/dm) and on error responses — TUI handlers call
	// Ack/Error on the queue to close the loop. In-memory only;
	// clean close or crash loses pending entries (documented
	// behavior per refactor_plan.md).
	sendQueue *Queue

	done chan struct{}
}

// New creates a new client.
func New(cfg Config) *Client {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Client{
		cfg:          cfg,
		logger:       cfg.Logger,
		profiles:     make(map[string]*protocol.Profile),
		groupMembers: make(map[string][]string),
		groupAdmins:  make(map[string]map[string]bool),
		dms:          make(map[string][2]string),
		retired:      make(map[string]string),
		epochKeys:    make(map[string]map[int64][]byte),
		currentEpoch: make(map[string]int64),
		seqCounters:  make(map[string]int64),
		sendQueue:    NewQueue(),
		done:         make(chan struct{}),
	}
}

// SendQueue returns the in-memory send queue. TUI uses this to
// Ack/Error entries on inbound broadcasts, and to read PendingCount
// for the quit-confirmation prompt.
func (c *Client) SendQueue() *Queue {
	return c.sendQueue
}

// Connect establishes the SSH connection and performs the protocol handshake.
func (c *Client) Connect() error {
	// Parse SSH key (with passphrase support)
	signer, err := loadSSHKey(c.cfg.KeyPath, c.cfg.OnPassphrase)
	if err != nil {
		return fmt.Errorf("load key: %w", err)
	}
	c.signer = signer

	// Extract raw ed25519 private key for crypto operations
	c.privKey, err = ParseRawEd25519Key(c.cfg.KeyPath, c.cfg.OnPassphrase)
	if err != nil {
		return fmt.Errorf("extract ed25519 key: %w", err)
	}

	// SSH dial
	addr := net.JoinHostPort(c.cfg.Host, fmt.Sprintf("%d", c.cfg.Port))
	sshCfg := &ssh.ClientConfig{
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback(c.cfg.DataDir, c.cfg.Host),
		Timeout:         10 * time.Second,
	}

	c.logger.Info("connecting", "addr", addr)
	conn, err := ssh.Dial("tcp", addr, sshCfg)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	c.conn = conn

	// Open Channel 1 (NDJSON protocol)
	ch, reqs, err := conn.OpenChannel("session", nil)
	if err != nil {
		conn.Close()
		return fmt.Errorf("open channel: %w", err)
	}
	go ssh.DiscardRequests(reqs)

	c.channel = ch
	c.enc = protocol.NewEncoder(ch)
	c.dec = protocol.NewDecoder(ch)

	// Open Channel 2 (downloads: server writes, client reads) — non-fatal if it fails
	dlCh, dlReqs, err := conn.OpenChannel("session", nil)
	if err != nil {
		c.logger.Warn("failed to open download channel", "error", err)
	} else {
		go ssh.DiscardRequests(dlReqs)
		c.downloadChan = dlCh
	}

	// Open Channel 3 (uploads: client writes, server reads) — non-fatal if it fails
	ulCh, ulReqs, err := conn.OpenChannel("session", nil)
	if err != nil {
		c.logger.Warn("failed to open upload channel", "error", err)
	} else {
		go ssh.DiscardRequests(ulReqs)
		c.uploadChan = ulCh
	}

	// Perform handshake
	if err := c.handshake(); err != nil {
		ch.Close()
		conn.Close()
		return fmt.Errorf("handshake: %w", err)
	}

	// Open encrypted local DB (key derived from SSH private key)
	if c.cfg.DataDir != "" {
		dbPath := filepath.Join(c.cfg.DataDir, "messages.db")

		// Derive DB encryption key from the SSH private key seed.
		// Both derivation and open must succeed — we never store
		// messages unencrypted.
		dbKey, err := store.DeriveDBKey(c.privKey.Seed())
		if err != nil {
			c.logger.Error("failed to derive DB key — local storage disabled", "error", err)
		} else if st, err := store.Open(dbPath, dbKey); err != nil {
			c.logger.Error("failed to open local DB — local storage disabled", "path", dbPath, "error", err)
		} else {
			c.store = st

			// Load last_synced from DB
			if synced, err := st.GetState("last_synced"); err == nil && synced != "" {
				c.lastSynced = synced
			}

			// Load cached groups so sidebar is populated
			// before the server sends a fresh group_list
			if groups, err := st.GetAllGroups(); err == nil {
				c.mu.Lock()
				for id, info := range groups {
					if _, ok := c.groupMembers[id]; !ok {
						c.groupMembers[id] = strings.Split(info[1], ",")
					}
				}
				c.mu.Unlock()
			}

			// Load cached 1:1 DMs
			if dms, err := st.GetAllDMs(); err == nil {
				c.mu.Lock()
				for _, dm := range dms {
					if _, ok := c.dms[dm.ID]; !ok {
						c.dms[dm.ID] = [2]string{dm.UserA, dm.UserB}
					}
				}
				c.mu.Unlock()
			}
		}
	}

	// Start message loop
	go c.readLoop()

	// Start SSH keepalive — detects dead connections faster than TCP timeout.
	// Sends keepalive@openssh.com every 30s. If 3 consecutive fail, closes
	// the connection to trigger the TUI's reconnect logic.
	go c.keepalive()

	// Phase 17c Step 5 Gap 2/3: start the send-queue driver. Sweeps
	// timeouts + triggers Category A retries with exponential backoff.
	// Exits when c.done closes.
	go c.runSendQueueDriver()

	return nil
}

// Close disconnects from the server.
func (c *Client) Close() error {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	if c.store != nil {
		c.store.Close()
	}
	if c.channel != nil {
		c.channel.Close()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// handshake performs the server_hello -> client_hello -> welcome exchange.
func (c *Client) handshake() error {
	// Read server_hello
	raw, err := c.dec.DecodeRaw()
	if err != nil {
		return fmt.Errorf("read server_hello: %w", err)
	}
	var hello protocol.ServerHello
	if err := json.Unmarshal(raw, &hello); err != nil {
		return fmt.Errorf("parse server_hello: %w", err)
	}
	if hello.Type != "server_hello" || hello.Protocol != "sshkey-chat" {
		return fmt.Errorf("unexpected server response: %s", hello.Type)
	}

	c.logger.Info("server hello",
		"server", hello.ServerID,
		"version", hello.Version,
		"capabilities", hello.Capabilities,
	)

	// Send client_hello
	err = c.enc.Encode(protocol.ClientHello{
		Type:          "client_hello",
		Protocol:      "sshkey-chat",
		Version:       1,
		Client:        ClientName,
		ClientVersion: ClientVersion,
		DeviceID:      c.cfg.DeviceID,
		LastSyncedAt:  c.lastSynced,
		Capabilities: []string{
			"typing", "reactions", "read_receipts", "file_transfer",
			"link_previews", "presence", "pins", "mentions",
			"unread", "status", "signatures",
		},
	})
	if err != nil {
		return fmt.Errorf("send client_hello: %w", err)
	}

	// Read welcome
	raw, err = c.dec.DecodeRaw()
	if err != nil {
		return fmt.Errorf("read welcome: %w", err)
	}
	var welcome protocol.Welcome
	if err := json.Unmarshal(raw, &welcome); err != nil {
		return fmt.Errorf("parse welcome: %w", err)
	}
	if welcome.Type != "welcome" {
		return fmt.Errorf("expected welcome, got %s", welcome.Type)
	}

	c.mu.Lock()
	c.userID = welcome.User
	c.displayName = welcome.DisplayName
	c.admin = welcome.Admin
	c.rooms = welcome.Rooms
	c.groups = welcome.Groups
	c.capabilities = welcome.ActiveCapabilities
	c.mu.Unlock()

	c.logger.Info("connected",
		"user", welcome.User,
		"admin", welcome.Admin,
		"rooms", welcome.Rooms,
		"capabilities", welcome.ActiveCapabilities,
	)

	return nil
}

// readLoop reads messages from the server and dispatches to callbacks.
func (c *Client) readLoop() {
	for {
		select {
		case <-c.done:
			return
		default:
		}

		raw, err := c.dec.DecodeRaw()
		if err != nil {
			if c.cfg.OnError != nil {
				select {
				case <-c.done:
					// Local shutdown path (explicit Close); suppress
					// reconnect-trigger errors.
				default:
					c.cfg.OnError(err)
				}
			}
			return
		}

		msgType, err := protocol.TypeOf(raw)
		if err != nil {
			continue
		}

		// Phase 17c Step 5 residual: generic corr_id dispatch. Any
		// server response or broadcast carrying corr_id matches an
		// in-flight send-queue entry. Route to Error (for the 3
		// error types — so Category C/D entries surface correctly)
		// or Ack (for everything else).
		//
		// Why this is at the readLoop level rather than per-case:
		// it's a single untyped unmarshal of just corr_id + code,
		// which gives us ack/error-classification parity across all
		// 15 CorrID-carrying verbs in one place. Per-case wiring
		// would need 11+ duplicated calls.
		dispatchCorrID(c, msgType, raw)

		// Handle protocol-level messages internally
		c.handleInternal(msgType, raw)

		// Forward to UI callback
		if c.cfg.OnMessage != nil {
			c.cfg.OnMessage(msgType, raw)
		}
	}
}

// handleInternal processes messages that need client-side state updates.
func (c *Client) handleInternal(msgType string, raw json.RawMessage) {
	switch msgType {
	case "profile":
		var p protocol.Profile
		if err := json.Unmarshal(raw, &p); err == nil {
			c.mu.Lock()
			c.profiles[p.User] = &p
			if p.Retired {
				c.retired[p.User] = p.RetiredAt
			} else {
				delete(c.retired, p.User)
			}
			c.mu.Unlock()
			c.StoreProfile(&p)
		}
	case "epoch_key":
		var ek protocol.EpochKey
		if err := json.Unmarshal(raw, &ek); err == nil {
			c.storeEpochKey(ek.Room, ek.Epoch, ek.WrappedKey)
			// Phase 17c Step 5 Gap 4: Category B state-fix apply.
			// A fresh epoch_key after an invalid_epoch rejection is
			// the state-fix the server promised — re-send any
			// queued entries that failed with invalid_epoch for
			// this room.
			c.TriggerEpochRetry(ek.Room)
		}
	case "epoch_trigger":
		c.handleEpochTrigger(raw)
	case "epoch_confirmed":
		c.handleEpochConfirmed(raw)
	case "sync_complete":
		var sc protocol.SyncComplete
		if err := json.Unmarshal(raw, &sc); err == nil {
			c.mu.Lock()
			c.lastSynced = sc.SyncedTo
			c.mu.Unlock()
			if c.store != nil {
				c.store.SetState("last_synced", sc.SyncedTo)
			}
		}
	case "message":
		c.storeRoomMessage(raw, true)
	case "group_message":
		c.storeGroupMessage(raw, true)
	case "edited":
		c.storeEditedRoomMessage(raw)
	case "group_edited":
		c.storeEditedGroupMessage(raw)
	case "dm_edited":
		c.storeEditedDMMessage(raw)
	case "group_created":
		var g protocol.GroupCreated
		if err := json.Unmarshal(raw, &g); err == nil {
			c.mu.Lock()
			c.groupMembers[g.Group] = g.Members
			// Phase 14: populate the in-memory admin set from the
			// new Admins field. On a fresh create this is always
			// just the creator, but the server-sourced list is
			// authoritative — don't assume.
			adminSet := make(map[string]bool, len(g.Admins))
			for _, a := range g.Admins {
				adminSet[a] = true
			}
			c.groupAdmins[g.Group] = adminSet
			localIsAdmin := adminSet[c.userID]
			c.mu.Unlock()
			if c.store != nil {
				c.store.StoreGroup(g.Group, g.Name, strings.Join(g.Members, ","))
				// Persist the local user's admin flag so the TUI
				// pre-check survives restart. Separate write from
				// StoreGroup so the upsert can't clobber this
				// later.
				if err := c.store.SetLocalUserGroupAdmin(g.Group, localIsAdmin); err != nil {
					c.logger.Warn("SetLocalUserGroupAdmin on group_created",
						"group", g.Group, "error", err)
				}
			}
		}
	case "group_added_to":
		// Phase 14: direct notification that an admin added the local
		// user to an existing group. Insert the group into local
		// state so the sidebar updates immediately without waiting
		// for a group_list refetch. The local user is NEVER an admin
		// at add time (promote is a separate event), so the is_admin
		// flag is explicitly false. TUI layer (app.go) handles the
		// toast notification and sidebar insertion.
		var gat protocol.GroupAddedTo
		if err := json.Unmarshal(raw, &gat); err == nil {
			c.mu.Lock()
			c.groupMembers[gat.Group] = gat.Members
			adminSet := make(map[string]bool, len(gat.Admins))
			for _, a := range gat.Admins {
				adminSet[a] = true
			}
			c.groupAdmins[gat.Group] = adminSet
			c.mu.Unlock()
			if c.store != nil {
				if err := c.store.StoreGroup(gat.Group, gat.Name, strings.Join(gat.Members, ",")); err != nil {
					c.logger.Warn("StoreGroup on group_added_to",
						"group", gat.Group, "error", err)
				}
				// The added user is explicitly NOT an admin.
				if err := c.store.SetLocalUserGroupAdmin(gat.Group, false); err != nil {
					c.logger.Warn("SetLocalUserGroupAdmin on group_added_to",
						"group", gat.Group, "error", err)
				}
				// Ensure the group isn't in left_at > 0 state — if
				// the user was previously removed or /delete'd and
				// is now being re-added, un-archive them locally.
				if err := c.store.MarkGroupRejoined(gat.Group); err != nil {
					c.logger.Warn("MarkGroupRejoined on group_added_to",
						"group", gat.Group, "error", err)
				}
			}
		}
	case "room_list":
		var rl protocol.RoomList
		if err := json.Unmarshal(raw, &rl); err == nil && c.store != nil {
			for _, r := range rl.Rooms {
				if err := c.store.UpsertRoom(r.ID, r.Name, r.Topic, r.Members); err != nil {
					c.logger.Warn("failed to cache room", "room_id", r.ID, "error", err)
				}
			}

			// Phase 20: clear stale left_at / leave_reason for every
			// room the server still reports as active. This is the
			// replacement for the old reconciliation walk — server is
			// now authoritative via left_rooms catchup (delivered
			// BEFORE room_list on the handshake), so any room present
			// in this list is by definition active.
			for _, r := range rl.Rooms {
				if err := c.store.MarkRoomRejoined(r.ID); err != nil {
					c.logger.Warn("MarkRoomRejoined on room_list",
						"room", r.ID, "error", err)
				}
			}
		}
	case "group_list":
		var gl protocol.GroupList
		if err := json.Unmarshal(raw, &gl); err == nil {
			c.mu.Lock()
			for _, g := range gl.Groups {
				c.groupMembers[g.ID] = g.Members
				// Phase 14: populate the in-memory admin set from
				// each GroupInfo.Admins field. On a pre-Phase-14
				// server this will be empty, and IsGroupAdmin will
				// correctly return false until a live promote
				// arrives (upgrade-safe).
				adminSet := make(map[string]bool, len(g.Admins))
				for _, a := range g.Admins {
					adminSet[a] = true
				}
				c.groupAdmins[g.ID] = adminSet
			}
			c.mu.Unlock()
			if c.store != nil {
				for _, g := range gl.Groups {
					c.store.StoreGroup(g.ID, g.Name, strings.Join(g.Members, ","))
					// Persist the local user's admin flag per
					// group so the pre-check survives restart.
					localIsAdmin := false
					for _, a := range g.Admins {
						if a == c.userID {
							localIsAdmin = true
							break
						}
					}
					if err := c.store.SetLocalUserGroupAdmin(g.ID, localIsAdmin); err != nil {
						c.logger.Warn("SetLocalUserGroupAdmin on group_list",
							"group", g.ID, "error", err)
					}
				}

				// Phase 20: clear stale left_at / leave_reason for every
				// group the server still reports as active. Server is
				// now authoritative via left_groups catchup (delivered
				// BEFORE group_list on the handshake), so any group in
				// this list is by definition active.
				for _, g := range gl.Groups {
					if err := c.store.MarkGroupRejoined(g.ID); err != nil {
						c.logger.Warn("MarkGroupRejoined on group_list",
							"group", g.ID, "error", err)
					}
				}
			}
		}
	case "room_event":
		// Phase 20: persist room audit events (leave/join/topic/rename/
		// retire) to the local room_events table so sync replay and
		// live broadcasts produce identical local state. The TUI layer
		// (app.go) handles inline rendering and coalescing.
		var re protocol.RoomEvent
		if err := json.Unmarshal(raw, &re); err == nil && c.store != nil {
			if err := c.store.RecordRoomEvent(
				re.Room, re.Event, re.User, re.By, re.Reason, re.Name, time.Now().Unix(),
			); err != nil {
				c.logger.Warn("RecordRoomEvent",
					"room", re.Room, "event", re.Event, "error", err)
			}
		}
	case "group_event":
		var ge protocol.GroupEvent
		if err := json.Unmarshal(raw, &ge); err != nil {
			break
		}
		// Phase 14: unified dispatch for all five group event types.
		// State updates happen here (member list, in-memory admin set,
		// persistent local admin flag). Rendering, coalescing, and the
		// audit trail persistence live in the TUI layer (see app.go)
		// so the client layer stays focused on data-plane correctness.
		c.mu.Lock()
		switch ge.Event {
		case "leave":
			if members, ok := c.groupMembers[ge.Group]; ok {
				filtered := members[:0]
				for _, m := range members {
					if m != ge.User {
						filtered = append(filtered, m)
					}
				}
				c.groupMembers[ge.Group] = filtered
			}
			// Demoted-by-leave: if the leaving user was an admin,
			// drop them from the admin set too. Keeps the in-memory
			// admin set in sync even when kicks land via performGroupLeave.
			if set, ok := c.groupAdmins[ge.Group]; ok {
				delete(set, ge.User)
			}
		case "join":
			// New member added by an admin. Append to members list
			// if not already present. New members are never admins
			// at add time — promote is a separate event.
			found := false
			for _, m := range c.groupMembers[ge.Group] {
				if m == ge.User {
					found = true
					break
				}
			}
			if !found {
				c.groupMembers[ge.Group] = append(c.groupMembers[ge.Group], ge.User)
			}
		case "promote":
			// Add target to the in-memory admin set. If the target
			// is the local user, also persist the local flag so the
			// TUI pre-check survives restart.
			if c.groupAdmins[ge.Group] == nil {
				c.groupAdmins[ge.Group] = make(map[string]bool)
			}
			c.groupAdmins[ge.Group][ge.User] = true
			if ge.User == c.userID && c.store != nil {
				if err := c.store.SetLocalUserGroupAdmin(ge.Group, true); err != nil {
					c.logger.Warn("SetLocalUserGroupAdmin on promote",
						"group", ge.Group, "error", err)
				}
			}
		case "demote":
			if set, ok := c.groupAdmins[ge.Group]; ok {
				delete(set, ge.User)
			}
			if ge.User == c.userID && c.store != nil {
				if err := c.store.SetLocalUserGroupAdmin(ge.Group, false); err != nil {
					c.logger.Warn("SetLocalUserGroupAdmin on demote",
						"group", ge.Group, "error", err)
				}
			}
		case "rename":
			// The data-layer mirror of group_renamed — update the
			// cached name so reconnects render correctly. TUI layer
			// handles the in-memory sidebar entry + system message.
			if c.store != nil {
				var members string
				if all, err := c.store.GetAllGroups(); err == nil {
					if entry, ok := all[ge.Group]; ok {
						members = entry[1]
					}
				}
				// Unlock around the store call to avoid holding
				// the lock during I/O. Re-lock not needed — we're
				// done mutating in-memory state for this event.
				c.mu.Unlock()
				if err := c.store.StoreGroup(ge.Group, ge.Name, members); err != nil {
					c.logger.Warn("failed to update cached group name on rename event",
						"group", ge.Group, "error", err)
				}
				c.mu.Lock() // re-lock to match the defer semantics below
			}
		}
		c.mu.Unlock()

		// Audit trail: persist every event to the local group_events
		// table for /audit replay. Best-effort — failure logged, not
		// surfaced to the caller. Sync replay uses the same helper so
		// live and replay rows are byte-identical.
		if c.store != nil {
			if err := c.store.RecordGroupEvent(
				ge.Group, ge.Event, ge.User, ge.By, ge.Reason, ge.Name, ge.Quiet, time.Now().Unix(),
			); err != nil {
				c.logger.Warn("RecordGroupEvent",
					"group", ge.Group, "event", ge.Event, "error", err)
			}
		}
	case "group_renamed":
		// Update the cached name in the local store so subsequent
		// reconnects render the new name even if the live group_renamed
		// event arrives before group_list. The TUI handler at app.go
		// already updates the in-memory sidebar entry; this case is the
		// data-layer counterpart so the persistent state stays in sync.
		var gr protocol.GroupRenamed
		if err := json.Unmarshal(raw, &gr); err == nil && c.store != nil {
			// Read existing members so StoreGroup can preserve them.
			// (StoreGroup is an upsert keyed by id, but it overwrites
			// the members column too — we have to read-modify-write.)
			var members string
			if all, err := c.store.GetAllGroups(); err == nil {
				if entry, ok := all[gr.Group]; ok {
					members = entry[1]
				}
			}
			if err := c.store.StoreGroup(gr.Group, gr.Name, members); err != nil {
				c.logger.Warn("failed to update cached group name", "group", gr.Group, "error", err)
			}
		}
	case "group_left":
		var gl protocol.GroupLeft
		if err := json.Unmarshal(raw, &gl); err == nil {
			// Server confirmed our leave_group — drop the group from in-memory
			// members and mark it archived in the local DB.
			c.mu.Lock()
			delete(c.groupMembers, gl.Group)
			c.mu.Unlock()
			if c.store != nil {
				if err := c.store.MarkGroupLeft(gl.Group, time.Now().Unix(), gl.Reason); err != nil {
					c.logger.Warn("failed to mark group left", "group", gl.Group, "error", err)
				}
			}
		}
	case "group_deleted":
		var gd protocol.GroupDeleted
		if err := json.Unmarshal(raw, &gd); err == nil {
			// Server confirmed a delete_group from this account, possibly
			// from another device. Apply the local /delete effects on
			// THIS device too: drop in-memory members, mark left, and
			// purge every locally-stored message for the group.
			//
			// Idempotent — receiving group_deleted on a device that has
			// already purged just runs the no-op DELETE statements again.
			c.mu.Lock()
			delete(c.groupMembers, gd.Group)
			c.mu.Unlock()
			if c.store != nil {
				// group_deleted is a distinct action tracked by the
				// separate deleted_groups sidecar — not a leave reason.
				if err := c.store.MarkGroupLeft(gd.Group, time.Now().Unix(), ""); err != nil {
					c.logger.Warn("MarkGroupLeft on group_deleted", "group", gd.Group, "error", err)
				}
				if err := c.store.PurgeGroupMessages(gd.Group); err != nil {
					c.logger.Warn("PurgeGroupMessages on group_deleted", "group", gd.Group, "error", err)
				}
			}
			c.logger.Info("group deleted", "group", gd.Group)
		}
	case "deleted_groups":
		var dgl protocol.DeletedGroupsList
		if err := json.Unmarshal(raw, &dgl); err == nil {
			// Sync catchup. The handshake delivered every group ID this
			// user has previously /delete'd that hasn't been pruned. Run
			// the same purge path for each entry as if a live group_deleted
			// echo had arrived. Idempotent on already-purged entries.
			if c.store != nil {
				for _, groupID := range dgl.Groups {
					// deleted_groups catchup — delete is a distinct action
					// tracked by its own sidecar, so no leave reason.
					if err := c.store.MarkGroupLeft(groupID, time.Now().Unix(), ""); err != nil {
						c.logger.Warn("MarkGroupLeft on deleted_groups", "group", groupID, "error", err)
					}
					if err := c.store.PurgeGroupMessages(groupID); err != nil {
						c.logger.Warn("PurgeGroupMessages on deleted_groups", "group", groupID, "error", err)
					}
				}
				c.mu.Lock()
				for _, groupID := range dgl.Groups {
					delete(c.groupMembers, groupID)
				}
				c.mu.Unlock()
			}
		}
	case "left_rooms":
		// Phase 20: server-authoritative multi-device leave catchup.
		// On connect handshake, the server sends the most recent leave
		// per room for this user (with reason code) BEFORE room_list,
		// so the sidebar can render the right archived state.
		var lr protocol.LeftRoomsList
		if err := json.Unmarshal(raw, &lr); err == nil && c.store != nil {
			for _, entry := range lr.Rooms {
				if err := c.store.MarkRoomLeft(entry.Room, entry.LeftAt, entry.Reason); err != nil {
					c.logger.Warn("MarkRoomLeft on left_rooms catchup",
						"room", entry.Room, "error", err)
				}
			}
		}
	case "left_groups":
		// Phase 20: group DM analogue of left_rooms.
		var lg protocol.LeftGroupsList
		if err := json.Unmarshal(raw, &lg); err == nil && c.store != nil {
			for _, entry := range lg.Groups {
				if err := c.store.MarkGroupLeft(entry.Group, entry.LeftAt, entry.Reason); err != nil {
					c.logger.Warn("MarkGroupLeft on left_groups catchup",
						"group", entry.Group, "error", err)
				}
				c.mu.Lock()
				delete(c.groupMembers, entry.Group)
				c.mu.Unlock()
			}
		}
	case "room_left":
		var rl protocol.RoomLeft
		if err := json.Unmarshal(raw, &rl); err == nil {
			// Server confirmed our leave_room — mark the room archived in the
			// local DB. The room metadata row stays so the sidebar can render
			// the entry as greyed/read-only on this and subsequent reconnects.
			if c.store != nil {
				if err := c.store.MarkRoomLeft(rl.Room, time.Now().Unix(), rl.Reason); err != nil {
					c.logger.Warn("failed to mark room left", "room", rl.Room, "error", err)
				}
			}
		}
	case "room_retired":
		// Phase 12: broadcast from the server's runRoomRetirementProcessor
		// telling every connected member of a room that the room has
		// been retired. Update the local rooms row to flip the retired
		// flag and overwrite the cached display name with the
		// post-retirement (suffixed) form so the sidebar and info panel
		// render the right state.
		var rr protocol.RoomRetired
		if err := json.Unmarshal(raw, &rr); err == nil {
			if c.store != nil {
				if err := c.store.MarkRoomRetired(rr.Room, rr.DisplayName, time.Now().Unix()); err != nil {
					c.logger.Warn("MarkRoomRetired on room_retired", "room", rr.Room, "error", err)
				}
			}
			c.logger.Info("room retired", "room", rr.Room, "display_name", rr.DisplayName)
		}
	case "room_updated":
		// Phase 16 Gap 1: broadcast from the server's
		// runRoomUpdatesProcessor when an admin runs `sshkey-ctl
		// update-topic` or `sshkey-ctl rename-room`. Carries the FULL
		// post-change room state {DisplayName, Topic} so the client
		// can apply the event with a single UPDATE on the local
		// rooms row. One handler covers both verbs — whichever field
		// changed gets reflected on the next render; the unchanged
		// field is overwritten with its current value (a no-op).
		//
		// The sidebar and info panel use GetRoomName / GetRoomTopic
		// which read from this same rooms table, so the next render
		// pass picks up the new values automatically. No extra
		// invalidation needed.
		var ru protocol.RoomUpdated
		if err := json.Unmarshal(raw, &ru); err == nil {
			if c.store != nil {
				if err := c.store.UpdateRoomNameTopic(ru.Room, ru.DisplayName, ru.Topic); err != nil {
					c.logger.Warn("UpdateRoomNameTopic on room_updated", "room", ru.Room, "error", err)
				}
			}
			c.logger.Info("room updated", "room", ru.Room, "display_name", ru.DisplayName, "topic", ru.Topic)
		}
	case "retired_rooms":
		// Phase 12: offline-catchup list sent during the connect
		// handshake listing every retired room the user is still a
		// member of. Apply the same local effects as room_retired for
		// each entry. Idempotent on already-marked rooms.
		var rrl protocol.RetiredRoomsList
		if err := json.Unmarshal(raw, &rrl); err == nil {
			if c.store != nil {
				now := time.Now().Unix()
				for _, rr := range rrl.Rooms {
					if err := c.store.MarkRoomRetired(rr.Room, rr.DisplayName, now); err != nil {
						c.logger.Warn("MarkRoomRetired on retired_rooms catchup",
							"room", rr.Room, "error", err)
					}
				}
			}
		}
	case "room_deleted":
		// Phase 12: server confirmed a delete_room from this account,
		// possibly from another device. Apply the local /delete
		// effects on THIS device too: mark left, purge every locally
		// stored message for the room, drop epoch keys. The row
		// itself stays (the TUI app layer handles sidebar entry
		// removal via sidebar.RemoveRoom). Parallel to the
		// group_deleted handler above.
		//
		// Idempotent — receiving room_deleted on a device that has
		// already purged just runs the no-op DELETE statements again.
		var rd protocol.RoomDeleted
		if err := json.Unmarshal(raw, &rd); err == nil {
			if c.store != nil {
				// room_deleted is a distinct action tracked by the
				// separate deleted_rooms sidecar — not a leave reason.
				if err := c.store.MarkRoomLeft(rd.Room, time.Now().Unix(), ""); err != nil {
					c.logger.Warn("MarkRoomLeft on room_deleted", "room", rd.Room, "error", err)
				}
				if err := c.store.PurgeRoomMessages(rd.Room); err != nil {
					c.logger.Warn("PurgeRoomMessages on room_deleted", "room", rd.Room, "error", err)
				}
			}
			c.logger.Info("room deleted", "room", rd.Room)
		}
	case "deleted_rooms":
		// Phase 12: offline-catchup list sent during the connect
		// handshake listing every room ID this user has previously
		// /delete'd. Run the same purge path for each entry as if a
		// live room_deleted echo had arrived. Idempotent on
		// already-purged entries. Parallel to deleted_groups handler.
		var drl protocol.DeletedRoomsList
		if err := json.Unmarshal(raw, &drl); err == nil {
			if c.store != nil {
				now := time.Now().Unix()
				for _, roomID := range drl.Rooms {
					// deleted_rooms catchup — distinct action, no leave reason.
					if err := c.store.MarkRoomLeft(roomID, now, ""); err != nil {
						c.logger.Warn("MarkRoomLeft on deleted_rooms", "room", roomID, "error", err)
					}
					if err := c.store.PurgeRoomMessages(roomID); err != nil {
						c.logger.Warn("PurgeRoomMessages on deleted_rooms", "room", roomID, "error", err)
					}
				}
			}
		}
	case "dm_list":
		var dl protocol.DMList
		if err := json.Unmarshal(raw, &dl); err == nil {
			c.mu.Lock()
			for _, dm := range dl.DMs {
				c.dms[dm.ID] = [2]string{dm.Members[0], dm.Members[1]}
			}
			c.mu.Unlock()
			if c.store != nil {
				for _, dm := range dl.DMs {
					c.store.StoreDM(dm.ID, dm.Members[0], dm.Members[1])
					// Catch-up: if the server reports we have already left
					// this DM (from a /delete on another device, or as a
					// side effect of retirement), apply the same local
					// effects we would have applied if dm_left had reached
					// us live. Idempotent — MarkDMLeft / PurgeDMMessages
					// can be called repeatedly without harm.
					if dm.LeftAtForCaller > 0 && c.store.GetDMLeftAt(dm.ID) == 0 {
						if err := c.store.MarkDMLeft(dm.ID, dm.LeftAtForCaller); err != nil {
							c.logger.Warn("MarkDMLeft on sync", "dm", dm.ID, "error", err)
						}
						if err := c.store.PurgeDMMessages(dm.ID); err != nil {
							c.logger.Warn("PurgeDMMessages on sync", "dm", dm.ID, "error", err)
						}
					}
				}
			}
		}
	case "dm_created":
		var dc protocol.DMCreated
		if err := json.Unmarshal(raw, &dc); err == nil {
			c.mu.Lock()
			c.dms[dc.DM] = [2]string{dc.Members[0], dc.Members[1]}
			c.mu.Unlock()
			if c.store != nil {
				c.store.StoreDM(dc.DM, dc.Members[0], dc.Members[1])
			}
		}
	case "dm":
		c.storeDMMessage(raw, true)
	case "dm_left":
		var dl protocol.DMLeft
		if err := json.Unmarshal(raw, &dl); err == nil {
			// Server confirmed leave_dm. Apply the local /delete effects:
			// flip the local left_at flag and purge every message we've
			// stored for this dm_id (plus the reactions hanging off them).
			// The direct_messages row stays so multi-device sync from a
			// different device can recognise the leave on next reconnect.
			//
			// Echoes from another of this user's devices land here too —
			// the local effect is identical regardless of which session
			// initiated the leave, which is exactly the multi-device
			// behaviour we want.
			if c.store != nil {
				if err := c.store.MarkDMLeft(dl.DM, time.Now().Unix()); err != nil {
					c.logger.Error("MarkDMLeft", "dm", dl.DM, "error", err)
				}
				if err := c.store.PurgeDMMessages(dl.DM); err != nil {
					c.logger.Error("PurgeDMMessages", "dm", dl.DM, "error", err)
				}
			}
			c.logger.Info("DM left (silent)", "dm", dl.DM)
		}
	case "upload_ready":
		var ur protocol.UploadReady
		if err := json.Unmarshal(raw, &ur); err == nil {
			HandleUploadReady(ur.UploadID)
		}
	case "upload_complete":
		var uc protocol.UploadComplete
		if err := json.Unmarshal(raw, &uc); err == nil {
			HandleUploadComplete(uc.UploadID, uc.FileID)
		}
	case "upload_error":
		var ue protocol.UploadError
		if err := json.Unmarshal(raw, &ue); err == nil {
			HandleUploadError(ue.UploadID, fmt.Errorf("%s: %s", ue.Code, ue.Message))
		}
	case "download_start":
		var ds protocol.DownloadStart
		if err := json.Unmarshal(raw, &ds); err == nil {
			HandleDownloadStart(ds.FileID, ds.ContentHash)
		}
	case "download_error":
		var de protocol.DownloadError
		if err := json.Unmarshal(raw, &de); err == nil {
			HandleDownloadError(de.FileID, fmt.Errorf("%s: %s", de.Code, de.Message))
		}
	case "admin_notify":
		var an protocol.AdminNotify
		if err := json.Unmarshal(raw, &an); err == nil && an.Event == "pending_key" {
			c.mu.Lock()
			c.hasPendingKeys = true
			c.mu.Unlock()
		}
	case "pending_keys_list":
		var pkl protocol.PendingKeysList
		if err := json.Unmarshal(raw, &pkl); err == nil {
			c.mu.Lock()
			c.pendingKeys = pkl.Keys
			c.hasPendingKeys = len(pkl.Keys) > 0
			c.mu.Unlock()
		}
	case "room_members_list":
		var rml protocol.RoomMembersList
		if err := json.Unmarshal(raw, &rml); err == nil {
			c.mu.Lock()
			c.roomMembersRoom = rml.Room
			c.roomMembers = rml.Members
			c.mu.Unlock()
		}
	case "reaction":
		c.storeReaction(raw)
	case "reaction_removed":
		var rm protocol.ReactionRemoved
		if err := json.Unmarshal(raw, &rm); err == nil && c.store != nil {
			c.store.DeleteReaction(rm.ReactionID)
		}
	case "deleted":
		var d protocol.Deleted
		if err := json.Unmarshal(raw, &d); err == nil && c.store != nil {
			fileIDs, _ := c.store.DeleteMessage(d.ID, d.DeletedBy)
			// Clean up locally cached files
			if len(fileIDs) > 0 && c.cfg.DataDir != "" {
				filesDir := filepath.Join(c.cfg.DataDir, "files")
				for _, fid := range fileIDs {
					os.Remove(filepath.Join(filesDir, fid))
				}
			}
		}
	case "sync_batch":
		c.handleSyncBatchKeys(raw)
		return // sync_batch messages are forwarded from handleSyncBatchKeys
	case "history_result":
		c.handleHistoryKeys(raw)
	case "user_retired":
		var ur protocol.UserRetired
		if err := json.Unmarshal(raw, &ur); err == nil {
			c.mu.Lock()
			// Record retirement; keep historical profile intact so signature
			// verification still works against messages from before retirement.
			c.retired[ur.User] = time.Unix(ur.Ts, 0).UTC().Format(time.RFC3339)
			c.mu.Unlock()
		}
	case "user_unretired":
		// Phase 16 Gap 1: inverse of user_retired. The server fires
		// this when an admin runs `sshkey-ctl unretire-user` to
		// reverse a mistaken retirement. We delete the user from
		// c.retired so the [retired] marker is flushed from sidebar
		// labels, info panels, and message headers on the next
		// render. The user's profile entry stays intact — only the
		// retired-state cache is updated.
		//
		// Note: this does NOT restore room/group/DM memberships.
		// The unretirement is intentionally minimal — operators
		// re-add memberships via add-to-room or in-group /add. If
		// the user reconnects after being unretired, the normal
		// handshake flow will re-populate any active context.
		var uu protocol.UserUnretired
		if err := json.Unmarshal(raw, &uu); err == nil {
			c.mu.Lock()
			delete(c.retired, uu.User)
			c.mu.Unlock()
		}
	case "retired_users":
		var ru protocol.RetiredUsers
		if err := json.Unmarshal(raw, &ru); err == nil {
			c.mu.Lock()
			for _, u := range ru.Users {
				c.retired[u.User] = u.RetiredAt
			}
			c.mu.Unlock()
		}
	case "device_revoked":
		// The server will close the SSH channel next. The UI layer receives
		// this event via OnMessage and should call Close() to stop the
		// reconnect loop (otherwise the client will keep trying to auth
		// with the revoked device_id). See tui/app.go device_revoked handler.
	}
}

// storeEpochKey unwraps and stores an epoch key in memory AND persists it
// to the local encrypted DB so it survives across app restarts. Without
// persistence, the client would lose the ability to decrypt past messages.
//
// If the key is already in memory for this (room, epoch), the unwrap and
// DB write are skipped entirely — avoids redundant ECDH+HKDF work when
// the server re-sends a key the client already has (common on reconnect).
func (c *Client) storeEpochKey(room string, epoch int64, wrappedKey string) {
	// Skip if already in memory (server re-sent a key we already have)
	c.mu.RLock()
	existing := c.epochKeys[room][epoch]
	c.mu.RUnlock()
	if existing != nil {
		// Still update currentEpoch in case this is a newer epoch number
		c.mu.Lock()
		if epoch > c.currentEpoch[room] {
			c.currentEpoch[room] = epoch
		}
		c.mu.Unlock()
		return
	}

	key, err := c.UnwrapKey(wrappedKey)
	if err != nil {
		c.logger.Warn("failed to unwrap epoch key", "room", room, "epoch", epoch, "error", err)
		return
	}

	c.mu.Lock()
	if c.epochKeys[room] == nil {
		c.epochKeys[room] = make(map[int64][]byte)
	}
	c.epochKeys[room][epoch] = key
	if epoch > c.currentEpoch[room] {
		c.currentEpoch[room] = epoch
	}
	store := c.store
	c.mu.Unlock()

	// Persist to local DB (survives restart). Failure here is non-fatal —
	// the in-memory copy is usable for this session.
	if store != nil {
		if err := store.StoreEpochKey(room, epoch, key); err != nil {
			c.logger.Warn("failed to persist epoch key", "room", room, "epoch", epoch, "error", err)
		}
	}
}

// keepalive sends periodic SSH keepalive requests to detect dead connections.
func (c *Client) keepalive() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	failures := 0
	const maxFailures = 3

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			if c.conn == nil {
				return
			}
			_, _, err := c.conn.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				failures++
				c.logger.Warn("keepalive failed", "failures", failures, "error", err)
				if failures >= maxFailures {
					c.logger.Error("connection dead — closing after keepalive failures", "failures", failures)
					c.Close()
					return
				}
			} else {
				failures = 0
			}
		}
	}
}

// UserID returns the authenticated user's nanoid.
func (c *Client) UserID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.userID
}

// IsAdmin returns whether the user is a server admin.
func (c *Client) IsAdmin() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.admin
}

// Rooms returns the list of rooms the user has access to.
func (c *Client) Rooms() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rooms
}

// Profile returns the profile for a user.
func (c *Client) Profile(user string) *protocol.Profile {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profiles[user]
}

// FindUserByName searches the profile cache for a user whose display
// name or user ID matches the given name (case-insensitive). Returns
// ("", false) if no match is found. Used by /add in the TUI to
// resolve @user arguments against the pool of users the client has
// ever seen — necessary because /add's target is by definition not
// yet a member of the current group, so GroupMembers() lookups don't
// work. Phase 14.
//
// Retired users are matched — Phase 14 /add explicitly rejects
// retired targets on the server side (ErrUnknownUser), and the
// client pre-check would be wrong to silently filter them out of
// autocomplete.
func (c *Client) FindUserByName(name string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for uid, p := range c.profiles {
		if p != nil && strings.EqualFold(p.DisplayName, name) {
			return uid, true
		}
		if strings.EqualFold(uid, name) {
			return uid, true
		}
	}
	return "", false
}

// IsRetired returns true if the user's account has been retired, along with
// the retirement timestamp. TUI layers use this to render [retired] markers
// on historical messages, disable sends to retired users in 1:1 DMs, and
// exclude them from mention completion / new DM candidates.
func (c *Client) IsRetired(user string) (bool, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ts, ok := c.retired[user]
	return ok, ts
}

// RetiredUsers returns a snapshot of all known retired users mapped to their
// retirement timestamps. Used by the TUI to iterate retirement state.
func (c *Client) RetiredUsers() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]string, len(c.retired))
	for k, v := range c.retired {
		out[k] = v
	}
	return out
}

// PublicKeyAuthorized returns the SSH public key in authorized_keys format
// (e.g., "ssh-ed25519 AAAA... user@host"). Suitable for sharing with admins.
func (c *Client) PublicKeyAuthorized() string {
	if c.signer == nil {
		return ""
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(c.signer.PublicKey())))
}

// KeyFingerprint returns the SHA256 fingerprint of the user's SSH public key.
func (c *Client) KeyFingerprint() string {
	if c.signer == nil {
		return ""
	}
	return ssh.FingerprintSHA256(c.signer.PublicKey())
}

// DisplayName returns the display name for a username (nanoid). Falls back to
// the raw username if no profile is cached — this happens briefly on first
// connect before profiles arrive.
func (c *Client) DisplayName(username string) string {
	c.mu.RLock()
	p := c.profiles[username]
	c.mu.RUnlock()
	if p != nil && p.DisplayName != "" {
		return p.DisplayName
	}
	return username // fallback: show raw ID until profile arrives
}

// DisplayRoomName returns the display name for a room nanoid ID. Reads from
// the local DB (persisted from server room_list). Falls back to the raw ID
// if the room isn't cached yet.
func (c *Client) DisplayRoomName(roomID string) string {
	if c.store != nil {
		return c.store.GetRoomName(roomID)
	}
	return roomID
}

// DisplayRoomTopic returns the topic for a room nanoid ID, or an empty
// string if no topic is set (or the room isn't cached yet). Phase 18:
// parallel to DisplayRoomName, wraps the store helper so TUI code can
// read topics without touching the store directly. Empty-string return
// lets the render layer omit the topic line cleanly via `if topic != ""`.
//
// Live topic updates after the initial room_list are deferred to Phase 16
// (CLI audit + `room_updated` broadcast) — today's resolver reads whatever
// the most recent room_list persisted, which is "current topic as of last
// reconnect". Good enough for the display-only scope of Phase 18.
func (c *Client) DisplayRoomTopic(roomID string) string {
	if c.store != nil {
		return c.store.GetRoomTopic(roomID)
	}
	return ""
}

// SetStoreForTesting attaches a store to the Client from an external
// package. Production code sets c.store during New() / Connect() flows;
// this helper exists so tests in other packages (e.g. tui) can exercise
// methods that read through the store without spinning up a full SSH
// connection. Do not call from production code.
func SetStoreForTesting(c *Client, s *store.Store) {
	c.store = s
}

// SetProfileForTesting adds a profile to the Client's in-memory profile
// cache from an external package. Production code populates c.profiles
// via the readLoop `profile` handler; this helper lets tui-layer tests
// exercise methods that read through the cache (FindUserByName,
// DisplayName, Profile) without spinning up a full SSH connection.
// Phase 21 F29 closure added this alongside the existing
// SetStoreForTesting helper.
// Do not call from production code.
func SetProfileForTesting(c *Client, p *protocol.Profile) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.profiles[p.User] = p
}

// SetUserIDForTesting sets the authenticated local user ID from an
// external package. Production code assigns c.userID during handshake;
// this helper exists for tui-layer tests that need self-identity
// dependent behavior (for example Up-arrow edit targeting) without a
// live SSH session.
//
// Do not call from production code.
func SetUserIDForTesting(c *Client, userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.userID = userID
}

// SetEncoderForTesting attaches a protocol encoder from an external package.
// Production code initializes c.enc during Connect/Reconnect; this helper lets
// package-external tests capture outbound frames without a live SSH session.
// Do not call from production code.
func SetEncoderForTesting(c *Client, enc *protocol.Encoder) {
	c.enc = enc
}

// GroupMembers returns the member list for a group DM.
func (c *Client) GroupMembers(groupID string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.groupMembers[groupID]
}

// GroupAdmins returns the admin user IDs for a group DM, sorted for
// deterministic ordering. Reads the in-memory admin set that the
// dispatch path maintains from group_list catchup and group_event
// {promote,demote} broadcasts. Phase 14.
func (c *Client) GroupAdmins(groupID string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	set, ok := c.groupAdmins[groupID]
	if !ok {
		return nil
	}
	admins := make([]string, 0, len(set))
	for userID := range set {
		admins = append(admins, userID)
	}
	// Sort for deterministic rendering — map iteration order is random.
	for i := 1; i < len(admins); i++ {
		for j := i; j > 0 && admins[j-1] > admins[j]; j-- {
			admins[j-1], admins[j] = admins[j], admins[j-1]
		}
	}
	return admins
}

// IsGroupAdmin reports whether the given user is currently tracked as
// an admin of the given group. False if the group isn't cached or the
// user isn't in its admin set. Used by the info panel and sidebar
// rendering for per-member admin markers. Phase 14.
func (c *Client) IsGroupAdmin(groupID, userID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	set, ok := c.groupAdmins[groupID]
	if !ok {
		return false
	}
	return set[userID]
}

// DMMembers returns the member pair for a 1:1 DM.
func (c *Client) DMMembers(dmID string) [2]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dms[dmID]
}

// DMOther returns the other party in a 1:1 DM (not the current user).
func (c *Client) DMOther(dmID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	pair := c.dms[dmID]
	if pair[0] == c.userID {
		return pair[1]
	}
	return pair[0]
}

// DMs returns the list of 1:1 DM IDs the client knows about.
func (c *Client) DMs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]string, 0, len(c.dms))
	for id := range c.dms {
		ids = append(ids, id)
	}
	return ids
}

// ForEachProfile calls fn for each known user profile.
func (c *Client) ForEachProfile(fn func(p *protocol.Profile)) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, p := range c.profiles {
		fn(p)
	}
}

// Enc returns the protocol encoder for sending raw messages.
func (c *Client) Enc() *protocol.Encoder {
	return c.enc
}

// Done returns a channel that's closed when the client is disconnected.
func (c *Client) Done() <-chan struct{} {
	return c.done
}
