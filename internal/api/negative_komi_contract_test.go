package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func Test負のコミの対局が二連続パスで終局して往復できる(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"komi":-0.5,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/moves", `{"coordinate":"pass"}`), http.StatusOK)
	finished := performJSON(t, server.Handler(), http.MethodPost, path+"/ai-moves", `{}`)
	assertStatus(t, finished, http.StatusOK)
	var aiResult aiMoveResponse
	if err := json.Unmarshal(finished.Body.Bytes(), &aiResult); err != nil {
		t.Fatal(err)
	}
	state = aiResult.Game
	result := state.Game.Result
	if result == nil || result.Winner != "B" || result.Margin != 0.5 || result.BlackScore != 0.5 || result.WhiteScore != 0 {
		t.Fatalf("負のコミの終局結果 = %+v", result)
	}

	exported := performJSON(t, server.Handler(), http.MethodGet, path+"/sgf", ``)
	assertStatus(t, exported, http.StatusOK)
	imported := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader(exported.Body.String()))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	server.Handler().ServeHTTP(imported, request)
	assertStatus(t, imported, http.StatusCreated)
	var importedState gameResponse
	if err := json.Unmarshal(imported.Body.Bytes(), &importedState); err != nil {
		t.Fatal(err)
	}
	reexported := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+importedState.Game.ID+"/sgf", ``)
	assertStatus(t, reexported, http.StatusOK)
	if exported.Body.String() != reexported.Body.String() {
		t.Fatalf("負のコミのSGFが往復で変化した\n1回目=%s\n2回目=%s", exported.Body.String(), reexported.Body.String())
	}

	jsonExport := performJSON(t, server.Handler(), http.MethodGet, path+"/json", ``)
	assertStatus(t, jsonExport, http.StatusOK)
	var before importGameJSONRequest
	if err := json.Unmarshal(jsonExport.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	jsonImport := performJSON(t, server.Handler(), http.MethodPost, "/api/games/import-json", jsonExport.Body.String())
	assertStatus(t, jsonImport, http.StatusCreated)
	var jsonState gameResponse
	if err := json.Unmarshal(jsonImport.Body.Bytes(), &jsonState); err != nil {
		t.Fatal(err)
	}
	jsonAgain := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+jsonState.Game.ID+"/json", ``)
	assertStatus(t, jsonAgain, http.StatusOK)
	var after importGameJSONRequest
	if err := json.Unmarshal(jsonAgain.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	before.Game.ID, after.Game.ID = "", ""
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("負のコミのJSONが往復で変化した\n1回目=%+v\n2回目=%+v", before, after)
	}

	restarted, err := NewPersistentServer(server.dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	restored := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+jsonState.Game.ID+"/json", ``)
	assertStatus(t, restored, http.StatusOK)
	var afterRestart importGameJSONRequest
	if err := json.Unmarshal(restored.Body.Bytes(), &afterRestart); err != nil {
		t.Fatal(err)
	}
	afterRestart.Game.ID = ""
	if !reflect.DeepEqual(after, afterRestart) {
		t.Fatalf("負のコミの対局が再起動で変化した\n再起動前=%+v\n再起動後=%+v", after, afterRestart)
	}
}
