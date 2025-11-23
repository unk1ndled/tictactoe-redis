import React, { useState, useEffect, useRef } from "react";
import { Users, Eye, Play, RotateCcw, Wifi, WifiOff } from "lucide-react";

export default function TicTacToeClient() {
  const [ws, setWs] = useState(null);
  const [connected, setConnected] = useState(false);
  const [role, setRole] = useState(null); // 'player' or 'spectator'
  const [boardId, setBoardId] = useState(null);
  const [symbol, setSymbol] = useState(null); // 'X' or 'O'
  const [board, setBoard] = useState({
    cells: Array(9).fill("_"),
    turn: "X",
    winner: "",
  });
  const [error, setError] = useState("");
  const wsRef = useRef(null);

  useEffect(() => {
    // Small delay to ensure component is mounted
    const timer = setTimeout(() => {
      connectWebSocket();
    }, 100);

    return () => {
      clearTimeout(timer);
      if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
        wsRef.current.close();
      }
    };
  }, []);

  const connectWebSocket = () => {
    // Prevent multiple connections
    if (wsRef.current && wsRef.current.readyState === WebSocket.CONNECTING) {
      return;
    }

    try {
      const socket = new WebSocket("ws://localhost:8080/ws");

      socket.onopen = () => {
        console.log("✓ Connected to server");
        setConnected(true);
        setError("");
      };

      socket.onclose = (event) => {
        console.log("✗ Disconnected from server");
        setConnected(false);
        wsRef.current = null;

        // Only auto-reconnect if we were previously connected
        if (event.wasClean === false) {
          console.log("Reconnecting in 3 seconds...");
          setTimeout(connectWebSocket, 3000);
        }
      };

      socket.onerror = (error) => {
        console.error("WebSocket error:", error);
        setError("Cannot connect to server. Is it running on port 8080?");
      };

      socket.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data);
          handleMessage(data);
        } catch (err) {
          console.error("Failed to parse message:", err);
        }
      };

      wsRef.current = socket;
      setWs(socket);
    } catch (err) {
      console.error("Failed to create WebSocket:", err);
      setError("Failed to create connection");
    }
  };

  const handleMessage = (data) => {
    console.log("Received:", data);

    switch (data.type) {
      case "joined":
        console.log("Joined successfully:", data);
        setRole(data.role);
        setBoardId(data.boardId);
        setSymbol(data.symbol || null);
        // Request board state
        setTimeout(() => {
          send({ type: "get_board", boardId: data.boardId });
          send({ type: "subscribe", boardId: data.boardId });
        }, 100);
        break;

      case "board_state":
        console.log("Board state received:", data);
        updateBoard(data.board);
        break;

      case "move_made":
        console.log("Move made:", data);
        updateBoard({
          cells: data.cells,
          winner: data.winner || board.winner,
          turn: data.turn || board.turn, // <-- add this
        });
        break;

      case "reset_done":
        console.log("Board reset");
        send({ type: "get_board", boardId: boardId });
        break;

      case "error":
        console.log("Error:", data.error);
        setError(data.error);
        setTimeout(() => setError(""), 3000);
        break;

      default:
        console.log("Unknown message type:", data.type);
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
      console.log("Sending:", message);
      wsRef.current.send(JSON.stringify(message));
    } else {
      console.error("WebSocket not connected, cannot send:", message);
      setError("Not connected to server");
    }
  };

  const joinAsPlayer = () => {
    console.log("Attempting to join as player...");
    send({ type: "join" });
  };

  const joinAsSpectator = () => {
    console.log("Attempting to join as spectator...");
    send({ type: "join_spectator" });
  };

  const makeMove = (position) => {
    if (role !== "player") {
      setError("Spectators cannot make moves");
      return;
    }
    if (board.winner) {
      setError("Game is over");
      return;
    }
    if (board.turn !== symbol) {
      setError("Not your turn");
      return;
    }
    if (board.cells[position] !== "_") {
      setError("Cell already taken");
      return;
    }

    send({
      type: "move",
      boardId: boardId,
      position: position + 1, // Lua uses 1-based indexing
      symbol: symbol,
    });
  };

  const resetBoard = () => {
    send({ type: "reset", boardId: boardId });
  };

  // Welcome Screen
  if (!role) {
    return (
      <div className="min-h-screen bg-gradient-to-br from-blue-50 to-indigo-100 flex items-center justify-center p-4">
        <div className="bg-white rounded-2xl shadow-2xl p-8 max-w-md w-full">
          <div className="text-center mb-8">
            <h1 className="text-4xl font-bold text-gray-800 mb-2">
              Tic-Tac-Toe
            </h1>
            <p className="text-gray-600">Redis + Lua Demo</p>
            <div className="flex items-center justify-center mt-4 gap-2">
              {connected ? (
                <>
                  <Wifi className="w-5 h-5 text-green-500" />
                  <span className="text-green-600 text-sm">Connected</span>
                </>
              ) : (
                <>
                  <WifiOff className="w-5 h-5 text-red-500" />
                  <span className="text-red-600 text-sm">Connecting...</span>
                </>
              )}
            </div>
          </div>

          <div className="space-y-4">
            <button
              onClick={joinAsPlayer}
              disabled={!connected}
              className="w-full bg-indigo-600 hover:bg-indigo-700 disabled:bg-gray-300 text-white font-semibold py-4 px-6 rounded-xl transition-all transform hover:scale-105 flex items-center justify-center gap-3"
            >
              <Play className="w-6 h-6" />
              Join as Player
            </button>

            <button
              onClick={joinAsSpectator}
              disabled={!connected}
              className="w-full bg-gray-600 hover:bg-gray-700 disabled:bg-gray-300 text-white font-semibold py-4 px-6 rounded-xl transition-all transform hover:scale-105 flex items-center justify-center gap-3"
            >
              <Eye className="w-6 h-6" />
              Watch as Spectator
            </button>
          </div>

          {error && (
            <div className="mt-4 p-4 bg-red-100 border-l-4 border-red-500 text-red-700 rounded text-sm">
              <p className="font-semibold">Connection Error</p>
              <p className="mt-1">{error}</p>
              <p className="mt-2 text-xs">
                Make sure the Go server is running:{" "}
                <code className="bg-red-200 px-1 rounded">go run main.go</code>
              </p>
              <button
                onClick={connectWebSocket}
                className="mt-3 w-full bg-red-600 hover:bg-red-700 text-white font-semibold py-2 px-4 rounded transition-all"
              >
                Retry Connection
              </button>
            </div>
          )}
        </div>
      </div>
    );
  }

  // Game Screen
  return (
    <div className="min-h-screen bg-gradient-to-br from-blue-50 to-indigo-100 p-4">
      <div className="max-w-2xl mx-auto">
        {/* Header */}
        <div className="bg-white rounded-2xl shadow-lg p-6 mb-6">
          <div className="flex items-center justify-between">
            <div>
              <h1 className="text-3xl font-bold text-gray-800">
                Board #{boardId}
              </h1>
              <div className="flex items-center gap-4 mt-2">
                <div className="flex items-center gap-2">
                  {role === "player" ? (
                    <Users className="w-5 h-5 text-indigo-600" />
                  ) : (
                    <Eye className="w-5 h-5 text-gray-600" />
                  )}
                  <span className="text-sm font-medium text-gray-600">
                    {role === "player" ? `Playing as ${symbol}` : "Spectating"}
                  </span>
                </div>
                <div className="flex items-center gap-2">
                  {connected ? (
                    <Wifi className="w-4 h-4 text-green-500" />
                  ) : (
                    <WifiOff className="w-4 h-4 text-red-500" />
                  )}
                </div>
              </div>
            </div>
            <button
              onClick={resetBoard}
              className="bg-gray-200 hover:bg-gray-300 text-gray-700 font-semibold py-2 px-4 rounded-lg transition-all flex items-center gap-2"
            >
              <RotateCcw className="w-4 h-4" />
              Reset
            </button>
          </div>

          {/* Turn Indicator */}
          {!board.winner && (
            <div className="mt-4 p-3 bg-blue-50 rounded-lg">
              <p className="text-center text-blue-800 font-medium">
                {role === "player" && board.turn === symbol ? (
                  <span className="text-green-600 font-bold">
                    Your turn! ({symbol})
                  </span>
                ) : (
                  <span>Turn: {board.turn}</span>
                )}
              </p>
            </div>
          )}

          {/* Winner */}
          {board.winner && (
            <div className="mt-4 p-4 bg-gradient-to-r from-green-400 to-blue-500 rounded-lg">
              <p className="text-center text-white text-xl font-bold">
                {board.winner === "draw"
                  ? "It's a Draw!"
                  : board.winner === symbol
                  ? "🎉 You Won!"
                  : `${board.winner} Wins!`}
              </p>
            </div>
          )}

          {/* Error Message */}
          {error && (
            <div className="mt-4 p-3 bg-red-100 border border-red-400 text-red-700 rounded-lg text-sm">
              {error}
            </div>
          )}
        </div>

        {/* Game Board */}
        <div className="bg-white rounded-2xl shadow-lg p-8">
          <div className="grid grid-cols-3 gap-3 max-w-md mx-auto">
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
                  aspect-square rounded-xl text-5xl font-bold transition-all
                  ${
                    cell === "X"
                      ? "text-blue-600"
                      : cell === "O"
                      ? "text-red-600"
                      : "text-gray-300"
                  }
                  ${
                    role === "player" &&
                    !board.winner &&
                    board.turn === symbol &&
                    cell === "_"
                      ? "bg-indigo-50 hover:bg-indigo-100 cursor-pointer transform hover:scale-105"
                      : "bg-gray-50 cursor-not-allowed"
                  }
                  ${board.winner ? "opacity-60" : ""}
                  shadow-md hover:shadow-lg
                `}
              >
                {cell === "_" ? "" : cell}
              </button>
            ))}
          </div>
        </div>

        {/* Instructions */}
        <div className="mt-6 bg-white rounded-2xl shadow-lg p-6">
          <h3 className="font-bold text-gray-800 mb-2">How it works:</h3>
          <ul className="text-sm text-gray-600 space-y-1">
            <li>
              • <strong>Players:</strong> Wait for your turn and click a cell to
              play
            </li>
            <li>
              • <strong>Spectators:</strong> Watch the game in real-time
            </li>
            <li>
              • <strong>Redis Lua:</strong> All moves validated atomically
            </li>
            <li>
              • <strong>Pub/Sub:</strong> Updates broadcast instantly to all
              viewers
            </li>
          </ul>
        </div>
      </div>
    </div>
  );
}
