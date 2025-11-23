import React, { useState, useEffect, useRef } from "react";
import {
  Users,
  Eye,
  Play,
  RotateCcw,
  Wifi,
  WifiOff,
  User,
  MessageSquare,
} from "lucide-react";

export default function TicTacToeClient() {
  const [ws, setWs] = useState(null);
  const [connected, setConnected] = useState(false);

  const [username, setUsername] = useState("");
  const [hasJoined, setHasJoined] = useState(false);

  const [role, setRole] = useState("spectator");
  const [symbol, setSymbol] = useState(null);
  const [boardId, setBoardId] = useState(null);
  const [spectators, setSpectators] = useState([]);
  const [playerNames, setPlayerNames] = useState({
    X: "Waiting...",
    O: "Waiting...",
  });
  const [board, setBoard] = useState({
    cells: Array(9).fill("_"),
    turn: "X",
    winner: "",
  });
  const [error, setError] = useState("");
  const wsRef = useRef(null);

  // --- Chat State ---
  const [chatHistory, setChatHistory] = useState([]);
  const [msgInput, setMsgInput] = useState("");

  useEffect(() => {
    const timer = setTimeout(() => connectWebSocket(), 100);
    return () => {
      clearTimeout(timer);
      if (wsRef.current) wsRef.current.close();
    };
  }, []);

  useEffect(() => {
    if (!hasJoined) return;
    if (playerNames.X === username) {
      setRole("player");
      setSymbol("X");
    } else if (playerNames.O === username) {
      setRole("player");
      setSymbol("O");
    } else {
      setRole("spectator");
      setSymbol(null);
    }
  }, [playerNames, username, hasJoined]);

  const connectWebSocket = () => {
    if (wsRef.current && wsRef.current.readyState === WebSocket.CONNECTING)
      return;
    try {
      // --- CHANGED: Dynamic Connection ---
      // This allows it to work on localhost OR a real server IP automatically.
      const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
      const host = window.location.hostname;
      // We assume port 8080 for the backend (mapped in Docker Compose)
      const socket = new WebSocket(`${protocol}//${host}:8080/ws`);

      socket.onopen = () => {
        setConnected(true);
        setError("");
      };
      socket.onclose = (e) => {
        setConnected(false);
        wsRef.current = null;
        if (!e.wasClean) setTimeout(connectWebSocket, 3000);
      };
      socket.onmessage = (event) => {
        try {
          handleMessage(JSON.parse(event.data));
        } catch (err) {}
      };
      wsRef.current = socket;
      setWs(socket);
    } catch (err) {
      setError("Failed to create connection");
    }
  };

  const handleMessage = (data) => {
    switch (data.type) {
      case "joined":
        setHasJoined(true);
        setBoardId(data.boardId);
        send({ type: "get_board", boardId: data.boardId });
        break;

      case "board_state":
        updateBoard(data.board);
        setPlayerNames({
          X: data.board.playerXName || "Waiting...",
          O: data.board.playerOName || "Waiting...",
        });
        break;

      case "spectators_update":
        setSpectators(data.spectators || []);
        break;

      case "move_made":
        updateBoard({
          cells: data.cells,
          winner: data.winner || board.winner,
          turn: data.turn || board.turn,
        });
        break;

      case "error":
        setError(data.error);
        setTimeout(() => setError(""), 3000);
        break;

      case "chat":
        setChatHistory((prev) => [...prev, data]);
        const chatContainer = document.getElementById("chat-container");
        if (chatContainer) chatContainer.scrollTop = chatContainer.scrollHeight;
        break;

      case "chat_history":
        const parsedHistory = data.history.map((str) => JSON.parse(str));
        setChatHistory(parsedHistory);
        break;
    }
  };

  const updateBoard = (boardData) => {
    const cells =
      typeof boardData.cells === "string"
        ? boardData.cells.split(",")
        : boardData.cells || Array(9).fill("_");

    setBoard({
      cells: cells,
      turn: boardData.turn || board.turn,
      winner: boardData.winner || "",
    });
  };

  const send = (msg) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(msg));
    }
  };

  const joinGame = () => {
    if (!username.trim()) {
      setError("Name required");
      return;
    }
    send({ type: "join", name: username });
  };

  const makeMove = (position) => {
    if (
      role !== "player" ||
      board.winner ||
      board.turn !== symbol ||
      board.cells[position] !== "_"
    )
      return;
    send({
      type: "move",
      boardId: boardId,
      position: position + 1,
      symbol: symbol,
    });
  };

  const sendChat = () => {
    if (!msgInput.trim()) return;
    send({
      type: "chat",
      boardId: boardId,
      name: username,
      content: msgInput,
    });
    setMsgInput("");
  };

  if (!hasJoined) {
    return (
      <div className="min-h-screen bg-slate-950 flex items-center justify-center p-4 font-sans">
        <div className="bg-white/10 backdrop-blur-lg border border-white/20 p-8 -2xl max-w-md w-full shadow-2xl">
          <h1 className="text-4xl font-bold text-white text-center mb-2">
            Tic-Tac-Toe
          </h1>
          <p className="text-gray-300 text-center mb-6">
            King of the Hill Mode
          </p>
          <input
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="Enter Username"
            className="w-full bg-slate-900/50 text-white -xl py-3 px-4 mb-4 border border-slate-500/30 outline-none focus:border-slate-400"
          />
          <button
            onClick={joinGame}
            disabled={!connected || !username}
            className="w-full bg-slate-600 hover:bg-slate-500 text-white font-bold py-3 px-6 -xl transition-all disabled:opacity-50"
          >
            Join Lobby
          </button>
          <div className="mt-4 text-center">
            {connected ? (
              <span className="text-emerald-400 text-xs">● Connected</span>
            ) : (
              <span className="text-red-400 text-xs">● Disconnected</span>
            )}
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-slate-950 flex flex-col md:flex-row text-white font-sans">
      <div className="md:w-80 bg-slate-900/40 border-r border-white/10 p-6 flex flex-col gap-6">
        <div>
          <h2 className="text-slate-300 text-xs font-bold uppercase tracking-wider mb-3">
            Current Players
          </h2>
          <div className="space-y-2">
            <div
              className={`p-3 -lg border ${
                board.turn === "X"
                  ? "bg-amber-500/20 border-amber-500"
                  : "bg-white/5 border-white/10"
              }`}
            >
              <div className="flex justify-between items-center">
                <span className="font-bold text-amber-500">X</span>
                <span>{playerNames.X}</span>
              </div>
            </div>
            <div
              className={`p-3 -lg border ${
                board.turn === "O"
                  ? "bg-cyan-500/20 border-cyan-500"
                  : "bg-white/5 border-white/10"
              }`}
            >
              <div className="flex justify-between items-center">
                <span className="font-bold text-cyan-500">O</span>
                <span>{playerNames.O}</span>
              </div>
            </div>
          </div>
        </div>

        <div className="flex-1 flex flex-col min-h-0">
          <h2 className="text-slate-300 text-xs font-bold uppercase tracking-wider mb-3 flex justify-between">
            <span>Queue</span>
            <span>{spectators.length}</span>
          </h2>
          <div className="flex-1 overflow-y-auto space-y-1 pr-2 custom-scrollbar max-h-40">
            {spectators.map((name, i) => (
              <div
                key={i}
                className="flex items-center gap-3 bg-white/5 p-2  text-sm"
              >
                <span className="text-white/40 font-mono text-xs">
                  #{i + 1}
                </span>
                <span
                  className={
                    name === username
                      ? "text-slate-300 font-bold"
                      : "text-gray-300"
                  }
                >
                  {name}
                </span>
              </div>
            ))}
          </div>
        </div>

        <div className="flex flex-col h-64 border-t border-white/10 pt-4">
          <h2 className="text-slate-300 text-xs font-bold uppercase tracking-wider mb-3 flex items-center gap-2">
            <MessageSquare size={12} /> Chat
          </h2>
          <div
            id="chat-container"
            className="flex-1 overflow-y-auto space-y-2 mb-2 pr-2 custom-scrollbar bg-black/20  p-2"
          >
            {chatHistory.length === 0 && (
              <div className="text-white/20 text-xs text-center mt-4">
                No messages yet...
              </div>
            )}
            {chatHistory.map((msg, i) => (
              <div key={i} className="text-xs break-words">
                <span
                  className={`font-bold mr-2 ${
                    msg.name === username
                      ? "text-emerald-400"
                      : "text-slate-300"
                  }`}
                >
                  {msg.name}:
                </span>
                <span className="text-gray-300">{msg.content}</span>
              </div>
            ))}
          </div>
          <div className="flex gap-2">
            <input
              className="flex-1 bg-white/10  px-2 py-1 outline-none text-white text-sm focus:bg-white/20 transition-colors"
              value={msgInput}
              onChange={(e) => setMsgInput(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && sendChat()}
              placeholder="Message..."
            />
          </div>
        </div>
      </div>

      <div className="flex-1 flex flex-col relative">
        <div className="p-4 bg-slate-900/20 flex justify-between items-center">
          <div className="flex items-center gap-2">
            <div
              className={`w-2 h-2 -full ${
                role === "player" ? "bg-emerald-500" : "bg-gray-500"
              }`}
            ></div>
            <span className="font-bold">
              {role === "player"
                ? `Playing as ${symbol}`
                : "Spectating / In Queue"}
            </span>
          </div>
          {role === "player" && (
            <div className="text-xs bg-slate-600 px-2 py-1 ">
              You are PLAYING!
            </div>
          )}
        </div>

        <div className="flex-1 flex flex-col items-center justify-center p-8">
          <div className="mb-8 text-center h-16">
            {board.winner ? (
              <div className="animate-bounce px-6 py-2 bg-white text-slate-900 -full text-xl font-bold shadow-lg">
                {board.winner === "draw"
                  ? "It's a Draw!"
                  : `${
                      board.winner === "X" ? playerNames.X : playerNames.O
                    } Wins!`}
                <div className="text-xs font-normal mt-1 opacity-70">
                  Starting next game in 3s...
                </div>
              </div>
            ) : (
              <h2 className="text-3xl font-light">
                <span
                  className={
                    board.turn === "X"
                      ? "text-amber-500 font-bold"
                      : "text-cyan-500 font-bold"
                  }
                >
                  {board.turn === "X" ? playerNames.X : playerNames.O}'s
                </span>{" "}
                turn
              </h2>
            )}
          </div>

          <div className="grid grid-cols-3 gap-5 max-w-sm w-full">
            {board.cells.map((cell, index) => (
              <button
                key={index}
                onClick={() => makeMove(index)}
                disabled={
                  role !== "player" ||
                  board.winner ||
                  board.turn !== symbol ||
                  cell !== "_"
                }
                className={`
                    aspect-square -xl text-5xl font-bold shadow-lg transition-all transform
                    ${
                      cell === "_"
                        ? "bg-white/10 hover:bg-white/15"
                        : "bg-white/5"
                    }
                    ${cell === "X" ? "text-amber-500" : "text-cyan-500"}
                    ${
                      role === "player" &&
                      board.turn === symbol &&
                      !board.winner &&
                      cell === "_"
                        ? "border-2 border-slate-500/50 hover:scale-110"
                        : ""
                    }
                    `}
              >
                {cell !== "_" && cell}
              </button>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
