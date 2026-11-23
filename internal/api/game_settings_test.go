package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func importSGFForSettingsTest(t *testing.T, handler http.Handler, source string) gameResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader(source))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertStatus(t, response, http.StatusCreated)
	var state gameResponse
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func Test読み込んだ対局は設定するまで対局の操作を拒否する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "D5"}).Handler()
	state := importSGFForSettingsTest(t, handler, "(;FF[4]SZ[9];B[ee])")
	if state.AILevel != nil || state.HumanColor != nil {
		t.Fatalf("読み込んだ対局の設定 = AIレベル%v、人の石の色%v", state.AILevel, state.HumanColor)
	}
	path := "/api/games/" + state.Game.ID
	for _, operation := range []struct{ suffix, body string }{
		{"/moves", `{"coordinate":"D4"}`},
		{"/ai-moves", `{}`},
		{"/resign", `{}`},
		{"/undo", `{}`},
	} {
		rejected := performJSON(t, handler, http.MethodPost, path+operation.suffix, operation.body)
		if rejected.Code != http.StatusConflict {
			t.Fatalf("%sの状態コード = %d、本文 = %s", operation.suffix, rejected.Code, rejected.Body.String())
		}
	}

	updated := performJSON(t, handler, http.MethodPatch, path+"/settings", `{"aiLevel":2,"humanColor":"W"}`)
	assertStatus(t, updated, http.StatusOK)
	if err := json.Unmarshal(updated.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if value(state.AILevel) != 2 || value(state.HumanColor) != "W" {
		t.Fatalf("変更後の設定 = AIレベル%v、人の石の色%v", state.AILevel, state.HumanColor)
	}
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"D4"}`), http.StatusOK)
}

func Test人の石の色を変えると待ったで取り消す手も変わる(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "D5"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPatch, path+"/settings", `{"humanColor":"W"}`), http.StatusOK)

	undone := performJSON(t, handler, http.MethodPost, path+"/undo", `{}`)
	assertStatus(t, undone, http.StatusOK)
	if err := json.Unmarshal(undone.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Game.Moves) != 1 || state.Game.Moves[0].Coordinate != "E5" || value(state.AILevel) != 1 {
		t.Fatalf("待った後の局面 = %+v", state.Game.Moves)
	}
}

func Test設定の変更はAIが手を決める処理を中止する(t *testing.T) {
	analyzer := &cancellableBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	handler := newTestServer(t, analyzer).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"W"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	aiDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { aiDone <- performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`) }()
	waitForTestSignal(t, analyzer.started, "AIの思考の開始")
	assertStatus(t, performJSON(t, handler, http.MethodPatch, path+"/settings", `{"aiLevel":3}`), http.StatusOK)
	waitForTestSignal(t, analyzer.canceled, "設定の変更によるAIの思考の中止")
	assertStatus(t, waitForTestSignal(t, aiDone, "AIの手の要求の終了"), http.StatusConflict)
}

func Test設定の変更は値のない指定と範囲外の値を拒否する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "D5"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID + "/settings"
	for _, body := range []string{`{}`, `{"aiLevel":5}`, `{"humanColor":"X"}`, `{"aiLevel":null}`} {
		assertStatus(t, performJSON(t, handler, http.MethodPatch, path, body), http.StatusBadRequest)
	}
	assertStatus(t, performJSON(t, handler, http.MethodPatch, "/api/games/000000000000000000000000/settings", `{"aiLevel":2}`), http.StatusNotFound)
}

func Test未設定の設定をJSONの書き出しと読み込みで保つ(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "D5"}).Handler()
	state := importSGFForSettingsTest(t, handler, "(;FF[4]SZ[9];B[ee])")
	exported := performJSON(t, handler, http.MethodGet, "/api/games/"+state.Game.ID+"/json", "")
	assertStatus(t, exported, http.StatusOK)
	if !strings.Contains(exported.Body.String(), `"humanColor":null`) || !strings.Contains(exported.Body.String(), `"aiColor":null`) || !strings.Contains(exported.Body.String(), `"aiLevel":null`) {
		t.Fatalf("書き出したJSON = %s", exported.Body.String())
	}
	imported := performJSON(t, handler, http.MethodPost, "/api/games/import-json", exported.Body.String())
	assertStatus(t, imported, http.StatusCreated)
	if err := json.Unmarshal(imported.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.AILevel != nil || state.HumanColor != nil {
		t.Fatalf("読み込んだ対局の設定 = AIレベル%v、人の石の色%v", state.AILevel, state.HumanColor)
	}
}
