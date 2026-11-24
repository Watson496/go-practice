# AI agentへのMCPサーバーの登録

Go PracticeはMCPの標準入出力サーバーを備えています。MCPに対応するAI agentへ登録すると、表示中の局面を共有し、その局面について質問できます。AI agentは局面の解析と棋譜編集に加え、対局の作成・操作・読み書き・管理を行えます。

次の例では、`/path/to/go-practice`を、配布版を展開したディレクトリの絶対パスに置き換えます。MCPサーバーの登録形式はクライアントごとに異なります。設定する場所や手順は、利用するクライアントの説明に従ってください。

```json
{
  "command": "/path/to/go-practice/bin/go-practice",
  "args": ["mcp", "--server-url", "http://127.0.0.1:8080"]
}
```

AI agentを利用する間は、Go Practiceを`http://127.0.0.1:8080`で起動しておきます。MCPサーバーを登録した後は、AI agentとの新しい対話を開きます。その対話では、Go Practiceが公開するツールを利用できます。
