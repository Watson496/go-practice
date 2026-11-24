# 実行環境

Go Practiceの配布版を実行するための環境です。

- Apple Silicon搭載のMac
- KataGo 1.18.2と解析用のモデル・設定

Homebrewの`/opt/homebrew/opt/katago`にKataGoがある場合は、自動で検出します。モデルはHomebrewで配布される3つの固定ファイル名、解析設定は`analysis_example.cfg`を探します。それ以外のファイルを使う場合は、起動時に`--katago`、`--model`、`--config`を指定します。
