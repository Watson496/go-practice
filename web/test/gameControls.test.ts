import assert from "node:assert/strict";
import { test } from "vitest";
import type { GameResponse } from "../src/api";
import { gameControls } from "../src/gameControls";
import { gameResponseFixture } from "./testSupport";

function state(overrides: Partial<GameResponse> = {}): GameResponse {
  const base = gameResponseFixture("game", "対局", {}, { canUndo: true });
  return { ...base, ...overrides, game: overrides.game ?? base.game };
}

test("対局モードでは人の手番に対局操作を許可する", () => {
  assert.deepEqual(gameControls(state(), "play"), {
    configured: true,
    playing: true,
    humanTurn: true,
    aiTurn: false,
    variation: true,
    board: true,
    pass: true,
    undo: true,
    resign: true,
  });
});

test("分析モードでは変化だけを操作できる", () => {
  const controls = gameControls(
    state({ humanColor: "W", canUndo: true }),
    "analysis",
  );
  assert.equal(controls.board, true);
  assert.equal(controls.pass, true);
  assert.equal(controls.undo, false);
  assert.equal(controls.resign, false);
});

test("終局と未設定の対局では対局操作を許可しない", () => {
  for (const unfinished of [
    state({ aiLevel: null, humanColor: null }),
    state({ aiLevel: 2, humanColor: null }),
    state({ aiLevel: null, humanColor: "B" }),
  ]) {
    assert.equal(gameControls(unfinished, "play").board, false);
    assert.equal(gameControls(unfinished, "play").aiTurn, false);
  }
  const finished = state();
  finished.game = {
    ...finished.game,
    currentPlayer: "W",
    status: "finished",
  };
  assert.equal(gameControls(finished, "play").aiTurn, false);
  assert.equal(gameControls(finished, "play").pass, false);
  assert.equal(gameControls(finished, "play").resign, false);
});
