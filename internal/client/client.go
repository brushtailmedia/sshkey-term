// Package client implements the SSH client connection and protocol handling.
package client

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
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
	cfg     Config
	conn    *ssh.Client
	channel ssh.Channel
	enc     *protocol.Encoder
	dec     *protocol.Decoder
	logger  *slog.Logger
	store   *store.Store

	mu          sync.RWMutex
	username    string
	displayName string
	admin       bool
	rooms       []string
	convs       []string
	capabilities []string
	profiles     map[string]*protocol.Profile
	convMembers  map[string][]string         // conversation ID -> member usernames
	epochKeys    map[string]map[int64][]byte // room -> epoch -> unwrapped key
	currentEpoch map[string]int64            // room -> current epoch number
	seqCounters  map[string]int64            // "room:x" or "conv:x" -> next seq
	privKey      ed25519.PrivateKey
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
		convMembers:  make(map[string][]string),
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

	// Perform handshake
	if err := c.handshake(); err != nil {
		ch.Close()
		conn.Close()
		return fmt.Errorf("handshake: %w", err)
	}

	// Open encrypted local DB (key derived from SSH private key)
	if c.cfg.DataDir != "" {
		dbPath := filepath.Join(c.cfg.DataDir, "messages.db")

		// Derive DB encryption key from the SSH private key seed
		dbKey, err := store.DeriveDBKey(c.privKey.Seed())
		if err != nil {
			c.logger.Warn("failed to derive DB key", "error", err)
		}

		st, err := store.Open(dbPath, dbKey)
		if err != nil {
			c.logger.Warn("failed to open local DB", "path", dbPath, "error", err)
		} else {
			c.store = st

			// Load last_synced from DB
			if synced, err := st.GetState("last_synced"); err == nil && synced != "" {
				c.lastSynced = synced
			}
		}
	}

	// Start message loop
	go c.readLoop()

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
	c.username = welcome.User
	c.displayName = welcome.DisplayName
	c.admin = welcome.Admin
	c.rooms = welcome.Rooms
	c.convs = welcome.Conversations
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
	case "dm":
		c.storeDMMessage(raw)
	case "dm_created":
		var dm protocol.DMCreated
		if err := json.Unmarshal(raw, &dm); err == nil {
			c.mu.Lock()
			c.convMembers[dm.Conversation] = dm.Members
			c.mu.Unlock()
		}
	case "conversation_list":
		var cl protocol.ConversationList
		if err := json.Unmarshal(raw, &cl); err == nil {
			c.mu.Lock()
			for _, conv := range cl.Conversations {
				c.convMembers[conv.ID] = conv.Members
			}
			c.mu.Unlock()
		}
	case "conversation_event":
		var ce protocol.ConversationEvent
		if err := json.Unmarshal(raw, &ce); err == nil && ce.Event == "leave" {
			c.mu.Lock()
			if members, ok := c.convMembers[ce.Conversation]; ok {
				filtered := members[:0]
				for _, m := range members {
					if m != ce.User {
						filtered = append(filtered, m)
					}
				}
				c.convMembers[ce.Conversation] = filtered
			}
			c.mu.Unlock()
		}
	case "deleted":
		var d protocol.Deleted
		if err := json.Unmarshal(raw, &d); err == nil && c.store != nil {
			c.store.DeleteMessage(d.ID)
		}
	case "sync_batch":
		c.handleSyncBatchKeys(raw)
		return // sync_batch messages are forwarded from handleSyncBatchKeys
	case "history_result":
		c.handleHistoryKeys(raw)
	}
}

// storeEpochKey unwraps and stores an epoch key.
func (c *Client) storeEpochKey(room string, epoch int64, wrappedKey string) {
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
	c.mu.Unlock()
}

// Username returns the authenticated username.
func (c *Client) Username() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.username
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

// ConvMembers returns the member list for a conversation.
func (c *Client) ConvMembers(convID string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.convMembers[convID]
}

// Enc returns the protocol encoder for sending raw messages.
func (c *Client) Enc() *protocol.Encoder {
	return c.enc
}

// Done returns a channel that's closed when the client is disconnected.
func (c *Client) Done() <-chan struct{} {
	return c.done
}
