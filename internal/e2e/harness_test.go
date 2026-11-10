//go:build e2e

package e2e

// The harness: binary discovery, one server process per test, the real ctl
// for bootstrap, real clients with a frame recorder, bounded waits. No
// time.Sleep anywhere — every wait is a ticker or a channel with a deadline.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/testutil"
)

// Binaries resolved once by TestMain.
var (
	serverBin  string
	ctlBin     string
	skipReason string
)

// defaultWait is the deadline for every wait unless SSHKEY_E2E_TIMEOUT says
// otherwise.
var defaultWait = 10 * time.Second

func TestMain(m *testing.M) {
	if v := os.Getenv("SSHKEY_E2E_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			defaultWait = d
		}
	}
	os.Exit(runMain(m))
}

// runMain resolves the binaries — prebuilt (SSHKEY_E2E_BIN) or built from a
// server checkout (SSHKEY_CHAT_REPO) — and runs the suite; with neither set
// every test skips. Returned rather than exiting so the build dir's cleanup
// runs.
func runMain(m *testing.M) int {
	if dir := os.Getenv("SSHKEY_E2E_BIN"); dir != "" {
		serverBin = filepath.Join(dir, "sshkey-server")
		ctlBin = filepath.Join(dir, "sshkey-ctl")
		for _, b := range []string{serverBin, ctlBin} {
			if _, err := os.Stat(b); err != nil {
				skipReason = fmt.Sprintf("e2e: %s not found under SSHKEY_E2E_BIN=%s", filepath.Base(b), dir)
				break
			}
		}
		return m.Run()
	}
	repo := os.Getenv("SSHKEY_CHAT_REPO")
	if repo == "" {
		skipReason = "e2e: set SSHKEY_E2E_BIN (prebuilt sshkey-server + sshkey-ctl) or SSHKEY_CHAT_REPO (a sshkey-chat checkout to build them from)"
		return m.Run()
	}
	buildDir, err := os.MkdirTemp("", "sshkey-e2e-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		return 1
	}
	defer os.RemoveAll(buildDir)
	goBin := os.Getenv("SSHKEY_E2E_GO")
	if goBin == "" {
		p, err := exec.LookPath("go")
		if err != nil {
			fmt.Fprintln(os.Stderr, "e2e: no go on PATH and SSHKEY_E2E_GO unset; cannot build the server binaries from SSHKEY_CHAT_REPO")
			return 1
		}
		goBin = p
	}
	for _, b := range [][2]string{{"sshkey-server", "./cmd/sshkey-server"}, {"sshkey-ctl", "./cmd/sshkey-ctl"}} {
		out := filepath.Join(buildDir, b[0])
		cmd := exec.Command(goBin, "build", "-o", out, b[1])
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if combined, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "e2e: building %s from %s: %v\n%s", b[0], repo, err, combined)
			return 1
		}
	}
	serverBin = filepath.Join(buildDir, "sshkey-server")
	ctlBin = filepath.Join(buildDir, "sshkey-ctl")
	return m.Run()
}

// ----- environment: one server process per test -----

type envOptions struct {
	configOverride func(toml string) string
}

type envOption func(*envOptions)

// Env is one running server with its config dir, data dir, log and the
// sessions connected to it.
type Env struct {
	t         *testing.T
	root      string
	configDir string
	dataDir   string
	logPath   string
	port      int

	cmd    *exec.Cmd
	exited chan error
	sink   *logSink

	mu       sync.Mutex
	sessions []*Session
	stopped  bool
}

// start writes the config, launches sshkey-server on port 0 and waits for
// the address it bound. The test's cleanup stops the server and removes the
// working directory unless the test failed (or SSHKEY_E2E_KEEP=1).
func start(t *testing.T, opts ...envOption) *Env {
	t.Helper()
	if skipReason != "" {
		t.Skip(skipReason)
	}
	var o envOptions
	for _, opt := range opts {
		opt(&o)
	}
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	root, err := os.MkdirTemp("", "sshkey-e2e-"+safe+"-")
	if err != nil {
		t.Fatal(err)
	}
	e := &Env{
		t:         t,
		root:      root,
		configDir: filepath.Join(root, "config"),
		dataDir:   filepath.Join(root, "data"),
		logPath:   filepath.Join(root, "server.log"),
	}
	for _, d := range []string{e.configDir, e.dataDir, filepath.Join(root, "keys"), filepath.Join(root, "clients")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	toml := serverTOML
	if o.configOverride != nil {
		toml = o.configOverride(toml)
	}
	if err := os.WriteFile(filepath.Join(e.configDir, "server.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	e.startServer()
	return e
}

// logSink receives the server's stdout and stderr: every line goes to the
// log file, and the first "server listening" record yields the bound port.
// Used as cmd.Stdout/Stderr directly so cmd.Wait owns the pipes.
type logSink struct {
	mu      sync.Mutex
	file    *os.File
	partial []byte
	portCh  chan int
	found   bool
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.file.Write(p)
	s.partial = append(s.partial, p...)
	for {
		i := indexByte(s.partial, '\n')
		if i < 0 {
			break
		}
		line := s.partial[:i]
		s.partial = s.partial[i+1:]
		if s.found {
			continue
		}
		var rec struct {
			Msg  string `json:"msg"`
			Addr string `json:"addr"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.Msg != "server listening" {
			continue
		}
		if _, portStr, err := net.SplitHostPort(rec.Addr); err == nil {
			if n, err := strconv.Atoi(portStr); err == nil && n > 0 {
				s.found = true
				s.portCh <- n
			}
		}
	}
	return len(p), nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func (e *Env) startServer() {
	t := e.t
	t.Helper()
	f, err := os.Create(e.logPath)
	if err != nil {
		t.Fatal(err)
	}
	e.sink = &logSink{file: f, portCh: make(chan int, 1)}
	cmd := exec.Command(serverBin, "-config", e.configDir, "-data", e.dataDir)
	cmd.Stdout = e.sink
	cmd.Stderr = e.sink
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", serverBin, err)
	}
	e.cmd = cmd
	e.exited = make(chan error, 1)
	go func() { e.exited <- cmd.Wait() }()

	select {
	case p := <-e.sink.portCh:
		e.port = p
	case err := <-e.exited:
		t.Fatalf("server exited before listening: %v\n%s", err, e.tailLog())
	case <-time.After(20 * time.Second):
		t.Fatalf("server did not report a listening address within 20s\n%s", e.tailLog())
	}
}

// tailLog returns the last 50 lines of the server log for failure output.
func (e *Env) tailLog() string {
	b, err := os.ReadFile(e.logPath)
	if err != nil {
		return "(no server log)"
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > 50 {
		lines = lines[len(lines)-50:]
	}
	return "--- server log (tail) ---\n" + strings.Join(lines, "\n")
}

// StopServer sends SIGTERM and requires a clean exit within 10s; the data
// dir is left in place (StopServer is what a test calls to inspect it).
func (e *Env) StopServer() {
	t := e.t
	e.mu.Lock()
	cmd := e.cmd
	e.cmd = nil
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-e.exited:
		if err != nil {
			t.Errorf("server exit after SIGTERM: %v, want a clean exit\n%s", err, e.tailLog())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-e.exited
		t.Errorf("server did not exit within 10s of SIGTERM; killed\n%s", e.tailLog())
	}
	e.sink.mu.Lock()
	e.sink.file.Close()
	e.sink.mu.Unlock()
}

// Stop closes every session, stops the server and removes the working dir
// — unless the test failed or SSHKEY_E2E_KEEP=1, in which case the dir is
// kept and its path printed.
func (e *Env) Stop() {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return
	}
	e.stopped = true
	sessions := e.sessions
	e.mu.Unlock()
	for _, s := range sessions {
		_ = s.C.Close()
	}
	e.StopServer()
	if e.t.Failed() || os.Getenv("SSHKEY_E2E_KEEP") == "1" {
		e.t.Logf("e2e: kept %s (server log: %s)", e.root, e.logPath)
		return
	}
	os.RemoveAll(e.root)
}

// Ctl runs sshkey-ctl against this server's config and data dirs and
// returns its combined output; a non-zero exit fails the test with it.
func (e *Env) Ctl(args ...string) string {
	e.t.Helper()
	full := append([]string{"--config", e.configDir, "--data", e.dataDir}, args...)
	out, err := exec.Command(ctlBin, full...).CombinedOutput()
	if err != nil {
		e.t.Fatalf("sshkey-ctl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// ----- users -----

// User is a keypair on disk plus, once approved and resolved, its server ID.
type User struct {
	Name    string
	KeyPath string
	PubLine string // "ssh-ed25519 AAAA…" as approve wants it
	ID      string // usr_… after ResolveIDs
}

// NewKey generates an Ed25519 key for name and writes it the way the term
// does (OpenSSH PEM); the user is not approved.
func (e *Env) NewKey(name string) *User {
	t := e.t
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(e.root, "keys", name+"_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return &User{Name: name, KeyPath: keyPath, PubLine: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))}
}

// NewUser is NewKey plus approval through the real ctl, with a display name
// and room memberships (rooms must exist — add-room first).
func (e *Env) NewUser(name string, admin bool, rooms ...string) *User {
	e.t.Helper()
	u := e.NewKey(name)
	args := []string{"approve", "--key", u.PubLine, "--name", name}
	if len(rooms) > 0 {
		args = append(args, "--rooms", strings.Join(rooms, ","))
	}
	if admin {
		args = append(args, "--admin")
	}
	e.Ctl(args...)
	return u
}

// ResolveIDs fills User.ID from one `list-users` run (approve prints only
// the display name).
func (e *Env) ResolveIDs(users ...*User) {
	t := e.t
	t.Helper()
	out := e.Ctl("list-users")
	byName := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "usr_") {
			continue
		}
		rest := strings.Join(fields[1:], " ")
		const key = `display_name="`
		i := strings.Index(rest, key)
		if i < 0 {
			continue
		}
		name := rest[i+len(key):]
		if j := strings.Index(name, `"`); j >= 0 {
			byName[name[:j]] = fields[0]
		}
	}
	for _, u := range users {
		id, ok := byName[u.Name]
		if !ok {
			t.Fatalf("list-users has no %q:\n%s", u.Name, out)
		}
		u.ID = id
	}
}

// deviceID derives a production-shape device id (dev_ + 21 alphabet
// characters) from a label, so a test can name devices "a1", "b2".
func deviceID(label string) string { return testutil.DeviceID(label) }

// ----- sessions: a real client with a frame recorder -----

type frame struct {
	typ string
	raw json.RawMessage
}

// recorder keeps every inbound frame. OnMessage appends under a mutex and
// returns — it never blocks the client's read loop beyond that append —
// and waiters scan the slice, so two waiters may match the same frame and
// nothing is ever consumed.
type recorder struct {
	mu     sync.Mutex
	frames []frame
	errs   []error
}

func (r *recorder) push(typ string, raw json.RawMessage) {
	cp := append(json.RawMessage(nil), raw...)
	r.mu.Lock()
	r.frames = append(r.frames, frame{typ: typ, raw: cp})
	r.mu.Unlock()
}

func (r *recorder) pushErr(err error) {
	r.mu.Lock()
	r.errs = append(r.errs, err)
	r.mu.Unlock()
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.frames)
}

func (r *recorder) find(from int, typ string, pred func(json.RawMessage) bool) (json.RawMessage, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := from; i < len(r.frames); i++ {
		f := r.frames[i]
		if f.typ != typ {
			continue
		}
		if pred == nil || pred(f.raw) {
			return f.raw, true
		}
	}
	return nil, false
}

func (r *recorder) summary() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	counts := map[string]int{}
	for _, f := range r.frames {
		counts[f.typ]++
	}
	var parts []string
	for typ, n := range counts {
		parts = append(parts, fmt.Sprintf("%s×%d", typ, n))
	}
	if len(r.errs) > 0 {
		parts = append(parts, fmt.Sprintf("errors=%v", r.errs))
	}
	return strings.Join(parts, " ")
}

// Session is one connected device of one user.
type Session struct {
	t    *testing.T
	env  *Env
	User *User
	C    *client.Client
	rec  *recorder
}

// newClient builds a real client for u on this server with a fresh data
// dir per device.
func (e *Env) newClient(u *User, device string) (*client.Client, *recorder, chan struct{}) {
	t := e.t
	t.Helper()
	dataDir := filepath.Join(e.root, "clients", u.Name+"-"+device)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	synced := make(chan struct{}, 1)
	c := client.New(client.Config{
		Host:     "127.0.0.1",
		Port:     e.port,
		KeyPath:  u.KeyPath,
		DeviceID: deviceID(u.Name + "/" + device),
		DataDir:  dataDir,
		User:     u.Name, // the requested-name hint an unapproved key carries
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnMessage: func(typ string, raw json.RawMessage) {
			rec.push(typ, raw)
			if typ == "sync_complete" {
				select {
				case synced <- struct{}{}:
				default:
				}
			}
		},
		OnError: rec.pushErr,
	})
	return c, rec, synced
}

// Connect connects u's device and waits for sync_complete.
func (e *Env) Connect(u *User, device string) *Session {
	t := e.t
	t.Helper()
	c, rec, synced := e.newClient(u, device)
	if err := c.Connect(); err != nil {
		t.Fatalf("connect %s/%s: %v\n%s", u.Name, device, err, e.tailLog())
	}
	s := &Session{t: t, env: e, User: u, C: c, rec: rec}
	e.mu.Lock()
	e.sessions = append(e.sessions, s)
	e.mu.Unlock()
	select {
	case <-synced:
	case <-time.After(defaultWait):
		t.Fatalf("%s/%s: no sync_complete within %s; frames: %s\n%s", u.Name, device, defaultWait, rec.summary(), e.tailLog())
	}
	if c.Store() == nil {
		t.Fatalf("%s/%s: local store did not open", u.Name, device)
	}
	return s
}

// TryConnect returns the connect error instead of failing (for keys that
// must be refused).
func (e *Env) TryConnect(u *User, device string) error {
	c, _, _ := e.newClient(u, device)
	err := c.Connect()
	if err == nil {
		e.mu.Lock()
		e.sessions = append(e.sessions, &Session{t: e.t, env: e, User: u, C: c, rec: &recorder{}})
		e.mu.Unlock()
	}
	return err
}

// Mark returns a cursor: WaitAfter(mark, …) only matches frames recorded
// after it, for "the next one of these".
func (s *Session) Mark() int { return s.rec.count() }

// Wait returns the first recorded frame of typ (any time), failing the test
// after the default deadline.
func (s *Session) Wait(typ string) json.RawMessage {
	s.t.Helper()
	return s.WaitAfter(0, typ, nil)
}

// WaitWhere is Wait with a predicate on the frame.
func (s *Session) WaitWhere(typ string, pred func(json.RawMessage) bool) json.RawMessage {
	s.t.Helper()
	return s.WaitAfter(0, typ, pred)
}

// WaitAfter polls the recorder on a 10ms ticker until a matching frame
// recorded at or after mark appears, or the deadline passes.
func (s *Session) WaitAfter(mark int, typ string, pred func(json.RawMessage) bool) json.RawMessage {
	t := s.t
	t.Helper()
	deadline := time.Now().Add(defaultWait)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if raw, ok := s.rec.find(mark, typ, pred); ok {
			return raw
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: no matching %q frame within %s; frames seen: %s\n%s", s.User.Name, typ, defaultWait, s.rec.summary(), s.env.tailLog())
		}
		<-tick.C
	}
}

// ExpectNone is a bounded negative check: no matching frame recorded at or
// after mark within d. The positive checks around it prove the channel is
// live; this proves the server (or the client) did not send.
func (s *Session) ExpectNone(mark int, typ string, pred func(json.RawMessage) bool, d time.Duration) {
	t := s.t
	t.Helper()
	deadline := time.Now().Add(d)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if raw, ok := s.rec.find(mark, typ, pred); ok {
			t.Fatalf("%s: unexpected %q frame: %s", s.User.Name, typ, raw)
		}
		if time.Now().After(deadline) {
			return
		}
		<-tick.C
	}
}

// Raw writes a frame exactly as given, bypassing the client's own checks —
// for frames the real client would refuse to build, and for verbs the term
// still sends by hand (leave_group).
func (s *Session) Raw(v any) {
	s.t.Helper()
	if err := s.C.Enc().Encode(v); err != nil {
		s.t.Fatalf("%s: raw encode: %v", s.User.Name, err)
	}
}

// ----- small parsers and predicates -----

// decodeOK reports whether raw unmarshals into v — for predicates.
func decodeOK(raw json.RawMessage, v any) bool {
	return json.Unmarshal(raw, v) == nil
}

func decode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %T from %s: %v", v, raw, err)
	}
	return v
}

func groupIs(group string) func(json.RawMessage) bool {
	return func(raw json.RawMessage) bool {
		var v struct {
			Group string `json:"group"`
		}
		return json.Unmarshal(raw, &v) == nil && v.Group == group
	}
}

func msgIs(id string) func(json.RawMessage) bool {
	return func(raw json.RawMessage) bool {
		var v struct {
			ID string `json:"id"`
		}
		return json.Unmarshal(raw, &v) == nil && v.ID == id
	}
}

func codeIs(code string) func(json.RawMessage) bool {
	return func(raw json.RawMessage) bool {
		var v protocol.Error
		return json.Unmarshal(raw, &v) == nil && v.Code == code
	}
}

func groupEventIs(group, event, user string) func(json.RawMessage) bool {
	return func(raw json.RawMessage) bool {
		var v protocol.GroupEvent
		return json.Unmarshal(raw, &v) == nil && v.Group == group && v.Event == event && v.User == user
	}
}

func roomEventIs(room, event, user string) func(json.RawMessage) bool {
	return func(raw json.RawMessage) bool {
		var v protocol.RoomEvent
		return json.Unmarshal(raw, &v) == nil && v.Room == room && v.Event == event && v.User == user
	}
}

// waitStoredBody polls the session's local store until the message is
// there with the given plaintext body — the proof that the real client
// decrypted what the real server relayed.
func (s *Session) waitStoredBody(group, msgID, body string) {
	t := s.t
	t.Helper()
	deadline := time.Now().Add(defaultWait)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		msgs, err := s.C.Store().GetGroupMessages(group, 500)
		if err == nil {
			for _, m := range msgs {
				if m.ID == msgID && m.Body == body {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			var got []string
			for _, m := range msgs {
				got = append(got, fmt.Sprintf("%s=%q", m.ID, m.Body))
			}
			t.Fatalf("%s: message %s with body %q not in the local store within %s (err=%v); have %v", s.User.Name, msgID, body, defaultWait, err, got)
		}
		<-tick.C
	}
}
