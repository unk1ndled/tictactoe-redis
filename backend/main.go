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
	// Mutex for the File Database to prevent concurrent write errors
	fileMu sync.Mutex
)

// --- Lua Script for Game Logic ---
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
	Content    string            `json:"content,omitempty"`
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

// --- File Database Helpers ---

func writeToDisk(msgJSON string) {
	fileMu.Lock()
	defer fileMu.Unlock()

	// Append to file, create if not exists
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

	if len(lines) > limit {
		return lines[len(lines)-limit:]
	}
	return lines
}

// --- WebSocket Logic ---

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

	if c.name != "" {
		key := fmt.Sprintf("board:%d:queue", c.boardID)
		rdb.LRem(ctx, key, 0, c.name)
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
	// Support Docker (REDIS_ADDR) or Localhost
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	rdb = redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal("Redis connection failed:", err)
	}
	defer rdb.Close()

	sha, err := rdb.ScriptLoad(ctx, luaScript).Result()
	if err != nil {
		log.Fatal("Failed to load Lua script:", err)
	}

	// Init board
	for i := 0; i < 1; i++ {
		boardKey := fmt.Sprintf("board:%d", i)
		exists, _ := rdb.Exists(ctx, boardKey).Result()
		if exists == 0 {
			resetBoardState(i)
		}
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

	log.Println(" Server running on port 8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func subscribeToBoard(c *client, boardID int) {
	if c.pubsub != nil {
		return
	}
	// sub
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

func broadcastSpectators(boardID int) {
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
	rdb.HSet(ctx, boardKey,
		"cells", "_,_,_,_,_,_,_,_,_",
		"turn", "X",
		"winner", "",
	)
}

func rotatePlayers(boardID int) {
	boardKey := fmt.Sprintf("board:%d", boardID)
	queueKey := fmt.Sprintf("board:%d:queue", boardID)

	pX, _ := rdb.HGet(ctx, boardKey, "playerXName").Result()
	pO, _ := rdb.HGet(ctx, boardKey, "playerOName").Result()

	if pX != "" && pX != "Waiting..." {
		rdb.RPush(ctx, queueKey, pX)
	}
	if pO != "" && pO != "Waiting..." {
		rdb.RPush(ctx, queueKey, pO)
	}

	newX, err1 := rdb.LPop(ctx, queueKey).Result()
	if err1 == redis.Nil {
		newX = "Waiting..."
	}
	newO, err2 := rdb.LPop(ctx, queueKey).Result()
	if err2 == redis.Nil {
		newO = "Waiting..."
	}

	rdb.HSet(ctx, boardKey, "playerXName", newX, "playerOName", newO)
	resetBoardState(boardID)

	data, _ := rdb.HGetAll(ctx, boardKey).Result()
	bBoard, _ := json.Marshal(map[string]interface{}{"type": "board_state", "board": data})
	rdb.Publish(ctx, fmt.Sprintf("board:%d", boardID), bBoard)
	broadcastSpectators(boardID)
}

func handleClientMessage(c *client, sha string, msg Message) {
	switch msg.Type {
	case "join":
		boardID := 0
		c.boardID = boardID
		c.name = msg.Name
		boardKey := fmt.Sprintf("board:%d", boardID)

		currX, _ := rdb.HGet(ctx, boardKey, "playerXName").Result()
		currO, _ := rdb.HGet(ctx, boardKey, "playerOName").Result()

		assigned := false
		if currX == "" || currX == "Waiting..." {
			rdb.HSet(ctx, boardKey, "playerXName", msg.Name)
			assigned = true
		} else if currO == "" || currO == "Waiting..." {
			rdb.HSet(ctx, boardKey, "playerOName", msg.Name)
			assigned = true
		}

		if !assigned {
			rdb.RPush(ctx, fmt.Sprintf("board:%d:queue", boardID), msg.Name)
		}

		sendJSON(c, map[string]interface{}{
			"type":    "joined",
			"boardId": boardID,
		})

		subscribeToBoard(c, boardID)

		data, _ := rdb.HGetAll(ctx, boardKey).Result()
		b, _ := json.Marshal(map[string]interface{}{"type": "board_state", "board": data})
		rdb.Publish(ctx, fmt.Sprintf("board:%d", boardID), b)
		broadcastSpectators(boardID)

	case "get_board":
		board := fmt.Sprintf("board:%d", msg.BoardID)
		data, _ := rdb.HGetAll(ctx, board).Result()
		sendJSON(c, map[string]interface{}{"type": "board_state", "board": data})
		specs, _ := rdb.LRange(ctx, fmt.Sprintf("board:%d:queue", msg.BoardID), 0, -1).Result()
		sendJSON(c, map[string]interface{}{"type": "spectators_update", "spectators": specs})

		//
		chatKey := fmt.Sprintf("board:%d:chat", msg.BoardID)

		exists, _ := rdb.Exists(ctx, chatKey).Result()
		var history []string

		if exists > 0 {
			// Cache HIT
			history, _ = rdb.LRange(ctx, chatKey, 0, -1).Result()
			log.Println("Cache Hit")
		} else {
			// Cache MISS: Load from File, Populate Redis
			history = loadFromDisk(50)
			if len(history) > 0 {
				for _, jsonMsg := range history {
					rdb.RPush(ctx, chatKey, jsonMsg)
				}
				rdb.Expire(ctx, chatKey, time.Hour)
			}
			log.Println("Cache miss, updating cache")
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

		res, err := rdb.EvalSha(ctx, sha, []string{board}, pos, msg.Symbol).Result()
		if err != nil {
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

		if winner != "" {
			go func() {
				time.Sleep(3 * time.Second)
				rotatePlayers(msg.BoardID)
			}()
		}

	case "chat":
		chatKey := fmt.Sprintf("board:%d:chat", msg.BoardID)

		chatPayload := map[string]string{
			"type":    "chat",
			"name":    msg.Name,
			"content": msg.Content,
		}
		jsonBytes, _ := json.Marshal(chatPayload)
		jsonStr := string(jsonBytes)

		// WRITE TO DISK (Persistence)
		writeToDisk(jsonStr)

		// WRITE TO CACHE (Redis)
		rdb.RPush(ctx, chatKey, jsonStr)
		rdb.LTrim(ctx, chatKey, -50, -1)
		rdb.Expire(ctx, chatKey, time.Hour)

		// BROADCAST
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
