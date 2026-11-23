import { describe, expect, test } from "vitest";
import type { Game, GameResponse, GameTreeNode } from "../src/api";
import { gameName, pausedWhenOpened } from "../src/gamePresentation";
import {
  adjacentBranchNodeId,
  extendSelectedVariation,
  layoutGameTree,
  nextTreeNodeId,
  treePathToNode,
  variationSubtreeNodeIds,
  walkTree,
} from "../src/gameTree";
import { defaultKomi, handicapOptions } from "../src/newGame";
import { optimisticStones } from "../src/optimisticBoard";

describe("新しい対局の選択肢", () => {
  test.each([
    [5, ["0", "2", "3", "4", "5"]],
    [6, ["0", "2", "3", "4"]],
    [7, ["0", "2", "3", "4", "5", "6", "7", "8", "9"]],
    [19, ["0", "2", "3", "4", "5", "6", "7", "8", "9"]],
  ])("%d路盤の置石の上限を反映する", (boardSize, expected) => {
    expect(handicapOptions(boardSize).map((option) => option.value)).toEqual(
      expected,
    );
  });

  test("置石の有無に応じてコミの既定値を返す", () => {
    expect(defaultKomi(0)).toBe(7.5);
    expect(defaultKomi(2)).toBe(0.5);
  });
});

describe("保存した対局の表示", () => {
  const game = {
    game: { status: "playing", currentPlayer: "W" },
    humanColor: "B",
    startedAt: "2026-11-16T12:34:00+09:00",
  } as GameResponse;

  test("名前がなければ開始日時から既定名を作る", () => {
    expect(gameName(game)).toBe("261116-1234");
    expect(gameName({ ...game, name: "　対局　" })).toBe("　対局　");
  });

  test("AIの手番で開いた対局だけを中止状態にする", () => {
    expect(pausedWhenOpened(game)).toBe(true);
    expect(
      pausedWhenOpened({
        ...game,
        game: { ...game.game, currentPlayer: "B" },
      }),
    ).toBe(false);
  });
});

describe("応答を待つ間の盤面", () => {
  const game = {
    boardSize: 5,
    stones: { B2: "W", A2: "B", B1: "B", C2: "B" },
  } satisfies Pick<Game, "boardSize" | "stones">;

  test("着手で呼吸点を失った石を取り除く", () => {
    expect(optimisticStones(game, { color: "B", coordinate: "B3" })).toEqual({
      A2: "B",
      B1: "B",
      B3: "B",
      C2: "B",
    });
  });

  test("パスでは盤面を変えない", () => {
    expect(optimisticStones(game, { color: "B", coordinate: "pass" })).toBe(
      game.stones,
    );
  });
});

describe("棋譜ツリーの移動", () => {
  const nodes: GameTreeNode[] = [
    { id: "root" },
    { id: "n1", parentId: "root", move: { color: "B", coordinate: "A1" } },
    { id: "n2", parentId: "n1", move: { color: "W", coordinate: "B1" } },
    { id: "n3", parentId: "n1", move: { color: "W", coordinate: "C1" } },
    {
      id: "n4",
      parentId: "n3",
      properties: [{ ident: "C", values: ["メモ"] }],
    },
  ];
  const mainLine = new Set(["root", "n1", "n2"]);

  test("主分岐では主分岐の子を進む", () => {
    expect(nextTreeNodeId(nodes, "n1", mainLine)).toBe("n2");
    expect(walkTree(nodes, "root", mainLine, "forward", 2)).toBe("n2");
  });

  test("変化では選択中の変化を進み、親へ戻る", () => {
    expect(nextTreeNodeId(nodes, "n3", mainLine)).toBe("n4");
    expect(walkTree(nodes, "n4", mainLine, "back", 2)).toBe("n1");
  });

  test("局面までの経路と削除対象の部分木を返す", () => {
    expect(treePathToNode(nodes, "n4")).toEqual(["root", "n1", "n3", "n4"]);
    expect(variationSubtreeNodeIds(nodes, "n3")).toEqual(["n3", "n4"]);
  });

  test("選択した変化を生成順に末端まで延ばす", () => {
    expect(extendSelectedVariation(nodes, ["root", "n1"])).toEqual([
      "root",
      "n1",
      "n2",
    ]);
  });

  test("主分岐を先頭に配置して同じ深さの隣の変化を返す", () => {
    const layout = layoutGameTree(nodes, mainLine);
    expect(layout.items.map((item) => item.node.id)).toEqual([
      "root",
      "n1",
      "n2",
      "n3",
      "n4",
    ]);
    expect(adjacentBranchNodeId(nodes, "n2", mainLine, 1)).toBe("n3");
  });
});
