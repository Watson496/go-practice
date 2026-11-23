package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"go-practice/internal/api"
	"go-practice/internal/apicontract"
	"go-practice/internal/game"
	"go-practice/internal/katago"
)

func newMCPAPI(t *testing.T) *httptest.Server {
	t.Helper()
	backend, err := api.NewPersistentServer(t.TempDir(), mcpIntegrationAI{})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(backend.Handler())
	t.Cleanup(app.Close)
	return app
}

func Testツール一覧と表示中の局面を取得する(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	backend, err := api.NewPersistentServer(t.TempDir(), mcpIntegrationAI{})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(backend.Handler())
	defer app.Close()
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
	contextID := state.Game.ID + ":root"
	nodeID := variationState.Game.CurrentNodeID
	selection := strings.NewReader(`{"gameId":"` + state.Game.ID + `","nodeId":"root"}`)
	selectionRequest, err := http.NewRequest(http.MethodPost, app.URL+"/api/agent/selection", selection)
	if err != nil {
		t.Fatal(err)
	}
	selectionRequest.Header.Set("Content-Type", "application/json")
	selectionResponse, err := app.Client().Do(selectionRequest)
	if err != nil || selectionResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("表示中の局面の通知 = %v、エラー = %v", selectionResponse, err)
	}
	_ = selectionResponse.Body.Close()
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"select_position","arguments":{"contextId":"` + contextID + `","nodeId":"` + nodeID + `"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_selected_position","arguments":{}}}`,
	}, "\n") + "\n"
	result, logs := runMCPTranscript(t, app.URL, input, "debug")
	for _, expected := range []string{`"protocolVersion":"2025-11-25"`, `"name":"get_selected_position"`, `"name":"select_position"`, `"name":"analyze_position"`, `\"contextId\":\"` + state.Game.ID + `:`, `\"currentNodeId\":\"` + nodeID + `\"`} {
		if !strings.Contains(result, expected) {
			t.Fatalf("%sに%sがない", result, expected)
		}
	}
	if logs == "" {
		t.Fatal("デバッグログが空です")
	}
}

func Test未対応の通信規約版には対応版を返す(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2099-01-01","capabilities":{},"clientInfo":{"name":"verification","version":"1"}}}` + "\n"
	output, _ := runMCPTranscript(t, "", input, "")
	if !strings.Contains(output, `"protocolVersion":"2025-11-25"`) {
		t.Fatalf("対応する通信規約版を返していない：%s", output)
	}
}

func Test不正な初期化とツール一覧要求を拒否する(t *testing.T) {
	invalidInitialization, _ := runMCPTranscript(t, "", `{"jsonrpc":"2.0","id":9,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":null,"clientInfo":{"name":"test","version":"1"}}}`+"\n", "")
	var initializationResponse rpcTestResponse
	if err := json.Unmarshal([]byte(invalidInitialization), &initializationResponse); err != nil {
		t.Fatal(err)
	}
	if string(initializationResponse.ID) != "9" || initializationResponse.Error == nil || initializationResponse.Error.Code != -32602 {
		t.Fatalf("不正な初期化への応答 = %+v", initializationResponse)
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":null}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/list","params":{"unknown":true}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/list","params":{"cursor":1}}`,
	}, "\n") + "\n"
	output, _ := runMCPTranscript(t, "", input, "")
	for _, expected := range []string{
		`"id":1,"result":`,
		`"id":2,"error":{"code":-32600`,
		`"id":3,"error":{"code":-32602`,
		`"id":4,"error":{"code":-32602`,
		`"id":5,"error":{"code":-32602`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("応答に%qがない：%s", expected, output)
		}
	}
}

func Testヘルプを表示する(t *testing.T) {
	var help bytes.Buffer
	if err := Run(strings.NewReader(""), &bytes.Buffer{}, &help, []string{"-h"}); err != flag.ErrHelp {
		t.Fatalf("エラー = %v、期待値 = %v", err, flag.ErrHelp)
	}
	if help.Len() == 0 {
		t.Fatal("ヘルプが空です")
	}
}

func Testログレベル引数を環境変数より優先する(t *testing.T) {
	t.Setenv("LOG_LEVEL", "invalid")
	if err := Run(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, []string{"--log-level", "info"}); err != nil {
		t.Fatal(err)
	}
}

func Test不正なJSONRPC要求へ通信規約誤りを返す(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0",`,
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"missing","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":null,"method":"ping"}`,
	}, "\n") + "\n"
	output, _ := runMCPTranscript(t, "", input, "")
	for _, expected := range []string{`"code":-32700`, `"code":-32600`, `"code":-32602`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("応答に%qがない：%s", expected, output)
		}
	}
}

func Test重複項目と大文字小文字違いを拒否する(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"準備","method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":2,"Method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"2025-11-25","protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"Name":"list_games","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_game","arguments":{"gameId":"g1","gameId":"g2"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"get_game","arguments":{"GameId":"g1"}}}`,
	}, "\n") + "\n"
	output, _ := runMCPTranscript(t, "", input, "")
	responses := decodeRPCResponses(t, output)
	if len(responses) != 7 {
		t.Fatalf("不正なJSON-RPCへの応答数 = %d：%s", len(responses), output)
	}
	for index, response := range responses[1:] {
		if index >= 4 {
			_ = toolResponseText(t, response, true)
			continue
		}
		if response.Error == nil {
			t.Fatalf("%d番目の不正なJSON-RPCが受理された：%s", index+1, output)
		}
		if index < 2 {
			wantID := fmt.Sprint(index + 1)
			if string(response.ID) != wantID || response.Error.Code != -32600 {
				t.Fatalf("要求自体が不正な応答 = %+v", response)
			}
			continue
		}
		if string(response.ID) != fmt.Sprint(index+1) || response.Error.Code != -32602 {
			t.Fatalf("要求 IDに対応しないパラメーターエラー = %+v", response)
		}
	}
}

func Test通知から処理を実行しない(t *testing.T) {
	app := newMCPAPI(t)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","method":"tools/list","params":{"_meta":{"progressToken":"通知-1"}}}`,
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"delete_game","arguments":{"gameId":"g1"},"_meta":{"progressToken":"通知-2"}}}`,
		`{"jsonrpc":"2.0","method":"unknown"}`,
	}, "\n") + "\n"
	output, _ := runMCPTranscript(t, app.URL, input, "")
	if output != "" {
		t.Fatalf("通知に応答がある：出力=%s", output)
	}
}

func Test空の要求IDを処理せず拒否する(t *testing.T) {
	app := newMCPAPI(t)
	input := `{"jsonrpc":"2.0","id":null,"method":"tools/call","params":{"name":"delete_game","arguments":{"gameId":"g1"},"_meta":{}}}` + "\n"
	output, _ := runMCPTranscript(t, app.URL, input, "")
	responses := decodeRPCResponses(t, output)
	if len(responses) != 1 || len(responses[0].ID) != 0 || responses[0].Error == nil || responses[0].Error.Code != -32600 {
		t.Fatalf("要求IDがnullのときの応答=%+v", responses)
	}
}

func Testツールから実際のアプリ機能を呼び出す(t *testing.T) {
	backend, err := api.NewPersistentServer(t.TempDir(), mcpIntegrationAI{})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(backend.Handler())
	defer app.Close()
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"準備","method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_game","arguments":{"boardSize":9,"komi":6.5,"aiLevel":2,"humanColor":"B"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_games","arguments":{}}}`,
	}, "\n") + "\n"
	output, _ := runMCPTranscript(t, app.URL, input, "")
	responses := decodeRPCResponses(t, output)
	if len(responses) != 3 || string(responses[1].ID) != "1" || string(responses[2].ID) != "2" || responses[1].Error != nil || responses[2].Error != nil {
		t.Fatalf("要求 IDに対応するMCP応答 = %+v", responses)
	}
	for _, expected := range []string{`\"boardSize\":9`, `\"komi\":6.5`, `\"aiLevel\":2`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("MCP応答に%sがない：%s", expected, output)
		}
	}
}

func TestHTTPとMCPで同じ対局操作の結果が一致する(t *testing.T) {
	backend, err := api.NewPersistentServer(t.TempDir(), mcpIntegrationAI{})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(backend.Handler())
	defer app.Close()

	type state struct {
		Game struct {
			ID            string  `json:"id"`
			BoardSize     int     `json:"boardSize"`
			Komi          float64 `json:"komi"`
			CurrentPlayer string  `json:"currentPlayer"`
			Status        string  `json:"status"`
			Moves         []struct {
				Color      string `json:"color"`
				Coordinate string `json:"coordinate"`
			} `json:"moves"`
		} `json:"game"`
		AILevel    int    `json:"aiLevel"`
		HumanColor string `json:"humanColor"`
	}
	decode := func(contents []byte) state {
		t.Helper()
		var value state
		if err := json.Unmarshal(contents, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	httpCreated := decode(callAPIJSON(t, app.Client(), http.MethodPost, app.URL+"/api/games", `{"boardSize":9,"komi":6.5,"aiLevel":2,"humanColor":"B"}`, http.StatusCreated))
	mcpCreated := decode([]byte(callToolThroughRun(t, app.URL, "create_game", `{"boardSize":9,"komi":6.5,"aiLevel":2,"humanColor":"B"}`)))
	httpMoved := decode(callAPIJSON(t, app.Client(), http.MethodPost, app.URL+"/api/games/"+httpCreated.Game.ID+"/moves", `{"coordinate":"E5"}`, http.StatusOK))
	mcpMoved := decode([]byte(callToolThroughRun(t, app.URL, "play_move", `{"gameId":"`+mcpCreated.Game.ID+`","coordinate":"E5"}`)))

	httpMoved.Game.ID = ""
	mcpMoved.Game.ID = ""
	if !reflect.DeepEqual(httpMoved, mcpMoved) {
		t.Fatalf("HTTPとMCPの操作結果が一致しません：HTTP=%+v、MCP=%+v", httpMoved, mcpMoved)
	}
}

func Test変更ツールが実APIの状態を変える(t *testing.T) {
	backend, err := api.NewPersistentServer(t.TempDir(), mcpIntegrationAI{})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(backend.Handler())
	defer app.Close()
	createdText := callToolThroughRun(t, app.URL, "create_game", `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state struct {
		Game struct {
			ID             string `json:"id"`
			CurrentNodeID  string `json:"currentNodeId"`
			MainLineNodeID string `json:"mainLineNodeId"`
			Status         string `json:"status"`
			Moves          []struct {
				Coordinate string `json:"coordinate"`
			} `json:"moves"`
			Result    json.RawMessage `json:"result"`
			TreeNodes []struct {
				ID string `json:"id"`
			} `json:"treeNodes"`
		} `json:"game"`
	}
	if err := json.Unmarshal([]byte(createdText), &state); err != nil || state.Game.ID == "" {
		t.Fatalf("「create_game」の応答 = %s、エラー = %v", createdText, err)
	}
	gameID := state.Game.ID
	callToolThroughRun(t, app.URL, "play_move", `{"gameId":"`+gameID+`","coordinate":"E5"}`)
	if text := callToolThroughRun(t, app.URL, "get_game", `{"gameId":"`+gameID+`"}`); !strings.Contains(text, `"coordinate":"E5"`) {
		t.Fatalf("「play_move」の実行後の対局 = %s", text)
	}
	callToolThroughRun(t, app.URL, "request_ai_move", `{"gameId":"`+gameID+`"}`)
	if text := callToolThroughRun(t, app.URL, "get_game", `{"gameId":"`+gameID+`"}`); !strings.Contains(text, `"coordinate":"D5"`) {
		t.Fatalf("「request_ai_move」の実行後の対局 = %s", text)
	}
	callToolThroughRun(t, app.URL, "resign_game", `{"gameId":"`+gameID+`"}`)
	if text := callToolThroughRun(t, app.URL, "get_game", `{"gameId":"`+gameID+`"}`); !strings.Contains(text, `"status":"finished"`) {
		t.Fatalf("「resign_game」の実行後の対局 = %s", text)
	}
	callToolThroughRun(t, app.URL, "undo_move", `{"gameId":"`+gameID+`"}`)
	undoText := callToolThroughRun(t, app.URL, "get_game", `{"gameId":"`+gameID+`"}`)
	if err := json.Unmarshal([]byte(undoText), &state); err != nil {
		t.Fatal(err)
	}
	if state.Game.Status != "playing" || len(state.Game.Moves) != 2 || state.Game.CurrentNodeID != state.Game.MainLineNodeID || string(state.Game.Result) != "null" {
		t.Fatalf("「undo_move」の実行後の対局 = %s", undoText)
	}
	variationText := callToolThroughRun(t, app.URL, "play_variation", `{"gameId":"`+gameID+`","coordinate":"C3"}`)
	if err := json.Unmarshal([]byte(variationText), &state); err != nil || state.Game.CurrentNodeID == "" {
		t.Fatalf("「play_variation」の応答 = %s、エラー = %v", variationText, err)
	}
	variationID := state.Game.CurrentNodeID
	callToolThroughRun(t, app.URL, "update_node", `{"gameId":"`+gameID+`","nodeId":"`+variationID+`","note":"MCPのメモ","important":true,"expectedNote":"","expectedImportant":false}`)
	callToolThroughRun(t, app.URL, "promote_variation", `{"gameId":"`+gameID+`","nodeId":"`+variationID+`"}`)

	gameText := callToolThroughRun(t, app.URL, "get_game", `{"gameId":"`+gameID+`"}`)
	if !strings.Contains(gameText, "MCPのメモ") || !strings.Contains(gameText, `"mainLineNodeId":"`+variationID+`"`) {
		t.Fatalf("MCP変更後の対局 = %s", gameText)
	}
	sgfText := callToolThroughRun(t, app.URL, "export_sgf", `{"gameId":"`+gameID+`"}`)
	jsonText := callToolThroughRun(t, app.URL, "export_json", `{"gameId":"`+gameID+`"}`)
	sgfArgs, err := json.Marshal(map[string]string{"contents": sgfText})
	if err != nil {
		t.Fatal(err)
	}
	jsonArgs, err := json.Marshal(map[string]string{"contents": jsonText})
	if err != nil {
		t.Fatal(err)
	}
	importedSGF := callToolThroughRun(t, app.URL, "import_sgf", string(sgfArgs))
	importedJSON := callToolThroughRun(t, app.URL, "import_json", string(jsonArgs))
	for name, imported := range map[string]string{"SGF": importedSGF, "JSON": importedJSON} {
		var importedState struct {
			Game struct {
				ID    string `json:"id"`
				Moves []any  `json:"moves"`
			} `json:"game"`
		}
		if err := json.Unmarshal([]byte(imported), &importedState); err != nil || importedState.Game.ID == "" || len(importedState.Game.Moves) != 3 {
			t.Fatalf("%sの読み込み後の対局 = %s、エラー = %v", name, imported, err)
		}
	}

	passGameText := callToolThroughRun(t, app.URL, "create_game", `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	if err := json.Unmarshal([]byte(passGameText), &state); err != nil {
		t.Fatal(err)
	}
	callToolThroughRun(t, app.URL, "play_move", `{"gameId":"`+state.Game.ID+`","coordinate":"pass"}`)
	if text := callToolThroughRun(t, app.URL, "get_game", `{"gameId":"`+state.Game.ID+`"}`); !strings.Contains(text, `"coordinate":"pass"`) {
		t.Fatalf("パスの実行後の対局 = %s", text)
	}

	deleteVariationText := callToolThroughRun(t, app.URL, "play_variation", `{"gameId":"`+state.Game.ID+`","coordinate":"D5"}`)
	if err := json.Unmarshal([]byte(deleteVariationText), &state); err != nil {
		t.Fatal(err)
	}
	callToolThroughRun(t, app.URL, "delete_variation", `{"gameId":"`+state.Game.ID+`","nodeId":"`+state.Game.CurrentNodeID+`","expectedNodeIds":["`+state.Game.CurrentNodeID+`"]}`)
	deletedVariationID := state.Game.CurrentNodeID
	text := callToolThroughRun(t, app.URL, "get_game", `{"gameId":"`+state.Game.ID+`"}`)
	if err := json.Unmarshal([]byte(text), &state); err != nil {
		t.Fatal(err)
	}
	for _, node := range state.Game.TreeNodes {
		if node.ID == deletedVariationID {
			t.Fatalf("「delete_variation」の実行後も削除局面が残っている：%s", text)
		}
	}
}

func Test状態と保存のツールが実APIを使う(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "壊れた保存.json"), []byte(`{"schemaVersion":`), 0o600); err != nil {
		t.Fatal(err)
	}
	backend, err := api.NewPersistentServer(dataDir, mcpIntegrationAI{})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(backend.Handler())
	defer app.Close()
	created := callToolThroughRun(t, app.URL, "create_game", `{"boardSize":9}`)
	var state struct {
		Game struct {
			ID string `json:"id"`
		} `json:"game"`
	}
	if err := json.Unmarshal([]byte(created), &state); err != nil {
		t.Fatal(err)
	}
	gameID := state.Game.ID
	callToolThroughRun(t, app.URL, "update_node", `{"gameId":"`+gameID+`","nodeId":"root","note":"最初","important":false,"expectedNote":"","expectedImportant":false}`)
	callToolThroughRun(t, app.URL, "update_node", `{"gameId":"`+gameID+`","nodeId":"root","note":"次","important":true,"expectedNote":"最初","expectedImportant":false}`)
	if got := callToolThroughRun(t, app.URL, "get_game", `{"gameId":"`+gameID+`"}`); !strings.Contains(got, `"values":["次"]`) {
		t.Fatalf("「update_node」の連続実行後 = %s", got)
	}
	callToolThroughRun(t, app.URL, "rename_game", `{"gameId":"`+gameID+`","name":"統合テスト","expectedName":""}`)
	if got := callToolThroughRun(t, app.URL, "get_game", `{"gameId":"`+gameID+`"}`); !strings.Contains(got, `"name":"統合テスト"`) {
		t.Fatalf("「rename_game」の実行後 = %s", got)
	}
	storageErrors := callToolThroughRun(t, app.URL, "list_storage_errors", `{}`)
	var failures []struct {
		File   string `json:"file"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(storageErrors), &failures); err != nil || len(failures) != 1 || failures[0].File != "壊れた保存.json" || failures[0].Reason == "" {
		t.Fatalf("「list_storage_errors」の実行結果 = %s、エラー = %v", storageErrors, err)
	}
	variation := callToolThroughRun(t, app.URL, "play_variation", `{"gameId":"`+gameID+`","coordinate":"D4"}`)
	var variationState struct {
		Game struct {
			CurrentNodeID string `json:"currentNodeId"`
		} `json:"game"`
	}
	if err := json.Unmarshal([]byte(variation), &variationState); err != nil || variationState.Game.CurrentNodeID == "" || variationState.Game.CurrentNodeID == "root" {
		t.Fatalf("「play_variation」の応答 = %s、エラー = %v", variation, err)
	}
	variationID := variationState.Game.CurrentNodeID
	callToolThroughRun(t, app.URL, "navigate_game", `{"gameId":"`+gameID+`","nodeId":"root"}`)
	if got := callToolThroughRun(t, app.URL, "get_game", `{"gameId":"`+gameID+`"}`); !strings.Contains(got, `"currentNodeId":"root"`) {
		t.Fatalf("「navigate_game」の実行後 = %s", got)
	}
	analysis := callToolThroughRun(t, app.URL, "get_analysis", `{"gameId":"`+gameID+`","nodeId":"root","visits":1}`)
	if !strings.Contains(analysis, `"visits":1`) || !strings.Contains(analysis, `"currentPlayer":"B"`) {
		t.Fatalf("「get_analysis」の実行結果 = %s", analysis)
	}
	currentAnalysis := callToolThroughRun(t, app.URL, "get_analysis", `{"gameId":"`+gameID+`"}`)
	if !strings.Contains(currentAnalysis, `"visits":100`) || !strings.Contains(currentAnalysis, `"currentPlayer":"B"`) {
		t.Fatalf("表示中の局面に対する「get_analysis」の実行結果 = %s", currentAnalysis)
	}

	selection := strings.NewReader(`{"gameId":"` + gameID + `","nodeId":"root"}`)
	req, err := http.NewRequest(http.MethodPost, app.URL+"/api/agent/selection", selection)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Client().Do(req)
	if err != nil || res.StatusCode != http.StatusNoContent {
		t.Fatalf("表示中の局面設定状態コード = %v、エラー = %v", res, err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}
	selected := callToolThroughRun(t, app.URL, "get_selected_position", `{}`)
	var context struct {
		ContextID string `json:"contextId"`
	}
	if err := json.Unmarshal([]byte(selected), &context); err != nil || context.ContextID == "" {
		t.Fatalf("「get_selected_position」の実行結果 = %s", selected)
	}
	callToolThroughRun(t, app.URL, "select_position", `{"contextId":"`+context.ContextID+`","nodeId":"`+variationID+`"}`)
	selected = callToolThroughRun(t, app.URL, "get_selected_position", `{}`)
	if !strings.Contains(selected, `"currentNodeId":"`+variationID+`"`) {
		t.Fatalf("select_position後 = %s", selected)
	}
	if err := json.Unmarshal([]byte(selected), &context); err != nil || context.ContextID == "" {
		t.Fatalf("移動後の表示中の局面 = %s", selected)
	}
	analyzed := callToolThroughRun(t, app.URL, "analyze_position", `{"contextId":"`+context.ContextID+`","maxVisits":1}`)
	if !strings.Contains(analyzed, `"visits":1`) {
		t.Fatalf("analyze_position = %s", analyzed)
	}
	callToolThroughRun(t, app.URL, "show_variation", `{"contextId":"`+context.ContextID+`","moves":["E5"],"title":"説明"}`)
	if got := callToolThroughRun(t, app.URL, "get_game", `{"gameId":"`+gameID+`"}`); !strings.Contains(got, `"coordinate":"E5"`) {
		t.Fatalf("「show_variation」の実行後 = %s", got)
	}
	callToolThroughRun(t, app.URL, "delete_game", `{"gameId":"`+gameID+`"}`)
	response, err := app.Client().Get(app.URL + "/api/games/" + gameID)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("「delete_game」の実行後の状態コード = %d", response.StatusCode)
	}
}

type mcpIntegrationAI struct{}

func (mcpIntegrationAI) AnalyzePosition(_ context.Context, snapshot game.Snapshot, visits int) (katago.Summary, error) {
	return katago.Summary{
		TurnNumber:    len(snapshot.Moves),
		CurrentPlayer: snapshot.CurrentPlayer,
		Visits:        visits,
		Ownership:     make([]float64, snapshot.BoardSize*snapshot.BoardSize),
		Candidates:    []katago.CandidateSummary{{Move: "D5", Visits: visits, Prior: 1, Winrate: 0.5}},
	}, nil
}

func (mcpIntegrationAI) AnalysisVersion() string { return "MCP統合テスト" }

func Test未知のツール引数を項目名順で拒否する(t *testing.T) {
	app := newMCPAPI(t)
	response := callToolResponseThroughRun(t, app.URL, "get_game", `{"gameId":"g1","z":true,"a":true}`)
	_ = toolResponseText(t, response, true)
}

func Testツール引数をAPIと同じ値域で拒否する(t *testing.T) {
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
	overlongMoves := `[` + strings.TrimSuffix(strings.Repeat(`"pass",`, apicontract.MaximumAnalysisSequenceSize+1), ",") + `]`
	for _, test := range []struct {
		name      string
		arguments string
	}{
		{name: "create_game", arguments: `{"boardSize":8}`},
		{name: "create_game", arguments: `{"boardSize":9.0}`},
		{name: "create_game", arguments: `{"boardSize":9,"aiLevel":1e0}`},
		{name: "create_game", arguments: `{"boardSize":9,"aiLevel":5}`},
		{name: "create_game", arguments: `{"boardSize":9,"handicap":1}`},
		{name: "create_game", arguments: `{"boardSize":9,"humanColor":"black"}`},
		{name: "create_game", arguments: `{"boardSize":9,"komi":-100.1}`},
		{name: "create_game", arguments: `{"boardSize":9,"komi":100.1}`},
		{name: "create_game", arguments: `{"boardSize":9,"komi":7.25}`},
		{name: "create_game", arguments: `{"boardSize":9,"komi":0.49999999999999999}`},
		{name: "create_game", arguments: `{"boardSize":9,"komi":1e400}`},
		{name: "get_analysis", arguments: `{"gameId":"` + state.Game.ID + `","nodeId":"root","visits":0}`},
		{name: "get_analysis", arguments: `{"gameId":"` + state.Game.ID + `","nodeId":"root","visits":2001}`},
		{name: "get_analysis", arguments: `{"gameId":"` + state.Game.ID + `","nodeId":"root","visits":1e2}`},
		{name: "analyze_position", arguments: `{"contextId":"g1:root","sequence":` + overlongMoves + `}`},
		{name: "show_variation", arguments: `{"contextId":"g1:root","moves":` + overlongMoves + `}`},
	} {
		t.Run(test.name+test.arguments, func(t *testing.T) {
			response := callToolResponseThroughRun(t, app.URL, test.name, test.arguments)
			_ = toolResponseText(t, response, true)
		})
	}
}

func Testツールの文字列選択肢をJSON値として比較する(t *testing.T) {
	app := newMCPAPI(t)
	callToolThroughRun(t, app.URL, "create_game", `{"boardSize":9,"humanColor":"\u0042"}`)
}

func Testメモ更新に変更前の値を必要とする(t *testing.T) {
	app := newMCPAPI(t)
	for _, arguments := range []string{
		`{"gameId":"g1","nodeId":"n1","note":"新しいメモ","important":true}`,
		`{"gameId":"g1","nodeId":"n1","note":"新しいメモ","important":true,"expectedNote":"以前のメモ"}`,
	} {
		response := callToolResponseThroughRun(t, app.URL, "update_node", arguments)
		_ = toolResponseText(t, response, true)
	}
}

func Test変化名を省略してHTTPへ渡せる(t *testing.T) {
	app := newMCPAPI(t)
	created := callToolThroughRun(t, app.URL, "create_game", `{"boardSize":9}`)
	var state struct {
		Game struct {
			ID string `json:"id"`
		} `json:"game"`
	}
	if err := json.Unmarshal([]byte(created), &state); err != nil {
		t.Fatal(err)
	}
	callAPIJSON(t, app.Client(), http.MethodPost, app.URL+"/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`, http.StatusNoContent)
	response := callToolResponseThroughRun(t, app.URL, "show_variation", `{"contextId":"`+state.Game.ID+`:root","moves":["E5"]}`)
	_ = toolResponseText(t, response, false)
}

func callToolThroughRun(t *testing.T, baseURL string, name, arguments string) string {
	t.Helper()
	return toolResponseText(t, callToolResponseThroughRun(t, baseURL, name, arguments), false)
}

func callToolResponseThroughRun(t *testing.T, baseURL string, name, arguments string) rpcTestResponse {
	t.Helper()
	params, err := json.Marshal(map[string]any{
		"name":      name,
		"arguments": json.RawMessage(arguments),
		"_meta":     map[string]any{"progressToken": "全ツール検証"},
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  json.RawMessage(params),
	})
	if err != nil {
		t.Fatal(err)
	}
	responses := runMCPRequests(t, baseURL,
		`{"jsonrpc":"2.0","id":"初期化","method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"検証用","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		string(message),
	)
	if len(responses) != 2 {
		t.Fatalf("%s：応答数 = %d", name, len(responses))
	}
	for _, response := range responses {
		if string(response.ID) == "1" {
			return response
		}
	}
	t.Fatalf("%s：ツール応答がありません", name)
	return rpcTestResponse{}
}

type rpcTestResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeRPCResponses(t *testing.T, output string) []rpcTestResponse {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	responses := make([]rpcTestResponse, 0, len(lines))
	for _, line := range lines {
		var response rpcTestResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("JSON-RPC応答を解釈できません：%q：%v", line, err)
		}
		if len(response.ID) == 0 && response.Error == nil || len(response.Result) == 0 && response.Error == nil {
			t.Fatalf("JSON-RPC応答にIDまたは結果がありません：%s", line)
		}
		responses = append(responses, response)
	}
	sort.SliceStable(responses, func(i, j int) bool {
		var left, right float64
		if json.Unmarshal(responses[i].ID, &left) == nil && json.Unmarshal(responses[j].ID, &right) == nil {
			return left < right
		}
		return string(responses[i].ID) < string(responses[j].ID)
	})
	return responses
}
