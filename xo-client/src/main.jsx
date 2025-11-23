import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import TicTacToeClient from "./TicTacToeClient.jsx";

createRoot(document.getElementById("root")).render(
  <StrictMode>
    <TicTacToeClient />
  </StrictMode>
);
