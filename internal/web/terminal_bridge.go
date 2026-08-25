package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"

	"github.com/asheshgoplani/agent-deck/internal/tmuxutf8"
)

var ErrTmuxSessionNotFound = errors.New("tmux session not found")

type wsConnWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func newWSConnWriter(conn *websocket.Conn) *wsConnWriter {
	return &wsConnWriter{conn: conn}
}

func (w *wsConnWriter) WriteJSON(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return w.conn.WriteJSON(v)
}

func (w *wsConnWriter) WriteBinary(data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return w.conn.WriteMessage(websocket.BinaryMessage, data)
}

type tmuxPTYBridge struct {
	tmuxSession    string
	tmuxSocketName string // tmux -L selector captured from Instance (issue #687)
	sessionID      string
	writer         *wsConnWriter

	cmd *exec.Cmd

	// ptmxMu guards ptmx against a concurrent Close/Resize race. Close
	// closes the PTY file and nils the pointer under the write lock;
	// Resize reads under the read lock so Setsize cannot hit a freshly
	// closed fd. Observed as an intermittent TestTmuxPTYBridgeResize
	// -race failure on CI (v1.7.4, v1.7.5 release workflows).
	ptmxMu sync.RWMutex
	ptmx   *os.File

	closeOnce sync.Once
	done      chan struct{}
}

func newTmuxPTYBridge(tmuxSession, tmuxSocketName, sessionID string, writer *wsConnWriter) (*tmuxPTYBridge, error) {
	if tmuxSession == "" {
		return nil, fmt.Errorf("tmux session name is required")
	}
	if writer == nil {
		return nil, fmt.Errorf("writer is required")
	}
	exists, err := tmuxSessionExists(tmuxSession, tmuxSocketName)
	if err != nil {
		return nil, fmt.Errorf("check tmux session %q: %w", tmuxSession, err)
	}
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrTmuxSessionNotFound, tmuxSession)
	}

	cmd := tmuxAttachCommand(tmuxSession, tmuxSocketName)

	// #issue: pty.Start (no size) leaves this attach client's PTY at
	// whatever the kernel handed back for a freshly-opened pty -- 0x0 --
	// until the browser's first real resize message lands a round-trip
	// later. Since Session.Start sets `window-size latest` (internal/tmux/
	// tmux.go), tmux re-arbitrates the pane to match the MOST RECENT
	// client the instant this one attaches, shrinking a healthy birth size
	// down to 0x0 and delivering SIGWINCH. For most tools that's a
	// harmless, self-correcting reflow -- but it lands inside the very
	// first raw-mode paint of Claude Code's untrusted-folder trust prompt
	// (any brand-new project directory), which doesn't survive a 0x0
	// resize: the process exits within ~300ms of spawn, tmux tears the
	// session down with it, and the browser's corrective resize never gets
	// a chance to land. Starting the attach client's PTY at the session's
	// actual current size (or a sane non-zero fallback) closes the window.
	size := tmuxPaneStartupSize(tmuxSession, tmuxSocketName)
	ptmx, err := pty.StartWithSize(cmd, &size)
	if err != nil {
		return nil, fmt.Errorf("start tmux pty: %w", err)
	}

	b := &tmuxPTYBridge{
		tmuxSession:    tmuxSession,
		tmuxSocketName: tmuxSocketName,
		sessionID:      sessionID,
		writer:         writer,
		cmd:            cmd,
		ptmx:           ptmx,
		done:           make(chan struct{}),
	}

	go b.streamOutput()
	return b, nil
}

// snapshotPtmx returns the current ptmx *os.File under RLock. It returns
// nil if the bridge has been Closed. Consumers (WriteInput, streamOutput)
// use this to read the field race-free with respect to Close()'s
// Lock-guarded `b.ptmx = nil` store. The returned *os.File itself is
// goroutine-safe with respect to Close (Go's runtime poller handles
// Close vs. blocked I/O), so callers need not hold the RLock during the
// I/O syscall. (V1.9 T5, race-review 2.1.)
func (b *tmuxPTYBridge) snapshotPtmx() *os.File {
	b.ptmxMu.RLock()
	defer b.ptmxMu.RUnlock()
	return b.ptmx
}

func (b *tmuxPTYBridge) streamOutput() {
	defer close(b.done)

	buf := make([]byte, 4096)
	for {
		ptmx := b.snapshotPtmx()
		if ptmx == nil {
			return
		}
		n, err := ptmx.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if writeErr := b.writer.WriteBinary(chunk); writeErr != nil {
				b.Close()
				return
			}
		}

		if err != nil {
			if !errors.Is(err, io.EOF) {
				_ = b.writer.WriteJSON(wsServerMessage{
					Type:      "status",
					Event:     "session_closed",
					SessionID: b.sessionID,
					Time:      time.Now().UTC(),
				})
			}
			b.Close()
			return
		}
	}
}

func (b *tmuxPTYBridge) WriteInput(data string) error {
	if b == nil {
		return fmt.Errorf("bridge not initialized")
	}
	if data == "" {
		return nil
	}
	ptmx := b.snapshotPtmx()
	if ptmx == nil {
		return fmt.Errorf("bridge not initialized")
	}
	_, err := ptmx.Write([]byte(data))
	return err
}

func (b *tmuxPTYBridge) Resize(cols, rows int) error {
	if b == nil {
		return fmt.Errorf("bridge not initialized")
	}
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("invalid dimensions: cols=%d rows=%d", cols, rows)
	}
	if cols < 10 || rows < 3 {
		return fmt.Errorf("dimensions too small for a usable terminal: cols=%d rows=%d", cols, rows)
	}

	b.ptmxMu.RLock()
	defer b.ptmxMu.RUnlock()
	if b.ptmx == nil {
		return fmt.Errorf("bridge not initialized")
	}

	// Resize the local PTY master. This sends SIGWINCH to the tmux attach
	// process. Because the attach client (see tmuxAttachCommand) is no longer
	// flagged `-f ignore-size`, the tmux server now uses this client's PTY
	// size as its declared geometry and re-arbitrates the window dimensions
	// per the session's `window-size` policy (`latest` as of #issue "Fix
	// terminal text clipping on narrow clients (phone)" — set at
	// Session.Start in internal/tmux/tmux.go; was `largest` before that).
	// The previous `tmux resize-window` call here was removed because it
	// implicitly flipped the session option to `window-size=manual` and
	// pinned the window to the web viewport, which dragged native attached
	// clients (Ghostty, iTerm) along with it. Under `latest`, tmux instead
	// re-arbitrates to whichever client was MOST RECENTLY active — this is
	// exactly why newTmuxPTYBridge's attach must never start its PTY at a
	// degenerate 0x0 size (see tmuxPaneStartupSize): under this policy a
	// brand-new, not-yet-sized client can shrink the pane, not just grow it.
	if err := pty.Setsize(b.ptmx, &pty.Winsize{
		Rows: uint16(rows), // #nosec G115 -- terminal rows fits in uint16; PTY ABI enforces this
		Cols: uint16(cols), // #nosec G115 -- terminal cols fits in uint16; PTY ABI enforces this
	}); err != nil {
		return fmt.Errorf("resize pty: %w", err)
	}

	return nil
}

func (b *tmuxPTYBridge) Close() {
	if b == nil {
		return
	}
	b.closeOnce.Do(func() {
		b.ptmxMu.Lock()
		if b.ptmx != nil {
			_ = b.ptmx.Close()
			b.ptmx = nil
		}
		b.ptmxMu.Unlock()
		if b.cmd != nil && b.cmd.Process != nil {
			pgid, err := syscall.Getpgid(b.cmd.Process.Pid)
			if err == nil {
				_ = syscall.Kill(-pgid, syscall.SIGTERM)
			} else {
				_ = b.cmd.Process.Kill()
			}
		}
		if b.cmd != nil {
			_ = b.cmd.Wait()
		}
	})
}

// tmuxHasSessionProbeTimeout bounds the has-session existence probe. The web
// bridge re-probes on every (re)connect, which is a cadence: on tmux 3.0a a
// client that has exhausted its fd table spins at 100% CPU in EMFILE retries
// and never exits, so an unbounded probe both hangs the connect request and
// leaks a core-burning orphan per reconnect attempt.
const tmuxHasSessionProbeTimeout = 3 * time.Second

func tmuxSessionExists(name, socketName string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxHasSessionProbeTimeout)
	defer cancel()
	cmd := tmuxCommandContext(ctx, socketName, "has-session", "-t", name)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}

	msg := strings.TrimSpace(string(output))
	if msg == "" {
		msg = err.Error()
	}
	return false, fmt.Errorf("tmux has-session failed: %s", msg)
}

// tmuxHeadlessFallbackCols/Rows mirror internal/tmux's headlessInitialCols/
// Rows (the size a detached `new-session` is born at under the web daemon,
// which has no controlling TTY of its own to size against) -- used here
// only when querying the session's actual current size fails, so the
// attach client still never starts at a degenerate 0x0.
const (
	tmuxHeadlessFallbackCols = 200
	tmuxHeadlessFallbackRows = 50
)

// tmuxPaneStartupSize returns the size to hand pty.StartWithSize for the
// web terminal's attach client: the target session's actual current window
// size when it can be queried, or a safe non-zero fallback otherwise. See
// the call site in newTmuxPTYBridge for why 0x0 (pty.Start's default) is
// unsafe under `window-size latest`.
func tmuxPaneStartupSize(sessionName, socketName string) pty.Winsize {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxHasSessionProbeTimeout)
	defer cancel()
	out, err := tmuxCommandContext(ctx, socketName, "display-message", "-t", sessionName, "-p", "#{window_width}x#{window_height}").Output()
	if err == nil {
		if cols, rows, ok := parseWxH(strings.TrimSpace(string(out))); ok {
			return pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}
		}
	}
	return pty.Winsize{Cols: tmuxHeadlessFallbackCols, Rows: tmuxHeadlessFallbackRows}
}

// parseWxH parses tmux's "#{window_width}x#{window_height}" format,
// rejecting anything that isn't two positive integers separated by "x".
func parseWxH(s string) (cols, rows int, ok bool) {
	parts := strings.SplitN(s, "x", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	c, err1 := strconv.Atoi(parts[0])
	r, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || c <= 0 || r <= 0 {
		return 0, 0, false
	}
	return c, r, true
}

// tmuxCommand assembles an `exec.Cmd` for tmux, selecting the server in the
// following precedence order: (1) explicit socketName from the caller — the
// session's stored TmuxSocketName captured at creation time, passed through
// as tmux `-L <name>`; (2) TMUX env var's socket path (legacy web-in-tmux
// behavior), passed through as `-S <path>`; (3) tmux's default server. The
// legacy env-based fallback is preserved so running `agent-deck web` inside
// an existing tmux pane keeps working for users who haven't opted into the
// new per-session socket config (issue #687 phase 1).
// Callers that poll on a cadence must use tmuxCommandContext with a deadline
// instead — see tmuxHasSessionProbeTimeout. This unbounded form is correct only
// for long-lived interactive commands such as attach-session.
func tmuxCommand(socketName string, args ...string) *exec.Cmd {
	return tmuxCommandContext(context.Background(), socketName, args...)
}

// tmuxCommandContext is the deadline-carrying variant of tmuxCommand. A context
// with a timeout lets exec.CommandContext SIGKILL a tmux client that has wedged
// on its own leaked fd table rather than blocking the caller forever.
//
// Every argv it builds carries tmux's global `-u` (#1867). This wrapper is the
// web daemon's counterpart to internal/tmux's tmuxArgs, and the daemon is the
// worst-affected process in the codebase: run under systemd or launchd it has
// no LANG/LC_* at all, so without `-u` tmux rewrites every non-ASCII byte it
// returns to "_". Unlike internal/tmux there is no interactive carve-out here,
// because the web attach's terminal is not the user's — it is xterm.js in a
// browser, which is unconditionally UTF-8. That is the same reasoning that
// already pins TERM=xterm-256color in tmuxAttachCommand below.
func tmuxCommandContext(ctx context.Context, socketName string, args ...string) *exec.Cmd {
	utf8Args := tmuxutf8.Prepend(args)
	// Explicit per-session socket name wins — this is the v1.7.50 path.
	if trimmed := strings.TrimSpace(socketName); trimmed != "" {
		finalArgs := append([]string{tmuxutf8.Flag, "-L", trimmed}, utf8Args[1:]...)
		cmd := exec.CommandContext(ctx, "tmux", finalArgs...)
		// Unset TMUX so tmux-in-tmux guards don't trip: we are explicitly
		// directing this to a different server than the one we're in.
		cmd.Env = environWithoutTMUX(os.Environ())
		return cmd
	}

	socketPath, hasSocket := tmuxSocketFromEnv()

	finalArgs := utf8Args
	if hasSocket {
		finalArgs = append([]string{tmuxutf8.Flag, "-S", socketPath}, utf8Args[1:]...)
	}

	cmd := exec.CommandContext(ctx, "tmux", finalArgs...)
	if hasSocket {
		cmd.Env = environWithoutTMUX(os.Environ())
	}
	return cmd
}

func tmuxAttachCommand(sessionName, socketName string) *exec.Cmd {
	// Web's attach is now a normal client whose PTY size participates in tmux's
	// `window-size=latest` arbitration (set at Session.Start). Previously we
	// passed `-f ignore-size` together with a manual `tmux resize-window` call
	// in (*tmuxPTYBridge).Resize; the manual resize-window flipped the session
	// option to `window-size=manual` and pinned the window to the web viewport
	// for ALL attached clients (Ghostty, iTerm) — the dots-in-window symptom.
	// With largest in effect, every client sees content sized to the biggest
	// viewer; smaller clients see a clipped portion rather than dot-filled void.
	// `-u` forces UTF-8 output regardless of the daemon's locale. Same class of
	// bug as the TERM handling below: when the web daemon runs under launchd/
	// systemd its environment carries no LANG/LC_*, so tmux treats this client as
	// non-UTF-8 and downgrades every non-ASCII glyph (⏵, box-drawing, spinners)
	// to '_' on the wire — the browser/mobile terminal then shows '_' where the
	// agent drew Unicode, while tmux's own buffer (capture-pane) stays correct.
	cmd := tmuxCommand(socketName, "-u", "attach-session", "-t", sessionName)
	// Guarantee a usable TERM for the attach client. When the web daemon runs
	// under launchd/systemd its environment carries no TERM, and a tmux attach
	// client with an empty/unset TERM aborts with "open terminal failed:
	// terminal does not support clear" — the web terminal then never renders
	// and the browser's resize message races the dying bridge into
	// RESIZE_FAILED. The browser side is xterm.js, so xterm-256color is the
	// correct client terminal type. A TERM the daemon legitimately inherited
	// (e.g. `agent-deck web` launched from an interactive shell) is preserved.
	cmd.Env = ensureTERM(cmd.Env)
	return cmd
}

// ensureTERM returns env with a non-empty TERM guaranteed. A nil env (the
// inherit-parent default) is materialized from os.Environ() first so the
// appended TERM is not dropped. An existing non-empty TERM is left untouched;
// an existing but empty TERM (`TERM=`) is replaced in place rather than
// shadowed by a duplicate entry — execve passes the slice verbatim and getenv
// resolution order for duplicate keys is unspecified, so a trailing append
// could leave the empty value winning and tmux would still abort.
func ensureTERM(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	for i, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			if strings.TrimSpace(kv[len("TERM="):]) == "" {
				env[i] = "TERM=xterm-256color"
			}
			return env
		}
	}
	return append(env, "TERM=xterm-256color")
}

func tmuxSocketFromEnv() (string, bool) {
	raw := strings.TrimSpace(os.Getenv("TMUX"))
	if raw == "" {
		return "", false
	}

	socketPart := raw
	if strings.Contains(raw, ",") {
		socketPart = strings.SplitN(raw, ",", 2)[0]
	}

	socketPart = strings.TrimSpace(socketPart)
	if socketPart == "" {
		return "", false
	}
	return socketPart, true
}

func environWithoutTMUX(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMUX=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	return filtered
}
