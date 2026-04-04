package client

import (
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// ReconnectConfig controls reconnection behaviour.
type ReconnectConfig struct {
	InitialDelay time.Duration
	MaxDelay     time.Duration
	MaxAttempts  int // 0 = unlimited
}

var DefaultReconnect = ReconnectConfig{
	InitialDelay: time.Second,
	MaxDelay:     60 * time.Second,
	MaxAttempts:  0,
}

// reconnectLoop attempts to reconnect with exponential backoff.
// Calls onStatus with the current state ("reconnecting", "connected", "failed").
func (c *Client) reconnectLoop(onStatus func(status string, attempt int, nextRetry time.Duration)) {
	cfg := DefaultReconnect
	delay := cfg.InitialDelay
	attempt := 0

	for {
		select {
		case <-c.done:
			return
		default:
		}

		attempt++
		if cfg.MaxAttempts > 0 && attempt > cfg.MaxAttempts {
			if onStatus != nil {
				onStatus("failed", attempt, 0)
			}
			return
		}

		if onStatus != nil {
			onStatus("reconnecting", attempt, delay)
		}

		// Wait before retry
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-c.done:
			timer.Stop()
			return
		}

		// Try to connect
		c.logger.Info("reconnecting", "attempt", attempt, "delay", delay)

		err := c.doConnect()
		if err != nil {
			c.logger.Warn("reconnect failed", "attempt", attempt, "error", err)

			// Exponential backoff
			delay *= 2
			if delay > cfg.MaxDelay {
				delay = cfg.MaxDelay
			}
			continue
		}

		// Success
		delay = cfg.InitialDelay
		attempt = 0
		if onStatus != nil {
			onStatus("connected", 0, 0)
		}

		// Run message loop (blocks until disconnected)
		c.readLoop()

		// If we get here, we disconnected — try again
		c.logger.Info("disconnected, will reconnect")
	}
}

// doConnect performs the SSH connection and handshake without starting the read loop.
func (c *Client) doConnect() error {
	signer, err := loadSSHKey(c.cfg.KeyPath, c.cfg.OnPassphrase)
	if err != nil {
		return err
	}
	c.signer = signer

	c.privKey, err = ParseRawEd25519Key(c.cfg.KeyPath, c.cfg.OnPassphrase)
	if err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)
	sshCfg := &ssh.ClientConfig{
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback(c.cfg.DataDir, c.cfg.Host),
		Timeout:         10 * time.Second,
	}

	conn, err := ssh.Dial("tcp", addr, sshCfg)
	if err != nil {
		return err
	}
	c.conn = conn

	ch, reqs, err := conn.OpenChannel("session", nil)
	if err != nil {
		conn.Close()
		return err
	}
	go ssh.DiscardRequests(reqs)

	c.channel = ch
	c.enc = protocol.NewEncoder(ch)
	c.dec = protocol.NewDecoder(ch)

	return c.handshake()
}

// ConnectWithReconnect connects and starts a reconnect loop in the background.
func (c *Client) ConnectWithReconnect(onStatus func(status string, attempt int, nextRetry time.Duration)) error {
	// Initial connect
	if err := c.Connect(); err != nil {
		// Start reconnect loop in background
		go c.reconnectLoop(onStatus)
		return err
	}

	// Connected — start a goroutine that reconnects on disconnect
	go func() {
		// Wait for the read loop to finish (disconnect)
		<-c.readerDone()

		// Don't reconnect if explicitly closed
		select {
		case <-c.done:
			return
		default:
		}

		c.reconnectLoop(onStatus)
	}()

	return nil
}

// readerDone returns a channel that closes when the read loop exits.
func (c *Client) readerDone() <-chan struct{} {
	// The read loop exits when it hits EOF or an error.
	// We use a separate channel for this.
	ch := make(chan struct{})
	go func() {
		// Poll until the connection is dead
		for {
			select {
			case <-c.done:
				close(ch)
				return
			case <-time.After(time.Second):
				if c.conn == nil {
					close(ch)
					return
				}
			}
		}
	}()
	return ch
}
