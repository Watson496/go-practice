# GitHub Releaseでの配布

配布物にはGo実行ファイルとビルド済みのWeb画面を含める。

## 配布物の作成

配布するバージョンは`internal/version/version.go`の`Current`で管理する。バージョンの更新をmainに取り込み、配布対象のコミットでCIが通過したことを確認する。[実行環境](../../user/reference/requirements.md)を満たすMacに[ビルド環境](../reference/build-environment.md)を用意し、対象コミットをチェックアウトする。`vX.Y.Z`を`Current`に合わせ、リポジトリのルートで次を実行する。

```sh
tag=vX.Y.Z
pnpm --dir web install --frozen-lockfile
make build
mkdir -p bin/release
tar -czf "bin/release/go-practice-${tag}.tar.gz" bin/go-practice web/dist
```

`bin/release/`に`go-practice-vX.Y.Z.tar.gz`が生成される。公開前にアーカイブを展開し、[利用者向けの起動手順](../../user/how-to/start-app.md)に沿って起動する。画面が開き、着手するとKataGoが応手することを確認する。

## Releaseの公開

配布物を作成したときと同じ`tag`を使う。次のコマンドはチェックアウト中のコミットをタグの対象とし、アーカイブを添付した下書きを作成する。

```sh
gh release create "$tag" \
    "bin/release/go-practice-${tag}.tar.gz" \
    --target "$(git rev-parse HEAD)" --title "$tag" --draft
```

GitHubのReleases画面で対象コミットと添付ファイルを確認し、変更点を記載して公開する。公開後にアーカイブをダウンロードできることを確認する。
