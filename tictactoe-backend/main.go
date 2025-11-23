package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

var (
	ctx      = context.Background()
	rdb      *redis.Client
	upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	fileMu sync.Mutex
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
	Name       string            `json:"name,omitempty"`
	Spectators []string          `json:"spectators,omitempty"`
	Board      map[string]string `json:"board,omitempty"`

	Content string `json:"content,omitempty"`
}

type client struct {
	conn    *websocket.Conn
	send    chan []byte
	mu      sync.Mutex
	closed  bool
	pubsub  *redis.PubSub
	done    chan struct{}
	name    string
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

	// Cleanup: Remove user from the queue if they leave
	if c.name != "" {
		// remove from spectator list (Queue)
		key := fmt.Sprintf("board:%d:queue", c.boardID)
		rdb.LRem(ctx, key, 0, c.name) // 0 removes all occurrences

		// Note: If the active player leaves, we strictly don't handle that
		// edge case here (game pauses), but the rotation handles the rest.
		broadcastSpectators(c.boardID)
	}

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
	// CHANGED: Use LRANGE instead of SMEMBERS to get ordered list
	key := fmt.Sprintf("board:%d:queue", boardID)
	specs, _ := rdb.LRange(ctx, key, 0, -1).Result()

	msg := Message{
		Type:       "spectators_update",
		Spectators: specs,
	}
	b, _ := json.Marshal(msg)
	rdb.Publish(ctx, fmt.Sprintf("board:%d", boardID), b)
}

func resetBoardState(boardID int) {
	boardKey := fmt.Sprintf("board:%d", boardID)
	// We do NOT clear player names here, only the grid
	rdb.HSet(ctx, boardKey,
		"cells", "_,_,_,_,_,_,_,_,_",
		"turn", "X",
		"winner", "",
	)
}

func rotatePlayers(boardID int) {
	boardKey := fmt.Sprintf("board:%d", boardID)
	queueKey := fmt.Sprintf("board:%d:queue", boardID)

	// 1. Get current players
	pX, _ := rdb.HGet(ctx, boardKey, "playerXName").Result()
	pO, _ := rdb.HGet(ctx, boardKey, "playerOName").Result()

	// 2. Push current players to back of queue (if they exist)
	if pX != "" && pX != "Waiting..." {
		rdb.RPush(ctx, queueKey, pX)
	}
	if pO != "" && pO != "Waiting..." {
		rdb.RPush(ctx, queueKey, pO)
	}

	// 3. Pop new players from front of queue
	// We try to get 2 people.
	newX, err1 := rdb.LPop(ctx, queueKey).Result()
	if err1 == redis.Nil {
		newX = "Waiting..."
	} // Queue empty

	newO, err2 := rdb.LPop(ctx, queueKey).Result()
	if err2 == redis.Nil {
		newO = "Waiting..."
	}

	// 4. Update Board State
	rdb.HSet(ctx, boardKey, "playerXName", newX, "playerOName", newO)

	// Reset the grid for the new game
	resetBoardState(boardID)

	// 5. Notify Everyone
	// Broadcast new board state (roles)
	data, _ := rdb.HGetAll(ctx, boardKey).Result()
	bBoard, _ := json.Marshal(map[string]interface{}{"type": "board_state", "board": data})
	rdb.Publish(ctx, fmt.Sprintf("board:%d", boardID), bBoard)

	// Broadcast new spectator list (since we popped and pushed)
	broadcastSpectators(boardID)
}
func handleClientMessage(c *client, sha string, msg Message) {
	switch msg.Type {
	case "join":
		boardID := 0
		c.boardID = boardID
		c.name = msg.Name
		boardKey := fmt.Sprintf("board:%d", boardID)

		// Check if seats are empty
		currX, _ := rdb.HGet(ctx, boardKey, "playerXName").Result()
		currO, _ := rdb.HGet(ctx, boardKey, "playerOName").Result()

		assigned := false

		// Logic: If seat empty, take it. Else, go to queue.
		if currX == "" || currX == "Waiting..." {
			rdb.HSet(ctx, boardKey, "playerXName", msg.Name)
			assigned = true
		} else if currO == "" || currO == "Waiting..." {
			rdb.HSet(ctx, boardKey, "playerOName", msg.Name)
			assigned = true
		}

		if !assigned {
			// Add to Queue (Spectator List)
			rdb.RPush(ctx, fmt.Sprintf("board:%d:queue", boardID), msg.Name)
		}

		// Reply to user
		sendJSON(c, map[string]interface{}{
			"type":    "joined",
			"boardId": boardID,
		})

		subscribeToBoard(c, boardID)

		// Broadcast updates
		data, _ := rdb.HGetAll(ctx, boardKey).Result()
		b, _ := json.Marshal(map[string]interface{}{"type": "board_state", "board": data})
		rdb.Publish(ctx, fmt.Sprintf("board:%d", boardID), b)
		broadcastSpectators(boardID)

	case "get_board":
		board := fmt.Sprintf("board:%d", msg.BoardID)
		data, _ := rdb.HGetAll(ctx, board).Result()
		sendJSON(c, map[string]interface{}{"type": "board_state", "board": data})
		// Send initial queue
		specs, _ := rdb.LRange(ctx, fmt.Sprintf("board:%d:queue", msg.BoardID), 0, -1).Result()
		sendJSON(c, map[string]interface{}{"type": "spectators_update", "spectators": specs})

		// --- PURE CACHE LOGIC: READ HISTORY ---
		chatKey := fmt.Sprintf("board:%d:chat", msg.BoardID)

		// 1. Ask Redis: "Do you have data?"
		exists, _ := rdb.Exists(ctx, chatKey).Result()

		var history []string

		if exists > 0 {
			// CACHE HIT: Fast read from RAM
			log.Println("Cache HIT: Reading from Redis")
			history, _ = rdb.LRange(ctx, chatKey, 0, -1).Result()
		} else {
			// CACHE MISS: Slow read from Disk (DB)
			log.Println("Cache MISS: Reading from File & Repopulating Redis")

			// 1. Read from DB
			history = loadFromDisk(50)

			// 2. Hydrate Cache (Populate Redis)
			if len(history) > 0 {
				// RPush accepts multiple values, but we loop here for simplicity
				for _, jsonMsg := range history {
					rdb.RPush(ctx, chatKey, jsonMsg)
				}
				// 3. Set TTL (Time To Live). Redis forgets this after 1 hour
				rdb.Expire(ctx, chatKey, time.Hour)
			}
		}

		sendJSON(c, map[string]interface{}{
			"type":    "chat_history",
			"history": history,
		})

	case "subscribe":
		subscribeToBoard(c, msg.BoardID)

	case "move":
		board := fmt.Sprintf("board:%d", msg.BoardID)
		pos := msg.Position

		// Run Lua Script
		res, err := rdb.EvalSha(ctx, sha, []string{board}, pos, msg.Symbol).Result()
		if err != nil {
			return
		} // handle error

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

		// Broadcast Move
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

		// IF GAME OVER -> ROTATE PLAYERS
		if winner != "" {
			// We delay slightly so players see the "Win" message, then rotate
			go func() {
				time.Sleep(3 * time.Second)
				rotatePlayers(msg.BoardID)
			}()
		}

	case "chat":
		chatKey := fmt.Sprintf("board:%d:chat", msg.BoardID)

		// 1. Prepare Data
		chatPayload := map[string]string{
			"type":    "chat",
			"name":    msg.Name,
			"content": msg.Content,
		}
		jsonBytes, _ := json.Marshal(chatPayload)
		jsonStr := string(jsonBytes)

		// 2. WRITE TO DB (Persistent File)
		// If the server crashes or Redis dies, this data is safe.
		writeToDisk(jsonStr)

		// 3. WRITE TO CACHE (Redis)
		// We update the cache so the NEXT read is fast.
		rdb.RPush(ctx, chatKey, jsonStr)
		rdb.LTrim(ctx, chatKey, -50, -1)    // Keep cache small
		rdb.Expire(ctx, chatKey, time.Hour) // Refresh the TTL

		// 4. Broadcast
		rdb.Publish(ctx, fmt.Sprintf("board:%d", msg.BoardID), jsonBytes)
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

func writeToDisk(msgJSON string) {
	fileMu.Lock()
	defer fileMu.Unlock()

	f, err := os.OpenFile("chat_history.jsonl", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println("DB Write Error:", err)
		return
	}
	defer f.Close()

	if _, err := f.WriteString(msgJSON + "\n"); err != nil {
		log.Println("DB Write Error:", err)
	}
}
func loadFromDisk(limit int) []string {
	fileMu.Lock()
	defer fileMu.Unlock()

	f, err := os.Open("chat_history.jsonl")
	if err != nil {
		return []string{} // File doesn't exist yet
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	// Return only the last 'limit' lines
	if len(lines) > limit {
		return lines[len(lines)-limit:]
	}
	return lines
}
