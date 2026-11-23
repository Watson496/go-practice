import type { PositionAnalysis } from "./api";
import { isAbortError } from "./errors";

type BrowserStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">;

function readStoredValue(storage: BrowserStorage, key: string) {
  try {
    return { ok: true as const, value: storage.getItem(key) };
  } catch {
    return { ok: false as const, value: null };
  }
}

export function writeStoredValue(
  storage: BrowserStorage,
  key: string,
  value: string,
): boolean {
  try {
    storage.setItem(key, value);
    return true;
  } catch {
    return false;
  }
}

export function removeStoredValue(
  storage: BrowserStorage,
  key: string,
): boolean {
  try {
    storage.removeItem(key);
    return true;
  } catch {
    return false;
  }
}

type ParsedStoredValue<Value> =
  | { ok: true; value: Value | null }
  | { ok: false; cleanupFailed: boolean };

function hasOnlyKeys<Key extends string>(
  value: unknown,
  expected: readonly Key[],
): value is Record<Key, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value))
    return false;
  const keys = Object.keys(value);
  return (
    keys.length === expected.length &&
    expected.every((key) => Object.hasOwn(value, key))
  );
}

export function loadParsedStoredValue<Value>(
  storage: BrowserStorage,
  key: string,
  parse: (saved: string) => Value,
): ParsedStoredValue<Value> {
  const stored = readStoredValue(storage, key);
  if (!stored.ok) return { ok: false, cleanupFailed: false };
  if (stored.value === null) return { ok: true, value: null };
  try {
    return { ok: true, value: parse(stored.value) };
  } catch {
    return { ok: false, cleanupFailed: !removeStoredValue(storage, key) };
  }
}

export function storedValueErrorMessage(
  result: { ok: false; cleanupFailed: boolean },
  unreadable: string,
  persistent: string,
): string;
export function storedValueErrorMessage<Value>(
  result: ParsedStoredValue<Value>,
  unreadable: string,
  persistent: string,
): string | null;
export function storedValueErrorMessage(
  result: ParsedStoredValue<unknown>,
  unreadable: string,
  persistent: string,
): string | null {
  if (result.ok) return null;
  return result.cleanupFailed ? persistent : unreadable;
}

export function parseStoredWorkspaceMode(saved: string): "play" | "analysis" {
  const parsed: unknown = JSON.parse(saved);
  if (
    !hasOnlyKeys(parsed, ["schemaVersion", "mode"]) ||
    parsed.schemaVersion !== browserSchemaVersion ||
    (parsed.mode !== "play" && parsed.mode !== "analysis")
  )
    throw new Error("モードの保存形式が不正です");
  return parsed.mode;
}

export function serializedWorkspaceMode(mode: "play" | "analysis"): string {
  return JSON.stringify({ schemaVersion: browserSchemaVersion, mode });
}

export function parseStoredGameID(saved: string): string {
  const parsed: unknown = JSON.parse(saved);
  if (
    !hasOnlyKeys(parsed, ["schemaVersion", "gameId"]) ||
    parsed.schemaVersion !== browserSchemaVersion ||
    typeof parsed.gameId !== "string" ||
    parsed.gameId.trim() === ""
  )
    throw new Error("保存データに対局の識別情報がありません");
  return parsed.gameId;
}

export function serializedGameID(gameId: string): string {
  return JSON.stringify({ schemaVersion: browserSchemaVersion, gameId });
}

type EvaluationHistory = Record<
  string,
  Pick<PositionAnalysis, "scoreLead" | "winrate" | "visits">
>;

export function parseEvaluationHistory(saved: string): EvaluationHistory {
  const parsed: unknown = JSON.parse(saved);
  if (
    !hasOnlyKeys(parsed, ["schemaVersion", "entries"]) ||
    parsed.schemaVersion !== browserSchemaVersion ||
    typeof parsed.entries !== "object" ||
    parsed.entries === null ||
    Array.isArray(parsed.entries)
  )
    throw new Error("評価履歴の保存形式が不正です");
  for (const [nodeId, value] of Object.entries(parsed.entries)) {
    if (
      nodeId.trim() === "" ||
      !hasOnlyKeys(value, ["scoreLead", "winrate", "visits"]) ||
      typeof value.scoreLead !== "number" ||
      !Number.isFinite(value.scoreLead) ||
      typeof value.winrate !== "number" ||
      !Number.isFinite(value.winrate) ||
      value.winrate < 0 ||
      value.winrate > 1 ||
      typeof value.visits !== "number" ||
      !Number.isInteger(value.visits) ||
      value.visits < 1
    )
      throw new Error("評価履歴の値が不正です");
  }
  return parsed.entries as EvaluationHistory;
}

export function serializedEvaluations(entries: EvaluationHistory): string {
  return JSON.stringify({ schemaVersion: browserSchemaVersion, entries });
}

export function pollingFailureKind(
  cause: unknown,
): "aborted" | "deleted" | "unavailable" {
  if (isAbortError(cause)) return "aborted";
  if (
    typeof cause === "object" &&
    cause !== null &&
    "status" in cause &&
    cause.status === 404
  )
    return "deleted";
  return "unavailable";
}

export function jsonValuesEqual(first: unknown, second: unknown): boolean {
  if (Object.is(first, second)) return true;
  if (Array.isArray(first) || Array.isArray(second)) {
    if (!Array.isArray(first) || !Array.isArray(second)) return false;
    return (
      first.length === second.length &&
      first.every((entry, index) => jsonValuesEqual(entry, second[index]))
    );
  }
  if (
    !first ||
    !second ||
    typeof first !== "object" ||
    typeof second !== "object"
  )
    return false;
  const firstEntries = Object.entries(first);
  const secondRecord = second as Record<string, unknown>;
  if (firstEntries.length !== Object.keys(secondRecord).length) return false;
  return firstEntries.every(
    ([key, value]) =>
      Object.hasOwn(secondRecord, key) &&
      jsonValuesEqual(value, secondRecord[key]),
  );
}

const browserSchemaVersion = "1";
