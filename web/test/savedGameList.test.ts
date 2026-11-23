import assert from "node:assert/strict";
import {
  findAllByRole,
  findByRole,
  fireEvent,
  getByRole,
  queryAllByRole,
} from "@testing-library/dom";
import { test } from "vitest";
import type { GameResponse } from "../src/api";
import {
  appServerResponse,
  deferred,
  gameResponseFixture,
  jsonResponse,
  renderApp,
  waitForCondition as waitFor,
} from "./testSupport";

function gameResponse(): GameResponse {
  return gameResponseFixture(
    "game-1",
    "練習対局",
    {
      boardSize: 9,
    },
    { startedAt: "2026-11-14T00:00:00Z" },
  );
}

function savedGame(id: string, name: string): GameResponse {
  const response = gameResponse();
  return { ...response, game: { ...response.game, id }, name };
}

function displayedGameNames(container: HTMLElement) {
  return queryAllByRole(container, "listitem").map(
    (element) =>
      getByRole(element, "button", {
        name: /^(?!.*を(?:並べ替える|削除)$)(?!.*の名前を変更$).+/,
      }).textContent,
  );
}

test("対局名は画面で打ち切らずサーバーへ同じ入力を送る", async () => {
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: "game-1" }),
  );
  const state = gameResponse();
  const renameBodies: Array<Record<string, unknown>> = [];
  Object.defineProperty(globalThis, "fetch", {
    configurable: true,
    value: async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/games/game-1" && init?.method === "PATCH") {
        renameBodies.push(JSON.parse(String(init.body)));
        return jsonResponse({ error: "対局名を受け付けられません。" }, 413);
      }
      const common = appServerResponse(path, init?.method ?? "GET", [state]);
      if (common) return common;
      return jsonResponse(state);
    },
  });

  const { container, act } = await renderApp({ notifications: false });

  const rename = async (value: string) => {
    await act(async () =>
      fireEvent.click(
        getByRole(container, "button", { name: "練習対局の名前を変更" }),
      ),
    );
    const input = await findByRole(document.body, "textbox", {
      name: "対局名",
    });
    await act(async () => fireEvent.change(input, { target: { value } }));
    const form = input.closest("form");
    assert.ok(form);
    await act(async () => fireEvent.submit(form));
  };

  const longName = "😀".repeat(81);
  await rename(longName);
  await waitFor(() => renameBodies.length === 1);
  assert.deepEqual(renameBodies[0], {
    name: longName,
    expectedName: "練習対局",
  });

  const retained = await findByRole(document.body, "textbox", {
    name: "対局名",
  });
  assert.equal((retained as HTMLInputElement).value, longName);

  await act(async () =>
    fireEvent.change(retained, { target: { value: "   " } }),
  );
  const retainedForm = retained.closest("form");
  assert.ok(retainedForm);
  await act(async () => fireEvent.submit(retainedForm));
  await waitFor(() => renameBodies.length === 2);
  assert.deepEqual(renameBodies[1], {
    name: "   ",
    expectedName: "練習対局",
  });
});

test("対局名の変更が競合したときはModalを開いたまま入力を残し、一覧を取り直す", async () => {
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: "game-1" }),
  );
  const state = gameResponse();
  let listRequests = 0;
  let renameRequests = 0;
  let releaseRename = () => {};
  Object.defineProperty(globalThis, "fetch", {
    configurable: true,
    value: async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/games/game-1" && init?.method === "PATCH") {
        renameRequests++;
        await new Promise<void>((resolve) => {
          releaseRename = resolve;
        });
        return jsonResponse(
          { error: "対局名は別の操作で更新されています。" },
          409,
        );
      }
      if (path === "/api/games") {
        listRequests++;
        return jsonResponse([{ ...state, name: "別の名前" }]);
      }
      const common = appServerResponse(path, init?.method ?? "GET", [state]);
      if (common) return common;
      return jsonResponse(state);
    },
  });

  const { container, act } = await renderApp({ notifications: false });

  await act(async () =>
    fireEvent.click(
      await findByRole(container, "button", { name: /の名前を変更$/ }),
    ),
  );
  const input = await findByRole(document.body, "textbox", { name: "対局名" });
  await act(async () =>
    fireEvent.change(input, { target: { value: "新しい名前" } }),
  );
  const form = input.closest("form");
  assert.ok(form);
  await act(async () => fireEvent.submit(form));
  await waitFor(() => renameRequests === 1);
  const listedBefore = listRequests;
  assert.ok(getByRole(document.body, "dialog", { name: "対局名を変更" }));

  await act(async () => releaseRename());
  await waitFor(() => listRequests > listedBefore);
  const remaining = getByRole(document.body, "textbox", { name: "対局名" });
  assert.equal((remaining as HTMLInputElement).value, "新しい名前");
});

test("起動時の古い対局一覧を捨てて最新の一覧を取り直す", async () => {
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: "game-1" }),
  );
  const displayed = gameResponse();
  const another = {
    ...gameResponse(),
    game: { ...gameResponse().game, id: "game-2" },
    name: "別の対局",
  };
  const deleted = {
    ...gameResponse(),
    game: { ...gameResponse().game, id: "deleted-game" },
    name: "削除済みの対局",
  };
  const firstList = deferred<Response>();
  const latestList = deferred<Response>();
  let listRequests = 0;
  Object.defineProperty(globalThis, "fetch", {
    configurable: true,
    value: async (input: string | URL | Request) => {
      const path = String(input);
      if (path === "/api/games") {
        listRequests++;
        return listRequests === 1 ? firstList.promise : latestList.promise;
      }
      const common = appServerResponse(path, "GET", [displayed]);
      if (common) return common;
      return jsonResponse(displayed);
    },
  });

  const { container, act } = await renderApp({ notifications: false });
  await findByRole(container, "button", { name: "練習対局" });

  await act(async () => firstList.resolve(jsonResponse([displayed, deleted])));
  await waitFor(() => listRequests === 2);
  assert.doesNotMatch(container.textContent ?? "", /削除済みの対局/);
  await act(async () => latestList.resolve(jsonResponse([displayed, another])));

  assert.ok(await findByRole(container, "button", { name: "別の対局" }));
  assert.equal(listRequests, 2);
});

test("続けて並べ替えたときは古い応答を一覧へ反映しない", async () => {
  const games = [
    savedGame("game-1", "一局目"),
    savedGame("game-2", "二局目"),
    savedGame("game-3", "三局目"),
  ];
  localStorage.setItem(
    "go-practice.latest-game-record",
    JSON.stringify({ schemaVersion: "1", gameId: "game-1" }),
  );
  const firstReorder = deferred<Response>();
  const secondReorder = deferred<Response>();
  const reorderBodies: string[][] = [];
  Object.defineProperty(globalThis, "fetch", {
    configurable: true,
    value: async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/games/order" && init?.method === "PUT") {
        reorderBodies.push(JSON.parse(String(init.body)).ids);
        return reorderBodies.length === 1
          ? firstReorder.promise
          : secondReorder.promise;
      }
      const common = appServerResponse(path, init?.method ?? "GET", games);
      if (common) return common;
      return jsonResponse(games[0]);
    },
  });

  const { container, act } = await renderApp({ notifications: false });

  const handles = await findAllByRole(container, "button", {
    name: /を並べ替える$/,
  });
  const placeRows = () => {
    for (const [index, row] of queryAllByRole(
      container,
      "listitem",
    ).entries()) {
      row.getBoundingClientRect = () => ({
        x: 0,
        y: index * 40,
        top: index * 40,
        left: 0,
        right: 300,
        bottom: index * 40 + 40,
        width: 300,
        height: 40,
        toJSON: () => undefined,
      });
    }
  };
  placeRows();
  const moveDown = async (handle: HTMLElement) => {
    await act(async () => {
      fireEvent.keyDown(handle, { key: " ", code: "Space", keyCode: 32 });
    });
    await act(async () => {
      fireEvent.keyDown(handle, {
        key: "ArrowDown",
        code: "ArrowDown",
        keyCode: 40,
      });
    });
    await act(async () => {
      fireEvent.keyDown(handle, { key: " ", code: "Space", keyCode: 32 });
    });
  };

  await moveDown(handles[0]);
  await waitFor(() => reorderBodies.length === 1);
  assert.deepEqual(displayedGameNames(container), [
    "二局目",
    "一局目",
    "三局目",
  ]);

  const updatedHandles = await findAllByRole(container, "button", {
    name: /を並べ替える$/,
  });
  placeRows();
  await moveDown(updatedHandles[1]);
  assert.deepEqual(displayedGameNames(container), [
    "二局目",
    "三局目",
    "一局目",
  ]);

  await act(async () => firstReorder.resolve(jsonResponse(games)));
  await waitFor(() => reorderBodies.length === 2);
  assert.deepEqual(displayedGameNames(container), [
    "二局目",
    "三局目",
    "一局目",
  ]);

  await act(async () =>
    secondReorder.resolve(jsonResponse([games[1], games[2], games[0]])),
  );
  await waitFor(
    () => displayedGameNames(container).join(",") === "二局目,三局目,一局目",
  );
});
