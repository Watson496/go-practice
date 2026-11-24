# 開発コマンドと起動オプション

コマンドはリポジトリのルートで実行する。`make help`は対象の一覧を表示する。

## ビルドと起動

- `make setup`（フロントエンドの依存関係の導入）
- `make build`（Goの実行ファイルと本番用フロントエンドのビルド）
- `make build.server`（`bin/go-practice`のビルド）
- `make build.web`（`web/dist`への本番用ビルド）
- `make run`（ビルド済みアプリの起動）
- `make run.server`（ビルド済みのAPIサーバーの起動）
- `make run.web`（Vite開発サーバーの起動）
- `make clean`（`bin/go-practice`と`web/dist`の削除）

## テストと静的検査

- `make check`（テスト、静的検査、書式検査、型検査の実行）
- `make test`（Goとフロントエンドのテスト）
- `make test.server`（データ競合検査付きのGoテスト）
- `make test.web`（フロントエンドのテスト）
- `make check.server`（Goの整形検査、`go vet`、deadcode、staticcheck）
- `make check.web`（Biomeによる静的検査と整形検査）
- `make lint.web`（Biomeによる静的検査）
- `make typecheck.web`（TypeScriptの型検査）
- `make format.web`（Biomeによる整形）

## 起動オプション

KataGoの追加設定は`--override-config`で指定する。配信するフロントエンドの成果物は`--web-dir`で指定する。起動時の指定方法は、[待受アドレスの変更](../../user/how-to/change-listen-address.md)、[対局の保存先の変更](../../user/how-to/change-data-directory.md)、[KataGoの実行ファイル・モデル・解析設定の指定](../../user/how-to/configure-katago.md)を参照する。

ログレベルは`debug`、`info`、`warn`、`error`のいずれかで、既定値は`info`である。アプリとMCPサーバーは環境変数`LOG_LEVEL`を参照する。`--log-level`を指定した場合はコマンドラインの値を優先する。
