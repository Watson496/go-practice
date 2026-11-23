import type { Game } from "./api";

export function resultDescription(result: Game["result"]): string {
  if (!result) return "";
  if (result.type === "recorded") return "終局";
  if (result.type === "points" && !("winner" in result)) return "持碁";
  const winner = result.winner === "B" ? "黒" : "白";
  const loser = result.winner === "B" ? "白" : "黒";
  if (result.type === "points") {
    return `${winner}の${result.margin}目勝ち`;
  }
  const reason = "投了";
  return `${winner}の勝ち（${loser}の${reason}）`;
}
