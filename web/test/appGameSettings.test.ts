import assert from "node:assert/strict";
import {
  findByRole,
  fireEvent,
  getAllByLabelText,
  getByRole,
  queryByRole,
  waitFor,
} from "@testing-library/dom";
import { onTestFinished, test, vi } from "vitest";
import type { GameResponse } from "../src/api";
import {
  appServerResponse,
  gameResponseFixture,
  renderApp as renderApplication,
} from "./testSupport";

function unsetGame(
  aiLevel: number | null = null,
  humanColor: "B" | "W" | null = null,
): GameResponse {
  return gameResponseFixture(
    "imported-game",
    "読み込んだ対局",
    {
      currentPlayer: "W",
      moves: [{ color: "B", coordinate: "C3" }],
      stones: { C3: "B" },
      treeNodes: [
        { id: "root" },
        { id: "n1", parentId: "root", move: { color: "B", coordinate: "C3" } },
      ],
      currentNodeId: "n1",
      mainLineNodeId: "n1",
    },
    {
      aiLevel,
      humanColor,
      startedAt: "2026-11-14T00:00:00Z",
    },
  );
}

async function renderApp(state: GameResponse, mode: "play" | "analysis") {
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  localStorage.setItem(
    "go-practice.workspace-mode",
    JSON.stringify({ schemaVersion: "1", mode }),
  );
  const requests: Array<{ path: string; method: string; body: string }> = [];
  let current = state;
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      const method = init?.method ?? "GET";
      requests.push({ path, method, body: String(init?.body ?? "") });
      if (path.endsWith("/settings") && method === "PATCH") {
        const settings = JSON.parse(String(init?.body));
        current = { ...current, ...settings };
        return Response.json(current);
      }
      if (path.endsWith("/ai-moves") && method === "DELETE")
        return new Response(null, { status: 204 });
      if (path.endsWith("/ai-moves")) return new Promise<Response>(() => {});
      const common = appServerResponse(path, method, [current]);
      if (common) return common;
      return Response.json(current);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  await renderApplication({ notifications: false });
  return { requests, body: document.body };
}

async function choose(dialog: HTMLElement, label: string, option: string) {
  const input = getAllByLabelText(dialog, label).find(
    (element) => element instanceof HTMLInputElement,
  );
  assert.ok(input);
  fireEvent.click(input);
  fireEvent.click(await findByRole(dialog, "option", { name: option }));
}

test("対局モードで設定が未設定の対局を開くと設定のModalを出す", async () => {
  const { body } = await renderApp(unsetGame(), "play");
  const dialog = await findByRole(body, "dialog", { name: "対局の設定" });
  const cancel = getByRole(dialog, "button", { name: "キャンセル" });
  cancel.focus();
  fireEvent.keyDown(cancel, { key: "?" });
  assert.equal(
    queryByRole(body, "dialog", { name: "キーボードショートカット" }),
    null,
  );
});

test("AIレベルだけが未設定の対局でも設定のModalを出す", async () => {
  const { body } = await renderApp(unsetGame(null, "B"), "play");
  await findByRole(body, "dialog", { name: "対局の設定" });
});

test("あなたの石の色だけが未設定の対局でも設定のModalを出す", async () => {
  const { body } = await renderApp(unsetGame(2, null), "play");
  await findByRole(body, "dialog", { name: "対局の設定" });
});

test("未設定の対局は設定してから対局モードに切り替える", async () => {
  const { requests, body } = await renderApp(unsetGame(), "analysis");
  const playMode = await findByRole(body, "radio", { name: "対局" });
  fireEvent.click(playMode);
  const dialog = await findByRole(body, "dialog", { name: "対局の設定" });
  assert.equal(
    (getByRole(body, "radio", { name: "分析" }) as HTMLInputElement).checked,
    true,
  );

  await choose(dialog, "あなた", "黒");
  await choose(dialog, "AIレベル", "レベル 2");
  fireEvent.click(getByRole(dialog, "button", { name: "変更する" }));

  await waitFor(() =>
    assert.equal(
      (getByRole(body, "radio", { name: "対局" }) as HTMLInputElement).checked,
      true,
    ),
  );
  const saved = requests.find((request) => request.path.endsWith("/settings"));
  assert.deepEqual(JSON.parse(saved?.body ?? "null"), {
    aiLevel: 2,
    humanColor: "B",
  });
  await waitFor(() =>
    assert.ok(requests.some((request) => request.path.endsWith("/ai-moves"))),
  );
});
