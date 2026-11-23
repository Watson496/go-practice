import assert from "node:assert/strict";
import { fireEvent, getByRole } from "@testing-library/dom";
import { test } from "vitest";
import type { GameResponse } from "../src/api";
import {
  appServerResponse,
  gameResponseFixture,
  jsonResponse,
  renderApp,
  waitForCondition as waitFor,
} from "./testSupport";

function gameResponse(note: string, important = false): GameResponse {
  return gameResponseFixture(
    "review-game",
    "メモ競合の確認",
    {
      boardSize: 9,
      rootProperties: note ? [{ ident: "C", values: [note] }] : [],
      treeNodes: [{ id: "root", important }],
    },
    { startedAt: "2026-11-14T00:00:00Z" },
  );
}

test("メモの競合後も編集中の内容を保持して最新値を保存条件にする", async () => {
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: "review-game" }),
  );
  localStorage.setItem(
    "go-practice.workspace-mode",
    JSON.stringify({ schemaVersion: "1", mode: "analysis" }),
  );
  let serverState = gameResponse("編集前");
  const patchBodies: Array<Record<string, unknown>> = [];
  Object.defineProperty(globalThis, "fetch", {
    configurable: true,
    value: async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/nodes/root") && init?.method === "PATCH") {
        const body = JSON.parse(String(init.body));
        patchBodies.push(body);
        if (patchBodies.length === 1) {
          serverState = gameResponse("サーバーの更新");
          return jsonResponse({ error: "メモが変更されています。" }, 409);
        }
        serverState = gameResponse(String(body.note), Boolean(body.important));
        return jsonResponse(serverState);
      }
      const common = appServerResponse(path, init?.method ?? "GET", [
        serverState,
      ]);
      if (common) return common;
      return jsonResponse(serverState);
    },
  });

  const { container, act } = await renderApp({ notifications: false });

  const note = getByRole(container, "textbox", { name: "この局面のメモ" });
  const important = getByRole(container, "checkbox", { name: "重要な局面" });
  await act(async () => {
    fireEvent.change(note, { target: { value: "自分の更新" } });
    fireEvent.click(important);
  });
  await act(async () =>
    fireEvent.click(getByRole(container, "button", { name: "保存" })),
  );
  await waitFor(() => patchBodies.length === 1);
  assert.equal((note as HTMLTextAreaElement).value, "自分の更新");
  assert.equal((important as HTMLInputElement).checked, true);

  await act(async () =>
    fireEvent.click(getByRole(container, "button", { name: "保存" })),
  );
  await waitFor(() => patchBodies.length === 2);
  assert.deepEqual(patchBodies[1], {
    note: "自分の更新",
    important: true,
    expectedNote: "サーバーの更新",
    expectedImportant: false,
  });
});

test("別の局面のメモの保存の応答が後から届いても、書きかけと編集中の通知を残す", async () => {
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: "review-game" }),
  );
  localStorage.setItem(
    "go-practice.workspace-mode",
    JSON.stringify({ schemaVersion: "1", mode: "analysis" }),
  );
  const withMove = (note: string, currentNodeId: string): GameResponse => {
    const response = gameResponse(note);
    response.game.treeNodes = [
      { id: "root" },
      { id: "n1", parentId: "root", move: { color: "B", coordinate: "E5" } },
    ];
    response.game.currentNodeId = currentNodeId;
    response.game.mainLineNodeId = "n1";
    return response;
  };
  let serverState = withMove("", "root");
  let releaseSave = () => {};
  let saveRequested = false;
  const interactionEnds: string[] = [];
  Object.defineProperty(globalThis, "fetch", {
    configurable: true,
    value: async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/ui/interaction") {
        if (init?.method === "DELETE") interactionEnds.push(String(init.body));
        return new Response(null, { status: 204 });
      }
      if (path.endsWith("/nodes/root") && init?.method === "PATCH") {
        saveRequested = true;
        await new Promise<void>((resolve) => {
          releaseSave = resolve;
        });
        serverState = withMove("rootのメモ", serverState.game.currentNodeId);
        return jsonResponse(serverState);
      }
      if (path.endsWith("/nodes/n1") && init?.method === "POST") {
        serverState = withMove(
          serverState.game.rootProperties?.[0]?.values[0] ?? "",
          "n1",
        );
        return jsonResponse(serverState);
      }
      const common = appServerResponse(path, init?.method ?? "GET", [
        serverState,
      ]);
      if (common) return common;
      return jsonResponse(serverState);
    },
  });

  const { container, act } = await renderApp({ notifications: false });

  const note = () =>
    getByRole(container, "textbox", {
      name: "この局面のメモ",
    }) as HTMLTextAreaElement;
  await act(async () =>
    fireEvent.change(note(), { target: { value: "rootのメモ" } }),
  );
  await act(async () =>
    fireEvent.click(getByRole(container, "button", { name: "保存" })),
  );
  await waitFor(() => saveRequested);

  await act(async () => fireEvent.keyDown(window, { key: "ArrowRight" }));
  await waitFor(() => serverState.game.currentNodeId === "n1");
  await waitFor(() => note().value === "");
  await act(async () =>
    fireEvent.change(note(), { target: { value: "n1の書きかけ" } }),
  );
  const endsBeforeResponse = interactionEnds.length;

  await act(async () => releaseSave());
  await new Promise((resolve) => setTimeout(resolve, 50));
  assert.equal(note().value, "n1の書きかけ");
  assert.equal(interactionEnds.length, endsBeforeResponse);
});

test("局面の移動の応答を待つ間は、メモの保存を無効にする", async () => {
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: "review-game" }),
  );
  localStorage.setItem(
    "go-practice.workspace-mode",
    JSON.stringify({ schemaVersion: "1", mode: "analysis" }),
  );
  const state = gameResponse("");
  state.game.treeNodes = [
    { id: "root" },
    { id: "n1", parentId: "root", move: { color: "B", coordinate: "E5" } },
  ];
  state.game.mainLineNodeId = "n1";
  let navigationRequested = false;
  Object.defineProperty(globalThis, "fetch", {
    configurable: true,
    value: async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/nodes/n1") && init?.method === "POST") {
        navigationRequested = true;
        return new Promise<Response>(() => {});
      }
      const common = appServerResponse(path, init?.method ?? "GET", [state]);
      if (common) return common;
      return jsonResponse(state);
    },
  });

  const { container, act } = await renderApp({ notifications: false });

  const save = getByRole(container, "button", { name: "保存" });
  assert.equal(save.hasAttribute("disabled"), false);
  await act(async () => fireEvent.keyDown(window, { key: "ArrowRight" }));
  await waitFor(() => navigationRequested);
  assert.equal(save.hasAttribute("disabled"), true);
});
