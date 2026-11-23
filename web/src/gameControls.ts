import type { GameResponse } from "./api";
import { canPlayVariation } from "./frontendState";
import { settingsConfigured } from "./gameSettings";

type WorkspaceMode = "play" | "analysis";

export function isAITurn(state: GameResponse) {
  return (
    state.game.status === "playing" &&
    state.game.currentPlayer !== state.humanColor
  );
}

export function gameControls(state: GameResponse | null, mode: WorkspaceMode) {
  const configured = state !== null && settingsConfigured(state);
  const playing = state?.game.status === "playing";
  const humanTurn = Boolean(
    playing &&
      configured &&
      state &&
      state.game.currentPlayer === state.humanColor,
  );
  const aiTurn = Boolean(configured && state && isAITurn(state));
  const variation = state !== null && canPlayVariation(state);
  return {
    configured,
    playing,
    humanTurn,
    aiTurn,
    variation,
    board: mode === "analysis" ? variation : humanTurn,
    pass: mode === "analysis" ? variation : humanTurn,
    undo: mode === "play" && Boolean(state?.canUndo),
    resign: mode === "play" && humanTurn,
  };
}
