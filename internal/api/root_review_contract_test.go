package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func importSGFGame(t *testing.T, handler http.Handler, source string) gameResponse {
	t.Helper()
	request := sgfImportRequest(source)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertStatus(t, response, http.StatusCreated)
	var state gameResponse
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func sgfImportRequest(source string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader(source))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	return request
}

func Test開始局面メモではすべての競合チェックに開始局面属性を使用する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	state := importSGFGame(t, server.Handler(), "(;FF[4]CA[UTF-8]SZ[9]C[最初のメモ])")
	path := "/api/games/" + state.Game.ID + "/nodes/root"
	first := performJSON(t, server.Handler(), http.MethodPatch, path, `{"note":"二回目","important":true,"expectedNote":"最初のメモ","expectedImportant":false}`)
	assertStatus(t, first, http.StatusOK)
	second := performJSON(t, server.Handler(), http.MethodPatch, path, `{"note":"三回目","important":false,"expectedNote":"二回目","expectedImportant":true}`)
	assertStatus(t, second, http.StatusOK)
	stale := performJSON(t, server.Handler(), http.MethodPatch, path, `{"note":"上書き","important":false,"expectedNote":"","expectedImportant":false}`)
	assertStatus(t, stale, http.StatusConflict)
}
