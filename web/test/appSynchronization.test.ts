import assert from "node:assert/strict";
import { getAllByRole, getByRole } from "@testing-library/dom";
import { onTestFinished, test, vi } from "vitest";
import type { GameResponse, GameTreeNode } from "../src/api";
import {
  analysisResponseFixture,
  appServerResponse,
  deferred,
  gameResponseFixture,
  jsonResponse,
  renderApp,
  waitForCondition as waitFor,
} from "./testSupport";

function responseAt(
  currentNodeId: "root" | "n1" | "n2",
  mainLineNodeId = currentNodeId,
): GameResponse {
  const nodes: GameTreeNode[] = [
    { id: "root" },
    {
      id: "n1",
      parentId: "root",
      move: { color: "B", coordinate: "E5" },
    },
    {
      id: "n2",
      parentId: "n1",
      move: { color: "W", coordinate: "D5" },
    },
  ];
  const length = currentNodeId === "root" ? 1 : currentNodeId === "n1" ? 2 : 3;
  return gameResponseFixture(
    "synchronization-game",
    "同期の確認",
    {
      boardSize: 9,
      currentPlayer: currentNodeId === "n1" ? "W" : "B",
      moves: nodes
        .slice(1, length)
        .flatMap((node) => (node.move ? [node.move] : [])),
      stones:
        currentNodeId === "root"
          ? {}
          : currentNodeId === "n1"
            ? { E5: "B" }
            : { E5: "B", D5: "W" },
      treeNodes: nodes,
      currentNodeId,
      mainLineNodeId,
    },
    {
      startedAt: "2026-11-14T00:00:00Z",
      canUndo: currentNodeId === "n2",
    },
  );
}

test("古い同期応答を捨てて最新の局面を表示し共有する", async () => {
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: "synchronization-game" }),
  );
  localStorage.setItem(
    "go-practice.workspace-mode",
    JSON.stringify({ schemaVersion: "1", mode: "analysis" }),
  );
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  onTestFinished(() => {
    vi.useRealTimers();
  });

  let serverState = responseAt("n1");
  let pendingPoll: ReturnType<typeof deferred<Response>> | null = null;
  let deferNextGameRead = false;
  const sharedNodeIDs: string[] = [];
  Object.defineProperty(globalThis, "fetch", {
    configurable: true,
    value: async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/agent/selection" && init?.method === "POST") {
        sharedNodeIDs.push(JSON.parse(String(init.body)).nodeId);
        return new Response(null, { status: 204 });
      }
      if (path.endsWith("/nodes/root") && init?.method === "POST") {
        serverState = responseAt("root");
        return jsonResponse(serverState);
      }
      if (
        path.endsWith("/api/games/synchronization-game") &&
        !init?.method &&
        deferNextGameRead
      ) {
        deferNextGameRead = false;
        pendingPoll = deferred<Response>();
        return pendingPoll.promise;
      }
      const common = appServerResponse(path, init?.method ?? "GET", [
        serverState,
      ]);
      if (common) return common;
      return jsonResponse(serverState);
    },
  });

  const { container, act } = await renderApp({ notifications: false });
  await waitFor(() => sharedNodeIDs.includes("n1"));

  deferNextGameRead = true;
  await act(async () => vi.advanceTimersByTimeAsync(1500));
  await waitFor(() => pendingPoll !== null);
  const rootNode = getByRole<HTMLButtonElement>(container, "button", {
    name: "開始局面",
  });
  await act(async () => rootNode.click());
  await waitFor(() => sharedNodeIDs.at(-1) === "root");

  await act(async () => pendingPoll?.resolve(jsonResponse(responseAt("n1"))));
  assert.equal(rootNode.getAttribute("aria-current"), "true");

  serverState = responseAt("n2");
  await act(async () => vi.advanceTimersByTimeAsync(1500));
  await waitFor(() => sharedNodeIDs.at(-1) === "n2");
  const currentNode = getAllByRole<HTMLButtonElement>(container, "button").find(
    (button) => button.getAttribute("aria-current") === "true",
  );
  assert.ok(currentNode);
  assert.equal(currentNode.disabled, false);
});

test("同期で局面が移動したときは分析へ切り替えてAIへ着手を求めない", async () => {
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: "synchronization-game" }),
  );
  localStorage.setItem(
    "go-practice.workspace-mode",
    JSON.stringify({ schemaVersion: "1", mode: "play" }),
  );
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  onTestFinished(() => {
    vi.useRealTimers();
  });

  let polling = false;
  let aiMoveRequests = 0;
  Object.defineProperty(globalThis, "fetch", {
    configurable: true,
    value: async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/ai-moves") && init?.method === "POST") {
        aiMoveRequests++;
        return jsonResponse({ game: responseAt("n1", "n2") });
      }
      if (path.includes("/analysis"))
        return jsonResponse(analysisResponseFixture(polling ? "W" : "B", 9));
      if (path === `/api/games/${responseAt("n2").game.id}`)
        return jsonResponse(
          polling ? responseAt("n1", "n2") : responseAt("n2"),
        );
      const common = appServerResponse(path, init?.method ?? "GET", [
        responseAt("n2"),
      ]);
      if (common) return common;
      return jsonResponse(responseAt("n2"));
    },
  });

  const { container, act } = await renderApp({ notifications: false });
  await waitFor(
    () =>
      (getByRole(container, "radio", { name: "対局" }) as HTMLInputElement)
        .checked,
  );

  polling = true;
  await act(async () => vi.advanceTimersByTimeAsync(1500));
  await waitFor(
    () =>
      (getByRole(container, "radio", { name: "分析" }) as HTMLInputElement)
        .checked,
  );
  assert.equal(aiMoveRequests, 0);
});
