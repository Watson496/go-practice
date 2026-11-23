import { isAbortError } from "./errors";

export type Color = "B" | "W";

export type Move = {
  color: Color;
  coordinate: string;
};

type PointResult = {
  type: "points";
  cause: "two_passes" | "recorded";
} & ({ winner: Color; margin: number } | { margin?: 0 }) &
  (
    | { scoreKnown: true; blackScore: number; whiteScore: number }
    | { scoreKnown: false; blackScore?: never; whiteScore?: never }
  );

type GameResult = (
  | PointResult
  | {
      type: "resignation";
      cause: "resignation" | "recorded";
      winner: Color;
      loser: Color;
      scoreKnown: false;
    }
  | {
      type: "recorded";
      cause: "recorded";
      value: string;
      scoreKnown: false;
    }
) & { deadStones?: string[] };

export type GameTreeNode = {
  id: string;
  parentId?: string;
  move?: Move;
  result?: Game["result"];
  properties?: Array<{ ident: string; values: string[] }>;
  important?: boolean;
  origin?: "agent";
  variationTitle?: string;
  highlighted?: boolean;
};

export type Game = {
  id: string;
  boardSize: number;
  komi: number;
  handicap: number;
  setupStones: Record<string, Color>;
  initialPlayer: Color;
  currentPlayer: Color;
  status: "playing" | "finished";
  moves: Move[];
  stones: Record<string, Color>;
  captures: Record<Color, number>;
  result: GameResult | null;
  treeNodes: GameTreeNode[];
  currentNodeId: string;
  mainLineNodeId: string;
  rootProperties?: Array<{ ident: string; values: string[] }>;
};

export type GameResponse = {
  game: Game;
  // AIのレベルと人の石の色は、読み込んだ対局などでは未設定（null）になる
  aiLevel: number | null;
  humanColor: Color | null;
  // 開始日時が不明の対局は、空の値になる
  startedAt: string;
  name?: string;
  canUndo: boolean;
};
type MoveResponse = { game: GameResponse };
export type PositionAnalysis = {
  currentPlayer: Color;
  scoreLead: number;
  winrate: number;
  visits: number;
  ownership?: number[];
  candidates: Array<{
    move: string;
    visits: number;
    scoreLead: number;
    winrate: number;
    pv: string[];
  }>;
};
type StorageLoadError = { file: string; reason: string };

export class APIError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

function normalizeStorageErrors(value: unknown): StorageLoadError[] {
  if (
    !Array.isArray(value) ||
    !value.every(
      (entry): entry is StorageLoadError =>
        typeof entry === "object" &&
        entry !== null &&
        Object.keys(entry).length === 2 &&
        "file" in entry &&
        typeof entry.file === "string" &&
        "reason" in entry &&
        typeof entry.reason === "string",
    )
  ) {
    throw new Error("保存ファイルのエラー一覧の形式が不正です。");
  }
  return value;
}

async function requestResponse(
  path: string,
  init?: RequestInit,
): Promise<Response> {
  try {
    return await fetch(path, init);
  } catch (cause) {
    if (isAbortError(cause)) throw cause;
    throw new Error("サーバーとの通信に失敗しました。", { cause });
  }
}

async function readResponseBody<Value>(
  read: () => Promise<Value>,
): Promise<Value> {
  try {
    return await read();
  } catch (cause) {
    if (isAbortError(cause)) throw cause;
    throw new Error("サーバーの応答を読み取れませんでした。", { cause });
  }
}

function responseErrorMessage(body: unknown, fallback: string): string {
  return typeof body === "object" &&
    body !== null &&
    "error" in body &&
    typeof body.error === "string"
    ? body.error
    : fallback;
}

function httpFailureMessage(status: number): string {
  return `要求に失敗しました（HTTP ${status}）`;
}

async function responseError(
  response: Response,
  fallback: string,
): Promise<APIError> {
  const { body } = await readJSONResponse(response);
  return new APIError(responseErrorMessage(body, fallback), response.status);
}

async function readJSONResponse(
  response: Response,
): Promise<{ body: unknown; valid: boolean }> {
  const text = await readResponseBody(() => response.text());
  try {
    return { body: JSON.parse(text), valid: true };
  } catch {
    return { body: null, valid: false };
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await requestResponse(path, init);
  if (!response.ok) {
    throw await responseError(response, httpFailureMessage(response.status));
  }
  const { body, valid } = await readJSONResponse(response);
  if (!valid) {
    throw new Error("サーバーの応答を読み取れませんでした。");
  }
  return body as T;
}

export function createGame(
  boardSize: number,
  aiLevel: number,
  handicap: number,
  humanColor: Color,
  komi: number,
): Promise<GameResponse> {
  return request<GameResponse>("/api/games", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ boardSize, aiLevel, handicap, humanColor, komi }),
  });
}

export function importGameSGF(contents: BodyInit): Promise<GameResponse> {
  return request<GameResponse>("/api/games/import", {
    method: "POST",
    headers: { "Content-Type": "application/x-go-sgf; charset=utf-8" },
    body: contents,
  });
}

export function importGameJSON(contents: BodyInit): Promise<GameResponse> {
  return request<GameResponse>("/api/games/import-json", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: contents,
  });
}

export function getGame(
  id: string,
  signal?: AbortSignal,
): Promise<GameResponse> {
  return request<GameResponse>(`/api/games/${id}`, { signal });
}

export function listGames(): Promise<GameResponse[]> {
  return request<GameResponse[]>("/api/games");
}

export function reorderGames(ids: string[]): Promise<GameResponse[]> {
  return request<GameResponse[]>("/api/games/order", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ids }),
  });
}

export type UIInteraction = {
  token: string;
  kind: "review" | "rename" | "reorder" | "delete-game" | "delete-variation";
  gameId?: string;
  nodeId?: string;
  nodeIds?: string[];
};

export async function beginUIInteraction(
  interaction: UIInteraction,
): Promise<void> {
  const response = await requestResponse("/api/ui/interaction", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(interaction),
  });
  if (!response.ok) {
    throw await responseError(response, httpFailureMessage(response.status));
  }
}

export async function endUIInteraction(token: string): Promise<void> {
  const response = await requestResponse("/api/ui/interaction", {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token }),
  });
  if (!response.ok) {
    throw await responseError(response, httpFailureMessage(response.status));
  }
}

export async function cancelAIMoves(id: string): Promise<void> {
  const response = await requestResponse(`/api/games/${id}/ai-moves`, {
    method: "DELETE",
  });
  if (!response.ok) {
    throw await responseError(response, httpFailureMessage(response.status));
  }
}

export async function listStorageErrors(): Promise<StorageLoadError[]> {
  return normalizeStorageErrors(await request<unknown>("/api/storage-errors"));
}

export async function deleteGame(id: string): Promise<void> {
  await request<unknown>(`/api/games/${id}`, {
    method: "DELETE",
  });
}

export function renameGame(
  id: string,
  name: string,
  expectedName: string,
): Promise<GameResponse> {
  return request<GameResponse>(`/api/games/${id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, expectedName }),
  });
}

export function updateGameSettings(
  id: string,
  settings: { aiLevel: number; humanColor: Color },
): Promise<GameResponse> {
  return request<GameResponse>(`/api/games/${id}/settings`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(settings),
  });
}

export function playMove(
  id: string,
  coordinate: string,
): Promise<MoveResponse> {
  return request<MoveResponse>(`/api/games/${id}/moves`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ coordinate }),
  });
}

export function playAIMove(
  id: string,
  signal?: AbortSignal,
): Promise<MoveResponse> {
  return request<MoveResponse>(`/api/games/${id}/ai-moves`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({}),
    signal,
  });
}

export function playVariation(
  id: string,
  coordinate: string,
): Promise<GameResponse> {
  return request<GameResponse>(`/api/games/${id}/variations`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ coordinate }),
  });
}

export function resignGame(id: string): Promise<GameResponse> {
  return request<GameResponse>(`/api/games/${id}/resign`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({}),
  });
}

export function undoMove(id: string): Promise<GameResponse> {
  return request<GameResponse>(`/api/games/${id}/undo`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({}),
  });
}

export function navigateGameTree(
  id: string,
  nodeId: string,
): Promise<GameResponse> {
  return request<GameResponse>(
    `/api/games/${id}/nodes/${encodeURIComponent(nodeId)}`,
    {
      method: "POST",
    },
  );
}

export function deleteGameTreeVariation(
  id: string,
  nodeId: string,
  expectedNodeIds: string[],
): Promise<GameResponse> {
  return request<GameResponse>(
    `/api/games/${id}/nodes/${encodeURIComponent(nodeId)}`,
    {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ expectedNodeIds }),
    },
  );
}

export function promoteGameTreeVariation(
  id: string,
  nodeId: string,
): Promise<GameResponse> {
  return request<GameResponse>(
    `/api/games/${id}/nodes/${encodeURIComponent(nodeId)}/main-line`,
    {
      method: "POST",
    },
  );
}

export function updateGameTreeNode(
  id: string,
  nodeId: string,
  input: {
    note: string;
    important: boolean;
    expectedNote: string;
    expectedImportant: boolean;
  },
): Promise<GameResponse> {
  return request<GameResponse>(
    `/api/games/${id}/nodes/${encodeURIComponent(nodeId)}`,
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    },
  );
}

export async function getGameSGF(id: string): Promise<Blob> {
  const response = await requestResponse(`/api/games/${id}/sgf`);
  if (!response.ok) {
    throw await responseError(response, httpFailureMessage(response.status));
  }
  return readResponseBody(() => response.blob());
}

export async function getGameJSON(id: string): Promise<Blob> {
  const response = await requestResponse(`/api/games/${id}/json`);
  if (!response.ok) {
    throw await responseError(response, httpFailureMessage(response.status));
  }
  return readResponseBody(() => response.blob());
}

export function getGameAnalysis(
  id: string,
  nodeId: string,
  signal?: AbortSignal,
): Promise<PositionAnalysis> {
  const query = new URLSearchParams();
  query.set("nodeId", nodeId);
  return request<PositionAnalysis>(
    `/api/games/${id}/analysis?${query.toString()}`,
    { signal },
  );
}

export function setAgentSelection(
  gameId: string,
  nodeId: string,
  signal?: AbortSignal,
): Promise<void> {
  return requestResponse("/api/agent/selection", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ gameId, nodeId }),
    signal,
  }).then(async (response) => {
    if (response.ok) return;
    throw await responseError(
      response,
      `AI agentとの表示中の局面の共有に失敗しました（HTTP ${response.status}）`,
    );
  });
}
