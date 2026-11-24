# KataGoの実行ファイル・モデル・解析設定の指定

KataGoを自動検出できない場合は、`--katago`で実行ファイル、`--model`でモデル、`--config`で解析設定を指定します。例のパスを手元のファイルに置き換え、アーカイブを展開したディレクトリで実行します。

```sh
./bin/go-practice serve --katago /path/to/katago --model /path/to/model.bin.gz --config /path/to/analysis.cfg
```
