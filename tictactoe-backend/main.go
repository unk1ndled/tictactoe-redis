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

// Lua script remains the same
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

redis.call('HSET', board, 'cells', join(cellTbl), 'turn', turn, 'winner', winner)
return { status, join(cellTbl), winner, turn }
`

type Message struct {
	Type       string            `json:"type"`
	BoardID    int               `json:"boardId,omitempty"`
	Position   int               `json:"position,omitempty"`
	Symbol     string            `json:"symbol,omitempty"`
	Name       string            `json:"name,omitempty"`       // <-- Added Name
	Spectators []string          `json:"spectators,omitempty"` // <-- For broadcasting lists
	Board      map[string]string `json:"board,omitempty"`      // <-- Fixed type for HGetAll
}

type client struct {
	conn   *websocket.Conn
	send   chan []byte
	mu     sync.Mutex
	closed bool
	pubsub *redis.PubSub
	done   chan struct{}
	// State to track for cleanup on disconnect
	name    string
	role    string
	boardID int
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

// close handles WebSocket closure and Redis cleanup
func (c *client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true

	// --- Cleanup Logic ---
	if c.name != "" {
		log.Printf("Cleaning up user: %s (%s)", c.name, c.role)
		if c.role == "spectator" {
			key := fmt.Sprintf("board:%d:spectators", c.boardID)
			rdb.SRem(ctx, key, c.name)
			broadcastSpectators(c.boardID)
		} else if c.role == "player" {
			// Optional: clear the player seat if they disconnect?
			// For now, we keep the game state, but maybe announce they left.
			// To reset fully, we'd set playerX="" in Redis.
		}
	}
	// ---------------------

	close(c.done)
	if c.pubsub != nil {
		_ = c.pubsub.Close()
	}
	close(c.send)
	_ = c.conn.Close()
}

func main() {
	rdb = redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal("Redis connection failed:", err)
	}
	defer rdb.Close()

	sha, err := rdb.ScriptLoad(ctx, luaScript).Result()
	if err != nil {
		log.Fatal("Failed to load Lua script:", err)
	}

	// Initialize board 0
	for i := 0; i < 1; i++ {
		boardKey := fmt.Sprintf("board:%d", i)
		exists, _ := rdb.Exists(ctx, boardKey).Result()
		if exists == 0 {
			resetBoardState(i)
		}
		// clear spectators on restart
		rdb.Del(ctx, fmt.Sprintf("board:%d:spectators", i))
	}

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := newClient(conn)

		go func() {
			for {
				var msg Message
				if err := conn.ReadJSON(&msg); err != nil {
					c.close()
					return
				}
				handleClientMessage(c, sha, msg)
			}
		}()

		<-c.done
	})

	log.Println("🚀 Server running on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func subscribeToBoard(c *client, boardID int) {
	if c.pubsub != nil {
		return
	}
	channel := fmt.Sprintf("board:%d", boardID)
	pubsub := rdb.Subscribe(ctx, channel)
	c.pubsub = pubsub

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

// Helper to fetch all spectators and broadcast via PubSub
func broadcastSpectators(boardID int) {
	key := fmt.Sprintf("board:%d:spectators", boardID)
	specs, _ := rdb.SMembers(ctx, key).Result()

	msg := Message{
		Type:       "spectators_update",
		Spectators: specs,
	}

	b, _ := json.Marshal(msg)
	rdb.Publish(ctx, fmt.Sprintf("board:%d", boardID), b)
}

func resetBoardState(boardID int) {
	boardKey := fmt.Sprintf("board:%d", boardID)
	rdb.HSet(ctx, boardKey,
		"cells", "_,_,_,_,_,_,_,_,_",
		"turn", "X",
		"winner", "",
		"playerX", "",
		"playerO", "",
		"playerXName", "Waiting...",
		"playerOName", "Waiting...",
	)
}

func handleClientMessage(c *client, sha string, msg Message) {
	switch msg.Type {
	case "join":
		boardID := 0
		symbol := "X"
		boardKey := fmt.Sprintf("board:%d", boardID)

		playerX, _ := rdb.HGet(ctx, boardKey, "playerX").Result()
		playerO, _ := rdb.HGet(ctx, boardKey, "playerO").Result()

		if playerX != "" && playerO != "" {
			sendJSON(c, map[string]string{"type": "error", "error": "Board is full"})
			return
		}

		if playerX != "" {
			symbol = "O"
		}

		// Store Client Info
		c.role = "player"
		c.boardID = boardID
		c.name = msg.Name

		// Update Redis
		playerKey := "player" + symbol              // e.g. playerX
		playerNameKey := "player" + symbol + "Name" // e.g. playerXName

		rdb.HSet(ctx, boardKey, playerKey, "active", playerNameKey, msg.Name)

		sendJSON(c, map[string]interface{}{
			"type":    "joined",
			"role":    "player",
			"boardId": boardID,
			"symbol":  symbol,
		})

		subscribeToBoard(c, boardID)

		// Force a board update so everyone sees the new player name
		data, _ := rdb.HGetAll(ctx, boardKey).Result()
		b, _ := json.Marshal(map[string]interface{}{"type": "board_state", "board": data})
		rdb.Publish(ctx, fmt.Sprintf("board:%d", boardID), b)

		// Also send current spectators to the new player
		broadcastSpectators(boardID)

	case "join_spectator":
		boardID := 0
		c.role = "spectator"
		c.boardID = boardID
		c.name = msg.Name

		// Add to Redis Set
		sKey := fmt.Sprintf("board:%d:spectators", boardID)
		rdb.SAdd(ctx, sKey, msg.Name)

		sendJSON(c, map[string]interface{}{"type": "joined", "role": "spectator", "boardId": 0})
		subscribeToBoard(c, 0)

		// Broadcast new list to everyone
		broadcastSpectators(boardID)

		// Send board state so they see current players
		data, _ := rdb.HGetAll(ctx, fmt.Sprintf("board:%d", boardID)).Result()
		sendJSON(c, map[string]interface{}{"type": "board_state", "board": data})

	case "get_board":
		board := fmt.Sprintf("board:%d", msg.BoardID)
		data, _ := rdb.HGetAll(ctx, board).Result()
		sendJSON(c, map[string]interface{}{"type": "board_state", "board": data})
		// Also refresh spectator list for this user
		specs, _ := rdb.SMembers(ctx, fmt.Sprintf("board:%d:spectators", msg.BoardID)).Result()
		sendJSON(c, map[string]interface{}{"type": "spectators_update", "spectators": specs})

	case "subscribe":
		subscribeToBoard(c, msg.BoardID)

	case "move":
		board := fmt.Sprintf("board:%d", msg.BoardID)
		pos := msg.Position

		res, err := rdb.EvalSha(ctx, sha, []string{board}, pos, msg.Symbol).Result()
		if err != nil {
			sendJSON(c, map[string]string{"type": "error", "error": err.Error()})
			return
		}

		arr, ok := res.([]interface{})
		if !ok || len(arr) < 2 {
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

		update := map[string]interface{}{
			"type":     "move_made",
			"position": pos,
			"symbol":   msg.Symbol,
			"cells":    newCells,
			"turn":     nextTurn,
			"winner":   winner,
		}
		b, _ := json.Marshal(update)
		rdb.Publish(ctx, fmt.Sprintf("board:%d", msg.BoardID), b)

	case "reset":
		resetBoardState(msg.BoardID)
		update, _ := json.Marshal(map[string]string{"type": "reset_done"})
		rdb.Publish(ctx, fmt.Sprintf("board:%d", msg.BoardID), update)
	}
}

func sendJSON(c *client, v interface{}) {
	b, _ := json.Marshal(v)
	c.safeSend(b)
}

func (c *client) safeSend(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.send <- b:
	default:
	}
}
