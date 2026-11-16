package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sshx/internal/auth"
	"sync"
	"time"

	"github.com/coder/websocket"

	"sshx/internal/session"
)

// WebSocket protocol v1 (see docs/DESIGN.md §3.2):
//
//   Text frames — JSON control messages:
//     client → server: {"type":"attach","sessionId":"..","channelId":1,"cols":80,"rows":24,
//                       "sizeMode":"negotiate"|"passive"}
//                      {"type":"resize","channelId":1,"cols":120,"rows":40}
//                      {"type":"detach","channelId":1}
//     server → client: {"type":"attached","channelId":1,"sessionId":".."}
//                      {"type":"size","channelId":1,"cols":100,"rows":40}
//                      {"type":"channel-closed","channelId":1}
//                      {"type":"session-event","sessionId":"..","event":"created"|"closed"}
//                      {"type":"cwd","sessionId":"..","cwd":"/var/log"}
//                      {"type":"metrics","sessionId":"..","sample":{...}}  // monitor sample
//                      {"type":"alert","nodeId":"..","level":"..","message":".."}
//                      {"type":"error","channelId":1,"message":".."}
//
//   Binary frames — terminal bytes: [1-byte channelId][payload].
//     client → server: terminal input; server → client: terminal output.
//
// The client picks channel IDs (1-255), letting it map frames without a
// round-trip. One WebSocket multiplexes any number of terminals.

type controlMsg struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId,omitempty"`
	ChannelID int    `json:"channelId,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
	// SizeMode is "negotiate" (default) or "passive"; see session.SizeMode.
	// A passive client — typically a phone watching a session opened on a
	// desktop — never resizes the PTY.
	SizeMode string          `json:"sizeMode,omitempty"`
	Event    string          `json:"event,omitempty"`
	Cwd      string          `json:"cwd,omitempty"`
	Message  string          `json:"message,omitempty"`
	Sample   json.RawMessage `json:"sample,omitempty"` // monitor metrics
	NodeID   string          `json:"nodeId,omitempty"` // alert origin
	Level    string          `json:"level,omitempty"`  // alert level
}

// wsConn is one client connection: a set of channel attachments plus a
// serialized writer.
type wsConn struct {
	conn     *websocket.Conn
	sessions *session.Manager
	log      *slog.Logger

	out   chan wsFrame
	stop  context.CancelFunc
	mu    sync.Mutex
	chans map[int]*wsChannel
}

type wsChannel struct {
	readOnly bool
	term     *session.Terminal
	att      *session.Attachment
	cancel   context.CancelFunc
	done     chan struct{}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)
	c := &wsConn{
		out: make(chan wsFrame, 256), stop: cancel,
		conn:     conn,
		sessions: s.sessions,
		log:      s.log,
		chans:    map[int]*wsChannel{},
	}
	defer c.detachAll()

	go c.writeLoop(ctx)
	sid := auth.SessionFromContext(r.Context())
	if s.passAuth != nil {
		untrack := s.trackSocket(sid, conn)
		defer untrack()
		if _, err := s.store.GetSession(sid); err != nil {
			return
		}
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.passAuth != nil {
					if _, err := s.store.GetSession(sid); err != nil {
						conn.CloseNow()
						return
					}
				}
				pingCtx, stop := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(pingCtx)
				stop()
				if err != nil {
					conn.CloseNow()
					return
				}
			}
		}
	}()

	unsubscribe := s.subscribeEvents(func(ev session.Event) {
		if ev.Type == "cwd" {
			c.writeControl(ctx, controlMsg{
				Type: "cwd", SessionID: string(ev.SessionID), Cwd: ev.Cwd,
			})
			return
		}
		c.writeControl(ctx, controlMsg{
			Type: "session-event", SessionID: string(ev.SessionID), Event: ev.Type,
		})
	})
	defer unsubscribe()

	unsubMetrics := s.subscribeMetrics(func(sessionID string, sample []byte) {
		c.pushMetrics(ctx, sessionID, sample)
	})
	defer unsubMetrics()

	unsubAlerts := s.subscribeAlerts(func(nodeID, level, message string) {
		c.pushAlert(ctx, nodeID, level, message)
	})
	defer unsubAlerts()

	c.readLoop(ctx)
}

func (c *wsConn) readLoop(ctx context.Context) {
	for {
		typ, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		switch typ {
		case websocket.MessageText:
			var msg controlMsg
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			c.handleControl(ctx, msg)
		case websocket.MessageBinary:
			if len(data) < 2 {
				continue
			}
			c.handleInput(int(data[0]), data[1:])
		}
	}
}

func (c *wsConn) handleControl(ctx context.Context, msg controlMsg) {
	switch msg.Type {
	case "attach":
		c.attach(ctx, msg)
	case "resize":
		if ch := c.channel(msg.ChannelID); ch != nil {
			ch.att.Resize(msg.Cols, msg.Rows)
		}
	case "detach":
		c.closeChannel(msg.ChannelID)
	}
}

func (c *wsConn) attach(ctx context.Context, msg controlMsg) {
	sess, ok := c.sessions.Get(session.ID(msg.SessionID))
	if !ok {
		c.writeControl(ctx, controlMsg{Type: "error", ChannelID: msg.ChannelID, Message: "session not found"})
		return
	}
	if msg.ChannelID < 1 || msg.ChannelID > 255 || msg.Cols > 1000 || msg.Rows > 1000 {
		c.writeControl(ctx, controlMsg{Type: "error", Message: "channelId must be 1-255"})
		return
	}
	c.closeChannel(msg.ChannelID) // idempotent re-attach on the same id

	// Attaching does not simply set the PTY size: a passive client (phone)
	// leaves it alone, and a negotiating one only shrinks it to the smallest
	// attached viewport. Otherwise a second client resizes the terminal out
	// from under the first.
	term := sess.Terminal()
	att := term.Attach(session.SizeMode(msg.SizeMode), msg.Cols, msg.Rows)
	out, closed := att.Out, att.Closed

	chCtx, cancel := context.WithCancel(ctx)
	ch := &wsChannel{term: term, att: att, cancel: cancel, done: make(chan struct{}), readOnly: msg.ReadOnly || msg.SizeMode == "passive"}
	c.mu.Lock()
	c.chans[msg.ChannelID] = ch
	c.mu.Unlock()

	// Send the authoritative size BEFORE the replay. xterm applies the size
	// to its grid before writing replay bytes; if the replay contained an
	// in-band resize or row-clear escape (vim resize, clear-and-redraw, etc.)
	// doing it in the wrong order misplaces those rows.
	curCols, curRows := term.Size()
	c.writeControl(ctx, controlMsg{
		Type: "attached", ChannelID: msg.ChannelID, SessionID: msg.SessionID,
	})
	c.writeControl(ctx, controlMsg{
		Type: "size", ChannelID: msg.ChannelID,
		Cols: curCols, Rows: curRows,
	})
	c.writeBinary(ctx, msg.ChannelID, screenReplay(att.Replay))

	go func() {
		defer close(ch.done)
		defer c.closeChannelIf(msg.ChannelID, ch)
		for {
			select {
			case sz := <-att.Sizes:
				// The PTY geometry is authoritative; the client renders at
				// it even if its own viewport is a different shape.
				if !c.writeControl(chCtx, controlMsg{
					Type: "size", ChannelID: msg.ChannelID, Cols: sz[0], Rows: sz[1],
				}) {
					return
				}
			case data, ok := <-out:
				if !ok {
					return
				}
				if !c.writeBinary(chCtx, msg.ChannelID, data) {
					return
				}
			case <-closed:
				// Drain anything already buffered, then finish.
				for {
					select {
					case data, ok := <-out:
						if ok {
							c.writeBinary(chCtx, msg.ChannelID, data)
							continue
						}
					default:
					}
					break
				}
				kind := "channel-closed"
				if !term.IsClosed() {
					kind = "channel-reset"
				}
				c.writeControl(chCtx, controlMsg{Type: kind, ChannelID: msg.ChannelID})
				return
			case <-chCtx.Done():
				return
			}
		}
	}()
}

func (c *wsConn) handleInput(channelID int, payload []byte) {
	if ch := c.channel(channelID); ch != nil && !ch.readOnly {
		ch.term.Write(payload)
	}
}

func (c *wsConn) channel(id int) *wsChannel {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.chans[id]
}

// Wait for the old producer before reusing a channel ID. Its queued frames
// precede the new attached/reset marker and cannot corrupt the replacement.
func (c *wsConn) closeChannel(id int) {
	c.mu.Lock()
	ch := c.chans[id]
	delete(c.chans, id)
	c.mu.Unlock()
	if ch != nil {
		ch.cancel()
		ch.att.Detach()
		if ch.done != nil {
			<-ch.done
		}
	}
}
func (c *wsConn) closeChannelIf(id int, expected *wsChannel) {
	c.mu.Lock()
	if c.chans[id] != expected {
		c.mu.Unlock()
		return
	}
	delete(c.chans, id)
	c.mu.Unlock()
	expected.cancel()
	expected.att.Detach()
}

func (c *wsConn) detachAll() {
	c.mu.Lock()
	chans := c.chans
	c.chans = map[int]*wsChannel{}
	c.mu.Unlock()
	for _, ch := range chans {
		ch.cancel()
		ch.att.Detach()
	}
}

func (c *wsConn) writeControl(ctx context.Context, msg controlMsg) bool {
	data, _ := json.Marshal(msg)
	return c.enqueue(ctx, websocket.MessageText, data)
}

// pushMetrics sends a monitor sample to this client.
func (c *wsConn) pushMetrics(ctx context.Context, sessionID string, sample []byte) {
	c.writeControl(ctx, controlMsg{
		Type:      "metrics",
		SessionID: sessionID,
		Sample:    sample,
	})
}

// pushAlert sends an alert notification to this client.
func (c *wsConn) pushAlert(ctx context.Context, nodeID, level, message string) {
	c.writeControl(ctx, controlMsg{
		Type:    "alert",
		NodeID:  nodeID,
		Level:   level,
		Message: message,
	})
}

func (c *wsConn) writeBinary(ctx context.Context, channelID int, payload []byte) bool {
	if len(payload) == 0 {
		return true
	}
	frame := make([]byte, 1+len(payload))
	frame[0] = byte(channelID)
	copy(frame[1:], payload)
	return c.enqueue(ctx, websocket.MessageBinary, frame)
}

type wsFrame struct {
	kind websocket.MessageType
	data []byte
}

// Listeners never perform network I/O. A slow browser is disconnected and
// can reattach; it cannot stall other sessions, monitor collectors or shutdown.
func (c *wsConn) enqueue(ctx context.Context, kind websocket.MessageType, data []byte) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case c.out <- wsFrame{kind, data}:
		return true
	default:
		c.stop()
		c.conn.CloseNow()
		return false
	}
}
func (c *wsConn) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-c.out:
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.conn.Write(writeCtx, f.kind, f.data)
			cancel()
			if err != nil {
				c.stop()
				c.conn.CloseNow()
				return
			}
		}
	}
}
