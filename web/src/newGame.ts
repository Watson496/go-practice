type HandicapOption = { value: string; label: string };

const handicaps: HandicapOption[] = [0, 2, 3, 4, 5, 6, 7, 8, 9].map(
  (value) => ({
    value: String(value),
    label: value === 0 ? "互先" : `${value}子局`,
  }),
);

export function handicapOptions(boardSize: number): HandicapOption[] {
  const maximum = boardSize % 2 === 0 ? 4 : boardSize === 5 ? 5 : 9;
  return handicaps.filter((option) => Number(option.value) <= maximum);
}

export function defaultKomi(handicap: number) {
  return handicap > 0 ? 0.5 : 7.5;
}
