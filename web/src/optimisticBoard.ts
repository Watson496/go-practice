import type { Color, Game, Move } from "./api";

export function optimisticStones(
  game: Pick<Game, "boardSize" | "stones">,
  move: Move,
): Record<string, Color> {
  if (move.coordinate.toLowerCase() === "pass") return game.stones;
  const stones = { ...game.stones, [move.coordinate]: move.color };
  const opponent = move.color === "B" ? "W" : "B";
  for (const neighbor of boardNeighbors(move.coordinate, game.boardSize)) {
    if (stones[neighbor] !== opponent) continue;
    const group = connectedGroup(neighbor, stones, game.boardSize);
    const hasLiberty = group.some((stone) =>
      boardNeighbors(stone, game.boardSize).some(
        (candidate) => stones[candidate] === undefined,
      ),
    );
    if (!hasLiberty) {
      for (const stone of group) delete stones[stone];
    }
  }
  return stones;
}

function connectedGroup(
  start: string,
  stones: Record<string, Color>,
  boardSize: number,
) {
  const color = stones[start];
  const seen = new Set([start]);
  const queue = [start];
  while (queue.length > 0) {
    const coordinate = queue.shift();
    if (!coordinate) break;
    for (const neighbor of boardNeighbors(coordinate, boardSize)) {
      if (stones[neighbor] === color && !seen.has(neighbor)) {
        seen.add(neighbor);
        queue.push(neighbor);
      }
    }
  }
  return [...seen];
}

function boardNeighbors(coordinate: string, boardSize: number) {
  const match = /^([A-HJ-T])(\d{1,2})$/i.exec(coordinate);
  if (!match) return [];
  const columns = "ABCDEFGHJKLMNOPQRST";
  const x = columns.indexOf(match[1].toUpperCase());
  const y = Number(match[2]) - 1;
  return [
    [x - 1, y],
    [x + 1, y],
    [x, y - 1],
    [x, y + 1],
  ]
    .filter(
      ([candidateX, candidateY]) =>
        candidateX >= 0 &&
        candidateX < boardSize &&
        candidateY >= 0 &&
        candidateY < boardSize,
    )
    .map(
      ([candidateX, candidateY]) => `${columns[candidateX]}${candidateY + 1}`,
    );
}
