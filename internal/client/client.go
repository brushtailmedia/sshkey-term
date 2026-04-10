// Package client implements the SSH client connection and protocol handling.
package client

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
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
	DataDir  string // per-server data directory (e.g., ~/.sshkey-chat/chat.example.com/)

	// Callbacks
	OnMessage    func(msgType string, raw json.RawMessage)
	OnError      func(err error)
	OnPassphrase PassphraseFunc // called if key is passphrase-protected

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

	mu          sync.RWMutex
	userID      string // nanoid (usr_ prefix) — immutable identity
	displayName string
	admin       bool
	rooms       []string
	groups      []string
	capabilities []string
	profiles     map[string]*protocol.Profile
	groupMembers map[string][]string         // group DM ID -> member userIDs
	dms          map[string][2]string        // 1:1 DM ID -> [userA, userB]
	retired      map[string]string           // retired userID -> retired_at timestamp
	epochKeys      map[string]map[int64][]byte // room -> epoch -> unwrapped key
	currentEpoch   map[string]int64            // room -> current epoch number
	seqCounters    map[string]int64            // "room:x" or "group:x" or "dm:x" -> next seq
	hasPendingKeys  bool                        // true when admin_notify arrived (cleared on list refresh)
	pendingKeys     []protocol.PendingKeyEntry  // populated by pending_keys_list
	roomMembersRoom string                      // room for latest room_members_list
	roomMembers     []string                    // member usernames from room_members_list
	privKey        ed25519.PrivateKey
	signer      ssh.Signer
	lastSynced  string

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
		dms:          make(map[string][2]string),
		retired:      make(map[string]string),
		epochKeys:    make(map[string]map[int64][]byte),
		currentEpoch: make(map[string]int64),
		seqCounters:  make(map[string]int64),
		done:         make(chan struct{}),
	}
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
			if err != io.EOF {
				if c.cfg.OnError != nil {
					c.cfg.OnError(err)
				}
			}
			return
		}

		msgType, err := protocol.TypeOf(raw)
		if err != nil {
			continue
		}

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
		c.storeRoomMessage(raw)
	case "group_message":
		c.storeGroupMessage(raw)
	case "group_created":
		var g protocol.GroupCreated
		if err := json.Unmarshal(raw, &g); err == nil {
			c.mu.Lock()
			c.groupMembers[g.Group] = g.Members
			c.mu.Unlock()
			if c.store != nil {
				c.store.StoreGroup(g.Group, g.Name, strings.Join(g.Members, ","))
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

			// Multi-device offline catchup for room /leave (and admin
			// removal — rooms only): any locally-active room not in the
			// server's authoritative list means we either left it on
			// another device while this one was offline, or an admin
			// removed us. Mark it as left so the sidebar treats it
			// consistently with the device that initiated the leave.
			// Same pattern as the group_list reconciliation.
			serverIDs := make(map[string]bool, len(rl.Rooms))
			for _, r := range rl.Rooms {
				serverIDs[r.ID] = true
			}
			if activeIDs, err := c.store.GetActiveRoomIDs(); err == nil {
				now := time.Now().Unix()
				for _, id := range activeIDs {
					if !serverIDs[id] {
						if err := c.store.MarkRoomLeft(id, now); err != nil {
							c.logger.Warn("MarkRoomLeft on room_list reconciliation",
								"room", id, "error", err)
						}
					}
				}
			}
		}
	case "group_list":
		var gl protocol.GroupList
		if err := json.Unmarshal(raw, &gl); err == nil {
			c.mu.Lock()
			for _, g := range gl.Groups {
				c.groupMembers[g.ID] = g.Members
			}
			c.mu.Unlock()
			if c.store != nil {
				for _, g := range gl.Groups {
					c.store.StoreGroup(g.ID, g.Name, strings.Join(g.Members, ","))
				}

				// Multi-device offline catchup for /leave: any locally-
				// active group (left_at == 0) that's not in the server's
				// authoritative response means we left it on another
				// device while this one was offline. Mark it as left so
				// the sidebar treats it consistently with the device
				// that initiated the leave (greyed/read-only).
				//
				// 1:1 DMs handle this via LeftAtForCaller in dm_list
				// because the server keeps the row alive after leave;
				// groups can't use that pattern because /leave actually
				// removes the user from group_members. The reconciliation
				// is the client-side analog.
				//
				// Composes correctly with the deleted_groups handler:
				// deleted_groups arrives BEFORE group_list in the
				// handshake, so by the time we reach this point, any
				// /delete'd groups have already been marked as left
				// AND purged. The reconciliation only fires on local
				// rows that are still active — the genuine /leave-on-
				// other-device cases.
				serverIDs := make(map[string]bool, len(gl.Groups))
				for _, g := range gl.Groups {
					serverIDs[g.ID] = true
				}
				if activeIDs, err := c.store.GetActiveGroupIDs(); err == nil {
					now := time.Now().Unix()
					for _, id := range activeIDs {
						if !serverIDs[id] {
							if err := c.store.MarkGroupLeft(id, now); err != nil {
								c.logger.Warn("MarkGroupLeft on group_list reconciliation",
									"group", id, "error", err)
							}
							c.mu.Lock()
							delete(c.groupMembers, id)
							c.mu.Unlock()
						}
					}
				}
			}
		}
	case "group_event":
		var ge protocol.GroupEvent
		if err := json.Unmarshal(raw, &ge); err == nil && ge.Event == "leave" {
			c.mu.Lock()
			if members, ok := c.groupMembers[ge.Group]; ok {
				filtered := members[:0]
				for _, m := range members {
					if m != ge.User {
						filtered = append(filtered, m)
					}
				}
				c.groupMembers[ge.Group] = filtered
			}
			c.mu.Unlock()
			// Note: the leaver never receives this event — they have already
			// been removed from group_members, so they are not in the broadcast
			// set. Self-leave is handled by "group_left".
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
				if err := c.store.MarkGroupLeft(gl.Group, time.Now().Unix()); err != nil {
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
				if err := c.store.MarkGroupLeft(gd.Group, time.Now().Unix()); err != nil {
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
					if err := c.store.MarkGroupLeft(groupID, time.Now().Unix()); err != nil {
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
	case "room_left":
		var rl protocol.RoomLeft
		if err := json.Unmarshal(raw, &rl); err == nil {
			// Server confirmed our leave_room — mark the room archived in the
			// local DB. The room metadata row stays so the sidebar can render
			// the entry as greyed/read-only on this and subsequent reconnects.
			if c.store != nil {
				if err := c.store.MarkRoomLeft(rl.Room, time.Now().Unix()); err != nil {
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
				if err := c.store.MarkRoomLeft(rd.Room, time.Now().Unix()); err != nil {
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
					if err := c.store.MarkRoomLeft(roomID, now); err != nil {
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
		c.storeDMMessage(raw)
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

// GroupMembers returns the member list for a group DM.
func (c *Client) GroupMembers(groupID string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.groupMembers[groupID]
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
