# HTTP APIのエラー応答

ブラウザとMCPサーバーが使う内部HTTP APIの主なエラー応答である。以下のエラー応答は、理由を`error`に入れたJSONオブジェクトを返す。

- `400 Bad Request`
    - 入力の形式や値が不正。例として、着手座標の指定がない場合
- `404 Not Found`
    - 要求の対象となるAPI、対局、または棋譜の局面が見つからない場合
- `409 Conflict`
    - 対局の状態が操作と合わない場合や、取得後に他の操作が状態を変更した場合。例として、取得時の対局名が現在の名前と一致しない場合
- `413 Content Too Large`
    - リクエストボディが上限を超えた場合
- `415 Unsupported Media Type`
    - APIが要求する`Content-Type`と一致しない場合
- `422 Unprocessable Content`
    - 入力を適用した結果が棋譜の局面数や書き出しサイズなどの上限を超える場合
- `500 Internal Server Error`
    - 対局を保存できないなど、Goサーバー内で処理に失敗した場合
- `502 Bad Gateway`
    - KataGoの解析結果を取得できない場合や、その結果から有効なAIの着手を選べない場合
- `504 Gateway Timeout`
    - KataGoの応手の決定や局面の解析が制限時間内に完了しなかった場合
