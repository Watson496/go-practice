package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestJSON付随情報の空白が安定した固定点に達し再起動する(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, detailedFixedAnalyzer{fixedAnalyzer{move: "D5"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":2,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPatch, path, `{"name":"対局\r\n名\t固定点","expectedName":""}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPatch, path+"/nodes/root", `{"note":"開始\r\n二行目\t末尾","important":true,"expectedNote":"","expectedImportant":false}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`), http.StatusOK)
	resigned := performJSON(t, handler, http.MethodPost, path+"/resign", `{}`)
	assertStatus(t, resigned, http.StatusOK)
	if err := json.Unmarshal(resigned.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	resultID := state.Game.CurrentNodeID
	assertStatus(t, performJSON(t, handler, http.MethodPatch, path+"/nodes/"+resultID, `{"note":"結果\r\nメモ\t確認","important":true,"expectedNote":"","expectedImportant":false}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`), http.StatusNoContent)
	variation := performJSON(t, handler, http.MethodPost, "/api/agent/variations", `{"contextId":"`+state.Game.ID+`:root","moves":["D4"],"title":"AI\r\n候補\t図"}`)
	assertStatus(t, variation, http.StatusOK)
	var agentVariation struct {
		Game    gameResponse `json:"game"`
		NodeIDs []string     `json:"nodeIds"`
	}
	if err := json.Unmarshal(variation.Body.Bytes(), &agentVariation); err != nil {
		t.Fatal(err)
	}
	state = agentVariation.Game
	if len(agentVariation.NodeIDs) != 1 {
		t.Fatalf("AI agent変化の局面の識別情報 = %v", agentVariation.NodeIDs)
	}
	variationID := agentVariation.NodeIDs[0]
	expectedNote := testNodeComment(state.Game, variationID)
	expectedImportant := false
	for _, node := range state.Game.TreeNodes {
		if node.ID == variationID {
			expectedImportant = node.Important
		}
	}
	reviewBody, err := json.Marshal(map[string]any{
		"note": "変化\r\nメモ\t確認", "important": false,
		"expectedNote": expectedNote, "expectedImportant": expectedImportant,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, performJSON(t, handler, http.MethodPatch, path+"/nodes/"+variationID, string(reviewBody)), http.StatusOK)

	firstResponse := performJSON(t, handler, http.MethodGet, path+"/json", ``)
	assertStatus(t, firstResponse, http.StatusOK)
	var first importGameJSONRequest
	if err := json.Unmarshal(firstResponse.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Name != "対局 名 固定点" || testNodeComment(first.Game, "root") != "開始\n二行目 末尾" || testNodeComment(first.Game, resultID) != "結果\nメモ 確認" || testNodeComment(first.Game, variationID) != "変化\nメモ 確認" {
		t.Fatalf("JSON付随情報の空白保持が想定外：%+v", first)
	}
	foundTitle := false
	for _, node := range first.Game.TreeNodes {
		if node.ID == variationID {
			foundTitle = node.VariationTitle == "AI 候補 図"
		}
	}
	if !foundTitle {
		t.Fatalf("AI agent変化名の空白が保持されていない：%+v", first.Game.TreeNodes)
	}

	imported := performJSON(t, handler, http.MethodPost, "/api/games/import-json", firstResponse.Body.String())
	assertStatus(t, imported, http.StatusCreated)
	if err := json.Unmarshal(imported.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	secondResponse := performJSON(t, handler, http.MethodGet, "/api/games/"+state.Game.ID+"/json", ``)
	assertStatus(t, secondResponse, http.StatusOK)
	var second importGameJSONRequest
	if err := json.Unmarshal(secondResponse.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	first.Game.ID, second.Game.ID = "", ""
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("改行とタブを含むJSON付随情報が固定点に達していない\n最初=%+v\n再取り込み後=%+v", first, second)
	}
	restarted, err := NewPersistentServer(dataDir, detailedFixedAnalyzer{fixedAnalyzer{move: "D5"}})
	if err != nil {
		t.Fatal(err)
	}
	restored := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+state.Game.ID+"/json", ``)
	assertStatus(t, restored, http.StatusOK)
	var afterRestart importGameJSONRequest
	if err := json.Unmarshal(restored.Body.Bytes(), &afterRestart); err != nil {
		t.Fatal(err)
	}
	afterRestart.Game.ID = ""
	if !reflect.DeepEqual(second, afterRestart) {
		t.Fatalf("改行とタブを含むJSON付随情報が再起動後に変化した\n再起動前=%+v\n再起動後=%+v", second, afterRestart)
	}
}
