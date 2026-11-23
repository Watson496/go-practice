import type { PositionAnalysis } from "./api";

type Candidate = Pick<
  PositionAnalysis["candidates"][number],
  "scoreLead" | "winrate"
>;

export function candidateScoreLoss(best: Candidate, candidate: Candidate) {
  return Math.max(0, best.scoreLead - candidate.scoreLead);
}

export function formatVisits(visits: number) {
  if (visits >= 1_000_000)
    return `${(visits / 1_000_000).toFixed(visits >= 10_000_000 ? 0 : 1)}m`;
  if (visits >= 1_000)
    return `${(visits / 1_000).toFixed(visits >= 10_000 ? 0 : 1)}k`;
  return String(visits);
}

export function candidateWinrate(candidate: Candidate) {
  return candidate.winrate;
}

export function formatWinrate(winrate: number) {
  return `${(winrate * 100).toFixed(1)}%`;
}

export function candidateScoreLead(candidate: Candidate) {
  return candidate.scoreLead;
}

export function formatScoreLead(scoreLead: number) {
  return `${scoreLead >= 0 ? "+" : ""}${scoreLead.toFixed(1)}`;
}

export function candidateColor(loss: number, alpha = 1) {
  const stops = [
    { loss: 0, color: [30, 150, 60] },
    { loss: 0.5, color: [150, 205, 45] },
    { loss: 1.5, color: [238, 205, 35] },
    { loss: 3, color: [240, 125, 30] },
    { loss: 6, color: [205, 45, 45] },
    { loss: 12, color: [114, 33, 107] },
  ];
  const upperIndex = stops.findIndex((stop) => loss <= stop.loss);
  if (upperIndex < 0)
    return `rgba(${stops[stops.length - 1].color.join(", ")}, ${alpha})`;
  if (upperIndex === 0) return `rgba(${stops[0].color.join(", ")}, ${alpha})`;
  const lower = stops[upperIndex - 1];
  const upper = stops[upperIndex];
  const ratio = (loss - lower.loss) / (upper.loss - lower.loss);
  const color = lower.color.map((value, index) =>
    Math.round(value + (upper.color[index] - value) * ratio),
  );
  return `rgba(${color.join(", ")}, ${alpha})`;
}
