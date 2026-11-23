import assert from "node:assert/strict";
import { test } from "vitest";
import {
  jsonValuesEqual,
  loadParsedStoredValue,
  parseEvaluationHistory,
  parseStoredGameID,
  parseStoredWorkspaceMode,
  removeStoredValue,
  serializedEvaluations,
  serializedGameID,
  serializedWorkspaceMode,
  writeStoredValue,
} from "../src/browserState";

test("不正な前回対局の保存値を削除する", () => {
  const key = "go-practice.latest-game-record";
  localStorage.setItem(
    key,
    JSON.stringify({ schemaVersion: "1", gameId: " " }),
  );

  const result = loadParsedStoredValue(localStorage, key, parseStoredGameID);

  assert.deepEqual(result, { ok: false, cleanupFailed: false });
  assert.equal(localStorage.getItem(key), null);
});

test("不正な保存値を削除できなければ失敗を返す", () => {
  const storage = {
    getItem: () => "invalid",
    setItem() {},
    removeItem() {
      throw new Error("削除できません");
    },
  };

  assert.deepEqual(
    loadParsedStoredValue(storage, "mode", parseStoredWorkspaceMode),
    { ok: false, cleanupFailed: true },
  );
});

test("値域が不正な評価履歴を削除する", () => {
  for (const entries of [
    { "": { scoreLead: 0, winrate: 0.5, visits: 1 } },
    { n1: { scoreLead: Number.NaN, winrate: 0.5, visits: 1 } },
    { n1: { scoreLead: 0, winrate: -0.1, visits: 1 } },
    { n1: { scoreLead: 0, winrate: 1.1, visits: 1 } },
    { n1: { scoreLead: 0, winrate: 0.5, visits: 0 } },
    { n1: { scoreLead: 0, winrate: 0.5, visits: 1.5 } },
  ]) {
    localStorage.clear();
    const key = "go-practice.evaluations.game-1";
    localStorage.setItem(key, JSON.stringify({ schemaVersion: "1", entries }));

    const result = loadParsedStoredValue(
      localStorage,
      key,
      parseEvaluationHistory,
    );

    assert.deepEqual(result, { ok: false, cleanupFailed: false });
    assert.equal(localStorage.getItem(key), null);
  }
});

test("保存形式にない項目を含むブラウザ保存値を削除する", () => {
  for (const [key, saved, parse] of [
    [
      "go-practice.workspace-mode",
      '{"schemaVersion":"1","mode":"play","oldMode":"analysis"}',
      parseStoredWorkspaceMode,
    ],
    [
      "go-practice.latest-game-record",
      '{"schemaVersion":"1","gameId":"g1","previousGameId":"g0"}',
      parseStoredGameID,
    ],
    [
      "go-practice.evaluations.game-1",
      '{"schemaVersion":"1","entries":{"n1":{"scoreLead":0,"winrate":0.5,"visits":1,"ownership":[]}}}',
      parseEvaluationHistory,
    ],
  ] as const) {
    localStorage.clear();
    localStorage.setItem(key, saved);

    const result = loadParsedStoredValue(
      localStorage,
      key,
      parse as (value: string) => unknown,
    );

    assert.deepEqual(result, { ok: false, cleanupFailed: false });
    assert.equal(localStorage.getItem(key), null);
  }
});

test("空文字列の評価履歴を未保存として扱わず削除する", () => {
  const key = "go-practice.evaluations.game-1";
  localStorage.setItem(key, "");

  const result = loadParsedStoredValue(
    localStorage,
    key,
    parseEvaluationHistory,
  );

  assert.deepEqual(result, { ok: false, cleanupFailed: false });
  assert.equal(localStorage.getItem(key), null);
});

test("ブラウザへ保存する値は定めた形式で往復する", () => {
  assert.equal(
    parseStoredWorkspaceMode(serializedWorkspaceMode("analysis")),
    "analysis",
  );
  assert.equal(parseStoredGameID(serializedGameID("game-1")), "game-1");
  const evaluations = {
    n1: { scoreLead: 1.5, winrate: 0.6, visits: 20 },
  };
  assert.deepEqual(
    parseEvaluationHistory(serializedEvaluations(evaluations)),
    evaluations,
  );
});

test("項目順の違う保存値を同じ内容として扱う", () => {
  assert.equal(
    jsonValuesEqual(
      { game: { id: "g1", status: "playing" }, aiLevel: 1 },
      { aiLevel: 1, game: { status: "playing", id: "g1" } },
    ),
    true,
  );
});

test("ブラウザ保存領域への書き込み失敗を返す", () => {
  const storage = {
    getItem: () => null,
    setItem: () => {
      throw new Error("保存できません");
    },
    removeItem() {
      throw new Error("削除できません");
    },
  };
  assert.equal(
    writeStoredValue(storage, "mode", serializedWorkspaceMode("play")),
    false,
  );
  assert.equal(removeStoredValue(storage, "mode"), false);
});
