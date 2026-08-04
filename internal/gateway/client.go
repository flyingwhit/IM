package gateway

import (
	"encoding/json"
	"log"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ciel/im/internal/ws"
)

// Timeouts for WebSocket connections.
const (
	// writeWait is the deadline for writing a message to the peer.
	writeWait = 10 * time.Second

	// pongWait is the time allowed to read the next pong from the peer.
	// Must be greater than the ping interval on the client side.
	pongWait = 60 * time.Second

	// pingPeriod is the interval at which pings are sent.
	// Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// maxMessageSize is the maximum message size in bytes from the peer.
	maxMessageSize = 4096

	// sendBufferSize is the capacity of the outgoing message channel.
	sendBufferSize = 256
)

// Client represents a single WebSocket connection.
//
// Each Client runs two goroutines:
//   - readPump: reads messages from the WebSocket, dispatches to handlers
//   - writePump: reads from the send channel, writes to the WebSocket
//
// The send channel is the sole writer to the WebSocket — gorilla/websocket
// does not support concurrent writes.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	userID string
	connID string

	// send buffers outgoing messages. Messages are JSON-encoded []byte.
	send chan []byte
}

// NewClient creates a Client. The connection should already be upgraded
// to WebSocket (the caller is responsible for the HTTP upgrade handshake).
func NewClient(hub *Hub, conn *websocket.Conn, userID string, connID string) *Client {
	return &Client{
		hub:    hub,
		conn:   conn,
		userID: userID,
		connID: connID,
		send:   make(chan []byte, sendBufferSize),
	}
}

// readPump reads messages from the WebSocket connection.
//
// It runs in its own goroutine. When the connection closes (error or
// explicit close), it signals the Hub to unregister this client and
// closes the send channel, which causes writePump to exit.
//
// Message dispatch:
//   - message.send   → Hub.OnMessage callback (set by composition root)
//   - message.recall → Hub.OnMessage callback
//   - ping           → pong response
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		close(c.send)
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("ws read error: %v (user=%s, conn=%s)", err, c.userID, c.connID)
			}
			break
		}

		// Parse the envelope to determine the message type.
		var env ws.Envelope
		if err := json.Unmarshal(message, &env); err != nil {
			log.Printf("ws parse error: %v (user=%s)", err, c.userID)
			continue
		}

		switch env.Type {
		case ws.TypeMessageSend:
			if c.hub.OnMessage != nil {
				c.hub.OnMessage(c.userID, message)
			}
		case ws.TypeMessageRecall:
			if c.hub.OnMessage != nil {
				c.hub.OnMessage(c.userID, message)
			}
		case ws.TypePing:
			// Client heartbeat — respond with pong.
			pong := ws.MustEnvelope(ws.TypePong, nil)
			c.Send(pong)
		default:
			log.Printf("ws unknown type: %s (user=%s)", env.Type, c.userID)
		}
	}
}

// writePump writes messages from the send channel to the WebSocket.
//
// It runs in its own goroutine. It exits when the send channel is closed
// (which happens when readPump detects a disconnection).
//
// The send channel is the ONLY writer to the WebSocket connection.
// gorilla/websocket requires all writes to be serialized — using a
// dedicated goroutine with a channel achieves this naturally.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
		// Drain remaining messages from send channel so readPump's
		// defer can close the channel without blocking.
		for range c.send {
		}
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Channel closed — connection is done.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				log.Printf("ws write error: %v (user=%s, conn=%s)", err, c.userID, c.connID)
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("ws ping error: %v (user=%s, conn=%s)", err, c.userID, c.connID)
				return
			}
			c.hub.RefreshPresence(c.userID)
		}
	}
}

// Send enqueues a message envelope to be written to this connection.
// It is non-blocking: if the buffer is full, the message is dropped.
func (c *Client) Send(env *ws.Envelope) {
	data, err := json.Marshal(env)
	if err != nil {
		log.Printf("client: marshal error: %v", err)
		return
	}
	c.sendRaw(data)
}

// sendRaw enqueues pre-marshaled JSON bytes to be written to this connection.
// It is the single point for writing to the send channel — both Client.Send
// (one message, one client) and Hub.SendToUser (one message, many clients)
// route through here so that channel-write behavior (buffer full handling,
// metrics, rate limiting) is defined in one place.
//
// Non-blocking: if the buffer is full, the message is dropped for this
// connection. The message is already persisted in DB at this point.
func (c *Client) sendRaw(data []byte) {
	select {
	case c.send <- data:
	default:
		log.Printf("client: send buffer full for conn %s", c.connID)
	}
}
