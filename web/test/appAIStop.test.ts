import assert from "node:assert/strict";
import {
  findByLabelText,
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

test("開いている対局の削除では、削除の確認を出す前にAIの思考を中止する", async () => {
  const state = gameResponseFixture(
    "ai-turn-game",
    "思考中の対局",
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
    { startedAt: "2026-11-14T00:00:00Z", canUndo: true },
  );
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  let aiSignal: AbortSignal | undefined;
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      const common = appServerResponse(path, init?.method ?? "GET", [state]);
      if (common) return common;
      if (path.endsWith("/ai-moves")) {
        if (init?.method === "DELETE")
          return new Response(null, { status: 204 });
        aiSignal = init?.signal ?? undefined;
        return new Promise<Response>((_, reject) =>
          init?.signal?.addEventListener("abort", () =>
            reject(new DOMException("中止", "AbortError")),
          ),
        );
      }
      return Response.json(state);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  await renderApp({ notifications: false });

  fireEvent.click(
    await findByRole(document.body, "button", { name: "AIの思考を再開" }),
  );
  await findByRole(document.body, "button", { name: "中止" });
  assert.equal(aiSignal?.aborted, false);

  fireEvent.click(
    getByRole(document.body, "button", { name: "思考中の対局を削除" }),
  );
  await findByRole(document.body, "dialog", { name: "対局を削除" });
  assert.equal(aiSignal?.aborted, true);
});

test("AIの手番の棋譜を読み込んだ後は、ユーザーが再開するまでAIの手を始めない", async () => {
  const imported = gameResponseFixture(
    "imported-ai-turn",
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
    { startedAt: "2026-11-14T00:00:00Z", canUndo: true },
  );
  const opened: GameResponse = {
    ...imported,
    game: {
      ...imported.game,
      id: "opened-before-import",
      currentPlayer: "B",
      moves: [],
      stones: {},
      treeNodes: [{ id: "root" }],
      currentNodeId: "root",
      mainLineNodeId: "root",
    },
    name: "読み込み前の対局",
  };
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: opened.game.id }),
  );
  let aiRequested = false;
  const requests: Array<{ path: string; method: string }> = [];
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      requests.push({ path, method: init?.method ?? "GET" });
      const common = appServerResponse(path, init?.method ?? "GET", [opened]);
      if (common) return common;
      if (path === "/api/games/import-json" && init?.method === "POST")
        return Response.json(imported, { status: 201 });
      if (path.endsWith("/ai-moves")) {
        if (init?.method === "DELETE")
          return new Response(null, { status: 204 });
        aiRequested = true;
        return new Promise<Response>(() => {});
      }
      return Response.json(imported);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  const { act } = await renderApp({ notifications: false });

  const input = await findByLabelText<HTMLInputElement>(
    document.body,
    "JSONファイル",
  );
  const file = new File(["{}"], "game.json", { type: "application/json" });
  await act(async () => fireEvent.change(input, { target: { files: [file] } }));
  await findByRole(document.body, "button", { name: "AIの思考を再開" });
  await new Promise((resolve) => setTimeout(resolve, 50));
  assert.equal(aiRequested, false);
  const stopIndex = requests.findIndex(
    ({ path, method }) =>
      path === `/api/games/${opened.game.id}/ai-moves` && method === "DELETE",
  );
  const importIndex = requests.findIndex(
    ({ path, method }) =>
      path === "/api/games/import-json" && method === "POST",
  );
  assert.ok(stopIndex >= 0);
  assert.ok(importIndex > stopIndex);
});

test("AIの思考中の待ったに成功すると次のAIの手を始める", async () => {
  const state = gameResponseFixture(
    "idle-ai-game",
    "待った後のAI",
    {
      moves: [
        { color: "B", coordinate: "C3" },
        { color: "W", coordinate: "D3" },
      ],
      stones: { C3: "B", D3: "W" },
      treeNodes: [
        { id: "root" },
        { id: "n1", parentId: "root", move: { color: "B", coordinate: "C3" } },
        { id: "n2", parentId: "n1", move: { color: "W", coordinate: "D3" } },
      ],
      currentNodeId: "n2",
      mainLineNodeId: "n2",
    },
    { canUndo: true },
  );
  const aiTurn: GameResponse = {
    ...state,
    game: {
      ...state.game,
      currentPlayer: "W",
      moves: [...state.game.moves, { color: "B", coordinate: "pass" }],
      treeNodes: [
        ...state.game.treeNodes,
        { id: "n3", parentId: "n2", move: { color: "B", coordinate: "pass" } },
      ],
      currentNodeId: "n3",
      mainLineNodeId: "n3",
    },
  };
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: aiTurn.game.id }),
  );
  let aiRequests = 0;
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      const common = appServerResponse(path, init?.method ?? "GET", [aiTurn]);
      if (common) return common;
      if (path.endsWith("/undo")) return Response.json(state);
      if (path.endsWith("/moves") && init?.method === "POST")
        return Response.json({ game: aiTurn });
      if (path.endsWith("/ai-moves")) {
        if (init?.method === "DELETE")
          return new Response(null, { status: 204 });
        aiRequests++;
        return new Promise<Response>((_, reject) =>
          init?.signal?.addEventListener("abort", () =>
            reject(new DOMException("中止", "AbortError")),
          ),
        );
      }
      return Response.json(aiTurn);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  const { container, act } = await renderApp({ notifications: false });
  await act(async () =>
    fireEvent.click(
      await findByRole(container, "button", { name: "AIの思考を再開" }),
    ),
  );
  await waitFor(() => assert.equal(aiRequests, 1));
  await act(async () =>
    fireEvent.click(await findByRole(container, "button", { name: "待った" })),
  );
  const pass = await findByRole(container, "button", { name: "パス" });
  await waitFor(() => assert.equal(pass.hasAttribute("disabled"), false));
  await act(async () => fireEvent.click(pass));
  await waitFor(() => assert.equal(aiRequests, 2));
});

test("主分岐の末端で対局モードへ戻すとAIの手を再開する", async () => {
  const state = gameResponseFixture(
    "resume-on-play-mode",
    "モードで再開する対局",
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
    { canUndo: true },
  );
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  let aiRequests = 0;
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      const common = appServerResponse(path, init?.method ?? "GET", [state]);
      if (common) return common;
      if (path.endsWith("/ai-moves")) {
        if (init?.method === "DELETE")
          return new Response(null, { status: 204 });
        aiRequests++;
        return new Promise<Response>(() => {});
      }
      return Response.json(state);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  const { container, act } = await renderApp({ notifications: false });

  await findByRole(container, "button", { name: "AIの思考を再開" });
  await act(async () =>
    fireEvent.click(await findByRole(container, "radio", { name: "分析" })),
  );
  await act(async () =>
    fireEvent.click(await findByRole(container, "radio", { name: "対局" })),
  );

  await waitFor(() => assert.equal(aiRequests, 1));
  await findByRole(container, "button", { name: "中止" });
});

test("読み込みに失敗しても、止まっていなかったAIの手を次の人の操作後に始める", async () => {
  const state = gameResponseFixture(
    "failed-import-does-not-pause",
    "読み込み失敗後の対局",
    {
      moves: [
        { color: "B", coordinate: "C3" },
        { color: "W", coordinate: "D3" },
      ],
      stones: { C3: "B", D3: "W" },
      treeNodes: [
        { id: "root" },
        { id: "n1", parentId: "root", move: { color: "B", coordinate: "C3" } },
        { id: "n2", parentId: "n1", move: { color: "W", coordinate: "D3" } },
      ],
      currentNodeId: "n2",
      mainLineNodeId: "n2",
    },
    { canUndo: true },
  );
  const afterPass: GameResponse = {
    ...state,
    game: {
      ...state.game,
      currentPlayer: "W",
      moves: [...state.game.moves, { color: "B", coordinate: "pass" }],
      treeNodes: [
        ...state.game.treeNodes,
        { id: "n3", parentId: "n2", move: { color: "B", coordinate: "pass" } },
      ],
      currentNodeId: "n3",
      mainLineNodeId: "n3",
    },
  };
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  let aiRequests = 0;
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      const common = appServerResponse(path, init?.method ?? "GET", [state]);
      if (common) return common;
      if (path === "/api/games/import" && init?.method === "POST")
        return Response.json(
          { error: "SGFを読み込めません。" },
          { status: 400 },
        );
      if (path.endsWith("/moves") && init?.method === "POST")
        return Response.json({ game: afterPass });
      if (path.endsWith("/ai-moves")) {
        if (init?.method === "DELETE")
          return new Response(null, { status: 204 });
        aiRequests++;
        return new Promise<Response>(() => {});
      }
      return Response.json(state);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  const { container, act } = await renderApp({ notifications: false });

  const input = await findByLabelText<HTMLInputElement>(
    container,
    "SGFファイル",
  );
  await act(async () =>
    fireEvent.change(input, {
      target: { files: [new File(["invalid"], "invalid.sgf")] },
    }),
  );
  await act(async () =>
    fireEvent.click(await findByRole(container, "button", { name: "パス" })),
  );

  await waitFor(() => assert.equal(aiRequests, 1));
});

test("対局の作成、SGFの読み込み、別の対局を開く前にAIの手を中止する", async () => {
  const initial = gameResponseFixture("initial-game", "最初の対局");
  const another = gameResponseFixture("another-game", "別の対局");
  const created = gameResponseFixture("created-game", "作成した対局");
  const imported = gameResponseFixture("imported-game", "読み込んだ対局");
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: initial.game.id }),
  );
  const requests: Array<{ path: string; method: string }> = [];
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      const method = init?.method ?? "GET";
      requests.push({ path, method });
      const common = appServerResponse(path, method, [initial, another]);
      if (common) return common;
      if (path === "/api/games" && method === "POST")
        return Response.json(created, { status: 201 });
      if (path === "/api/games/import" && method === "POST")
        return Response.json(imported, { status: 201 });
      if (path.endsWith("/ai-moves") && method === "DELETE")
        return new Response(null, { status: 204 });
      return Response.json(initial);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  const { container, act } = await renderApp({ notifications: false });

  await findByRole(container, "button", {
    name: "最初の対局",
    current: true,
  });
  await act(async () =>
    fireEvent.click(
      await findByRole(container, "button", { name: "新しい対局" }),
    ),
  );
  await act(async () =>
    fireEvent.click(
      await findByRole(document.body, "button", { name: "この条件で開始" }),
    ),
  );
  await findByRole(container, "button", {
    name: "作成した対局",
    current: true,
  });

  const sgf = await findByLabelText<HTMLInputElement>(container, "SGFファイル");
  await act(async () =>
    fireEvent.change(sgf, {
      target: { files: [new File(["(;FF[4]SZ[5])"], "game.sgf")] },
    }),
  );
  await findByRole(container, "button", {
    name: "読み込んだ対局",
    current: true,
  });

  await act(async () =>
    fireEvent.click(
      await findByRole(container, "button", { name: "別の対局" }),
    ),
  );
  await findByRole(container, "button", { name: "別の対局", current: true });

  for (const operation of [
    { path: "/api/games", method: "POST" },
    { path: "/api/games/import", method: "POST" },
    { path: `/api/games/${another.game.id}`, method: "GET" },
  ]) {
    const operationIndex = requests.findIndex(
      (entry) =>
        entry.path === operation.path && entry.method === operation.method,
    );
    assert.ok(operationIndex > 0);
    const cancellation = requests[operationIndex - 1];
    assert.equal(cancellation.method, "DELETE");
    assert.match(cancellation.path, /\/ai-moves$/);
  }
});

test("過去の局面へ移動してから対局モードへ戻すとAIの手を始める", async () => {
  const latest = gameResponseFixture(
    "paused-after-navigation",
    "移動後の中止",
    {
      currentPlayer: "W",
      moves: [
        { color: "B", coordinate: "C3" },
        { color: "W", coordinate: "D3" },
        { color: "B", coordinate: "C2" },
      ],
      stones: { C3: "B", D3: "W", C2: "B" },
      treeNodes: [
        { id: "root" },
        { id: "n1", parentId: "root", move: { color: "B", coordinate: "C3" } },
        { id: "n2", parentId: "n1", move: { color: "W", coordinate: "D3" } },
        { id: "n3", parentId: "n2", move: { color: "B", coordinate: "C2" } },
      ],
      currentNodeId: "n3",
      mainLineNodeId: "n3",
    },
    { canUndo: true },
  );
  const past: GameResponse = {
    ...latest,
    game: {
      ...latest.game,
      currentPlayer: "W",
      moves: latest.game.moves.slice(0, 1),
      stones: { C3: "B" },
      currentNodeId: "n1",
    },
  };
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: latest.game.id }),
  );
  let aiRequests = 0;
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      const common = appServerResponse(path, init?.method ?? "GET", [latest]);
      if (common) return common;
      if (path.endsWith("/nodes/n1") && init?.method === "POST")
        return Response.json(past);
      if (path.endsWith("/ai-moves")) {
        if (init?.method === "DELETE")
          return new Response(null, { status: 204 });
        aiRequests++;
        return new Promise<Response>((_, reject) =>
          init?.signal?.addEventListener("abort", () =>
            reject(new DOMException("中止", "AbortError")),
          ),
        );
      }
      return Response.json(latest);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  const { container } = await renderApp({ notifications: false });

  fireEvent.click(
    await findByRole(container, "button", { name: "AIの思考を再開" }),
  );
  await waitFor(() => assert.equal(aiRequests, 1));
  fireEvent.click(
    await findByRole(container, "button", { name: "1手目 黒C3" }),
  );
  await waitFor(() =>
    assert.equal(
      (getByRole(container, "radio", { name: "分析" }) as HTMLInputElement)
        .checked,
      true,
    ),
  );
  fireEvent.click(getByRole(container, "radio", { name: "対局" }));
  await waitFor(() => assert.equal(aiRequests, 2));
  await findByRole(container, "button", { name: "中止" });
});

test("分析モードへの切り替えはAIの中止を待つ間も分析操作として扱う", async () => {
  const state = gameResponseFixture(
    "analysis-while-stopping",
    "分析へ切り替える対局",
    {
      moves: [
        { color: "B", coordinate: "C3" },
        { color: "W", coordinate: "D3" },
      ],
      stones: { C3: "B", D3: "W" },
      treeNodes: [
        { id: "root" },
        { id: "n1", parentId: "root", move: { color: "B", coordinate: "C3" } },
        { id: "n2", parentId: "n1", move: { color: "W", coordinate: "D3" } },
      ],
      currentNodeId: "n2",
      mainLineNodeId: "n2",
    },
    { humanColor: "W" },
  );
  const variation = {
    ...state,
    game: {
      ...state.game,
      currentPlayer: "W" as const,
      moves: [...state.game.moves, { color: "B" as const, coordinate: "pass" }],
      treeNodes: [
        ...state.game.treeNodes,
        {
          id: "n3",
          parentId: "n2",
          move: { color: "B" as const, coordinate: "pass" },
        },
      ],
      currentNodeId: "n3",
    },
  };
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: state.game.id }),
  );
  let releaseCancellation = () => {};
  let cancellationRequests = 0;
  const requests: Array<{ path: string; method: string }> = [];
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      const method = init?.method ?? "GET";
      requests.push({ path, method });
      const common = appServerResponse(path, method, [state]);
      if (common) return common;
      if (path.endsWith("/ai-moves") && method === "DELETE") {
        cancellationRequests++;
        await new Promise<void>((resolve) => {
          releaseCancellation = resolve;
        });
        return new Response(null, { status: 204 });
      }
      if (path.endsWith("/ai-moves") && method === "POST")
        return new Promise<Response>((_, reject) =>
          init?.signal?.addEventListener("abort", () =>
            reject(new DOMException("中止", "AbortError")),
          ),
        );
      if (path.endsWith("/variations") && method === "POST")
        return Response.json(variation);
      return Response.json(state);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  const { container, act } = await renderApp({ notifications: false });

  await act(async () =>
    fireEvent.click(
      await findByRole(container, "button", { name: "AIの思考を再開" }),
    ),
  );
  await waitFor(() =>
    assert.ok(
      requests.some(
        ({ path, method }) => path.endsWith("/ai-moves") && method === "POST",
      ),
    ),
  );

  await act(async () =>
    fireEvent.click(await findByRole(container, "radio", { name: "分析" })),
  );
  await waitFor(() => assert.equal(cancellationRequests, 1));
  assert.equal(
    (getByRole(container, "radio", { name: "分析" }) as HTMLInputElement)
      .checked,
    true,
  );
  await act(async () =>
    fireEvent.click(await findByRole(container, "button", { name: "パス" })),
  );
  await waitFor(() =>
    assert.ok(requests.some(({ path }) => path.endsWith("/variations"))),
  );
  assert.equal(
    requests.some(({ path }) => path.endsWith("/moves")),
    false,
  );
  await act(async () => releaseCancellation());
});

test("別の対局を開く操作は中止の応答を待ち、中止に失敗しても続ける", async () => {
  const initial = gameResponseFixture("stop-before-open", "開いている対局");
  const another = gameResponseFixture("open-after-stop", "次の対局");
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: initial.game.id }),
  );
  let releaseCancellation = () => {};
  let openRequests = 0;
  vi.stubGlobal(
    "fetch",
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      const method = init?.method ?? "GET";
      const common = appServerResponse(path, method, [initial, another]);
      if (common && path !== `/api/games/${another.game.id}`) return common;
      if (path.endsWith("/ai-moves") && method === "DELETE") {
        await new Promise<void>((resolve) => {
          releaseCancellation = resolve;
        });
        return Response.json(
          { error: "AIの思考を中止できませんでした。" },
          { status: 500 },
        );
      }
      if (path === `/api/games/${another.game.id}`) {
        openRequests++;
        return Response.json(another);
      }
      return Response.json(initial);
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });
  const { container, act } = await renderApp({ notifications: false });

  const open = await findByRole(container, "button", { name: "次の対局" });
  await act(async () => fireEvent.click(open));
  await waitFor(() =>
    assert.equal(
      getByRole(container, "button", { name: "新しい対局" }).hasAttribute(
        "disabled",
      ),
      true,
    ),
  );
  assert.equal(openRequests, 0);
  await act(async () => releaseCancellation());
  await waitFor(() => assert.equal(openRequests, 1));
  await findByRole(container, "button", { name: "次の対局", current: true });
});
