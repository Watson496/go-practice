# 本番用のビルド

[ビルド環境](../reference/build-environment.md)を用意し、リポジトリのルートでコマンドを実行する。

Goの実行ファイルと本番用フロントエンドをビルドする。

```sh
make setup
make build
```

Goの実装だけを変更した場合は、`make build.server`で実行ファイルを更新できる。フロントエンドだけをビルドする場合は、`make build.web`を実行する。
