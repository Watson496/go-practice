import assert from "node:assert/strict";
import { findByRole } from "@testing-library/dom";
import { onTestFinished, test, vi } from "vitest";
import {
  appServerResponse,
  gameResponseFixture,
  renderApp,
} from "./testSupport";

test("盤をキーで選んだ着手を送信し応答待ちには重ねて送らない", async () => {
  const state = gameResponseFixture(
    "game-1",
    "盤のキー操作",
    {
      boardSize: 9,
    },
    { startedAt: "2026-11-15T00:00:00Z" },
  );
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  const moveRequests: unknown[] = [];
  let finishMove: ((response: Response) => void) | undefined;
  const moveResponse = new Promise<Response>((resolve) => {
    finishMove = resolve;
  });
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/moves")) {
        moveRequests.push(JSON.parse(String(init?.body)));
        return moveResponse;
      }
      const common = appServerResponse(path, init?.method ?? "GET", [state]);
      if (common) return common;
      return Response.json(state);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
  });

  const { container, act } = await renderApp({ notifications: false });

  const board = await findByRole(container, "application", { name: /^9路盤/ });
  await act(async () => {
    board.focus();
    board.dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "ArrowRight",
        bubbles: true,
        cancelable: true,
      }),
    );
    board.dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "Enter",
        bubbles: true,
        cancelable: true,
      }),
    );
  });
  assert.equal(moveRequests.length, 1);
  assert.deepEqual(moveRequests[0], { coordinate: "F5" });
  assert.equal(board.getAttribute("aria-disabled"), "true");

  await act(async () => {
    board.dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "Enter",
        bubbles: true,
        cancelable: true,
      }),
    );
  });
  assert.equal(moveRequests.length, 1);

  finishMove?.(Response.json({ game: state }));
  await act(async () => moveResponse);
});
