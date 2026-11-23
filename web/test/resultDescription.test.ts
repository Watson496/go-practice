import assert from "node:assert/strict";
import { test } from "vitest";
import { resultDescription } from "../src/resultDescription";

test("目数と投了を表示し、それ以外の記録結果を終局と表示する", () => {
  assert.equal(
    resultDescription({
      type: "recorded",
      cause: "recorded",
      value: "B+T",
      scoreKnown: false,
    }),
    "終局",
  );
  assert.equal(
    resultDescription({
      type: "points",
      cause: "recorded",
      scoreKnown: false,
    }),
    "持碁",
  );
  assert.equal(
    resultDescription({
      type: "resignation",
      cause: "resignation",
      winner: "B",
      loser: "W",
      scoreKnown: false,
    }),
    "黒の勝ち（白の投了）",
  );
  assert.equal(
    resultDescription({
      type: "points",
      cause: "recorded",
      winner: "B",
      margin: 2.5,
      scoreKnown: false,
    }),
    "黒の2.5目勝ち",
  );
});
