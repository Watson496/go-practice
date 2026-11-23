import assert from "node:assert/strict";
import { test } from "vitest";
import {
  candidateScoreLead,
  candidateScoreLoss,
  candidateWinrate,
  formatScoreLead,
  formatWinrate,
} from "../src/analysisDisplay";

test("目差と勝率を共通の表示形式へ揃える", () => {
  assert.equal(formatScoreLead(1.24), "+1.2");
  assert.equal(formatScoreLead(0), "+0.0");
  assert.equal(formatScoreLead(-1.26), "-1.3");
  assert.equal(formatWinrate(0), "0.0%");
  assert.equal(formatWinrate(0.501), "50.1%");
  assert.equal(formatWinrate(1), "100.0%");
});

test("手番側から見た候補手の評価を表示に使う", () => {
  const blackBest = { scoreLead: 3, winrate: 0.7 };
  const blackCandidate = { scoreLead: 1.5, winrate: 0.6 };
  const whiteBest = { scoreLead: 3, winrate: 0.7 };
  const whiteCandidate = { scoreLead: 1.5, winrate: 0.6 };

  assert.equal(candidateWinrate(blackCandidate), 0.6);
  assert.equal(candidateScoreLead(blackCandidate), 1.5);
  assert.equal(candidateScoreLoss(blackBest, blackCandidate), 1.5);

  assert.equal(candidateWinrate(whiteCandidate), 0.6);
  assert.equal(candidateScoreLead(whiteCandidate), 1.5);
  assert.equal(candidateScoreLoss(whiteBest, whiteCandidate), 1.5);
});

test("手番側から見て良い候補との目差を負にしない", () => {
  const blackBest = { scoreLead: 3, winrate: 0.7 };
  const betterForBlack = { scoreLead: 4, winrate: 0.8 };
  const whiteBest = { scoreLead: 3, winrate: 0.7 };
  const betterForWhite = { scoreLead: 4, winrate: 0.8 };

  assert.equal(candidateScoreLoss(blackBest, betterForBlack), 0);
  assert.equal(candidateScoreLoss(whiteBest, betterForWhite), 0);
});
