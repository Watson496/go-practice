import assert from "node:assert/strict";
import { test } from "vitest";
import { formatVisits } from "../src/analysisDisplay";
import type { GameResponse } from "../src/api";
import { pollingFailureKind } from "../src/browserState";
import {
  applyAIMoveResponse,
  applyOptimisticMoveResponse,
  applyPolledGameResponse,
  applyReviewConflict,
  canPlayVariation,
  changedByNavigation,
  mergeReviewResponse,
  reviewTargetFor,
  rollbackOptimisticMove,
} from "../src/frontendState";
import { gameResponseFixture } from "./testSupport";

function game(): GameResponse {
  return gameResponseFixture(
    "game-1",
    "対局",
    {
      boardSize: 9,
      treeNodes: [
        { id: "root" },
        {
          id: "n1",
          parentId: "root",
          move: { color: "B", coordinate: "E5" },
        },
      ],
    },
    { startedAt: "2026-01-01T00:00:00Z" },
  );
}

test("結果の局面以外から変化へ着手できる", () => {
  const completion = game();
  completion.game.status = "finished";
  completion.game.result = {
    type: "points",
    cause: "two_passes",
    winner: "W",
    margin: 7.5,
    blackScore: 0,
    whiteScore: 7.5,
    scoreKnown: true,
    deadStones: [],
  };
  completion.game.treeNodes = [
    { id: "root" },
    { id: "n1", parentId: "root", move: { color: "B", coordinate: "pass" } },
    { id: "n2", parentId: "n1", move: { color: "W", coordinate: "pass" } },
    { id: "n3", parentId: "n2", result: completion.game.result },
  ];
  completion.game.currentNodeId = "n2";
  completion.game.mainLineNodeId = "n3";
  assert.equal(canPlayVariation(completion), true);

  completion.game.currentNodeId = "n3";
  assert.equal(canPlayVariation(completion), false);
});

function selectFirstMove(state: GameResponse, mainLine: boolean) {
  state.game.moves = [{ color: "B", coordinate: "E5" }];
  state.game.stones = { E5: "B" };
  state.game.currentPlayer = "W";
  state.game.currentNodeId = "n1";
  if (mainLine) state.game.mainLineNodeId = "n1";
}

test("探索数を画面と同じ短い形式で表示する", () => {
  assert.equal(formatVisits(1), "1");
  assert.equal(formatVisits(2_000), "2.0k");
});

test("メモ編集開始時の値を競合検査用に保持する", () => {
  const state = game();
  state.game.rootProperties = [{ ident: "C", values: ["編集前"] }];
  state.game.treeNodes[0].important = true;
  assert.deepEqual(reviewTargetFor(state), {
    gameId: "game-1",
    nodeId: "root",
    expectedNote: "編集前",
    expectedImportant: true,
  });
});

test("メモ保存の応答は同時に進んだ対局状態を巻き戻さない", () => {
  const current = game();
  current.name = "新しい名前";
  selectFirstMove(current, false);
  current.game.rootProperties = [
    { ident: "GN", values: ["新しい開始局面情報"] },
  ];
  current.game.treeNodes[0] = {
    id: "root",
    origin: "agent",
    variationTitle: "新しい変化名",
    highlighted: true,
    properties: current.game.rootProperties,
  };
  const response = game();
  response.game.rootProperties = [{ ident: "C", values: ["保存したメモ"] }];
  response.game.treeNodes[0] = {
    id: "root",
    properties: response.game.rootProperties,
    important: true,
  };

  const merged = mergeReviewResponse(current, response, "root");
  assert.equal(merged.name, "新しい名前");
  assert.equal(merged.game.currentNodeId, "n1");
  assert.equal(merged.game.treeNodes[0].important, true);
  assert.equal(merged.game.treeNodes[0].origin, "agent");
  assert.equal(merged.game.treeNodes[0].variationTitle, "新しい変化名");
  assert.equal(merged.game.treeNodes[0].highlighted, true);
  assert.deepEqual(merged.game.rootProperties, [
    { ident: "GN", values: ["新しい開始局面情報"] },
    { ident: "C", values: ["保存したメモ"] },
  ]);
});

test("別の対局へ移った後に届いたメモ保存応答を捨てる", () => {
  const current = game();
  current.game.id = "new-game";
  current.name = "現在の対局";
  const stale = game();
  stale.game.rootProperties = [{ ident: "C", values: ["古い応答"] }];

  assert.strictEqual(mergeReviewResponse(current, stale, "root"), current);
});

test("保存対象の局面が削除された後の応答で別の局面を書き換えない", () => {
  const current = game();
  current.game.treeNodes = [{ id: "root" }];
  const stale = game();
  stale.game.treeNodes.push({
    id: "deleted",
    important: true,
    properties: [{ ident: "C", values: ["削除済み"] }],
  });

  const merged = mergeReviewResponse(current, stale, "deleted");
  assert.deepEqual(merged.game.treeNodes, [{ id: "root" }]);
  assert.deepEqual(merged.game.rootProperties, undefined);
});

test("同期失敗を中止・削除・一時失敗へ分類する", () => {
  assert.equal(pollingFailureKind({ name: "AbortError" }), "aborted");
  assert.equal(pollingFailureKind({ status: 404 }), "deleted");
  assert.equal(pollingFailureKind(new Error("通信失敗")), "unavailable");
});

test("AI応答を要求開始時の状態で検証する", () => {
  const requested = game();
  const response = game();
  response.game.currentNodeId = "n2";
  assert.equal(applyAIMoveResponse(game(), requested, response), response);
  const changed = game();
  changed.game.currentPlayer = "W";
  assert.equal(applyAIMoveResponse(changed, requested, response), changed);
});

test("古い同期応答を新しい画面状態へ適用しない", () => {
  const requested = game();
  const latest = game();
  latest.game.currentNodeId = "n2";
  const moved = game();
  moved.game.currentNodeId = "n1";
  assert.strictEqual(applyPolledGameResponse(moved, requested, latest), moved);
  assert.strictEqual(
    applyPolledGameResponse(requested, requested, latest),
    latest,
  );
});

test("楽観的な着手の応答と失敗は対象の状態だけへ反映する", () => {
  const previous = game();
  const optimisticGame = {
    ...previous.game,
    currentPlayer: "W" as const,
    currentNodeId: "n1",
    moves: [{ color: "B" as const, coordinate: "E5" }],
  };
  const optimistic = { ...previous, game: optimisticGame };
  const response = game();
  response.game.currentNodeId = "n2";
  assert.strictEqual(
    applyOptimisticMoveResponse(optimistic, optimisticGame, previous, response),
    response,
  );
  assert.deepEqual(
    rollbackOptimisticMove(optimistic, optimisticGame, previous),
    previous,
  );
  const advanced = game();
  advanced.game.currentNodeId = "n3";
  assert.strictEqual(
    applyOptimisticMoveResponse(advanced, optimisticGame, previous, response),
    advanced,
  );
  assert.strictEqual(
    rollbackOptimisticMove(advanced, optimisticGame, previous),
    advanced,
  );
});

test("メモ競合の最新値を次の保存条件へ反映する", () => {
  const current = game();
  const target = reviewTargetFor(current);
  const latest = game();
  latest.game.rootProperties = [{ ident: "C", values: ["相手の更新"] }];
  latest.game.treeNodes[0].important = true;
  const conflict = applyReviewConflict(target, latest, {
    note: "自分の更新",
    important: false,
  });
  assert.equal(conflict.target.expectedNote, "相手の更新");
  assert.equal(conflict.note, "自分の更新");
  assert.equal(conflict.important, true);
  assert.equal(
    conflict.details,
    "サーバー上の最新メモは「相手の更新」、重要な局面はありです。編集中の内容は保持しています。",
  );
});

test("局面の移動で現在の局面だけが変わったときを見分ける", () => {
  const previous = game();
  const navigated = game();
  navigated.game.currentNodeId = "n1";
  assert.equal(changedByNavigation(previous, navigated), true);

  const navigatedAndReviewed = game();
  navigatedAndReviewed.game.currentNodeId = "n1";
  navigatedAndReviewed.game.treeNodes[0] = {
    ...navigatedAndReviewed.game.treeNodes[0],
    properties: [{ ident: "C", values: ["AI agentのメモ"] }],
  };
  assert.equal(changedByNavigation(previous, navigatedAndReviewed), true);

  const navigatedAndExtended = game();
  navigatedAndExtended.game.currentNodeId = "n1";
  navigatedAndExtended.game.treeNodes = [
    ...navigatedAndExtended.game.treeNodes,
    { id: "n2", parentId: "n1", move: { color: "W", coordinate: "D4" } },
  ];
  assert.equal(changedByNavigation(previous, navigatedAndExtended), true);

  const navigatedToNewVariation = game();
  navigatedToNewVariation.game.currentNodeId = "n2";
  navigatedToNewVariation.game.treeNodes = [
    ...navigatedToNewVariation.game.treeNodes,
    { id: "n2", parentId: "n1", move: { color: "W", coordinate: "D4" } },
  ];
  assert.equal(changedByNavigation(previous, navigatedToNewVariation), false);

  const deletedCurrentNode = game();
  deletedCurrentNode.game.currentNodeId = "root";
  deletedCurrentNode.game.treeNodes = deletedCurrentNode.game.treeNodes.filter(
    (node) => node.id !== "n1",
  );
  assert.equal(changedByNavigation(previous, deletedCurrentNode), false);

  const undone = game();
  undone.game.currentNodeId = "n1";
  undone.game.mainLineNodeId = "n1";
  assert.equal(changedByNavigation(previous, undone), false);

  const played = game();
  played.game.currentNodeId = "n2";
  played.game.mainLineNodeId = "n2";
  played.game.treeNodes = [
    ...played.game.treeNodes,
    { id: "n2", parentId: "root", move: { color: "B", coordinate: "D4" } },
  ];
  assert.equal(changedByNavigation(previous, played), false);

  const other = game();
  other.game.id = "game-2";
  other.game.currentNodeId = "n1";
  assert.equal(changedByNavigation(previous, other), false);
});

test("AI agentが変えた対局の設定を画面に反映する", () => {
  const requested = game();
  const latest = game();
  latest.humanColor = "W";
  assert.strictEqual(
    applyPolledGameResponse(requested, requested, latest),
    latest,
  );
});
