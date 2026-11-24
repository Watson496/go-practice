# 配布版の起動

[GitHub Releases](https://github.com/Watson496/go-practice/releases)の「Assets」から、`go-practice-vX.Y.Z.tar.gz`をダウンロードします。`vX.Y.Z`の部分にはリリースのバージョンが入ります。起動前に[実行環境](../reference/requirements.md)に記載したKataGo、モデル、解析設定を用意します。

アーカイブを専用のディレクトリに展開します。ターミナルで`bin`と`web`が入っているディレクトリを開き、次のコマンドでサーバーを起動します。

```sh
./bin/go-practice serve
```

ブラウザで `http://127.0.0.1:8080` を開くと、対局画面が表示されます。KataGoが自動で検出されない場合は、[実行ファイル・モデル・解析設定を指定](configure-katago.md)して起動します。

配布物がまだ公開されていないバージョンを試す場合は、[本番用のビルド](../../developer/how-to/build.md)で成果物を作り、リポジトリのルートで同じコマンドを実行します。
