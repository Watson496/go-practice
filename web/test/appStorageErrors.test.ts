import assert from "node:assert/strict";
import { findAllByRole, findByRole, fireEvent } from "@testing-library/dom";
import { onTestFinished, test, vi } from "vitest";
import type { GameResponse } from "../src/api";
import {
  appServerResponse,
  gameResponseFixture,
  renderApp,
  waitForCondition,
} from "./testSupport";

async function waitForNotification() {
  await findByRole(document.body, "alert");
}

function stubEmptyServer() {
  vi.stubGlobal("fetch", async (input: string | URL | Request) => {
    const common = appServerResponse(String(input), "GET", []);
    if (common) return common;
    return new Response(null, { status: 204 });
  });
}

function evaluationState(): GameResponse {
  return gameResponseFixture("stored-evaluation-game", "評価履歴を読む対局");
}

function stubEvaluationServer(state: GameResponse) {
  vi.stubGlobal("fetch", async (input: string | URL | Request) => {
    const path = String(input);
    const common = appServerResponse(path, "GET", [state]);
    if (common) return common;
    return Response.json(state);
  });
}

test("不正なモードを使わずに通知する", async () => {
  localStorage.setItem("go-practice.workspace-mode", "不正な保存値");
  stubEmptyServer();
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  const { container } = await renderApp();
  await waitForNotification();
  assert.ok(await findByRole(container, "button", { name: "新しい対局" }));
});

test("前回の対局をlocalStorageから読めないときに通知する", async () => {
  const getItem = Storage.prototype.getItem;
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(function (
    this: Storage,
    key: string,
  ) {
    if (key === "go-practice.latest-game-record")
      throw new DOMException("読み取れません", "SecurityError");
    return getItem.call(this, key);
  });
  stubEmptyServer();
  onTestFinished(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  const { container } = await renderApp();
  await waitForNotification();
  await waitForCondition(() => {
    const button = Array.from(container.querySelectorAll("button")).find(
      (candidate) => candidate.textContent === "新しい対局",
    );
    return button instanceof HTMLButtonElement && !button.disabled;
  }, "前回の対局の復元が終わっていません");
});

test("不正な評価履歴を使わずに通知する", async () => {
  const state = evaluationState();
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  localStorage.setItem(`go-practice.evaluations.${state.game.id}`, "不正な値");
  stubEvaluationServer(state);
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  await renderApp();
  await waitForNotification();
  assert.notEqual(
    localStorage.getItem(`go-practice.evaluations.${state.game.id}`),
    "不正な値",
  );
});

test("評価履歴をlocalStorageから読めないときは未保存として扱い通知する", async () => {
  const state = evaluationState();
  const key = `go-practice.evaluations.${state.game.id}`;
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  const getItem = Storage.prototype.getItem;
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(function (
    this: Storage,
    requested: string,
  ) {
    if (requested === key)
      throw new DOMException("読み取れません", "SecurityError");
    return getItem.call(this, requested);
  });
  stubEvaluationServer(state);
  onTestFinished(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  const { container } = await renderApp();
  await waitForNotification();
  assert.ok(await findByRole(container, "button", { name: "パス" }));
});

test("別の対局の評価履歴を読めないときは前の対局の履歴を残さない", async () => {
  const first = gameResponseFixture("first-evaluation", "評価のある対局", {
    currentPlayer: "W",
    moves: [{ color: "B", coordinate: "C3" }],
    stones: { C3: "B" },
    treeNodes: [
      { id: "root" },
      { id: "n1", parentId: "root", move: { color: "B", coordinate: "C3" } },
    ],
    currentNodeId: "n1",
    mainLineNodeId: "n1",
  });
  const second = gameResponseFixture(
    "second-evaluation",
    "評価を読めない対局",
    {
      currentPlayer: "W",
      moves: [{ color: "B", coordinate: "D3" }],
      stones: { D3: "B" },
      treeNodes: [
        { id: "root" },
        { id: "n1", parentId: "root", move: { color: "B", coordinate: "D3" } },
      ],
      currentNodeId: "n1",
      mainLineNodeId: "n1",
    },
  );
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: first.game.id }),
  );
  localStorage.setItem(
    `go-practice.evaluations.${first.game.id}`,
    JSON.stringify({
      schemaVersion: "1",
      entries: {
        root: { scoreLead: 8, winrate: 0.8, visits: 100 },
        n1: { scoreLead: 9, winrate: 0.9, visits: 100 },
      },
    }),
  );
  const secondKey = `go-practice.evaluations.${second.game.id}`;
  const getItem = Storage.prototype.getItem;
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(function (
    this: Storage,
    key: string,
  ) {
    if (key === secondKey)
      throw new DOMException("読み取れません", "SecurityError");
    return getItem.call(this, key);
  });
  vi.stubGlobal("fetch", async (input: string | URL | Request) => {
    const path = String(input);
    const common = appServerResponse(path, "GET", [first, second]);
    if (common && !path.includes("/analysis")) return common;
    if (path.includes("/analysis")) return new Promise<Response>(() => {});
    return Response.json(first);
  });
  onTestFinished(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  const { container, act } = await renderApp();
  await waitForCondition(
    () => (container.textContent ?? "").includes("+9"),
    "保存した評価履歴が表示されませんでした",
  );

  await act(async () =>
    fireEvent.click(
      await findByRole(container, "button", { name: second.name }),
    ),
  );
  await waitForNotification();
  await waitForCondition(
    () => !(container.textContent ?? "").includes("+9"),
    "前の対局の評価履歴が残っています",
  );
});

test("起動時に読み込めなかった保存ファイルを通知する", async () => {
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request) =>
      new Response(
        JSON.stringify(
          String(input) === "/api/storage-errors"
            ? [{ file: "broken.json", reason: "JSONが不正です" }]
            : [],
        ),
        { headers: { "Content-Type": "application/json" } },
      ),
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
  });

  await renderApp();

  const notifications = await findAllByRole(document.body, "alert");
  assert.ok(notifications.length >= 1);
});

test("評価履歴をlocalStorageに書き込めなくても、評価を表示して通知する", async () => {
  const state = gameResponseFixture(
    "evaluation-game",
    "評価履歴",
    {},
    {
      startedAt: "2026-11-14T00:00:00Z",
    },
  );
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  const setItem = Storage.prototype.setItem;
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (
    this: Storage,
    key: string,
    value: string,
  ) {
    if (key.startsWith("go-practice.evaluations."))
      throw new DOMException("容量を超えました", "QuotaExceededError");
    setItem.call(this, key, value);
  });
  vi.stubGlobal("fetch", async (input: string | URL | Request) => {
    const path = String(input);
    if (path.includes("/analysis"))
      return Response.json({
        currentPlayer: "B",
        scoreLead: 2.5,
        winrate: 0.6,
        visits: 100,
        ownership: Array(25).fill(0),
        candidates: [],
      });
    const common = appServerResponse(path, "GET", [state]);
    if (common) return common;
    return Response.json(state);
  });
  onTestFinished(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  const { container } = await renderApp();

  await waitForNotification();
  await waitForCondition(() => /\+2\.5目/.test(container.textContent ?? ""));
  assert.match(container.textContent ?? "", /\+2\.5目/);
});

test("モードをlocalStorageに書き込めないときに通知する", async () => {
  const state = gameResponseFixture(
    "mode-storage-game",
    "モード保存",
    {},
    {
      canUndo: true,
    },
  );
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  const setItem = Storage.prototype.setItem;
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (
    this: Storage,
    key: string,
    value: string,
  ) {
    if (key === "go-practice.workspace-mode")
      throw new DOMException("保存できません", "QuotaExceededError");
    setItem.call(this, key, value);
  });
  vi.stubGlobal("fetch", async (input: string | URL | Request) => {
    const path = String(input);
    if (path.endsWith("/ai-moves")) return new Response(null, { status: 204 });
    const common = appServerResponse(path, "GET", [state]);
    if (common) return common;
    return Response.json(state);
  });
  onTestFinished(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  const { container } = await renderApp();
  fireEvent.click(await findByRole(container, "radio", { name: "分析" }));
  await waitForNotification();
  assert.equal(
    (await findByRole(container, "button", { name: "待った" })).hasAttribute(
      "disabled",
    ),
    true,
  );
});
