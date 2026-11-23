package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go-practice/internal/api"
	"go-practice/internal/game"
	"go-practice/internal/katago"
)

type failedMCPOutput struct{}

func (failedMCPOutput) Write([]byte) (int, error) {
	return 0, fmt.Errorf("出力できません")
}

type failedMCPInput struct{}

func (failedMCPInput) Read([]byte) (int, error) {
	return 0, errors.New("秘密の入力エラー")
}

type gatedFailedMCPOutput struct {
	started chan struct{}
	allow   chan struct{}
}

type cancellableMCPAI struct {
	started   chan struct{}
	stopped   chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
}

type mcpProtocolSession struct {
	input   *io.PipeWriter
	output  *io.PipeReader
	decoder *json.Decoder
	done    chan error
}

type controlledClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *controlledClock) current() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *controlledClock) advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

func startMCPProtocolSession(t *testing.T, serverURL string) *mcpProtocolSession {
	return startMCPProtocolSessionWithClock(t, serverURL, time.Now)
}

func startMCPProtocolSessionWithClock(t *testing.T, serverURL string, now func() time.Time) *mcpProtocolSession {
	t.Helper()
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- run(inputReader, outputWriter, io.Discard, []string{"--server-url", serverURL}, now)
		_ = outputWriter.Close()
	}()
	session := &mcpProtocolSession{input: inputWriter, output: outputReader, decoder: json.NewDecoder(bufio.NewReader(outputReader)), done: done}
	t.Cleanup(func() {
		_ = inputWriter.Close()
		_ = outputReader.Close()
	})
	return session
}

func (session *mcpProtocolSession) write(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(session.input, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

func (session *mcpProtocolSession) read(t *testing.T) rpcTestResponse {
	t.Helper()
	var response rpcTestResponse
	if err := session.decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func (session *mcpProtocolSession) initialize(t *testing.T) {
	t.Helper()
	session.write(t, `{"jsonrpc":"2.0","id":"初期化","method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"検査用","version":"1"}}}`)
	response := session.read(t)
	if response.Error != nil || string(response.ID) != `"初期化"` {
		t.Fatalf("初期化応答 = %+v", response)
	}
	session.write(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
}

func (session *mcpProtocolSession) close(t *testing.T) {
	t.Helper()
	if err := session.input.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-session.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MCPサーバーが終了しない")
	}
}

func TestMCPツールは20回の連続呼出後に毎秒1回ずつ回復する(t *testing.T) {
	app := newMCPAPI(t)
	clock := &controlledClock{now: time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)}
	session := startMCPProtocolSessionWithClock(t, app.URL, clock.current)
	session.initialize(t)
	for index := 1; index <= 20; index++ {
		session.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"list_games","arguments":{}}}`, index))
		response := session.read(t)
		if response.Error != nil || bytes.Contains(response.Result, []byte(`"isError":true`)) {
			t.Fatalf("%d回目の応答 = %+v", index, response)
		}
	}
	session.write(t, `{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"list_games","arguments":{}}}`)
	rejected := session.read(t)
	if !bytes.Contains(rejected.Result, []byte(`"isError":true`)) {
		t.Fatalf("21回目の応答 = %s", rejected.Result)
	}
	clock.advance(2 * time.Second / 3)
	session.write(t, `{"jsonrpc":"2.0","id":22,"method":"tools/call","params":{"name":"list_games","arguments":{}}}`)
	stillRejected := session.read(t)
	if !bytes.Contains(stillRejected.Result, []byte(`"isError":true`)) {
		t.Fatalf("3分の2秒後の応答 = %+v", stillRejected)
	}
	clock.advance(time.Second - 2*time.Second/3)
	session.write(t, `{"jsonrpc":"2.0","id":23,"method":"tools/call","params":{"name":"list_games","arguments":{}}}`)
	recovered := session.read(t)
	if recovered.Error != nil || bytes.Contains(recovered.Result, []byte(`"isError":true`)) {
		t.Fatalf("1秒後の応答 = %+v", recovered)
	}
	session.close(t)
}

func TestMCPツールは引数の拒否も呼出回数に含める(t *testing.T) {
	app := newMCPAPI(t)
	session := startMCPProtocolSession(t, app.URL)
	session.initialize(t)
	for index := 1; index < 20; index++ {
		session.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"list_games","arguments":{}}}`, index))
		response := session.read(t)
		if response.Error != nil || bytes.Contains(response.Result, []byte(`"isError":true`)) {
			t.Fatalf("%d回目の応答 = %+v", index, response)
		}
	}
	session.write(t, `{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"list_games","arguments":{"unknown":true}}}`)
	invalid := session.read(t)
	if invalid.Error != nil || !bytes.Contains(invalid.Result, []byte(`"isError":true`)) {
		t.Fatalf("引数を拒否した応答 = %+v", invalid)
	}
	session.write(t, `{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"list_games","arguments":{}}}`)
	rejected := session.read(t)
	if !bytes.Contains(rejected.Result, []byte(`"isError":true`)) {
		t.Fatalf("引数の拒否に続く応答 = %s", rejected.Result)
	}
	session.close(t)
}

func TestMCPツールは初期化前に拒否した呼出も回数に含める(t *testing.T) {
	app := newMCPAPI(t)
	session := startMCPProtocolSession(t, app.URL)
	for index := 1; index <= 20; index++ {
		session.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"list_games","arguments":{}}}`, index))
		response := session.read(t)
		if response.Error == nil || response.Error.Code != -32600 {
			t.Fatalf("初期化前の%d回目の応答 = %+v", index, response)
		}
	}
	session.initialize(t)
	session.write(t, `{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"list_games","arguments":{}}}`)
	rejected := session.read(t)
	if !bytes.Contains(rejected.Result, []byte(`"isError":true`)) {
		t.Fatalf("初期化前の呼び出しに続く応答 = %s", rejected.Result)
	}
	session.close(t)
}

func (ai *cancellableMCPAI) AnalyzePosition(ctx context.Context, snapshot game.Snapshot, visits int) (katago.Summary, error) {
	ai.startOnce.Do(func() { close(ai.started) })
	<-ctx.Done()
	ai.stopOnce.Do(func() { close(ai.stopped) })
	return katago.Summary{}, ctx.Err()
}

func (*cancellableMCPAI) AnalysisVersion() string { return "MCP取り消し検査" }

func newCancellableMCPAPI(t *testing.T) (*httptest.Server, string, *cancellableMCPAI) {
	t.Helper()
	ai := &cancellableMCPAI{started: make(chan struct{}), stopped: make(chan struct{})}
	backend, err := api.NewPersistentServer(t.TempDir(), ai)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(backend.Handler())
	t.Cleanup(app.Close)
	created := callAPIJSON(t, app.Client(), http.MethodPost, app.URL+"/api/games", `{"boardSize":9}`, http.StatusCreated)
	var state struct {
		Game struct {
			ID string `json:"id"`
		} `json:"game"`
	}
	if err := json.Unmarshal(created, &state); err != nil {
		t.Fatal(err)
	}
	callAPIJSON(t, app.Client(), http.MethodPost, app.URL+"/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`, http.StatusNoContent)
	return app, state.Game.ID, ai
}

func (output gatedFailedMCPOutput) Write([]byte) (int, error) {
	close(output.started)
	<-output.allow
	return 0, fmt.Errorf("出力できません")
}

func Test実行中のツールを取り消しても稼働確認へ応答する(t *testing.T) {
	app, gameID, ai := newCancellableMCPAPI(t)

	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(inputReader, outputWriter, io.Discard, []string{"--server-url", app.URL})
		_ = outputWriter.Close()
	}()
	t.Cleanup(func() {
		_ = inputWriter.Close()
		_ = outputReader.Close()
	})
	decoder := json.NewDecoder(bufio.NewReader(outputReader))
	write := func(line string) {
		t.Helper()
		if _, err := io.WriteString(inputWriter, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	var initialized rpcTestResponse
	if err := decoder.Decode(&initialized); err != nil || string(initialized.ID) != "1" || initialized.Error != nil {
		t.Fatalf("初期化応答 = %+v、エラー = %v", initialized, err)
	}
	write(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	write(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"analyze_position","arguments":{"contextId":"` + gameID + `:root"}}}`)
	select {
	case <-ai.started:
	case <-time.After(time.Second):
		t.Fatal("ツール呼び出しが始まらない")
	}
	write(`{"jsonrpc":"2.0","id":4,"method":"tools/list"}`)
	write(`{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	readyResponses := make(chan rpcTestResponse, 2)
	go func() {
		for range 2 {
			var value rpcTestResponse
			_ = decoder.Decode(&value)
			readyResponses <- value
		}
	}()
	seen := make(map[string]bool)
	for range 2 {
		select {
		case response := <-readyResponses:
			if response.Error != nil {
				t.Fatalf("並行要求の応答 = %+v", response)
			}
			seen[string(response.ID)] = true
		case <-time.After(time.Second):
			t.Fatal("実行中のツール呼び出しが読み取り要求を妨げた")
		}
	}
	if !seen["3"] || !seen["4"] {
		t.Fatalf("並行要求の応答ID = %v", seen)
	}
	write(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2.0,"reason":"検証を終了","_meta":{"trace":"取り消し"}}}`)
	select {
	case <-ai.stopped:
	case <-time.After(time.Second):
		t.Fatal("取り消し通知でAPI要求が停止しない")
	}
	_ = inputWriter.Close()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("MCPサーバーが終了しない")
	}
	var unexpected rpcTestResponse
	if err := decoder.Decode(&unexpected); err == nil {
		t.Fatalf("取り消した要求へ応答した：%+v", unexpected)
	}
}

func Test入力の終了時に実行中の要求を取り消す(t *testing.T) {
	app, gameID, ai := newCancellableMCPAPI(t)

	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(inputReader, outputWriter, io.Discard, []string{"--server-url", app.URL})
		_ = outputWriter.Close()
	}()
	t.Cleanup(func() {
		_ = inputWriter.Close()
		_ = outputReader.Close()
	})
	decoder := json.NewDecoder(bufio.NewReader(outputReader))
	write := func(line string) {
		t.Helper()
		if _, err := io.WriteString(inputWriter, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	var initialized rpcTestResponse
	if err := decoder.Decode(&initialized); err != nil || initialized.Error != nil {
		t.Fatalf("初期化応答 = %+v、エラー = %v", initialized, err)
	}
	write(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	write(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"analyze_position","arguments":{"contextId":"` + gameID + `:root"}}}`)
	select {
	case <-ai.started:
	case <-time.After(time.Second):
		t.Fatal("要求が始まらない")
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ai.stopped:
	case <-time.After(time.Second):
		t.Fatal("入力が終了しても実行中の要求が取り消されない")
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("MCPサーバーが終了しない")
	}
}

func Test標準入力の終了前に受信した要求をすべて処理する(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping","params":{}}`,
	}, "\n") + "\n"
	for count := 0; count < 20; count++ {
		var output bytes.Buffer
		if err := Run(io.NopCloser(strings.NewReader(input)), &output, io.Discard, nil); err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(&output)
		ids := make(map[string]bool)
		for range 3 {
			var response rpcTestResponse
			if err := decoder.Decode(&response); err != nil || response.Error != nil {
				t.Fatalf("%d回目の応答 = %+v、解析エラー = %v、出力 = %s", count+1, response, err, output.String())
			}
			ids[string(response.ID)] = true
		}
		for _, id := range []string{"1", "2", "3"} {
			if !ids[id] {
				t.Fatalf("%d回目にID %sの応答がありません：%s", count+1, id, output.String())
			}
		}
		var extra rpcTestResponse
		if err := decoder.Decode(&extra); err != io.EOF {
			t.Fatalf("%d回目に余分な応答があります：%+v、解析エラー = %v", count+1, extra, err)
		}
	}
}

func Test空行を要求として扱わない(t *testing.T) {
	var output bytes.Buffer
	if err := Run(strings.NewReader(" \n\t\n"), &output, io.Discard, []string{"--server-url", "http://127.0.0.1:8080"}); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("空行への応答 = %s", output.String())
	}
}

func TestAPIのURLを起動時に検証する(t *testing.T) {
	for _, value := range []string{"", "localhost:8080", "ftp://localhost", "http://", "http://localhost/?token=x", "http://user:pass@localhost"} {
		err := Run(strings.NewReader(""), io.Discard, io.Discard, []string{"--server-url", value})
		if err == nil {
			t.Fatalf("URL %q のエラー = %v", value, err)
		}
	}
}

func Testアプリのサーバー障害を誤りレベルで記録する(t *testing.T) {
	dataDir := t.TempDir()
	backend, err := api.NewPersistentServer(dataDir, mcpIntegrationAI{})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(backend.Handler())
	defer app.Close()
	if err := os.RemoveAll(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataDir, []byte("保存先ではないファイル"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"準備","method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"検証用","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_game","arguments":{"boardSize":9}}}`,
	}, "\n") + "\n"
	output, logs := runMCPTranscript(t, app.URL, input, "error")
	responses := decodeRPCResponses(t, output)
	if len(responses) != 2 {
		t.Fatalf("ツール実行エラー = %+v", responses)
	}
	_ = toolResponseText(t, responses[1], true)
	if !strings.Contains(logs, "Go Practice APIが処理に失敗しました") || !strings.Contains(logs, "status=500") {
		t.Fatalf("API障害のログ = %s", logs)
	}
}

func Test不正な初期化完了通知ではツールを利用可能にしない(t *testing.T) {
	app := newMCPAPI(t)
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"検証用","version":"1"}}}`,
		`{"method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","unexpected":true}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{"unexpected":true}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
	)
	if len(responses) != 3 {
		t.Fatalf("応答数 = %d、期待値 = 3", len(responses))
	}
	if responses[0].Error != nil {
		t.Fatalf("初期化応答 = %+v", responses[0])
	}
	if responses[1].Error == nil || responses[1].Error.Code != -32600 {
		t.Fatalf("不正な通知後のツール一覧応答 = %+v", responses[1])
	}
	if responses[2].Error != nil {
		t.Fatalf("正しい通知後のツール一覧応答 = %+v", responses[2])
	}
}

func Test要求IDには文字列または数値を受け付ける(t *testing.T) {
	app := newMCPAPI(t)
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":"文字列","method":"ping"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3.0,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1e3,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3.5,"method":"ping"}`,
	)
	valid := 0
	for _, response := range responses {
		if response.Error == nil && (string(response.ID) == `"文字列"` || string(response.ID) == "2" || string(response.ID) == "3.0" || string(response.ID) == "1e3" || string(response.ID) == "3.5") {
			valid++
		}
	}
	if valid != 5 {
		t.Fatalf("要求IDへの応答 = %+v", responses)
	}
}

func Test要求の型が不正でも判別できたIDを応答に含める(t *testing.T) {
	app := newMCPAPI(t)
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":2,"id":5,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":33,"method":1}`,
	)
	if len(responses) != 2 {
		t.Fatalf("応答数 = %d", len(responses))
	}
	for index, response := range responses {
		wantID := []string{"5", "33"}[index]
		if string(response.ID) != wantID || response.Error == nil || response.Error.Code != -32600 {
			t.Fatalf("%d番目の応答 = %+v", index+1, response)
		}
	}
}

func Test重複した要求IDは応答のIDに使わない(t *testing.T) {
	app := newMCPAPI(t)
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":7,"id":8,"method":"ping"}`,
	)
	if len(responses) != 1 || len(responses[0].ID) != 0 || responses[0].Error == nil || responses[0].Error.Code != -32600 {
		t.Fatalf("応答 = %+v", responses)
	}
}

func Test項目の型が不正なMCP要求を具体的に拒否する(t *testing.T) {
	app := newMCPAPI(t)
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"検証用","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"cursor":null}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"_meta":[]}}`,
		`{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"protocolVersion":123,"capabilities":{},"clientInfo":{"name":"検証用","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"検証用","version":"1","icons":[{"src":"https://example.com/icon.png","mimeType":null}]}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/list","params":{"cursor":1,"_meta":[]}}`,
	)
	if len(responses) != 6 || responses[0].Error != nil {
		t.Fatalf("初期化を含む応答 = %+v", responses)
	}
	for index, response := range responses[1:] {
		if response.Error == nil || response.Error.Code != -32602 {
			t.Fatalf("%d番目の型が不正な要求への応答 = %+v", index+1, response)
		}
	}
}

func Test初期化完了通知より前はツールを利用できない(t *testing.T) {
	app := newMCPAPI(t)
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"検証用","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/list"}`,
	)
	if len(responses) != 4 {
		t.Fatalf("応答数 = %d", len(responses))
	}
	if responses[0].Error == nil || responses[0].Error.Code != -32600 {
		t.Fatalf("初期化前のツール一覧 = %+v", responses[0])
	}
	if responses[1].Error != nil {
		t.Fatalf("初期化応答 = %+v", responses[1])
	}
	if responses[2].Error == nil || responses[2].Error.Code != -32600 {
		t.Fatalf("初期化完了通知前のツール一覧 = %+v", responses[2])
	}
	if responses[3].Error != nil || !bytes.Contains(responses[3].Result, []byte(`"tools"`)) {
		t.Fatalf("初期化完了後のツール一覧 = %+v", responses[3])
	}
}

func Test無効なツール要求も呼出頻度の枠を消費する(t *testing.T) {
	app := newMCPAPI(t)
	lines := make([]string, 0, 121)
	for index := 1; index <= 120; index++ {
		lines = append(lines, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"存在しないツール","arguments":{}}}`, index))
	}
	lines = append(lines, `{"jsonrpc":"2.0","id":121,"method":"tools/call","params":{"name":"list_games","arguments":{}}}`)
	responses := runMCPRequests(t, app.URL, lines...)
	if len(responses) != len(lines) {
		t.Fatalf("応答数 = %d", len(responses))
	}
	for _, response := range responses[:20] {
		if response.Error == nil || response.Error.Code != -32602 {
			t.Fatalf("制限前の未知のツールへの応答 = %+v", response)
		}
	}
	for _, response := range responses[20:] {
		_ = toolResponseText(t, response, true)
	}
}

func Test処理中の要求と同じ値の数値IDを使えない(t *testing.T) {
	app, gameID, ai := newCancellableMCPAPI(t)
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(inputReader, outputWriter, io.Discard, []string{"--server-url", app.URL})
		_ = outputWriter.Close()
	}()
	t.Cleanup(func() {
		_ = inputWriter.Close()
		_ = outputReader.Close()
	})
	decoder := json.NewDecoder(bufio.NewReader(outputReader))
	write := func(line string) {
		t.Helper()
		if _, err := io.WriteString(inputWriter, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	var initialized rpcTestResponse
	if err := decoder.Decode(&initialized); err != nil || initialized.Error != nil {
		t.Fatalf("初期化応答 = %+v、エラー = %v", initialized, err)
	}
	write(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	write(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"analyze_position","arguments":{"contextId":"` + gameID + `:root"}}}`)
	select {
	case <-ai.started:
	case <-time.After(time.Second):
		t.Fatal("最初の要求が始まらない")
	}
	write(`{"jsonrpc":"2.0","id":4.0,"method":"tools/call","params":{"name":"list_games","arguments":{}}}`)
	var duplicate rpcTestResponse
	if err := decoder.Decode(&duplicate); err != nil || duplicate.Error == nil || duplicate.Error.Code != -32600 {
		t.Fatalf("処理中の数値IDと重複する要求への応答 = %+v、エラー = %v", duplicate, err)
	}
	if string(duplicate.ID) != "4.0" {
		t.Fatalf("重複した要求への応答ID = %s、期待値 = 4.0", duplicate.ID)
	}
	write(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":4}}`)
	if err := inputWriter.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("MCPサーバーが終了しない")
	}
}

func Test上限を超えたMCP要求を捨てて次の行を読み取る(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("12345\n次の要求\n"))
	if line, err := readMCPLine(reader, 4); !errors.Is(err, errMCPMessageTooLarge) || line != nil {
		t.Fatalf("過大な行の読取結果 = %q、エラー = %v", line, err)
	}
	line, err := readMCPLine(reader, 32)
	if err != nil || string(line) != "次の要求" {
		t.Fatalf("後続行の読取結果 = %q、エラー = %v", line, err)
	}
}

func Test不正なUTF8のJSONRPC要求を解析誤りとして拒否する(t *testing.T) {
	input := append([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"value":"`), 0xff)
	input = append(input, []byte("\"}}\n")...)
	var output bytes.Buffer
	if err := Run(bytes.NewReader(input), &output, io.Discard, nil); err != nil {
		t.Fatal(err)
	}
	var response rpcTestResponse
	if err := json.NewDecoder(&output).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != -32700 || len(response.ID) != 0 {
		t.Fatalf("応答 = %+v", response)
	}
}

func Test完了した要求IDを再利用できる(t *testing.T) {
	app := newMCPAPI(t)
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(inputReader, outputWriter, io.Discard, []string{"--server-url", app.URL})
		_ = outputWriter.Close()
	}()
	t.Cleanup(func() {
		_ = inputWriter.Close()
		_ = outputReader.Close()
	})
	decoder := json.NewDecoder(bufio.NewReader(outputReader))
	writeAndRead := func(line string) rpcTestResponse {
		t.Helper()
		if _, err := io.WriteString(inputWriter, line+"\n"); err != nil {
			t.Fatal(err)
		}
		var response rpcTestResponse
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	initialized := writeAndRead(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	if initialized.Error != nil {
		t.Fatalf("初期化応答 = %+v", initialized)
	}
	if _, err := io.WriteString(inputWriter, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"4", "4.0"} {
		response := writeAndRead(`{"jsonrpc":"2.0","id":` + id + `,"method":"ping"}`)
		if response.Error != nil || string(response.ID) != id {
			t.Fatalf("再利用した要求ID %sへの応答 = %+v", id, response)
		}
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("MCPサーバーが終了しない")
	}
}

func runMCPRequests(t *testing.T, serverURL string, lines ...string) []rpcTestResponse {
	t.Helper()
	autoInitialize := false
	for _, line := range lines {
		if strings.Contains(line, `"method":"initialize"`) {
			autoInitialize = false
			break
		}
		if strings.Contains(line, `"method":"tools/`) {
			autoInitialize = true
		}
	}
	if autoInitialize {
		lines = append([]string{
			`{"jsonrpc":"2.0","id":"準備","method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"検証用","version":"1"}}}`,
			`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		}, lines...)
	}
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(inputReader, outputWriter, io.Discard, []string{"--server-url", serverURL})
		_ = outputWriter.Close()
	}()
	t.Cleanup(func() {
		_ = inputWriter.Close()
		_ = outputReader.Close()
	})
	decoder := json.NewDecoder(bufio.NewReader(outputReader))
	responses := make([]rpcTestResponse, 0, len(lines))
	for _, line := range lines {
		if _, err := io.WriteString(inputWriter, line+"\n"); err != nil {
			t.Fatal(err)
		}
		expectsResponse := false
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &fields) != nil {
			expectsResponse = true
		} else if _, hasMethod := fields["method"]; !hasMethod {
			if !isJSONRPCResponse([]byte(line)) {
				expectsResponse = true
			}
		} else if _, hasID := fields["id"]; hasID {
			expectsResponse = true
		}
		if !expectsResponse {
			continue
		}
		var response rpcTestResponse
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("MCPサーバーが終了しない")
	}
	if autoInitialize {
		for index, response := range responses {
			if string(response.ID) == `"準備"` {
				return append(responses[:index], responses[index+1:]...)
			}
		}
		t.Fatal("初期化応答がありません")
	}
	return responses
}

func toolResponseText(t *testing.T, response rpcTestResponse, wantError bool) string {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("JSON-RPCのエラー = %+v", response.Error)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.IsError != wantError || len(result.Content) != 1 {
		t.Fatalf("ツールの実行結果 = %+v", result)
	}
	return result.Content[0].Text
}

func Test付随情報と利用元表示名を標準入出力で扱う(t *testing.T) {
	app := newMCPAPI(t)
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1","title":"検証client"},"_meta":{"progressToken":"初期化","vendor":{"enabled":true}}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping","params":{"_meta":{"trace":"ping"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"_meta":{"trace":"list"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_games","arguments":{},"_meta":{"trace":"call"}}}`,
	)
	if len(responses) != 4 {
		t.Fatalf("応答数 = %d", len(responses))
	}
	for index, response := range responses {
		if string(response.ID) != string(rune('1'+index)) || response.Error != nil {
			t.Fatalf("%d番目の_meta付き要求 = %+v", index+1, response)
		}
	}
	if text := toolResponseText(t, responses[3], false); strings.TrimSpace(text) != "[]" {
		t.Fatalf("「list_games」の実行結果 = %s", text)
	}
}

func Test不正な付随情報を標準入出力で拒否する(t *testing.T) {
	app := newMCPAPI(t)
	inputs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping","params":{"_meta":[]}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"_meta":"x"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_games","arguments":{},"_meta":{"x":1,"x":2}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1","icons":null}}}`,
	}
	responses := runMCPRequests(t, app.URL, inputs...)
	if len(responses) != len(inputs)-1 {
		t.Fatalf("応答数 = %d", len(responses))
	}
	for index, response := range responses {
		if string(response.ID) != string(rune('1'+index)) {
			t.Fatalf("%d番目の要求ID = %+v", index+1, response)
		}
		if index == 0 {
			if response.Error != nil {
				t.Fatalf("初期化応答 = %+v", response)
			}
			continue
		}
		if response.Error == nil || response.Error.Code != -32602 {
			t.Fatalf("%d番目の不正な_metaへの応答 = %+v", index+1, response)
		}
	}
}

func Test不正な要求を拒否する(t *testing.T) {
	app := newMCPAPI(t)
	inputs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping","params":[]}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/list","params":{"cursor":1}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/list","params":{"cursor":"未発行"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":[]}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"create_game","arguments":{"boardSize":9,"komi":null}}}`,
	}
	responses := runMCPRequests(t, app.URL, inputs...)
	if len(responses) != len(inputs)-1 {
		t.Fatalf("応答数 = %d", len(responses))
	}
	if responses[0].Error != nil {
		t.Fatalf("初期化応答 = %+v", responses[0])
	}
	for index, response := range responses[1:6] {
		if response.Error == nil {
			t.Fatalf("%d番目の不正な要求にエラーがない", index+2)
		}
	}
	_ = toolResponseText(t, responses[6], true)
}

func Test空のツール引数を拒否する(t *testing.T) {
	backend, err := api.NewPersistentServer(t.TempDir(), mcpIntegrationAI{})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(backend.Handler())
	defer app.Close()
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_game","arguments":{"gameId":""}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"navigate_game","arguments":{"gameId":"g1","nodeId":""}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"import_sgf","arguments":{"contents":""}}}`,
	)
	for _, response := range responses {
		_ = toolResponseText(t, response, true)
	}
}

func Test空の削除対象を拒否する(t *testing.T) {
	app := newMCPAPI(t)
	created := callToolThroughRun(t, app.URL, "create_game", `{"boardSize":9}`)
	var state struct {
		Game struct {
			ID string `json:"id"`
		} `json:"game"`
	}
	if err := json.Unmarshal([]byte(created), &state); err != nil || state.Game.ID == "" {
		t.Fatalf("作成した対局を読み取れません：%v", err)
	}
	variation := callToolThroughRun(t, app.URL, "play_variation", `{"gameId":"`+state.Game.ID+`","coordinate":"D4"}`)
	var variationState struct {
		Game struct {
			CurrentNodeID string `json:"currentNodeId"`
		} `json:"game"`
	}
	if err := json.Unmarshal([]byte(variation), &variationState); err != nil || variationState.Game.CurrentNodeID == "" {
		t.Fatalf("作成した変化を読み取れません：%v", err)
	}
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_variation","arguments":{"gameId":"`+state.Game.ID+`","nodeId":"`+variationState.Game.CurrentNodeID+`","expectedNodeIds":[]}}}`,
	)
	if len(responses) != 1 {
		t.Fatalf("空の削除対象への応答 = %+v", responses)
	}
	_ = toolResponseText(t, responses[0], true)
}

func Test現在版の利用元情報と作業情報を扱う(t *testing.T) {
	app := newMCPAPI(t)
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","title":"検証用","version":"1","description":"統合テスト","websiteUrl":"https://example.com/client","icons":[{"src":"data:image/png;base64,AA==","mimeType":"image/png","sizes":["48x48"],"theme":"dark"}]}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_games","arguments":{},"task":{"ttl":"未対応の値","拡張":{"enabled":true}}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_games","arguments":{},"task":null}}`,
	)
	if len(responses) != 3 || responses[0].Error != nil || string(responses[0].ID) != "1" {
		t.Fatalf("現在の通信規約版に沿う利用元情報への応答 = %+v", responses)
	}
	for _, response := range responses[1:] {
		if text := toolResponseText(t, response, false); strings.TrimSpace(text) != "[]" {
			t.Fatalf("未宣言のtask情報付き同期実行 = %s", text)
		}
	}
}

func Testツール誤りにAPIの理由を返す(t *testing.T) {
	app := newMCPAPI(t)
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_game","arguments":{"gameId":"存在しない対局"}}}`,
	)
	_ = toolResponseText(t, responses[0], true)
}

func Test受信した応答へ応答しない(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`,
		`{"jsonrpc":"2.0","id":2,"error":{"code":-32603,"message":"失敗"}}`,
		`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"解析失敗"}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := Run(strings.NewReader(input), &output, io.Discard, nil); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("受信した応答へさらに応答した：%s", output.String())
	}
}

func Test不正な通知へ応答しない(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","method":"tools/call","params":[]}`,
		`{"jsonrpc":"2.0","method":"unknown","extra":true}`,
		`{"jsonrpc":"2.0","id":"確認","method":"ping"}`,
	}, "\n") + "\n"
	output, _ := runMCPTranscript(t, "", input, "")
	responses := decodeRPCResponses(t, output)
	if len(responses) != 1 || string(responses[0].ID) != `"確認"` || responses[0].Error != nil {
		t.Fatalf("通知を除いた応答 = %+v", responses)
	}
}

func Test未知のツールを引数より先に報告する(t *testing.T) {
	app := newMCPAPI(t)
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"存在しないツール","arguments":null}}`,
	)
	if len(responses) != 1 || responses[0].Error == nil || responses[0].Error.Code != -32602 {
		t.Fatalf("未知のツールへの応答 = %+v", responses)
	}
}

func Testツール定義に共有入力制約を記載する(t *testing.T) {
	app := newMCPAPI(t)
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
	)
	if len(responses) != 1 || responses[0].Error != nil {
		t.Fatalf("ツール一覧への応答 = %+v", responses)
	}
	var listed struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Properties map[string]struct {
					Description string          `json:"description"`
					Default     json.RawMessage `json:"default"`
					MinLength   int             `json:"minLength"`
					MaxLength   int             `json:"maxLength"`
				} `json:"properties"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(responses[0].Result, &listed); err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		for name, property := range tool.InputSchema.Properties {
			if property.Description == "" {
				t.Fatalf("ツール%sの引数%sに説明がない", tool.Name, name)
			}
		}
		switch tool.Name {
		case "get_analysis":
			if got := string(tool.InputSchema.Properties["visits"].Default); got != "100" {
				t.Fatalf("局面解析の既定探索数 = %s", got)
			}
		case "update_node":
			if got := tool.InputSchema.Properties["note"].MaxLength; got != 0 {
				t.Fatalf("正規化前のメモに最大文字数が設定されています：%d", got)
			}
		case "show_variation":
			if got := tool.InputSchema.Properties["title"].MaxLength; got != 0 {
				t.Fatalf("正規化前の変化名に最大文字数が設定されています：%d", got)
			}
		}
	}
}

func Test共有文字数制限を検査する(t *testing.T) {
	app := newMCPAPI(t)
	created := callToolThroughRun(t, app.URL, "create_game", `{"boardSize":9}`)
	var state struct {
		Game struct {
			ID string `json:"id"`
		} `json:"game"`
	}
	if err := json.Unmarshal([]byte(created), &state); err != nil || state.Game.ID == "" {
		t.Fatalf("作成した対局を読み取れません：%v", err)
	}
	longName, _ := json.Marshal(strings.Repeat("名", 80+1))
	longNote, _ := json.Marshal(strings.Repeat("メ", 4000+1))
	longTitle, _ := json.Marshal(strings.Repeat("変", 80+1))
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":0,"method":"tools/call","params":{"name":"rename_game","arguments":{"gameId":"`+state.Game.ID+`","name":"","expectedName":""}}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rename_game","arguments":{"gameId":"%s","name":%s,"expectedName":""}}}`, state.Game.ID, longName),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"update_node","arguments":{"gameId":"%s","nodeId":"root","note":%s,"important":false,"expectedNote":"","expectedImportant":false}}}`, state.Game.ID, longNote),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"show_variation","arguments":{"contextId":"%s:root","moves":["D4"],"title":%s}}}`, state.Game.ID, longTitle),
	)
	if len(responses) != 4 {
		t.Fatalf("応答 = %+v", responses)
	}
	for _, response := range responses {
		_ = toolResponseText(t, response, true)
	}
}

func Test文章を正規化してから共有文字数制限を検査する(t *testing.T) {
	app := newMCPAPI(t)
	created := callToolThroughRun(t, app.URL, "create_game", `{"boardSize":9}`)
	var createdState struct {
		Game struct {
			ID string `json:"id"`
		} `json:"game"`
	}
	if err := json.Unmarshal([]byte(created), &createdState); err != nil {
		t.Fatal(err)
	}
	gameID := createdState.Game.ID
	name, _ := json.Marshal(strings.Repeat("名", 80) + " ")
	note, _ := json.Marshal(strings.Repeat("\r\n", 2001))
	title, _ := json.Marshal(strings.Repeat("変", 80) + " ")
	_ = callToolThroughRun(t, app.URL, "rename_game", fmt.Sprintf(`{"gameId":"%s","name":%s,"expectedName":""}`, gameID, name))
	_ = callToolThroughRun(t, app.URL, "update_node", fmt.Sprintf(`{"gameId":"%s","nodeId":"root","note":%s,"important":false,"expectedNote":"","expectedImportant":false}`, gameID, note))
	callAPIJSON(t, app.Client(), http.MethodPost, app.URL+"/api/agent/selection", `{"gameId":"`+gameID+`","nodeId":"root"}`, http.StatusNoContent)
	_ = callToolThroughRun(t, app.URL, "show_variation", fmt.Sprintf(`{"contextId":"%s:root","moves":["D4"],"title":%s}`, gameID, title))
	gameText := callToolThroughRun(t, app.URL, "get_game", `{"gameId":"`+gameID+`"}`)
	var state struct {
		Name string        `json:"name"`
		Game game.Snapshot `json:"game"`
	}
	if err := json.Unmarshal([]byte(gameText), &state); err != nil {
		t.Fatal(err)
	}
	if state.Name != strings.Repeat("名", 80) {
		t.Fatalf("正規化した対局名 = %q", state.Name)
	}
	var noteText string
	for _, property := range state.Game.RootProperties {
		if property.Ident == "C" && len(property.Values) == 1 {
			noteText = property.Values[0]
		}
	}
	if noteText != strings.Repeat("\n", 2001) {
		t.Fatalf("正規化したメモの文字数 = %d", len([]rune(noteText)))
	}
	if len(state.Game.TreeNodes) != 2 || state.Game.TreeNodes[1].VariationTitle != strings.Repeat("変", 80) {
		t.Fatalf("正規化した変化名を持つ局面 = %+v", state.Game.TreeNodes)
	}
}

func Testユーザーの入力誤りをサーバー障害として記録しない(t *testing.T) {
	app := newMCPAPI(t)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"準備","method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"検証用","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_game","arguments":{"boardSize":8}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_games","arguments":{}}}`,
	}, "\n") + "\n"
	output, logs := runMCPTranscript(t, app.URL, input, "error")
	responses := decodeRPCResponses(t, output)
	if len(responses) != 3 {
		t.Fatalf("応答数 = %d", len(responses))
	}
	_ = toolResponseText(t, responses[1], true)
	_ = toolResponseText(t, responses[2], false)
	if logs != "" {
		t.Fatalf("ユーザーの入力誤りをサーバー障害として記録した：%s", logs)
	}
}

func Test現在版の利用元機能を検証する(t *testing.T) {
	app := newMCPAPI(t)
	valid := `{"experimental":{"com.example/feature":{}},"roots":{"listChanged":true,"com.example/label":"利用元"},"sampling":{"context":{},"tools":{}},"elicitation":{"form":{},"url":{}},"tasks":{"list":{},"cancel":{},"requests":{"sampling":{"createMessage":{}},"elicitation":{"create":{}}},"com.example/value":null},"com.example/extension":true}`
	tests := []struct {
		name       string
		capability string
	}{
		{name: "有効な機能情報", capability: valid},
		{name: "実験的機能", capability: `{"experimental":{"example":true}}`},
		{name: "参照先一覧", capability: `{"roots":{"listChanged":"yes"}}`},
		{name: "生成要求", capability: `{"sampling":null}`},
		{name: "ユーザーへの入力要求", capability: `{"elicitation":{"form":false}}`},
		{name: "作業管理", capability: `{"tasks":{"list":false}}`},
		{name: "複数の不正な機能情報", capability: `{"sampling":null,"elicitation":{"form":false}}`},
	}
	for index, test := range tests {
		input := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":%s,"clientInfo":{"name":"test","version":"1"}}}`, index+1, test.capability)
		responses := runMCPRequests(t, app.URL, input)
		if len(responses) != 1 {
			t.Fatalf("%sへの応答 = %+v", test.name, responses)
		}
		response := responses[0]
		if index == 0 {
			if response.Error != nil {
				t.Fatalf("有効な機能情報への応答 = %+v", response)
			}
			continue
		}
		if response.Error == nil || response.Error.Code != -32602 {
			t.Fatalf("%sへの応答 = %+v", test.name, response)
		}
	}
}

func Testツール引数は省略できるが空値を拒否する(t *testing.T) {
	app := newMCPAPI(t)
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_games"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_games","arguments":null}}`,
	)
	if len(responses) != 2 || responses[0].Error != nil || responses[1].Error == nil || responses[1].Error.Code != -32602 {
		t.Fatalf("引数の省略とnullへの応答：%+v", responses)
	}
}

func Test空値を拒否する(t *testing.T) {
	app := newMCPAPI(t)
	inputs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_game","arguments":{"boardSize":9,"komi":null}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"update_node","arguments":{"gameId":"g1","nodeId":"root","note":"","important":null,"expectedNote":"","expectedImportant":false}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"analyze_position","arguments":{"contextId":"g1:root","sequence":null}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_game","arguments":{"gameId":null}}}`,
	}
	responses := runMCPRequests(t, app.URL, inputs...)
	if len(responses) != len(inputs) {
		t.Fatalf("応答数 = %d", len(responses))
	}
	for index, response := range responses {
		if string(response.ID) != string(rune('1'+index)) {
			t.Fatalf("%d番目のnull引数への応答 = %+v", index+1, response)
		}
		_ = toolResponseText(t, response, true)
	}
}

func callAPIJSON(t *testing.T, client *http.Client, method, url, body string, wantStatus int) []byte {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s 状態コード = %d、本文 = %s", method, url, response.StatusCode, data)
	}
	return data
}

func Test標準入出力から変更と局面検討の実APIを呼ぶ(t *testing.T) {
	backend, err := api.NewPersistentServer(t.TempDir(), mcpIntegrationAI{})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(backend.Handler())
	defer app.Close()
	created := callAPIJSON(t, app.Client(), http.MethodPost, app.URL+"/api/games", `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`, http.StatusCreated)
	var state struct {
		Game struct {
			ID string `json:"id"`
		} `json:"game"`
	}
	if err := json.Unmarshal(created, &state); err != nil || state.Game.ID == "" {
		t.Fatalf("対局作成 = %s、エラー = %v", created, err)
	}
	gameID := state.Game.ID
	callAPIJSON(t, app.Client(), http.MethodPost, app.URL+"/api/agent/selection", `{"gameId":"`+gameID+`","nodeId":"root"}`, http.StatusNoContent)
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"play_move","arguments":{"gameId":"`+gameID+`","coordinate":"E5"},"task":{"ttl":60000}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"request_ai_move","arguments":{"gameId":"`+gameID+`"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"show_variation","arguments":{"contextId":"`+gameID+`:root","moves":["C3"],"title":"Run経由"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"analyze_position","arguments":{"contextId":"`+gameID+`:root","maxVisits":7}}}`,
	)
	for index, response := range responses {
		if string(response.ID) != string(rune('1'+index)) {
			t.Fatalf("要求 IDと応答が対応しない：%+v", responses)
		}
		_ = toolResponseText(t, response, false)
	}
	gameText := callAPIJSON(t, app.Client(), http.MethodGet, app.URL+"/api/games/"+gameID, "", http.StatusOK)
	for _, expected := range []string{`"coordinate":"E5"`, `"coordinate":"D5"`, `"coordinate":"C3"`, `"variationTitle":"Run経由"`} {
		if !bytes.Contains(gameText, []byte(expected)) {
			t.Fatalf("Run経由の状態に%sがない：%s", expected, gameText)
		}
	}
	if text := toolResponseText(t, responses[3], false); !strings.Contains(text, `"visits":7`) {
		t.Fatalf("analyze_position = %s", text)
	}
}

func Test確認済みの部分木だけを削除する(t *testing.T) {
	backend, err := api.NewPersistentServer(t.TempDir(), mcpIntegrationAI{})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(backend.Handler())
	defer app.Close()
	created := callAPIJSON(t, app.Client(), http.MethodPost, app.URL+"/api/games", `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`, http.StatusCreated)
	var state struct {
		Game struct {
			ID            string `json:"id"`
			CurrentNodeID string `json:"currentNodeId"`
		} `json:"game"`
	}
	if err := json.Unmarshal(created, &state); err != nil {
		t.Fatal(err)
	}
	first := callAPIJSON(t, app.Client(), http.MethodPost, app.URL+"/api/games/"+state.Game.ID+"/variations", `{"coordinate":"E5"}`, http.StatusOK)
	if err := json.Unmarshal(first, &state); err != nil {
		t.Fatal(err)
	}
	firstNodeID := state.Game.CurrentNodeID
	second := callAPIJSON(t, app.Client(), http.MethodPost, app.URL+"/api/games/"+state.Game.ID+"/variations", `{"coordinate":"D5"}`, http.StatusOK)
	if err := json.Unmarshal(second, &state); err != nil {
		t.Fatal(err)
	}
	secondNodeID := state.Game.CurrentNodeID
	responses := runMCPRequests(t, app.URL,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_variation","arguments":{"gameId":"`+state.Game.ID+`","nodeId":"`+firstNodeID+`","expectedNodeIds":["`+firstNodeID+`"]}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"delete_variation","arguments":{"gameId":"`+state.Game.ID+`","nodeId":"`+firstNodeID+`","expectedNodeIds":["`+firstNodeID+`","`+secondNodeID+`"]}}}`,
	)
	if len(responses) != 2 {
		t.Fatalf("古い削除対象への応答 = %+v", responses)
	}
	_ = toolResponseText(t, responses[0], true)
	_ = toolResponseText(t, responses[1], false)
	game := callAPIJSON(t, app.Client(), http.MethodGet, app.URL+"/api/games/"+state.Game.ID, "", http.StatusOK)
	if bytes.Contains(game, []byte(firstNodeID)) || bytes.Contains(game, []byte(secondNodeID)) {
		t.Fatalf("削除後も変化が残っている：%s", game)
	}
}

func Test応答を書き込めなくなったら待機中の変更要求を実行しない(t *testing.T) {
	app := newMCPAPI(t)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create_game","arguments":{"boardSize":9}}}`,
	}, "\n") + "\n"
	inputReader, inputWriter := io.Pipe()
	output := gatedFailedMCPOutput{started: make(chan struct{}), allow: make(chan struct{})}
	finished := make(chan error, 1)
	go func() {
		finished <- Run(inputReader, output, io.Discard, []string{"--server-url", app.URL})
	}()
	if _, err := io.WriteString(inputWriter, input); err != nil {
		t.Fatal(err)
	}
	<-output.started
	close(output.allow)
	err := <-finished
	if err == nil {
		t.Fatalf("標準出力の失敗 = %v", err)
	}
	games := callAPIJSON(t, app.Client(), http.MethodGet, app.URL+"/api/games", "", http.StatusOK)
	if strings.TrimSpace(string(games)) != "[]" {
		t.Fatalf("応答不能になった後に作成された対局 = %s", games)
	}
}

func Test応答を書き込めなくなったら開いた入力を閉じて終了する(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	finished := make(chan error, 1)
	go func() {
		finished <- Run(inputReader, failedMCPOutput{}, io.Discard, nil)
	}()
	if _, err := io.WriteString(inputWriter, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatalf("標準出力の失敗 = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("標準出力の失敗後も標準入力を待ち続けました")
	}
	if _, err := io.WriteString(inputWriter, "\n"); err == nil {
		t.Fatal("標準出力の失敗後も標準入力が開いています")
	}
}

func Test標準入出力の内部エラー文言を表示しない(t *testing.T) {
	if err := Run(failedMCPInput{}, io.Discard, io.Discard, nil); err == nil {
		t.Fatalf("標準入力の失敗 = %v", err)
	}
	if err := Run(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`+"\n"), failedMCPOutput{}, io.Discard, nil); err == nil {
		t.Fatalf("標準出力の失敗 = %v", err)
	}
}

func Test標準入力の正常終了前に受信した要求へ応答する(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}` + "\n"
	for count := 0; count < 20; count++ {
		var output bytes.Buffer
		if err := Run(strings.NewReader(input), &output, io.Discard, nil); err != nil {
			t.Fatal(err)
		}
		var response rpcTestResponse
		if err := json.NewDecoder(&output).Decode(&response); err != nil || response.Error != nil || string(response.ID) != "1" {
			t.Fatalf("%d回目の応答 = %+v、解析エラー = %v、出力 = %s", count+1, response, err, output.String())
		}
	}
}
