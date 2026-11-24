# 開発環境での対局作成

GoのAPIサーバーとViteを別々に起動し、開発用の画面から9路盤の対局を作る。この構成では、画面の変更をViteで確認しながら、対局と棋譜を実際のGoサーバーで扱える。

## APIサーバー

[ビルド環境](../reference/build-environment.md)と[実行環境](../../user/reference/requirements.md)を用意する。リポジトリのルートで依存関係を導入し、Goの実行ファイルをビルドする。練習用の棋譜は一時ディレクトリに保存する。

```sh
make setup
make build.server
practice_data_dir=$(mktemp -d)
make run.server DATA_DIR="$practice_data_dir"
```

## ブラウザからの対局作成

別のターミナルでリポジトリのルートを開き、Viteを起動する。

```sh
make run.web
```

ブラウザで `http://127.0.0.1:5173` を開く。「新しい対局」から9路盤、黒番、置石なしで対局を作り、着手する。ページを再読み込みし、「保存した対局」から同じ対局を開いて、着手が残っていることを確認する。
