import type { GameResponse } from "./api";
import { jsonValuesEqual } from "./browserState";

export type ReviewTarget = {
  gameId: string;
  nodeId: string;
  expectedNote: string;
  expectedImportant: boolean;
};

export function canPlayVariation(state: GameResponse): boolean {
  const current = state.game.treeNodes.find(
    (node) => node.id === state.game.currentNodeId,
  );
  return Boolean(current && !current.result);
}

export function reviewTargetFor(
  state: GameResponse,
  nodeId = state.game.currentNodeId,
): ReviewTarget {
  const values = reviewValuesFor(state, nodeId);
  return {
    gameId: state.game.id,
    nodeId,
    expectedNote: values.note,
    expectedImportant: values.important,
  };
}

function reviewValuesFor(
  state: GameResponse,
  nodeId = state.game.currentNodeId,
) {
  const node = state.game.treeNodes.find(
    (candidate) => candidate.id === nodeId,
  );
  const properties =
    nodeId === "root" ? state.game.rootProperties : node?.properties;
  return {
    note:
      properties?.find((property) => property.ident === "C")?.values[0] ?? "",
    important: node?.important ?? false,
  };
}

function reviewConflictDetails(note: string, important: boolean) {
  return `サーバー上の最新メモは「${note || "（空欄）"}」、重要な局面は${important ? "あり" : "なし"}です。編集中の内容は保持しています。`;
}

function optimisticMoveIsCurrent(
  current: GameResponse,
  optimistic: GameResponse["game"],
  previous: GameResponse,
): boolean {
  return (
    current.game.id === previous.game.id &&
    current.game.currentNodeId === optimistic.currentNodeId &&
    current.game.moves.length === optimistic.moves.length &&
    current.game.moves.at(-1)?.color === optimistic.moves.at(-1)?.color &&
    current.game.moves.at(-1)?.coordinate ===
      optimistic.moves.at(-1)?.coordinate
  );
}

export function rollbackOptimisticMove(
  current: GameResponse | null,
  optimistic: GameResponse["game"],
  previous: GameResponse,
): GameResponse | null {
  if (!current || !optimisticMoveIsCurrent(current, optimistic, previous))
    return current;
  return {
    ...current,
    game: {
      ...current.game,
      currentPlayer: previous.game.currentPlayer,
      moves: previous.game.moves,
      stones: previous.game.stones,
      captures: previous.game.captures,
      treeNodes: previous.game.treeNodes,
      currentNodeId: previous.game.currentNodeId,
      mainLineNodeId: previous.game.mainLineNodeId,
      status: previous.game.status,
      result: previous.game.result,
    },
  };
}

export function applyOptimisticMoveResponse(
  current: GameResponse | null,
  optimistic: GameResponse["game"],
  previous: GameResponse,
  response: GameResponse,
): GameResponse | null {
  return current && optimisticMoveIsCurrent(current, optimistic, previous)
    ? response
    : current;
}

export function applyAIMoveResponse(
  current: GameResponse | null,
  requested: GameResponse,
  response: GameResponse,
): GameResponse | null {
  return current &&
    current.game.id === requested.game.id &&
    current.game.mainLineNodeId === requested.game.mainLineNodeId &&
    current.game.currentPlayer === requested.game.currentPlayer
    ? response
    : current;
}

export function applyPolledGameResponse(
  current: GameResponse | null,
  requested: GameResponse,
  latest: GameResponse,
): GameResponse | null {
  if (!current || !jsonValuesEqual(current, requested)) return current;
  return jsonValuesEqual(current, latest) ? current : latest;
}

// 同じ対局で、主分岐の末端を変えずに"現在の局面"が変わったとき、
// つまり局面の移動を含む応答を受け取ったときにtrueを返す。
export function changedByNavigation(
  previous: GameResponse,
  next: GameResponse,
): boolean {
  const previousNodeIds = new Set(
    previous.game.treeNodes.map((node) => node.id),
  );
  const nextNodeIds = new Set(next.game.treeNodes.map((node) => node.id));
  return (
    previous.game.id === next.game.id &&
    previous.game.currentNodeId !== next.game.currentNodeId &&
    previous.game.mainLineNodeId === next.game.mainLineNodeId &&
    previousNodeIds.has(next.game.currentNodeId) &&
    [...previousNodeIds].every((nodeId) => nextNodeIds.has(nodeId))
  );
}

export function applyReviewConflict(
  target: ReviewTarget,
  latest: GameResponse,
  edited: { note: string; important: boolean },
) {
  const latestReview = reviewValuesFor(latest, target.nodeId);
  return {
    target: reviewTargetFor(latest, target.nodeId),
    note: edited.note === target.expectedNote ? latestReview.note : edited.note,
    important:
      edited.important === target.expectedImportant
        ? latestReview.important
        : edited.important,
    details: reviewConflictDetails(latestReview.note, latestReview.important),
  };
}

export function mergeReviewResponse(
  current: GameResponse,
  response: GameResponse,
  nodeId: string,
): GameResponse {
  if (current.game.id !== response.game.id) return current;
  const updated = response.game.treeNodes.find((node) => node.id === nodeId);
  const mergeComment = (
    existing: Array<{ ident: string; values: string[] }> | undefined,
    incoming: Array<{ ident: string; values: string[] }> | undefined,
  ) => {
    const comment = incoming?.find((property) => property.ident === "C");
    return [
      ...(existing ?? []).filter((property) => property.ident !== "C"),
      ...(comment ? [comment] : []),
    ];
  };
  return {
    ...current,
    game: {
      ...current.game,
      rootProperties:
        nodeId === "root"
          ? mergeComment(
              current.game.rootProperties,
              response.game.rootProperties,
            )
          : current.game.rootProperties,
      treeNodes: current.game.treeNodes.map((node) =>
        node.id === nodeId && updated
          ? {
              ...node,
              important: updated.important,
              properties: mergeComment(node.properties, updated.properties),
            }
          : node,
      ),
    },
  };
}
