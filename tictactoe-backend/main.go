package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

var (
	ctx      = context.Background()
	rdb      *redis.Client
	upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
)

// Embedded Lua script (atomic move validation + update)
// KEYS[1] - board key
// ARGV[1] - position (1..9)
// ARGV[2] - symbol ("X" or "O")
var luaScript = `
local board = KEYS[1]
local pos = tonumber(ARGV[1])
local sym = ARGV[2]

local function split(s)
  local t = {}
  for token in string.gmatch(s, "[^,]+") do
    table.insert(t, token)
  end
  return t
end

local data = redis.call('HGETALL', board)
local boardMap = {}
for i = 1, #data, 2 do
  boardMap[data[i]] = data[i+1]
end

local cells = boardMap['cells'] or '_,_,_,_,_,_,_,_,_'
local turn = boardMap['turn'] or 'X'
local winner = boardMap['winner'] or ''

if winner ~= '' then
  return { 'error', cells, winner }
end

if sym ~= turn then
  return { 'error', cells, winner }
end

local cellTbl = split(cells)
if pos < 1 or pos > 9 then
  return { 'error', cells, winner }
end
if cellTbl[pos] ~= '_' then
  return { 'error', cells, winner }
end

cellTbl[pos] = sym

local function join(tbl)
  return table.concat(tbl, ',')
end

-- check winner
local lines = {
  {1,2,3},{4,5,6},{7,8,9},
  {1,4,7},{2,5,8},{3,6,9},
  {1,5,9},{3,5,7}
}
local w = ''
for _, line in ipairs(lines) do
  local a = cellTbl[line[1]]
  local b = cellTbl[line[2]]
  local c = cellTbl[line[3]]
  if a ~= '_' and a == b and b == c then
    w = a
    break
  end
end

local status = 'ok'
if w ~= '' then
  winner = w
  status = 'win'
else
  local draw = true
  for i = 1, 9 do
    if cellTbl[i] == '_' then
      draw = false
      break
    end
  end
  if draw then
    winner = 'draw'
    status = 'draw'
  else
    -- toggle turn
    if turn == 'X' then turn = 'O' else turn = 'X' end
  end
end

-- persist
redis.call('HSET', board, 'cells', join(cellTbl), 'turn', turn, 'winner', winner)

return { status, join(cellTbl), winner, turn }
`

// Message is the JSON shape received from clients
type Message struct {
	Type     string `json:"type"`
	BoardID  int    `json:"boardId,omitempty"`
	Position int    `json:"position,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
}

// client wraps a websocket connection and a single writer goroutine
type client struct {
	conn   *websocket.Conn
	send   chan []byte
	mu     sync.Mutex // protects closed
	closed bool
	pubsub *redis.PubSub
	done   chan struct{}
}

func newClient(conn *websocket.Conn) *client {
	c := &client{
		conn: conn,
		send: make(chan []byte, 256),
		done: make(chan struct{}),
	}
	go c.writer()
	return c
}

func (c *client) writer() {
	defer func() {
		c.conn.Close()
	}()
	for msg := range c.send {
		c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			log.Println("write error:", err)
			return
		}
	}
}

func (c *client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.done)
	if c.pubsub != nil {
		_ = c.pubsub.Close()
	}
	close(c.send)
	_ = c.conn.Close()
}

func main() {
	// Connect to Redis
	rdb = redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal("Redis connection failed:", err)
	}
	defer rdb.Close()

	// Load Lua script
	sha, err := rdb.ScriptLoad(ctx, luaScript).Result()
	if err != nil {
		log.Fatal("Failed to load Lua script:", err)
	}
	log.Printf("✓ Lua script loaded (sha=%s)", sha)

	// Initialize 9 boards if not present
	for i := 0; i < 1; i++ {
		boardKey := fmt.Sprintf("board:%d", i)
		exists, _ := rdb.Exists(ctx, boardKey).Result()
		if exists == 0 {
			rdb.HSet(ctx, boardKey,
				"cells", "_,_,_,_,_,_,_,_,_",
				"turn", "X",
				"winner", "",
				"playerX", "",
				"playerO", "",
			)
		}
	}
	log.Println("✓ Boards initialized")

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("upgrade error:", err)
			return
		}
		c := newClient(conn)
		log.Println("New WebSocket connection")

		// reader loop
		go func() {
			for {
				var msg Message
				if err := conn.ReadJSON(&msg); err != nil {
					log.Println("read error:", err)
					c.close()
					return
				}

				handleClientMessage(c, sha, msg)
			}
		}()

		// block until client done
		<-c.done
		log.Println("Connection closed")
	})

	// status page
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<h1>Redis Tic-Tac-Toe (fixed)</h1><p>WebSocket: ws://localhost:8080/ws</p>`))
	})

	log.Println("🚀 Server running on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// handleClientMessage handles messages and uses c.send to push replies
// Add this helper function above or below handleClientMessage
func subscribeToBoard(c *client, boardID int) {
	// Prevent double subscription
	if c.pubsub != nil {
		return
	}

	channel := fmt.Sprintf("board:%d", boardID)
	pubsub := rdb.Subscribe(ctx, channel)
	c.pubsub = pubsub

	// Start listening in a background goroutine
	go func() {
		defer pubsub.Close()
		ch := pubsub.Channel()
		for {
			select {
			case m, ok := <-ch:
				if !ok {
					return
				}
				c.safeSend([]byte(m.Payload))
			case <-c.done:
				return
			}
		}
	}()
}

// Update your main handler
func handleClientMessage(c *client, sha string, msg Message) {
	switch msg.Type {
	case "join":
		boardID := 0 // Hardcoded for now as per your logic
		symbol := "X"
		boardKey := fmt.Sprintf("board:%d", boardID)

		// Check existing players
		playerX, _ := rdb.HGet(ctx, boardKey, "playerX").Result()
		playerO, _ := rdb.HGet(ctx, boardKey, "playerO").Result()

		if playerX != "" && playerO != "" {
			sendJSON(c, map[string]string{"type": "error", "error": "Board is full"})
			return
		}

		// Assign Role
		if playerX != "" {
			symbol = "O"
		}

		playerKey := "player" + symbol
		rdb.HSet(ctx, boardKey, playerKey, "player-"+symbol)
		if symbol == "O" || (playerX != "" && symbol == "X") {
			rdb.HSet(ctx, boardKey, "status", "active")
		}

		// 1. Send Join Confirmation
		sendJSON(c, map[string]interface{}{
			"type":    "joined",
			"role":    "player",
			"boardId": boardID,
			"symbol":  symbol,
		})

		// 2. CRITICAL FIX: Auto-subscribe to Redis immediately
		subscribeToBoard(c, boardID)

	case "join_spectator":
		sendJSON(c, map[string]interface{}{"type": "joined", "role": "spectator", "boardId": 0})
		// Spectators also need to see updates!
		subscribeToBoard(c, 0)

	case "get_board":
		board := fmt.Sprintf("board:%d", msg.BoardID)
		data, _ := rdb.HGetAll(ctx, board).Result()
		sendJSON(c, map[string]interface{}{"type": "board_state", "board": data})

	case "subscribe":
		// You can keep this for manual subscriptions, or rely on the auto-join
		subscribeToBoard(c, msg.BoardID)

	case "move":
		board := fmt.Sprintf("board:%d", msg.BoardID)
		pos := msg.Position

		// Execute Lua Script
		res, err := rdb.EvalSha(ctx, sha, []string{board}, pos, msg.Symbol).Result()
		if err != nil {
			log.Println("move error:", err)
			sendJSON(c, map[string]string{"type": "error", "error": err.Error()})
			return
		}

		arr, ok := res.([]interface{})
		if !ok || len(arr) < 2 {
			sendJSON(c, map[string]string{"type": "error", "error": "invalid response from lua"})
			return
		}

		status, _ := arr[0].(string)
		newCells, _ := arr[1].(string)
		winner := ""
		if len(arr) > 2 {
			winner, _ = arr[2].(string)
		}

		if status == "error" {
			sendJSON(c, map[string]string{"type": "error", "error": "invalid move"})
			return
		}

		nextTurn := ""
		if len(arr) > 3 {
			nextTurn, _ = arr[3].(string)
		}

		// Construct broadcast message
		update := map[string]interface{}{
			"type":     "move_made",
			"position": pos,
			"symbol":   msg.Symbol,
			"cells":    newCells,
			"turn":     nextTurn, // <-- NEW
		}

		if winner != "" {
			update["winner"] = winner
		}
		b, _ := json.Marshal(update)

		channel := fmt.Sprintf("board:%d", msg.BoardID)

		// Publish to everyone (including the player who made the move)
		rdb.Publish(ctx, channel, b)

		// Note: You previously had c.safeSend(b) here.
		// Since the mover is now subscribed via 'join', they will receive the
		// message via Redis PubSub. You can remove c.safeSend(b) to avoid
		// the mover receiving the update twice.

	case "reset":
		board := fmt.Sprintf("board:%d", msg.BoardID)
		rdb.HSet(ctx, board, "cells", "_,_,_,_,_,_,_,_,_", "turn", "X", "winner", "", "playerX", "", "playerO", "")

		// Notify everyone that board was reset
		update, _ := json.Marshal(map[string]string{"type": "reset_done"})
		rdb.Publish(ctx, fmt.Sprintf("board:%d", msg.BoardID), update)
	}
}

// sendJSON marshals v and queues it to the client's send channel
func sendJSON(c *client, v interface{}) {
	b, _ := json.Marshal(v)
	c.safeSend(b)
}

// safeSend attempts to push to the client's send channel without panic if closed
func (c *client) safeSend(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.send <- b:
	default:
		// avoid blocking if client is slow; drop message or consider buffering more
		log.Println("dropping message to slow client")
	}
}
