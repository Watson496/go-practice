type SerialOutcome<Value> = { latest: boolean } & (
  | { ok: true; value: Value }
  | { ok: false; error: unknown }
);

// 要求を、前の要求の応答を待ってから1つずつ送る。サーバーは届いた順に処理するので、
// 始めた順にサーバーが処理する。結果のlatestは、その要求が最後に始めた要求かを示す。
export function createSerialRequests<Value>() {
  let queue: Promise<unknown> = Promise.resolve();
  let started = 0;
  return (send: () => Promise<Value>): Promise<SerialOutcome<Value>> => {
    const sequence = ++started;
    const outcome = queue.then(send).then(
      (value) => ({ ok: true as const, value, latest: sequence === started }),
      (error: unknown) => ({
        ok: false as const,
        error,
        latest: sequence === started,
      }),
    );
    queue = outcome;
    return outcome;
  };
}
