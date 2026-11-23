import type { Color, GameResponse } from "./api";

export const aiLevels = [1, 2, 3, 4].map((level) => ({
  value: String(level),
  label: `レベル ${level}`,
}));
export const aiLevelDescriptions: Record<string, string> = {
  "1": "50 visits。広い候補から選び、直前の場所で応酬を続けやすくします。",
  "2": "150 visits。基本的な戦術と、直前の場所での応酬をやや優先します。",
  "3": "300 visits。評価を重視しつつ、僅差なら局所の流れも残します。",
  "4": "500 visits。0.5目以内の上位候補を中心に選びます。",
};
export const humanColors = [
  { value: "B", label: "黒" },
  { value: "W", label: "白" },
];

export type ConfiguredSettings = { aiLevel: number; humanColor: Color };

// 着手、パス、AIの手、投了、待ったは、AIのレベルと人の石の色が設定されているときだけ行える
export function settingsConfigured(
  game: GameResponse,
): game is GameResponse & ConfiguredSettings {
  return game.aiLevel !== null && game.humanColor !== null;
}

// 対局の情報の欄で、その色の側を担当する者の表示
export function playerLabel(game: GameResponse, color: Color) {
  if (game.humanColor === null) return "未設定";
  if (game.humanColor === color) return "あなた";
  return game.aiLevel === null ? "AI レベル未設定" : `AI Lv.${game.aiLevel}`;
}
