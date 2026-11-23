import { onTestFinished } from "vitest";
import type { GameResponse } from "../src/api";

export function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

export function deferred<Value>() {
  let resolve!: (value: Value) => void;
  const promise = new Promise<Value>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

export async function waitForCondition(
  condition: () => boolean,
  message = "期待した画面状態を確認できませんでした",
) {
  const deadline = Date.now() + 2000;
  while (!condition()) {
    if (Date.now() > deadline) throw new Error(message);
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
}

export function gameResponseFixture(
  id: string,
  name: string,
  game: Partial<GameResponse["game"]> = {},
  response: Partial<Omit<GameResponse, "game">> = {},
): GameResponse {
  return {
    game: {
      id,
      boardSize: 5,
      komi: 7.5,
      handicap: 0,
      setupStones: {},
      initialPlayer: "B",
      currentPlayer: "B",
      status: "playing",
      result: null,
      moves: [],
      stones: {},
      captures: { B: 0, W: 0 },
      treeNodes: [{ id: "root" }],
      currentNodeId: "root",
      mainLineNodeId: "root",
      ...game,
    },
    aiLevel: 1,
    humanColor: "B",
    startedAt: "2026-11-22T00:00:00Z",
    name,
    canUndo: false,
    ...response,
  };
}

export function analysisResponseFixture(
  currentPlayer: "B" | "W",
  boardSize = 5,
) {
  return {
    currentPlayer,
    scoreLead: 0,
    winrate: 0.5,
    visits: 100,
    ownership: Array(boardSize * boardSize).fill(0),
    candidates: [],
  };
}

export function appServerResponse(
  path: string,
  method: string,
  games: GameResponse[],
): Response | undefined {
  if (path === "/api/storage-errors") return Response.json([]);
  if (path === "/api/games" && method === "GET") return Response.json(games);
  if (path === "/api/agent/selection" || path === "/api/ui/interaction")
    return new Response(null, { status: 204 });
  if (method === "GET") {
    const game = games.find(
      (candidate) => path === `/api/games/${candidate.game.id}`,
    );
    if (game) return Response.json(game);
    if (path.includes("/analysis")) {
      const analyzed =
        games.find((candidate) =>
          path.startsWith(`/api/games/${candidate.game.id}/`),
        ) ?? games[0];
      if (analyzed)
        return Response.json(
          analysisResponseFixture(
            analyzed.game.currentPlayer,
            analyzed.game.boardSize,
          ),
        );
    }
  }
  return undefined;
}

export async function renderApp(options: { notifications?: boolean } = {}) {
  const notificationModule =
    options.notifications === false
      ? null
      : await import("@mantine/notifications");
  const [{ MantineProvider }, { act, createElement }, { createRoot }, { App }] =
    await Promise.all([
      import("@mantine/core"),
      import("react"),
      import("react-dom/client"),
      import("../src/App"),
    ]);
  const container = document.getElementById("root");
  if (!container) throw new Error("描画先がありません");
  notificationModule?.notifications.clean();
  const root = createRoot(container);
  onTestFinished(() => {
    act(() => root.unmount());
    notificationModule?.notifications.clean();
  });
  await act(async () => {
    const app = createElement(App);
    root.render(
      notificationModule
        ? createElement(
            MantineProvider,
            { env: "test", forceColorScheme: "light" },
            createElement(notificationModule.Notifications),
            app,
          )
        : createElement(
            MantineProvider,
            { env: "test", forceColorScheme: "light" },
            app,
          ),
    );
  });
  return { container, act };
}
