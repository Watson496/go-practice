import assert from "node:assert/strict";
import {
  findByRole,
  fireEvent,
  getByRole,
  waitFor,
} from "@testing-library/dom";
import { onTestFinished, test, vi } from "vitest";
import type { GameResponse } from "../src/api";
import {
  appServerResponse,
  gameResponseFixture,
  renderApp,
} from "./testSupport";

test("読み込んだ記録結果を画面から待ったできる", async () => {
  const state = gameResponseFixture(
    "recorded-result-game",
    "記録結果の待った",
    {
      status: "finished",
      result: {
        type: "recorded",
        cause: "recorded",
        value: "Void",
        scoreKnown: false,
      },
      treeNodes: [
        { id: "root" },
        {
          id: "n1",
          parentId: "root",
          result: {
            type: "recorded",
            cause: "recorded",
            value: "Void",
            scoreKnown: false,
          },
        },
      ],
      currentNodeId: "n1",
      mainLineNodeId: "n1",
    },
    { startedAt: "2026-11-14T00:00:00Z", canUndo: true },
  );
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  const requests: Array<{ path: string; method: string }> = [];
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      requests.push({ path, method: init?.method ?? "GET" });
      const common = appServerResponse(path, init?.method ?? "GET", [state]);
      if (common) return common;
      return Response.json(state);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
  });

  const { container } = await renderApp({ notifications: false });

  const undo = await findByRole(container, "button", { name: "待った" });
  assert.equal(undo.hasAttribute("disabled"), false);
  fireEvent.click(undo);
  await waitFor(() => {
    assert.ok(
      requests.some(
        (request) =>
          request.path === `/api/games/${state.game.id}/undo` &&
          request.method === "POST",
      ),
    );
  });
});

test("二連続パスを終えた局面からパスの変化を作る", async () => {
  const state = gameResponseFixture(
    "two-pass-completion-game",
    "二連続パスの変化",
    {
      status: "finished",
      result: {
        type: "points",
        cause: "two_passes",
        winner: "W",
        margin: 7.5,
        blackScore: 0,
        whiteScore: 7.5,
        deadStones: [],
        scoreKnown: true,
      },
      moves: [
        { color: "B", coordinate: "pass" },
        { color: "W", coordinate: "pass" },
      ],
      treeNodes: [
        { id: "root" },
        {
          id: "n1",
          parentId: "root",
          move: { color: "B", coordinate: "pass" },
        },
        {
          id: "n2",
          parentId: "n1",
          move: { color: "W", coordinate: "pass" },
        },
        {
          id: "n3",
          parentId: "n2",
          result: {
            type: "points",
            cause: "two_passes",
            winner: "W",
            margin: 7.5,
            blackScore: 0,
            whiteScore: 7.5,
            deadStones: [],
            scoreKnown: true,
          },
        },
      ],
      currentNodeId: "n2",
      mainLineNodeId: "n3",
    },
    { startedAt: "2026-11-14T00:00:00Z", canUndo: true },
  );
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  localStorage.setItem(
    "go-practice.workspace-mode",
    JSON.stringify({ schemaVersion: "1", mode: "analysis" }),
  );
  const requests: Array<{ path: string; method: string; body: string }> = [];
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      requests.push({
        path,
        method: init?.method ?? "GET",
        body: String(init?.body ?? ""),
      });
      const common = appServerResponse(path, init?.method ?? "GET", [state]);
      if (common) return common;
      return Response.json(state);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
  });

  const { container } = await renderApp({ notifications: false });

  const pass = await findByRole(container, "button", { name: "パス" });
  assert.equal(pass.hasAttribute("disabled"), false);
  window.dispatchEvent(new KeyboardEvent("keydown", { key: "p" }));
  await waitFor(() => {
    const request = requests.find(
      (entry) =>
        entry.path === `/api/games/${state.game.id}/variations` &&
        entry.method === "POST",
    );
    assert.ok(request);
    assert.deepEqual(JSON.parse(request.body), { coordinate: "pass" });
  });
});

test("PageUpで現在の変化を主分岐にする", async () => {
  const state = gameResponseFixture(
    "keyboard-promotion",
    "キーで主分岐化",
    {
      currentPlayer: "W",
      moves: [{ color: "B", coordinate: "D3" }],
      stones: { D3: "B" },
      treeNodes: [
        { id: "root" },
        { id: "n1", parentId: "root", move: { color: "B", coordinate: "C3" } },
        { id: "n2", parentId: "root", move: { color: "B", coordinate: "D3" } },
      ],
      currentNodeId: "n2",
      mainLineNodeId: "n1",
    },
    { canUndo: true },
  );
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  localStorage.setItem(
    "go-practice.workspace-mode",
    JSON.stringify({ schemaVersion: "1", mode: "analysis" }),
  );
  const requests: Array<{ path: string; method: string }> = [];
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      requests.push({ path, method: init?.method ?? "GET" });
      const common = appServerResponse(path, init?.method ?? "GET", [state]);
      if (common) return common;
      return Response.json({
        ...state,
        game: { ...state.game, mainLineNodeId: "n2" },
      });
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  await renderApp();

  window.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp" }));
  await waitFor(() => {
    assert.ok(
      requests.some(
        ({ path, method }) =>
          path === `/api/games/${state.game.id}/nodes/n2/main-line` &&
          method === "POST",
      ),
    );
  });
});

test("棋譜の移動、領域予測、削除のショートカットを画面で操作する", async () => {
  const state = gameResponseFixture(
    "keyboard-navigation",
    "キーで移動する対局",
    {
      currentPlayer: "W",
      moves: [{ color: "B", coordinate: "C3" }],
      stones: { C3: "B" },
      treeNodes: [
        { id: "root" },
        { id: "n1", parentId: "root", move: { color: "B", coordinate: "C3" } },
        { id: "n2", parentId: "root", move: { color: "B", coordinate: "D3" } },
        { id: "n3", parentId: "n1", move: { color: "W", coordinate: "C2" } },
      ],
      currentNodeId: "n1",
      mainLineNodeId: "n3",
    },
    { canUndo: true },
  );
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  localStorage.setItem(
    "go-practice.workspace-mode",
    JSON.stringify({ schemaVersion: "1", mode: "analysis" }),
  );
  const navigated: string[] = [];
  let deleted = false;
  vi.stubGlobal("confirm", () => true);
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      const common = appServerResponse(path, init?.method ?? "GET", [state]);
      if (common) return common;
      const match = path.match(/\/nodes\/(root|n[1-3])$/);
      if (path.endsWith("/nodes/n1") && init?.method === "DELETE") {
        deleted = true;
        return Response.json({
          ...state,
          game: { ...state.game, currentNodeId: "root", mainLineNodeId: "n2" },
        });
      }
      if (match && init?.method === "POST") {
        navigated.push(match[1]);
        return Response.json({
          ...state,
          game: { ...state.game, currentNodeId: match[1] },
        });
      }
      return Response.json(state);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  const { container, act } = await renderApp({ notifications: false });
  const ownership = await findByRole(container, "switch", {
    name: "ownership",
  });
  const candidates = await findByRole(container, "switch", {
    name: "候補手",
  });
  const initiallyChecked = (ownership as HTMLInputElement).checked;
  const candidatesInitiallyChecked = (candidates as HTMLInputElement).checked;

  await act(async () =>
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "t" })),
  );
  await waitFor(() =>
    assert.notEqual((ownership as HTMLInputElement).checked, initiallyChecked),
  );
  await act(async () =>
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "e" })),
  );
  await waitFor(() =>
    assert.notEqual(
      (candidates as HTMLInputElement).checked,
      candidatesInitiallyChecked,
    ),
  );
  await act(async () =>
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown" })),
  );
  await waitFor(() => assert.deepEqual(navigated, ["n2"]));
  await act(async () =>
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp" })),
  );
  await waitFor(() => assert.deepEqual(navigated, ["n2", "n1"]));
  await act(async () =>
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown" })),
  );
  await waitFor(() => assert.deepEqual(navigated, ["n2", "n1", "n2"]));
  await act(async () =>
    window.dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowLeft", shiftKey: true }),
    ),
  );
  await waitFor(() => assert.deepEqual(navigated, ["n2", "n1", "n2", "root"]));
  await act(async () =>
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "End" })),
  );
  await waitFor(() =>
    assert.deepEqual(navigated, ["n2", "n1", "n2", "root", "n3"]),
  );
  await act(async () =>
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Home" })),
  );
  await waitFor(() =>
    assert.deepEqual(navigated, ["n2", "n1", "n2", "root", "n3", "root"]),
  );
  await act(async () =>
    window.dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowRight", metaKey: true }),
    ),
  );
  await waitFor(() =>
    assert.deepEqual(navigated, ["n2", "n1", "n2", "root", "n3", "root", "n3"]),
  );
  await act(async () =>
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "z" })),
  );
  await waitFor(() =>
    assert.deepEqual(navigated, [
      "n2",
      "n1",
      "n2",
      "root",
      "n3",
      "root",
      "n3",
      "n1",
    ]),
  );
  await act(async () =>
    window.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Backspace", metaKey: true }),
    ),
  );
  await waitFor(() => assert.equal(deleted, true));
});

test("十手戻る移動、一手戻る移動、局面以降の削除をキーで操作する", async () => {
  const treeNodes: GameResponse["game"]["treeNodes"] = [{ id: "root" }];
  const moves: GameResponse["game"]["moves"] = [];
  const coordinates = [
    "A1",
    "B1",
    "C1",
    "D1",
    "E1",
    "A2",
    "B2",
    "C2",
    "D2",
    "E2",
    "A3",
    "B3",
  ];
  for (let number = 1; number <= 12; number++) {
    const color = number % 2 === 1 ? "B" : "W";
    const coordinate = coordinates[number - 1];
    treeNodes.push({
      id: `n${number}`,
      parentId: number === 1 ? "root" : `n${number - 1}`,
      move: { color, coordinate },
    });
    moves.push({ color, coordinate });
  }
  const state = gameResponseFixture(
    "keyboard-distance",
    "移動幅を確かめる対局",
    {
      moves,
      stones: Object.fromEntries(
        coordinates.map((coordinate, index) => [
          coordinate,
          index % 2 === 0 ? "B" : "W",
        ]),
      ),
      treeNodes,
      currentNodeId: "n12",
      mainLineNodeId: "n12",
    },
    { canUndo: true },
  );
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  localStorage.setItem(
    "go-practice.workspace-mode",
    JSON.stringify({ schemaVersion: "1", mode: "analysis" }),
  );
  const navigated: string[] = [];
  let deleted = false;
  vi.stubGlobal("confirm", () => true);
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      const common = appServerResponse(path, init?.method ?? "GET", [state]);
      if (common) return common;
      const match = path.match(/\/nodes\/(n\d+)$/);
      if (match && init?.method === "DELETE") {
        deleted = true;
        return Response.json({
          ...state,
          game: {
            ...state.game,
            currentNodeId: "root",
            mainLineNodeId: "root",
          },
        });
      }
      if (match && init?.method === "POST") {
        navigated.push(match[1]);
        return Response.json({
          ...state,
          game: { ...state.game, currentNodeId: match[1] },
        });
      }
      if (path.includes("/analysis")) return new Promise<Response>(() => {});
      return Response.json(state);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  const { act } = await renderApp({ notifications: false });

  await act(async () =>
    window.dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowLeft", shiftKey: true }),
    ),
  );
  await waitFor(() => assert.deepEqual(navigated, ["n2"]));
  await act(async () =>
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft" })),
  );
  await waitFor(() => assert.deepEqual(navigated, ["n2", "n1"]));
  await act(async () =>
    window.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Delete", metaKey: true }),
    ),
  );
  await waitFor(() => assert.equal(deleted, true));
});

test("対局モードで候補手から変化を作れなかったときは表示を保つ", async () => {
  const state = gameResponseFixture(
    "candidate-failure-game",
    "候補手の失敗",
    {},
    { startedAt: "2026-11-14T00:00:00Z" },
  );
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/analysis"))
        return Response.json({
          currentPlayer: "B",
          scoreLead: 1,
          winrate: 0.55,
          visits: 100,
          ownership: Array(25).fill(0),
          candidates: [
            {
              move: "D4",
              scoreLead: 1,
              winrate: 0.55,
              visits: 50,
              pv: ["D4"],
            },
          ],
        });
      const common = appServerResponse(path, init?.method ?? "GET", [state]);
      if (common) return common;
      if (path.endsWith("/variations") && init?.method === "POST")
        return Response.json(
          { error: "対局は別の操作で更新されています。" },
          { status: 409 },
        );
      return Response.json(state);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
  });

  const { container, act } = await renderApp({ notifications: false });

  fireEvent.click(
    await findByRole(container, "switch", {
      name: "候補手パネルの表示",
    }),
  );
  const candidate = await findByRole(container, "row", { name: /D4/ });
  await act(async () => fireEvent.doubleClick(candidate));
  await waitFor(() => {
    assert.equal(
      (getByRole(container, "radio", { name: "対局" }) as HTMLInputElement)
        .checked,
      true,
    );
    assert.equal(
      (
        getByRole(container, "switch", {
          name: "候補手",
        }) as HTMLInputElement
      ).checked,
      false,
    );
    assert.equal(
      (
        getByRole(container, "switch", {
          name: "候補手パネルの表示",
        }) as HTMLInputElement
      ).checked,
      true,
    );
  });
});

test("AIの手番では人間のパスと投了を無効にする", async () => {
  const state = gameResponseFixture(
    "ai-turn-game",
    "AIの手番",
    {
      currentPlayer: "W",
      moves: [{ color: "B", coordinate: "C3" }],
      stones: { C3: "B" },
      treeNodes: [
        { id: "root" },
        {
          id: "n1",
          parentId: "root",
          move: { color: "B", coordinate: "C3" },
        },
      ],
      currentNodeId: "n1",
      mainLineNodeId: "n1",
    },
    { startedAt: "2026-11-14T00:00:00Z", canUndo: true },
  );
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  vi.stubGlobal("fetch", async (input: string | URL | Request) => {
    const path = String(input);
    const common = appServerResponse(path, "GET", [state]);
    if (common) return common;
    return Response.json(state);
  });
  onTestFinished(() => {
    vi.unstubAllGlobals();
  });

  const { container } = await renderApp({ notifications: false });

  const pass = await findByRole(container, "button", { name: "パス" });
  const resign = await findByRole(container, "button", { name: "投了" });
  assert.equal(pass.hasAttribute("disabled"), true);
  assert.equal(resign.hasAttribute("disabled"), true);
});
