# AI agentとの局面検討

[AI agentにMCPサーバーを登録](connect-ai-agent.md)し、Go Practiceを起動します。アプリで対局を開き、調べたい局面を選びます。AI agentに「画面で選んだ局面で、候補手の評価と読み筋を教えてください」と尋ねると、agentは`get_selected_position`で局面を取得できます。続けて解析や変化を求めるときは、この応答の`contextId`を使うと同じ局面を指定できます。ツールの一覧は[MCPツール](../reference/mcp-tools.md)にあります。
