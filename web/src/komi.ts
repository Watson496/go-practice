export const minimumKomi = -100;
export const maximumKomi = 100;

export function parseKomiInput(
  value: string,
  minimum = minimumKomi,
  maximum = maximumKomi,
) {
  const trimmed = value.trim();
  if (trimmed === "") {
    return { value: null, error: "コミを入力してください。" };
  }
  const numeric = Number(trimmed);
  if (
    /^-?\d+(?:\.(?:0+|50*))?$/.test(trimmed) &&
    numeric >= minimum &&
    numeric <= maximum
  ) {
    return { value: numeric, error: null };
  }
  return {
    value: null,
    error: `コミは${minimum}以上${maximum}以下の0.5目単位で入力してください。`,
  };
}
