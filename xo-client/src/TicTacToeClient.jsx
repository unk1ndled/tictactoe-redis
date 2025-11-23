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
        setError("Cannot connect to server.");
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
      <div className="min-h-screen bg-indigo-950 flex items-center justify-center p-4">
        <div className=" p-8 max-w-md w-full">
          <div className="text-center mb-8">
            <h1 className="text-4xl font-bold text-white mb-2">Tic-Tac-Toe</h1>
            <p className="text-gray-600">Redis Demo</p>
            <div className="flex items-center justify-center mt-4 gap-2  text-sm hover:text-2xl transition-all">
              {connected ? (
                <>
                  <Wifi className="w-5 h-5 text-emerald-500" />
                  <span className="text-emerald-600 ">Connected</span>
                </>
              ) : (
                <>
                  <WifiOff className="w-5 h-5 text-red-500" />
                  <span className="text-red-600 ">Connecting...</span>
                </>
              )}
            </div>
          </div>

          <div className="space-y-4">
            <button
              onClick={joinAsPlayer}
              disabled={!connected}
              className="w-full bg-indigo-600 hover:bg-indigo-700 disabled:bg-gray-300 text-white font-semibold py-4 px-6  transition-all transform hover:scale-105 flex items-center justify-center gap-3"
            >
              <Play className="w-6 h-6" />
              Join as Player
            </button>

            <button
              onClick={joinAsSpectator}
              disabled={!connected}
              className="w-full bg-gray-600 hover:bg-gray-700 disabled:bg-gray-300 text-white font-semibold py-4 px-6 -xl transition-all transform hover:scale-105 flex items-center justify-center gap-3"
            >
              <Eye className="w-6 h-6" />
              Watch as Spectator
            </button>
          </div>

          {error && (
            <div className="mt-4 p-4 bg-red-100 border-l-4 border-red-500 text-red-700  text-sm">
              <p className="font-semibold">Connection Error</p>
              <p className="mt-1">{error}</p>
              <p className="mt-2 text-xs">
                Make sure the Go server is running:{" "}
                <code className="bg-red-200 px-1 ">go run main.go</code>
              </p>
              <button
                onClick={connectWebSocket}
                className="mt-3 w-full bg-red-600 hover:bg-red-700 text-white font-semibold py-2 px-4  transition-all"
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
    <div className="min-h-screen bg-indigo-950 ">
      <div className=" mx-auto">
        {/* Header */}
        <div className="bg-slate-300  -2xl shadow-lg p-6 mb-6">
          <div className="w-full flex justify-center items-center gap-2">
            <span className="text-2xl font-bold capitalize text-indigo-950">
              server connection state{" "}
            </span>
            {connected ? (
              <Wifi className="w-10 h-10 text-emerald-500" />
            ) : (
              <WifiOff className="w-10 h-10 text-red-500" />
            )}
          </div>
          <div className="flex items-center justify-between">
            <div className="flex hover:text-3xl transition-all items-center gap-2">
              {role === "player" ? (
                <Users className="w-5 h-5 text-indigo-950" />
              ) : (
                <Eye className="w-5 h-5 text-gray-600" />
              )}
              <span className=" font-medium text-gray-600">
                {role === "player" ? `Playing as ${symbol}` : "Spectating"}
              </span>
            </div>

            {/* Turn Indicator */}
            {!board.winner && (
              <div className=" ">
                <p className="text-center text-5xl text-indigo-950 font-semibold">
                  {role === "player" && board.turn === symbol ? (
                    <span className="text-emerald-500  font-bold">
                      Your turn! ({symbol})
                    </span>
                  ) : (
                    <span>{board.turn}'s turn</span>
                  )}
                </p>
              </div>
            )}
            <button
              onClick={resetBoard}
              className="bg-slate-300 hover:text-3xl text-gray-700 font-semibold  -lg transition-all flex items-center gap-2"
            >
              <RotateCcw className="w-4 h-4" />
              Reset
            </button>
          </div>

          {/* Winner */}
          {board.winner && (
            <div className="mt-4 p-4 bg-indigo-950 -lg">
              <p className="text-center text-white text-xl font-bold">
                {board.winner === "draw"
                  ? "It's a Draw!"
                  : board.winner === symbol
                  ? " You Won!"
                  : `${board.winner} Wins!`}
              </p>
            </div>
          )}

          {/* Error Message */}
          {error && (
            <div className="mt-4 p-3 bg-red-100 border border-red-400 text-red-700 -lg text-sm">
              {error}
            </div>
          )}
        </div>

        {/* Game Board */}
        <div className="  p-8">
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
                  aspect-square -xl text-5xl font-bold transition-all
                  ${
                    cell === "X"
                      ? "text-amber-500"
                      : cell === "O"
                      ? "text-cyan-500"
                      : "text-gray-300"
                  }
                  ${
                    role === "player" &&
                    !board.winner &&
                    board.turn === symbol &&
                    cell === "_"
                      ? "bg-indigo-50 hover:bg-indigo-100 cursor-pointer transform hover:scale-105"
                      : "bg-indigo-800/50 cursor-not-allowed"
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
      </div>
    </div>
  );
}
