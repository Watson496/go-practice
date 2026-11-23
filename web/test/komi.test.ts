import assert from "node:assert/strict";
import { test } from "vitest";
import { maximumKomi, minimumKomi, parseKomiInput } from "../src/komi";

test("コミを文字列の0.5目単位で検査する", () => {
  for (const input of ["-100", "-0.5", "0", "7.5", "7.50", "100.0"]) {
    assert.notEqual(parseKomiInput(input).value, null, input);
  }
  for (const input of [
    "",
    "0.1",
    "0.49999999999999999",
    "7.50000000000000001",
    "101",
    "1e1",
  ]) {
    assert.equal(parseKomiInput(input).value, null, input);
  }
  assert.equal(minimumKomi, -100);
  assert.equal(maximumKomi, 100);
});
