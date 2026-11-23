import type { GameResponse } from "./api";
import { isAITurn } from "./gameControls";

function defaultGameName(game: GameResponse) {
  const startedAt = new Date(game.startedAt);
  const pair = (value: number) => String(value).padStart(2, "0");
  return `${pair(startedAt.getFullYear() % 100)}${pair(startedAt.getMonth() + 1)}${pair(startedAt.getDate())}-${pair(startedAt.getHours())}${pair(startedAt.getMinutes())}`;
}

export function gameName(game: GameResponse) {
  return game.name === undefined || game.name === ""
    ? defaultGameName(game)
    : game.name;
}

// AIの手番で開いた対局は、人が再開するまでAIの手を始めない。
export function pausedWhenOpened(game: GameResponse) {
  return isAITurn(game);
}
