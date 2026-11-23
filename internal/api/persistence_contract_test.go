package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go-practice/internal/game"
)

func TestSGFは標準プロパティだけで安定して往復する(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	assertStatus(t, created, http.StatusCreated)
	var original gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + original.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`), http.StatusOK)
	exported := performJSON(t, handler, http.MethodGet, path+"/sgf", ``)
	assertStatus(t, exported, http.StatusOK)
	startedAt, err := time.Parse(time.RFC3339Nano, original.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	for _, property := range []string{
		"DT[" + startedAt.Format("2006-01-02") + "]",
		";B[ee]", ";W[de]",
	} {
		if !strings.Contains(exported.Body.String(), property) {
			t.Fatalf("SGFに%sがない：%s", property, exported.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader(exported.Body.String()))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	importedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(importedRecorder, request)
	assertStatus(t, importedRecorder, http.StatusCreated)
	var imported gameResponse
	if err := json.Unmarshal(importedRecorder.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Game.CurrentNodeID != imported.Game.MainLineNodeID || imported.Game.MainLineNodeID == "root" {
		t.Fatalf("読み込んだ主分岐の末端を表示していない：現在=%s 主分岐=%s", imported.Game.CurrentNodeID, imported.Game.MainLineNodeID)
	}
	importedPath := "/api/games/" + imported.Game.ID
	fixedPoint := performJSON(t, handler, http.MethodGet, importedPath+"/sgf", ``)
	assertStatus(t, fixedPoint, http.StatusOK)
	if fixedPoint.Body.String() != exported.Body.String() {
		t.Fatalf("SGFが固定点に達していない：\n1回目：%s\n2回目：%s", exported.Body.String(), fixedPoint.Body.String())
	}

	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	restored := performJSON(t, restarted.Handler(), http.MethodGet, importedPath, ``)
	assertStatus(t, restored, http.StatusOK)
	var restoredState gameResponse
	if err := json.Unmarshal(restored.Body.Bytes(), &restoredState); err != nil {
		t.Fatal(err)
	}
	if restoredState.Game.CurrentNodeID != imported.Game.CurrentNodeID || restoredState.Game.MainLineNodeID != imported.Game.MainLineNodeID {
		t.Fatalf("再起動後の現在の局面と主分岐の末端が変わった：現在=%s 主分岐=%s", restoredState.Game.CurrentNodeID, restoredState.Game.MainLineNodeID)
	}
	if restoredState.StartedAt != imported.StartedAt {
		t.Fatalf("再起動後の開始日時 = %s、期待値 = %s", restoredState.StartedAt, imported.StartedAt)
	}
	restartedSGF := performJSON(t, restarted.Handler(), http.MethodGet, importedPath+"/sgf", ``)
	assertStatus(t, restartedSGF, http.StatusOK)
	if restartedSGF.Body.String() != exported.Body.String() {
		t.Fatalf("再起動後にSGFが変わった：\n復元前：%s\n復元後：%s", exported.Body.String(), restartedSGF.Body.String())
	}
}

func Test未知のGPプロパティと未解釈の結果をAPIで往復する(t *testing.T) {
	server, err := NewPersistentServer(t.TempDir(), fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	const source = "(;FF[4]SZ[9]RE[W+T]GPUNKNOWN[alpha];B[ee]GPAI[{value:1}])"
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader(source))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	imported := httptest.NewRecorder()
	server.Handler().ServeHTTP(imported, request)
	assertStatus(t, imported, http.StatusCreated)
	var state gameResponse
	if err := json.Unmarshal(imported.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Game.Status != "finished" || state.Game.Result == nil || state.Game.Result.Type != "recorded" || state.Game.Result.Value != "W+T" || state.Game.Result.Winner != "" {
		t.Fatalf("未解釈の結果が保たれていません：%+v", state.Game.Result)
	}
	exported := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID+"/sgf", ``)
	assertStatus(t, exported, http.StatusOK)
	for _, property := range []string{"RE[W+T]", "GPUNKNOWN[alpha]", "GPAI[{value:1}]"} {
		if !strings.Contains(exported.Body.String(), property) {
			t.Fatalf("書き出したSGFに%sがありません：%s", property, exported.Body.String())
		}
	}
}

func TestユーザーJSONの書き出しと読み込み書き出しはID正規化後に安定する(t *testing.T) {
	server := newTestServer(t, detailedFixedAnalyzer{fixedAnalyzer{move: "pass"}})
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":2,"humanColor":"B"}`)
	var original gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + original.Game.ID
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPatch, path,
		`{"name":"一局目\nJSON固定点","expectedName":""}`), http.StatusOK)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPatch, path+"/nodes/root",
		`{"note":"rootのメモ\n次の行","important":true,"expectedNote":"","expectedImportant":false}`), http.StatusOK)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/moves",
		`{"coordinate":"E5"}`), http.StatusOK)
	aiMove := performJSON(t, server.Handler(), http.MethodPost, path+"/ai-moves", `{}`)
	assertStatus(t, aiMove, http.StatusOK)
	var afterAI aiMoveResponse
	if err := json.Unmarshal(aiMove.Body.Bytes(), &afterAI); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/resign", `{}`), http.StatusOK)
	finished := performJSON(t, server.Handler(), http.MethodGet, path, ``)
	assertStatus(t, finished, http.StatusOK)
	if err := json.Unmarshal(finished.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}
	resultNodeID := original.Game.CurrentNodeID
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPatch, path+"/nodes/"+resultNodeID,
		`{"note":"結果のメモ","important":true,"expectedNote":"","expectedImportant":false}`), http.StatusOK)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/undo", `{}`), http.StatusOK)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, "/api/agent/selection",
		`{"gameId":"`+original.Game.ID+`","nodeId":"root"}`), http.StatusNoContent)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, "/api/agent/variations",
		`{"contextId":"`+original.Game.ID+`:root","moves":["D5"],"title":"agentのoption"}`), http.StatusOK)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/nodes/"+resultNodeID, ``), http.StatusOK)

	firstResponse := performJSON(t, server.Handler(), http.MethodGet, path+"/json", ``)
	assertStatus(t, firstResponse, http.StatusOK)
	var first importGameJSONRequest
	if err := json.Unmarshal(firstResponse.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	importedResponse := performJSON(t, server.Handler(), http.MethodPost, "/api/games/import-json", firstResponse.Body.String())
	if importedResponse.Code != http.StatusCreated {
		t.Fatalf("JSON読み込み状態コード = %d、本文 = %s", importedResponse.Code, importedResponse.Body.String())
	}
	var imported gameResponse
	if err := json.Unmarshal(importedResponse.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	secondResponse := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+imported.Game.ID+"/json", ``)
	assertStatus(t, secondResponse, http.StatusOK)
	var second importGameJSONRequest
	if err := json.Unmarshal(secondResponse.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	first.Game.ID, second.Game.ID = "", ""
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("JSON往復で内容が変化した\n最初=%+v\n再取り込み後=%+v", first, second)
	}
	if first.Name != "一局目 JSON固定点" || first.Game.MainLineNodeID == first.Game.CurrentNodeID || first.Game.Result == nil {
		t.Fatalf("複合状態を作れていない：%+v", first)
	}
	var hasAIDecision, hasResultNote, hasAgentVariation bool
	for _, node := range first.Game.TreeNodes {
		hasAIDecision = hasAIDecision || node.AIDecision != nil
		hasResultNote = hasResultNote || node.ID == resultNodeID && testNodeComment(first.Game, node.ID) == "結果のメモ"
		hasAgentVariation = hasAgentVariation || node.Origin == game.AgentOrigin && node.VariationTitle == "agentのoption"
	}
	if !hasAIDecision || !hasResultNote || !hasAgentVariation || testNodeComment(first.Game, "root") != "rootのメモ\n次の行" {
		t.Fatalf("JSON固定点の付随情報が不足している：%+v", first.Game)
	}
	restarted, err := NewPersistentServer(server.dataDir, detailedFixedAnalyzer{fixedAnalyzer{move: "pass"}})
	if err != nil {
		t.Fatal(err)
	}
	restored := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+imported.Game.ID+"/json", ``)
	assertStatus(t, restored, http.StatusOK)
	var restoredJSON importGameJSONRequest
	if err := json.Unmarshal(restored.Body.Bytes(), &restoredJSON); err != nil {
		t.Fatal(err)
	}
	restoredJSON.Game.ID = ""
	if !reflect.DeepEqual(second, restoredJSON) {
		t.Fatalf("JSON読み込み後の再起動で内容が変化した\n再起動前=%+v\n再起動後=%+v", second, restoredJSON)
	}
}

func Test点数結果のJSONが点数の内訳を安定した固定点に保つ(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/moves", `{"coordinate":"pass"}`), http.StatusOK)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/ai-moves", `{}`), http.StatusOK)
	firstResponse := performJSON(t, server.Handler(), http.MethodGet, path+"/json", ``)
	assertStatus(t, firstResponse, http.StatusOK)
	var first importGameJSONRequest
	if err := json.Unmarshal(firstResponse.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Game.Result == nil || !first.Game.Result.ScoreKnown || first.Game.Result.BlackScore != 0 || first.Game.Result.WhiteScore != 7.5 {
		t.Fatalf("点数の内訳がJSONにない：%+v", first.Game.Result)
	}
	imported := performJSON(t, server.Handler(), http.MethodPost, "/api/games/import-json", firstResponse.Body.String())
	assertStatus(t, imported, http.StatusCreated)
	var importedState gameResponse
	if err := json.Unmarshal(imported.Body.Bytes(), &importedState); err != nil {
		t.Fatal(err)
	}
	secondResponse := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+importedState.Game.ID+"/json", ``)
	assertStatus(t, secondResponse, http.StatusOK)
	var second importGameJSONRequest
	if err := json.Unmarshal(secondResponse.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	first.Game.ID, second.Game.ID = "", ""
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("点数の内訳を持つJSONが固定点に達していない\n最初=%+v\n再取り込み後=%+v", first, second)
	}
	restarted, err := NewPersistentServer(server.dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	restored := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+importedState.Game.ID+"/json", ``)
	assertStatus(t, restored, http.StatusOK)
	var restoredJSON importGameJSONRequest
	if err := json.Unmarshal(restored.Body.Bytes(), &restoredJSON); err != nil {
		t.Fatal(err)
	}
	restoredJSON.Game.ID = ""
	if !reflect.DeepEqual(second, restoredJSON) {
		t.Fatalf("点数の内訳を持つJSONが再起動後に変化した\n再起動前=%+v\n再起動後=%+v", second, restoredJSON)
	}
}

func Testユーザー操作により安定したSGF往復が生成される(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"komi":7.5,"aiLevel":2}`)
	assertStatus(t, created, http.StatusCreated)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	variation := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"E5"}`)
	assertStatus(t, variation, http.StatusOK)
	var variationState gameResponse
	if err := json.Unmarshal(variation.Body.Bytes(), &variationState); err != nil {
		t.Fatal(err)
	}
	variationNodeID := variationState.Game.CurrentNodeID
	assertStatus(t, performJSON(t, handler, http.MethodPatch, path+"/nodes/"+variationNodeID, `{"note":"optionのメモ","important":true,"expectedNote":"","expectedImportant":false}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/nodes/root", ``), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"pass"}`), http.StatusOK)
	finished := performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`)
	assertStatus(t, finished, http.StatusOK)
	var finishedState aiMoveResponse
	if err := json.Unmarshal(finished.Body.Bytes(), &finishedState); err != nil {
		t.Fatal(err)
	}
	resultNodeID := finishedState.Game.Game.CurrentNodeID
	assertStatus(t, performJSON(t, handler, http.MethodPatch, path+"/nodes/"+resultNodeID,
		`{"note":"二連続パス結果\n検討済み","important":true,"expectedNote":"","expectedImportant":false}`), http.StatusOK)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, path+"/sgf", nil))
	assertStatus(t, first, http.StatusOK)
	imported := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader(first.Body.String()))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	handler.ServeHTTP(imported, request)
	if imported.Code != http.StatusCreated {
		t.Fatalf("SGF再読み込み状態コード = %d、本文 = %s、SGF = %s", imported.Code, imported.Body.String(), first.Body.String())
	}
	var importedState gameResponse
	if err := json.Unmarshal(imported.Body.Bytes(), &importedState); err != nil {
		t.Fatal(err)
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/games/"+importedState.Game.ID+"/sgf", nil))
	assertStatus(t, second, http.StatusOK)
	if second.Body.String() != first.Body.String() {
		t.Fatalf("実操作で作ったSGFが固定点に達していない：\n1回目：%s\n2回目：%s", first.Body.String(), second.Body.String())
	}
	restarted, err := NewPersistentServer(server.dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	restored := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+importedState.Game.ID, ``)
	assertStatus(t, restored, http.StatusOK)
	restoredSGF := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+importedState.Game.ID+"/sgf", ``)
	assertStatus(t, restoredSGF, http.StatusOK)
	if restoredSGF.Body.String() != first.Body.String() {
		t.Fatalf("SGF読み込み後の再起動で固定点が崩れた：\n期待値：%s\n実際の値：%s", first.Body.String(), restoredSGF.Body.String())
	}
}

func Test読み込んだSGFが再起動後も安定する(t *testing.T) {
	for _, test := range []struct {
		name, source string
		expected     []string
	}{
		{name: "二連続パス後の続き", source: "(;FF[4]SZ[9];B[];W[];B[ee])"},
		{name: "タブと改行を含むコメント", source: "(;FF[4]SZ[9]C[root\tmemo](;B[ee]C[line1\r\nline2])(;B[dd]C[var\tmemo]))"},
		{name: "Unicode空白を含むコメント", source: "(;FF[4]CA[UTF-8]SZ[9]C[root\u00a0memo];B[ee]N[node\u2003name]C[result\u3000memo])", expected: []string{"C[root\u00a0memo]", "N[node\u2003name]", "C[result\u3000memo]"}},
		{name: "標準の付随情報", source: "(;FF[4]CA[UTF-8]SZ[9]XX[開始](;B[dd])(;B[ee]C[result memo]HO[1]YY[保持]))", expected: []string{"XX[開始]", "C[result memo]", "HO[1]", "YY[保持]"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader(test.source))
			request.Header.Set("Content-Type", "application/x-go-sgf")
			imported := httptest.NewRecorder()
			server.Handler().ServeHTTP(imported, request)
			assertStatus(t, imported, http.StatusCreated)
			var state gameResponse
			if err := json.Unmarshal(imported.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			path := "/api/games/" + state.Game.ID
			before := performJSON(t, server.Handler(), http.MethodGet, path+"/sgf", ``)
			assertStatus(t, before, http.StatusOK)
			for _, property := range test.expected {
				if !strings.Contains(before.Body.String(), property) {
					t.Fatalf("受理したSGFから%sが失われた：%s", property, before.Body.String())
				}
			}
			fixedPointRequest := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader(before.Body.String()))
			fixedPointRequest.Header.Set("Content-Type", "application/x-go-sgf")
			fixedPointImport := httptest.NewRecorder()
			server.Handler().ServeHTTP(fixedPointImport, fixedPointRequest)
			assertStatus(t, fixedPointImport, http.StatusCreated)
			var fixedPointState gameResponse
			if err := json.Unmarshal(fixedPointImport.Body.Bytes(), &fixedPointState); err != nil {
				t.Fatal(err)
			}
			fixedPointPath := "/api/games/" + fixedPointState.Game.ID
			fixedPoint := performJSON(t, server.Handler(), http.MethodGet, fixedPointPath+"/sgf", ``)
			assertStatus(t, fixedPoint, http.StatusOK)
			if fixedPoint.Body.String() != before.Body.String() {
				t.Fatalf("受理したSGFが読み書きの固定点に達していない：\n1回目：%s\n2回目：%s", before.Body.String(), fixedPoint.Body.String())
			}
			restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
			if err != nil {
				t.Fatal(err)
			}
			restored := performJSON(t, restarted.Handler(), http.MethodGet, path, ``)
			assertStatus(t, restored, http.StatusOK)
			after := performJSON(t, restarted.Handler(), http.MethodGet, path+"/sgf", ``)
			assertStatus(t, after, http.StatusOK)
			if after.Body.String() != before.Body.String() {
				t.Fatalf("受理したSGFが再起動後の固定点に達していない：\n1回目：%s\n2回目：%s", before.Body.String(), after.Body.String())
			}
			fixedPointAfterRestart := performJSON(t, restarted.Handler(), http.MethodGet, fixedPointPath+"/sgf", ``)
			assertStatus(t, fixedPointAfterRestart, http.StatusOK)
			if fixedPointAfterRestart.Body.String() != before.Body.String() {
				t.Fatalf("SGFを再読み込みした複製が再起動後に変化した：\n期待値：%s\n実際の値：%s", before.Body.String(), fixedPointAfterRestart.Body.String())
			}
		})
	}
}

func Test上限長のメモと開始局面の付随情報を再起動後も保持する(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	note := strings.Repeat("あ", 4000)
	source := "(;FF[4]CA[UTF-8]SZ[9]GN[\u3000対局名\u00a0]EV[保存検証]PB[黒番]PW[白番]C[" + note + "];B[ee]N[第一node])"
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader(source))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	imported := httptest.NewRecorder()
	server.Handler().ServeHTTP(imported, request)
	assertStatus(t, imported, http.StatusCreated)
	var state gameResponse
	if err := json.Unmarshal(imported.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Name != "\u3000対局名\u00a0" {
		t.Fatalf("Unicode空白を含むGNから作った対局名 = %q", state.Name)
	}
	path := "/api/games/" + state.Game.ID
	before := performJSON(t, server.Handler(), http.MethodGet, path+"/sgf", ``)
	assertStatus(t, before, http.StatusOK)
	beforeJSON := performJSON(t, server.Handler(), http.MethodGet, path+"/json", ``)
	assertStatus(t, beforeJSON, http.StatusOK)
	for _, property := range []string{"GN[\u3000対局名\u00a0]", "EV[保存検証]", "PB[黒番]", "PW[白番]", "N[第一node]", "C[" + note + "]"} {
		if !strings.Contains(before.Body.String(), property) {
			t.Fatalf("SGF 付随情報 %qが失われた", property[:min(len(property), 40)])
		}
	}
	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	after := performJSON(t, restarted.Handler(), http.MethodGet, path+"/sgf", ``)
	assertStatus(t, after, http.StatusOK)
	if after.Body.String() != before.Body.String() {
		t.Fatalf("最大長メモと開始局面 付随情報が再起動後に変化した")
	}
	afterJSON := performJSON(t, restarted.Handler(), http.MethodGet, path+"/json", ``)
	assertStatus(t, afterJSON, http.StatusOK)
	if afterJSON.Body.String() != beforeJSON.Body.String() {
		t.Fatalf("最大長メモと開始局面 付随情報のJSONが再起動後に変化した")
	}
}

func Test永続サーバーはHTTPとAIagentの操作を通じて構築された対局を復元する(t *testing.T) {
	dataDir := t.TempDir()
	analyzer := detailedFixedAnalyzer{fixedAnalyzer{move: "pass"}}
	server, err := NewPersistentServer(dataDir, analyzer)
	if err != nil {
		t.Fatal(err)
	}
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":2}`)
	assertStatus(t, created, http.StatusCreated)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/moves", `{"coordinate":"pass"}`), http.StatusOK)
	aiMove := performJSON(t, server.Handler(), http.MethodPost, path+"/ai-moves", `{}`)
	assertStatus(t, aiMove, http.StatusOK)
	var afterPass aiMoveResponse
	if err := json.Unmarshal(aiMove.Body.Bytes(), &afterPass); err != nil {
		t.Fatal(err)
	}
	resultNodeID := afterPass.Game.Game.CurrentNodeID
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPatch, path+"/nodes/"+resultNodeID,
		`{"note":"二連続パスの検討","important":true,"expectedNote":"","expectedImportant":false}`), http.StatusOK)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, "/api/agent/selection",
		`{"gameId":"`+state.Game.ID+`","nodeId":"root"}`), http.StatusNoContent)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, "/api/agent/variations",
		`{"contextId":"`+state.Game.ID+`:root","moves":["E5"],"title":"agentの別案"}`), http.StatusOK)
	beforeResponse := performJSON(t, server.Handler(), http.MethodGet, path, ``)
	assertStatus(t, beforeResponse, http.StatusOK)
	var before gameResponse
	if err := json.Unmarshal(beforeResponse.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if before.Game.Result == nil || before.Game.MainLineNodeID != resultNodeID {
		t.Fatalf("保存前の終局状態 = %+v", before.Game)
	}
	restarted, err := NewPersistentServer(dataDir, analyzer)
	if err != nil {
		t.Fatal(err)
	}
	afterResponse := performJSON(t, restarted.Handler(), http.MethodGet, path, ``)
	assertStatus(t, afterResponse, http.StatusOK)
	var after gameResponse
	if err := json.Unmarshal(afterResponse.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("実操作から保存した対局が一致しない\n保存前=%+v\n復元後=%+v", before, after)
	}
}

func TestHTTP経由で投了した対局を再起動後に復元する(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
	assertStatus(t, created, http.StatusCreated)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	resigned := performJSON(t, server.Handler(), http.MethodPost, path+"/resign", `{}`)
	assertStatus(t, resigned, http.StatusOK)
	var before gameResponse
	if err := json.Unmarshal(resigned.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	resultNodeID := before.Game.CurrentNodeID
	updated := performJSON(t, server.Handler(), http.MethodPatch, path+"/nodes/"+resultNodeID,
		`{"note":"投了結果の検討","important":true,"expectedNote":"","expectedImportant":false}`)
	assertStatus(t, updated, http.StatusOK)
	if err := json.Unmarshal(updated.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	afterResponse := performJSON(t, restarted.Handler(), http.MethodGet, path, ``)
	assertStatus(t, afterResponse, http.StatusOK)
	var after gameResponse
	if err := json.Unmarshal(afterResponse.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("実操作で投了した対局が一致しない\n保存前=%+v\n復元後=%+v", before, after)
	}
}

func Test永続サーバーは中間の主分岐と複数行の付随情報を復元する(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
	assertStatus(t, created, http.StatusCreated)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	first := performJSON(t, server.Handler(), http.MethodPost, path+"/moves", `{"coordinate":"E5"}`)
	assertStatus(t, first, http.StatusOK)
	if err := json.Unmarshal(first.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	firstNodeID := state.Game.CurrentNodeID
	second := performJSON(t, server.Handler(), http.MethodPost, path+"/ai-moves", `{}`)
	assertStatus(t, second, http.StatusOK)
	var aiState aiMoveResponse
	if err := json.Unmarshal(second.Body.Bytes(), &aiState); err != nil {
		t.Fatal(err)
	}
	secondNodeID := aiState.Game.Game.CurrentNodeID
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPatch, path,
		`{"name":"一局目\n検討用","expectedName":""}`), http.StatusOK)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPatch, path+"/nodes/"+firstNodeID,
		`{"note":"第一option\n次の行","important":true,"expectedNote":"","expectedImportant":false}`), http.StatusOK)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/undo", `{}`), http.StatusOK)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/nodes/"+secondNodeID, ``), http.StatusOK)

	beforeResponse := performJSON(t, server.Handler(), http.MethodGet, path, ``)
	assertStatus(t, beforeResponse, http.StatusOK)
	var before gameResponse
	if err := json.Unmarshal(beforeResponse.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if before.Game.MainLineNodeID != firstNodeID || before.Game.CurrentNodeID == firstNodeID {
		t.Fatalf("途中の主分岐の末端と現在の局面を区別できていない：%+v", before.Game)
	}
	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	afterResponse := performJSON(t, restarted.Handler(), http.MethodGet, path, ``)
	assertStatus(t, afterResponse, http.StatusOK)
	var after gameResponse
	if err := json.Unmarshal(afterResponse.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("途中の主分岐の末端と改行を含む付随情報が一致しない\n保存前=%+v\n復元後=%+v", before, after)
	}
}

func Test結果局面を削除した対局を再起動後に復元する(t *testing.T) {
	finish := map[string]func(*testing.T, http.Handler, string) string{
		"投了": func(t *testing.T, handler http.Handler, path string) string {
			response := performJSON(t, handler, http.MethodPost, path+"/resign", `{}`)
			assertStatus(t, response, http.StatusOK)
			var state gameResponse
			if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			return state.Game.CurrentNodeID
		},
		"二連続パス": func(t *testing.T, handler http.Handler, path string) string {
			assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"pass"}`), http.StatusOK)
			response := performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`)
			assertStatus(t, response, http.StatusOK)
			var state aiMoveResponse
			if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			return state.Game.Game.CurrentNodeID
		},
	}
	for name, finishGame := range finish {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
			if err != nil {
				t.Fatal(err)
			}
			created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
			assertStatus(t, created, http.StatusCreated)
			var state gameResponse
			if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			path := "/api/games/" + state.Game.ID
			resultNodeID := finishGame(t, server.Handler(), path)
			assertStatus(t, performJSON(t, server.Handler(), http.MethodDelete, path+"/nodes/"+resultNodeID, expectedNodeIDsBody(t, resultNodeID)), http.StatusOK)
			beforeResponse := performJSON(t, server.Handler(), http.MethodGet, path, ``)
			assertStatus(t, beforeResponse, http.StatusOK)
			var before gameResponse
			if err := json.Unmarshal(beforeResponse.Body.Bytes(), &before); err != nil {
				t.Fatal(err)
			}
			if before.Game.Result != nil || before.Game.Status != "playing" {
				t.Fatalf("結果局面削除後も終局結果が残っている：%+v", before.Game)
			}
			sgfBefore := performJSON(t, server.Handler(), http.MethodGet, path+"/sgf", ``)
			assertStatus(t, sgfBefore, http.StatusOK)
			if strings.Contains(sgfBefore.Body.String(), "RE[") || strings.Contains(sgfBefore.Body.String(), "TB[") || strings.Contains(sgfBefore.Body.String(), "TW[") {
				t.Fatalf("削除した結果の情報をSGFへ書き出した：%s", sgfBefore.Body.String())
			}
			jsonBefore := performJSON(t, server.Handler(), http.MethodGet, path+"/json", ``)
			assertStatus(t, jsonBefore, http.StatusOK)
			jsonImported := performJSON(t, server.Handler(), http.MethodPost, "/api/games/import-json", jsonBefore.Body.String())
			assertStatus(t, jsonImported, http.StatusCreated)
			var jsonState gameResponse
			if err := json.Unmarshal(jsonImported.Body.Bytes(), &jsonState); err != nil {
				t.Fatal(err)
			}
			jsonAfter := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+jsonState.Game.ID+"/json", ``)
			assertStatus(t, jsonAfter, http.StatusOK)
			var jsonBeforeState, jsonAfterState importGameJSONRequest
			if err := json.Unmarshal(jsonBefore.Body.Bytes(), &jsonBeforeState); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(jsonAfter.Body.Bytes(), &jsonAfterState); err != nil {
				t.Fatal(err)
			}
			jsonBeforeState.Game.ID, jsonAfterState.Game.ID = "", ""
			if !reflect.DeepEqual(jsonAfterState, jsonBeforeState) {
				t.Fatalf("結果局面削除後のJSONが固定点に達していない\n取り込み前=%+v\n取り込み後=%+v", jsonBeforeState, jsonAfterState)
			}
			restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
			if err != nil {
				t.Fatal(err)
			}
			afterResponse := performJSON(t, restarted.Handler(), http.MethodGet, path, ``)
			assertStatus(t, afterResponse, http.StatusOK)
			var after gameResponse
			if err := json.Unmarshal(afterResponse.Body.Bytes(), &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("結果局面削除後の状態が一致しない\n保存前=%+v\n復元後=%+v", before, after)
			}
			restartedJSON := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+jsonState.Game.ID+"/json", ``)
			assertStatus(t, restartedJSON, http.StatusOK)
			var restartedJSONState importGameJSONRequest
			if err := json.Unmarshal(restartedJSON.Body.Bytes(), &restartedJSONState); err != nil {
				t.Fatal(err)
			}
			restartedJSONState.Game.ID = ""
			if !reflect.DeepEqual(restartedJSONState, jsonBeforeState) {
				t.Fatalf("結果局面削除後にJSONから作った複製が再起動後に変化した\n再起動前=%+v\n再起動後=%+v", jsonBeforeState, restartedJSONState)
			}
		})
	}
}

func Test取り消した二連続パス結果をAIagentの変更後もSGF往復で復活させない(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	first := performJSON(t, server.Handler(), http.MethodPost, path+"/moves", `{"coordinate":"pass"}`)
	assertStatus(t, first, http.StatusOK)
	var firstState playMoveResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstState); err != nil {
		t.Fatal(err)
	}
	firstPassID := firstState.Game.Game.CurrentNodeID
	finished := performJSON(t, server.Handler(), http.MethodPost, path+"/ai-moves", `{}`)
	assertStatus(t, finished, http.StatusOK)
	var aiState aiMoveResponse
	if err := json.Unmarshal(finished.Body.Bytes(), &aiState); err != nil {
		t.Fatal(err)
	}
	resultID := aiState.Game.Game.CurrentNodeID
	assertStatus(t, performJSON(t, server.Handler(), http.MethodDelete, path+"/nodes/"+resultID, expectedNodeIDsBody(t, resultID)), http.StatusOK)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/nodes/root", ``), http.StatusOK)
	mainVariation := performJSON(t, server.Handler(), http.MethodPost, path+"/variations", `{"coordinate":"E5"}`)
	assertStatus(t, mainVariation, http.StatusOK)
	if err := json.Unmarshal(mainVariation.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/nodes/"+state.Game.CurrentNodeID+"/main-line", ``), http.StatusOK)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"`+firstPassID+`"}`), http.StatusNoContent)
	agentVariation := performJSON(t, server.Handler(), http.MethodPost, "/api/agent/variations", `{"contextId":"`+state.Game.ID+`:`+firstPassID+`","moves":["pass"],"title":"二連続パスの別図"}`)
	assertStatus(t, agentVariation, http.StatusOK)

	before := performJSON(t, server.Handler(), http.MethodGet, path+"/sgf", ``)
	assertStatus(t, before, http.StatusOK)
	if strings.Contains(before.Body.String(), "GP") {
		state := performJSON(t, server.Handler(), http.MethodGet, path, ``)
		assertStatus(t, state, http.StatusOK)
		t.Fatalf("SGFにアプリ固有プロパティが含まれています：%s\nAI agent応答=%s\n対局状態=%s", before.Body.String(), agentVariation.Body.String(), state.Body.String())
	}
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader(before.Body.String()))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	imported := httptest.NewRecorder()
	server.Handler().ServeHTTP(imported, request)
	assertStatus(t, imported, http.StatusCreated)
	var importedState gameResponse
	if err := json.Unmarshal(imported.Body.Bytes(), &importedState); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	after := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+importedState.Game.ID+"/sgf", ``)
	assertStatus(t, after, http.StatusOK)
	if after.Body.String() != before.Body.String() {
		t.Fatalf("結果の取り消しを持つ変化が固定点に達していない\n取り込み前=%s\n取り込み後=%s", before.Body.String(), after.Body.String())
	}
}

func Test取り消した二連続パス局面からの投了を再起動後も保持する(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/moves", `{"coordinate":"pass"}`), http.StatusOK)
	finished := performJSON(t, server.Handler(), http.MethodPost, path+"/ai-moves", `{}`)
	assertStatus(t, finished, http.StatusOK)
	var aiState aiMoveResponse
	if err := json.Unmarshal(finished.Body.Bytes(), &aiState); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, performJSON(t, server.Handler(), http.MethodDelete, path+"/nodes/"+aiState.Game.Game.CurrentNodeID, expectedNodeIDsBody(t, aiState.Game.Game.CurrentNodeID)), http.StatusOK)
	resigned := performJSON(t, server.Handler(), http.MethodPost, path+"/resign", `{}`)
	assertStatus(t, resigned, http.StatusOK)
	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	restored := performJSON(t, restarted.Handler(), http.MethodGet, path, ``)
	assertStatus(t, restored, http.StatusOK)
	if !strings.Contains(restored.Body.String(), `"cause":"resignation"`) {
		t.Fatalf("再起動後に投了結果がない：%s", restored.Body.String())
	}
}

func TestHTTPによる変更を保存できなければ状態を元に戻す(t *testing.T) {
	type requestSpec struct {
		method, path, body string
		activeNodeID       string
	}
	tests := map[string]func(*testing.T, *Server, gameResponse) requestSpec{
		"対局の削除": func(_ *testing.T, _ *Server, state gameResponse) requestSpec {
			return requestSpec{method: http.MethodDelete, path: "/api/games/" + state.Game.ID}
		},
		"人の着手": func(_ *testing.T, _ *Server, state gameResponse) requestSpec {
			return requestSpec{method: http.MethodPost, path: "/api/games/" + state.Game.ID + "/moves", body: `{"coordinate":"E5"}`}
		},
		"AIの着手": func(t *testing.T, server *Server, state gameResponse) requestSpec {
			path := "/api/games/" + state.Game.ID
			assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
			return requestSpec{method: http.MethodPost, path: path + "/ai-moves", body: `{}`}
		},
		"現在の局面の移動": func(t *testing.T, server *Server, state gameResponse) requestSpec {
			path := "/api/games/" + state.Game.ID
			assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/variations", `{"coordinate":"E5"}`), http.StatusOK)
			return requestSpec{method: http.MethodPost, path: path + "/nodes/root", body: ``}
		},
		"対局名": func(_ *testing.T, _ *Server, state gameResponse) requestSpec {
			return requestSpec{method: http.MethodPatch, path: "/api/games/" + state.Game.ID, body: `{"name":"未保存の名前","expectedName":""}`}
		},
		"解析キャッシュ": func(_ *testing.T, _ *Server, state gameResponse) requestSpec {
			return requestSpec{method: http.MethodGet, path: "/api/games/" + state.Game.ID + "/analysis?visits=101", body: ``}
		},
		"メモ": func(_ *testing.T, _ *Server, state gameResponse) requestSpec {
			return requestSpec{method: http.MethodPatch, path: "/api/games/" + state.Game.ID + "/nodes/root", body: `{"note":"未保存","important":true,"expectedNote":"","expectedImportant":false}`}
		},
		"待った": func(t *testing.T, server *Server, state gameResponse) requestSpec {
			path := "/api/games/" + state.Game.ID
			assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
			assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/ai-moves", `{}`), http.StatusOK)
			return requestSpec{method: http.MethodPost, path: path + "/undo", body: `{}`}
		},
		"投了": func(_ *testing.T, _ *Server, state gameResponse) requestSpec {
			return requestSpec{method: http.MethodPost, path: "/api/games/" + state.Game.ID + "/resign", body: `{}`}
		},
		"変化の追加": func(_ *testing.T, _ *Server, state gameResponse) requestSpec {
			return requestSpec{method: http.MethodPost, path: "/api/games/" + state.Game.ID + "/variations", body: `{"coordinate":"E5"}`}
		},
		"変化の削除": func(t *testing.T, server *Server, state gameResponse) requestSpec {
			response := performJSON(t, server.Handler(), http.MethodPost, "/api/games/"+state.Game.ID+"/variations", `{"coordinate":"E5"}`)
			assertStatus(t, response, http.StatusOK)
			var changed gameResponse
			if err := json.Unmarshal(response.Body.Bytes(), &changed); err != nil {
				t.Fatal(err)
			}
			return requestSpec{method: http.MethodDelete, path: "/api/games/" + state.Game.ID + "/nodes/" + changed.Game.CurrentNodeID, body: expectedNodeIDsBody(t, changed.Game.CurrentNodeID), activeNodeID: changed.Game.CurrentNodeID}
		},
		"変化の昇格": func(t *testing.T, server *Server, state gameResponse) requestSpec {
			response := performJSON(t, server.Handler(), http.MethodPost, "/api/games/"+state.Game.ID+"/variations", `{"coordinate":"E5"}`)
			assertStatus(t, response, http.StatusOK)
			var changed gameResponse
			if err := json.Unmarshal(response.Body.Bytes(), &changed); err != nil {
				t.Fatal(err)
			}
			return requestSpec{method: http.MethodPost, path: "/api/games/" + state.Game.ID + "/nodes/" + changed.Game.CurrentNodeID + "/main-line", body: ``}
		},
		"AI agentによる変化の追加": func(t *testing.T, server *Server, state gameResponse) requestSpec {
			selection := performJSON(t, server.Handler(), http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`)
			assertStatus(t, selection, http.StatusNoContent)
			return requestSpec{method: http.MethodPost, path: "/api/agent/variations", body: `{"contextId":"` + state.Game.ID + `:root","moves":["E5"],"title":"未保存"}`}
		},
		"AI agentによる局面移動": func(t *testing.T, server *Server, state gameResponse) requestSpec {
			path := "/api/games/" + state.Game.ID
			variation := performJSON(t, server.Handler(), http.MethodPost, path+"/variations", `{"coordinate":"E5"}`)
			assertStatus(t, variation, http.StatusOK)
			var changed gameResponse
			if err := json.Unmarshal(variation.Body.Bytes(), &changed); err != nil {
				t.Fatal(err)
			}
			return requestSpec{method: http.MethodPost, path: "/api/agent/navigation", body: `{"contextId":"` + state.Game.ID + `:root","nodeId":"` + changed.Game.CurrentNodeID + `"}`}
		},
	}
	for name, prepare := range tests {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			analysisCalls := 0
			server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass", analysisCalls: &analysisCalls})
			if err != nil {
				t.Fatal(err)
			}
			created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
			var state gameResponse
			if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			spec := prepare(t, server, state)
			analysis := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID+"/analysis?visits=100", ``)
			assertStatus(t, analysis, http.StatusOK)
			before := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID, ``)
			assertStatus(t, before, http.StatusOK)
			beforeSGF := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID+"/sgf", ``)
			assertStatus(t, beforeSGF, http.StatusOK)
			beforeJSON := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID+"/json", ``)
			assertStatus(t, beforeJSON, http.StatusOK)
			activeNodeID := spec.activeNodeID
			if activeNodeID == "" {
				activeNodeID = "root"
			}
			selection := performJSON(t, server.Handler(), http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"`+activeNodeID+`"}`)
			assertStatus(t, selection, http.StatusNoContent)
			beforeContext := performJSON(t, server.Handler(), http.MethodGet, "/api/agent/context", ``)
			assertStatus(t, beforeContext, http.StatusOK)
			canonical := filepath.Join(dataDir, state.Game.ID+".json")
			beforeFile, err := os.ReadFile(canonical)
			if err != nil {
				t.Fatal(err)
			}
			availableDataDir := dataDir + ".available"
			if err := os.Rename(dataDir, availableDataDir); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dataDir, []byte("保存先を利用できない状態にする"), 0o600); err != nil {
				t.Fatal(err)
			}
			response := performJSON(t, server.Handler(), spec.method, spec.path, spec.body)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("保存失敗状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
			if err := os.Remove(dataDir); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(availableDataDir, dataDir); err != nil {
				t.Fatal(err)
			}
			callsAfterFailure := analysisCalls
			after := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID, ``)
			assertStatus(t, after, http.StatusOK)
			if after.Body.String() != before.Body.String() {
				t.Fatalf("保存失敗後に状態が残った\n保存前=%s\n失敗後=%s", before.Body.String(), after.Body.String())
			}
			afterSGF := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID+"/sgf", ``)
			assertStatus(t, afterSGF, http.StatusOK)
			if afterSGF.Body.String() != beforeSGF.Body.String() {
				t.Fatalf("保存失敗後にメモリ上の棋譜が変化した\n保存前=%s\n失敗後=%s", beforeSGF.Body.String(), afterSGF.Body.String())
			}
			afterJSON := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID+"/json", ``)
			assertStatus(t, afterJSON, http.StatusOK)
			if afterJSON.Body.String() != beforeJSON.Body.String() {
				t.Fatalf("保存失敗後にメモリ上の保存内容が変化した\n保存前=%s\n失敗後=%s", beforeJSON.Body.String(), afterJSON.Body.String())
			}
			afterFile, err := os.ReadFile(canonical)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(afterFile, beforeFile) {
				t.Fatal("保存失敗で正本が変化した")
			}
			cached := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID+"/analysis?visits=100", ``)
			assertStatus(t, cached, http.StatusOK)
			if analysisCalls != callsAfterFailure {
				t.Fatalf("保存失敗後に解析キャッシュが失われた：解析回数 = %d、失敗直後 = %d", analysisCalls, callsAfterFailure)
			}
			afterContext := performJSON(t, server.Handler(), http.MethodGet, "/api/agent/context", ``)
			assertStatus(t, afterContext, http.StatusOK)
			if afterContext.Body.String() != beforeContext.Body.String() {
				t.Fatalf("保存失敗後に表示中の局面が変化した：保存前=%s、失敗後=%s", beforeContext.Body.String(), afterContext.Body.String())
			}
			switch name {
			case "メモ":
				moved := performJSON(t, server.Handler(), http.MethodPost, "/api/games/"+state.Game.ID+"/moves", `{"coordinate":"E5"}`)
				assertStatus(t, moved, http.StatusOK)
				if strings.Contains(moved.Body.String(), "未保存") {
					t.Fatalf("後続の着手へ保存に失敗したメモが混入した：%s", moved.Body.String())
				}
			case "対局名":
				updated := performJSON(t, server.Handler(), http.MethodPatch, "/api/games/"+state.Game.ID+"/nodes/root", `{"note":"保存できるメモ","important":false,"expectedNote":"","expectedImportant":false}`)
				assertStatus(t, updated, http.StatusOK)
				if strings.Contains(updated.Body.String(), "未保存の名前") {
					t.Fatalf("後続のメモ保存へ保存に失敗した対局名が混入した：%s", updated.Body.String())
				}
			}
		})
	}
}

func Test対局を新しく保存できなければ一覧と正本を変えない(t *testing.T) {
	tests := map[string]struct {
		path, contentType, body string
	}{
		"新規対局": {
			path:        "/api/games",
			contentType: "application/json",
			body:        `{"boardSize":9,"aiLevel":1}`,
		},
		"SGF読み込み": {
			path:        "/api/games/import",
			contentType: "application/x-go-sgf",
			body:        `(;FF[4]GM[1]SZ[9]RU[Chinese])`,
		},
		"JSON読み込み": {
			path:        "/api/games/import-json",
			contentType: "application/json",
		},
	}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
			if err != nil {
				t.Fatal(err)
			}
			created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
			assertStatus(t, created, http.StatusCreated)
			var state gameResponse
			if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if spec.path == "/api/games/import-json" {
				exported := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID+"/json", ``)
				assertStatus(t, exported, http.StatusOK)
				spec.body = exported.Body.String()
			}
			before := performJSON(t, server.Handler(), http.MethodGet, "/api/games", ``)
			assertStatus(t, before, http.StatusOK)
			canonical := filepath.Join(dataDir, state.Game.ID+".json")
			beforeFile, err := os.ReadFile(canonical)
			if err != nil {
				t.Fatal(err)
			}
			availableDataDir := dataDir + ".available"
			if err := os.Rename(dataDir, availableDataDir); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dataDir, []byte("保存先を利用できない状態にする"), 0o600); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, spec.path, strings.NewReader(spec.body))
			request.Header.Set("Content-Type", spec.contentType)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("保存失敗状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
			if err := os.Remove(dataDir); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(availableDataDir, dataDir); err != nil {
				t.Fatal(err)
			}
			after := performJSON(t, server.Handler(), http.MethodGet, "/api/games", ``)
			assertStatus(t, after, http.StatusOK)
			if after.Body.String() != before.Body.String() {
				t.Fatalf("保存失敗後に一覧が変化した\n保存前=%s\n失敗後=%s", before.Body.String(), after.Body.String())
			}
			files, err := filepath.Glob(filepath.Join(dataDir, "*.json"))
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 1 || files[0] != canonical {
				t.Fatalf("保存失敗後に正本の組が変化した：%v", files)
			}
			afterFile, err := os.ReadFile(canonical)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(afterFile, beforeFile) {
				t.Fatal("保存失敗後に既存の正本が変化した")
			}
		})
	}
}

func assertStatus(t *testing.T, response interface{ Result() *http.Response }, want int) {
	t.Helper()
	if got := response.Result().StatusCode; got != want {
		t.Fatalf("状態コード = %d、期待値 = %d", got, want)
	}
}
