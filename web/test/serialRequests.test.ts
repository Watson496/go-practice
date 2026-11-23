import assert from "node:assert/strict";
import { test } from "vitest";
import { createSerialRequests } from "../src/serialRequests";

test("要求は前の応答を待ってから送り、最後の要求の結果だけを最新とする", async () => {
  const send = createSerialRequests<string>();
  const sent: string[] = [];
  let releaseFirst = () => {};
  const first = send(() => {
    sent.push("first");
    return new Promise<string>((resolve) => {
      releaseFirst = () => resolve("first");
    });
  });
  const second = send(async () => {
    sent.push("second");
    return "second";
  });
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.deepEqual(sent, ["first"]);

  releaseFirst();
  assert.deepEqual(await first, { ok: true, value: "first", latest: false });
  assert.deepEqual(await second, { ok: true, value: "second", latest: true });
  assert.deepEqual(sent, ["first", "second"]);
});

test("失敗した要求の後も、次の要求を送る", async () => {
  const send = createSerialRequests<string>();
  const failed = await send(async () => {
    throw new Error("保存できません");
  });
  assert.equal(failed.ok, false);
  assert.equal(failed.latest, true);
  assert.deepEqual(await send(async () => "next"), {
    ok: true,
    value: "next",
    latest: true,
  });
});
