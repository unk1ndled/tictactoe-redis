import React, { useState, useEffect, useRef } from "react";
import { Users, Eye, Play, RotateCcw, Wifi, WifiOff, User } from "lucide-react";

export default function TicTacToeClient() {
  const [ws, setWs] = useState(null);
  const [connected, setConnected] = useState(false);
  const [role, setRole] = useState(null); // 'player' or 'spectator'
  const [boardId, setBoardId] = useState(null);
  const [symbol, setSymbol] = useState(null);

  // New State for Names
  const [username, setUsername] = useState("");
  const [spectators, setSpectators] = useState([]);
  const [playerNames, setPlayerNames] = useState({
    X: "none",
    O: "none",
  });

  const [board, setBoard] = useState({
    cells: Array(9).fill("_"),
    turn: "X",
    winner: "",
  });
  const [error, setError] = useState("");
  const wsRef = useRef(null);

  useEffect(() => {
    const timer = setTimeout(() => {
      connectWebSocket();
    }, 100);
    return () => {
      clearTimeout(timer);
      if (wsRef.current) wsRef.current.close();
    };
  }, []);

  const connectWebSocket = () => {
    if (wsRef.current && wsRef.current.readyState === WebSocket.CONNECTING)
      return;
    try {
      const socket = new WebSocket("ws://localhost:8080/ws");
      socket.onopen = () => {
        setConnected(true);
        setError("");
      };
      socket.onclose = (event) => {
        setConnected(false);
        wsRef.current = null;
        if (event.wasClean === false) setTimeout(connectWebSocket, 3000);
      };
      socket.onerror = (err) => {
        setError("Cannot connect to server.");
      };
      socket.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data);
          handleMessage(data);
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
        setRole(data.role);
        setBoardId(data.boardId);
        setSymbol(data.symbol || null);
        setTimeout(() => {
          send({ type: "get_board", boardId: data.boardId });
        }, 100);
        break;

      case "board_state":
        updateBoard(data.board);
        // Update player names from board state
        setPlayerNames({
          X: data.board.playerXName || "Waiting...",
          O: data.board.playerOName || "Waiting...",
        });
        break;

      case "spectators_update":
        // Update list of spectators
        setSpectators(data.spectators || []);
        break;

      case "move_made":
        updateBoard({
          cells: data.cells,
          winner: data.winner || board.winner,
          turn: data.turn || board.turn,
        });
        break;

      case "reset_done":
        send({ type: "get_board", boardId: boardId });
        break;

      case "error":
        setError(data.error);
        setTimeout(() => setError(""), 3000);
        break;
      default:
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

  const send = (message) => {
    if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(message));
    }
  };

  const joinAsPlayer = () => {
    if (!username.trim()) {
      setError("Please enter a name");
      return;
    }
    send({ type: "join", name: username });
  };

  const joinAsSpectator = () => {
    if (!username.trim()) {
      setError("Please enter a name");
      return;
    }
    send({ type: "join_spectator", name: username });
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

  const resetBoard = () => {
    send({ type: "reset", boardId: boardId });
  };

  // Welcome Screen
  if (!role) {
    return (
      <div className="min-h-screen bg-slate-950 flex items-center justify-center p-4 font-sans">
        <div className="bg-white/10 backdrop-blur-lg border border-white/20 p-8 rounded-2xl max-w-md w-full shadow-2xl">
          <div className="text-center mb-8">
            <h1 className="text-4xl font-bold text-white mb-2">Tic-Tac-Toe</h1>
            <p className="text-gray-300">Multiplayer Redis Demo</p>
            <div className="flex items-center justify-center mt-4 gap-2 text-sm">
              {connected ? (
                <span className="flex items-center gap-2 text-emerald-400">
                  <Wifi className="w-4 h-4" /> Connected
                </span>
              ) : (
                <span className="flex items-center gap-2 text-red-400">
                  <WifiOff className="w-4 h-4" /> Connecting...
                </span>
              )}
            </div>
          </div>

          <div className="space-y-4">
            {/* Name Input */}
            <div>
              <label className="text-white text-sm font-medium ml-1">
                Enter your Name
              </label>
              <div className="relative mt-1">
                <User className="absolute left-3 top-3 w-5 h-5 text-gray-400" />
                <input
                  type="text"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  placeholder="Guest123"
                  className="w-full bg-slate-900/50 border border-slate-500/30 text-white rounded-xl py-3 pl-10 pr-4 focus:outline-none focus:ring-2 focus:ring-slate-500 placeholder-slate-400/50"
                />
              </div>
            </div>

            <div className="grid grid-cols-2 gap-3 pt-2">
              <button
                onClick={joinAsPlayer}
                disabled={!connected || !username}
                className="bg-slate-600 hover:bg-slate-500 disabled:opacity-50 disabled:cursor-not-allowed text-white font-semibold py-4 px-6 rounded-xl transition-all flex flex-col items-center justify-center gap-2"
              >
                <Play className="w-6 h-6" />
                <span>Play</span>
              </button>

              <button
                onClick={joinAsSpectator}
                disabled={!connected || !username}
                className="bg-slate-700 hover:bg-slate-600 disabled:opacity-50 disabled:cursor-not-allowed text-white font-semibold py-4 px-6 rounded-xl transition-all flex flex-col items-center justify-center gap-2"
              >
                <Eye className="w-6 h-6" />
                <span>Watch</span>
              </button>
            </div>
          </div>
          {error && (
            <p className="mt-4 text-center text-red-400 text-sm bg-red-900/20 p-2 rounded">
              {error}
            </p>
          )}
        </div>
      </div>
    );
  }

  // Game Screen
  return (
    <div className="min-h-screen bg-slate-950 flex flex-col md:flex-row">
      {/* Sidebar: Players & Spectators */}
      <div className="md:w-80 bg-slate-900/50 border-r border-white/10 p-6 flex flex-col gap-8">
        <div>
          <h2 className="text-slate-200 font-bold text-sm uppercase tracking-wider mb-4 flex items-center gap-2">
            <Users className="w-4 h-4" /> Active Players
          </h2>
          <div className="space-y-3">
            <div
              className={`p-3 rounded-lg border ${
                board.turn === "X"
                  ? "bg-amber-500/20 border-amber-500"
                  : "bg-slate-950 border-white/10"
              }`}
            >
              <div className="text-xs text-amber-500 font-bold mb-1">
                PLAYER X
              </div>
              <div className="text-white font-medium truncate">
                {playerNames.X}
              </div>
            </div>
            <div
              className={`p-3 rounded-lg border ${
                board.turn === "O"
                  ? "bg-cyan-500/20 border-cyan-500"
                  : "bg-slate-950 border-white/10"
              }`}
            >
              <div className="text-xs text-cyan-500 font-bold mb-1">
                PLAYER O
              </div>
              <div className="text-white font-medium truncate">
                {playerNames.O}
              </div>
            </div>
          </div>
        </div>

        <div className="flex-1 overflow-hidden flex flex-col">
          <h2 className="text-slate-200 font-bold text-sm uppercase tracking-wider mb-4 flex items-center gap-2">
            <Eye className="w-4 h-4" /> Spectators ({spectators.length})
          </h2>
          <div className="flex-1 overflow-y-auto space-y-2 pr-2 custom-scrollbar">
            {spectators.length === 0 && (
              <p className="text-gray-500 text-sm italic">
                No spectators watching
              </p>
            )}
            {spectators.map((name, i) => (
              <div
                key={i}
                className="flex items-center gap-2 text-gray-300 bg-slate-950/50 p-2 rounded"
              >
                <div className="w-2 h-2 rounded-full bg-emerald-500 animate-pulse"></div>
                <span className="truncate text-sm">{name}</span>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* Main Game Area */}
      <div className="flex-1 flex flex-col">
        {/* Header */}
        <div className="p-6 flex justify-between items-center bg-slate-900/30">
          <div className="flex items-center gap-3">
            {role === "player" ? (
              <div className="bg-slate-600 text-white px-3 py-1 rounded-full text-sm font-bold">
                move symbol : {symbol}
              </div>
            ) : (
              <div className="bg-gray-600 text-white px-3 py-1 rounded-full text-sm font-bold">
                Spectating
              </div>
            )}
            <div className="text-slate-300 text-sm">
              Playing as <b>{username}</b>
            </div>
          </div>

          {role === "player" ? (
            <button
              onClick={resetBoard}
              className="bg-white/10 hover:bg-white/20 text-white px-4 py-2 rounded-lg text-sm font-medium transition-all flex items-center gap-2"
            >
              <RotateCcw className="w-4 h-4" /> Reset Board
            </button>
          ) : (
            <></>
          )}
        </div>

        {/* Board Container */}
        <div className="flex-1 flex flex-col items-center justify-center p-8">
          {/* Turn Indicator */}
          <div className="mb-8 text-center">
            {board.winner ? (
              <div className="inline-block px-8 py-3 bg-white text-slate-950 rounded-full text-2xl font-bold shadow-lg animate-bounce">
                {board.winner === "draw"
                  ? "It's a Draw!"
                  : `Winner: ${
                      board.winner === "X" ? playerNames.X : playerNames.O
                    }!`}
              </div>
            ) : (
              <h2 className="text-3xl text-white font-light">
                It is{" "}
                <span
                  className={`font-bold ${
                    board.turn === "X" ? "text-amber-500" : "text-cyan-500"
                  }`}
                >
                  {board.turn === "X" ? playerNames.X : playerNames.O}'s
                </span>{" "}
                turn
              </h2>
            )}
          </div>

          {/* Grid */}
          <div className="grid grid-cols-3 gap-4 max-w-md w-full aspect-square">
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
                    relative rounded-2xl text-6xl font-bold transition-all duration-200
                    flex items-center justify-center shadow-xl border-b-4
                    ${
                      cell === "_"
                        ? "bg-slate-800 border-slate-950"
                        : "bg-slate-700 border-slate-900"
                    }
                    ${
                      role === "player" &&
                      !board.winner &&
                      board.turn === symbol &&
                      cell === "_"
                        ? "hover:-translate-y-1 hover:bg-slate-600 cursor-pointer hover:border-b-8"
                        : ""
                    }
                    ${cell === "X" ? "text-amber-500" : "text-cyan-500"}
                    `}
              >
                {cell !== "_" && cell}
              </button>
            ))}
          </div>

          {error && (
            <div className="mt-6 bg-red-500/10 border border-red-500/50 text-red-200 px-4 py-2 rounded-lg">
              {error}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
