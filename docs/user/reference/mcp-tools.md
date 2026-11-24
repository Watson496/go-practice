# MCPツール

Go PracticeのMCPサーバーで利用できるツールの一覧です。接続方法は[AI agentへのMCPサーバーの登録](../how-to/connect-ai-agent.md)にあります。

## 局面検討

画面で選んだ局面を検討するときは、最初に`get_selected_position`を使います。応答の`contextId`を渡すと、その局面を起点に解析や変化の表示を行えます。

- `get_selected_position`
    - 画面で表示中の局面と盤面、既存のKataGo解析、局面を識別する`contextId`を取得する
- `select_position`
    - 棋譜にある局面へアプリの表示を移し、移動先の`contextId`を返する
- `analyze_position`
    - `contextId`で指定した局面、またはそこから着手列を進めた局面をKataGoで解析する
- `show_variation`
    - AI agentが回答で示す変化を棋譜ツリーに追加する
- `get_analysis`
    - 対局IDで指定した対局の局面をKataGoで解析する。局面IDを省略すると、その対局の現在の局面を解析する

## 対局管理

- `list_games`
    - 保存した対局を一覧の並び順で取得する
- `reorder_games`
    - 指定した対局を指定順で一覧の末尾に並べ、省略した対局を先頭側に置く
- `list_storage_errors`
    - 起動時に読み込めなかった保存ファイルとその理由を取得する
- `get_game`
    - 指定した対局の棋譜、設定、現在の局面を取得する
- `create_game`
    - 盤サイズや石の色、AIレベルなどを指定してKataGoとの対局を作成する
- `rename_game`
    - 保存した対局の名前を変更する
- `update_game_settings`
    - 対局のAIレベルや人の石の色を変更する
- `delete_game`
    - 保存した対局を削除する

## 対局操作

- `play_move`
    - 人の手番で着手またはパスを行う
- `request_ai_move`
    - AIの手番でKataGoに着手を求める
- `resign_game`
    - 人の手番で投了する
- `undo_move`
    - 直近の人の操作とそれ以降の局面を取り消し、取り消した手順を変化として残する

## 棋譜編集

- `navigate_game`
    - 対局IDと局面IDで指定した局面を、その対局の現在の局面にする
- `update_node`
    - 局面のメモと「重要な局面」の印を更新する
- `play_variation`
    - 現在の局面から、その局面の手番の側の手として変化を1手進める
- `delete_variation`
    - 指定した局面と、それに続く局面を削除する
- `promote_variation`
    - 指定した局面を通る手順を主分岐にする

## 棋譜の読み込みと書き出し

- `import_sgf`
    - SGFの文字列を新しい対局として読み込む
- `import_json`
    - Go Practiceが書き出したJSONの文字列を読み込む
- `export_sgf`
    - 指定した対局をSGFの文字列として書き出する
- `export_json`
    - 指定した対局を再読み込みできるJSONの文字列として書き出する
