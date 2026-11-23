package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func Test画面操作の通知は存在する対象だけを受け付ける(t *testing.T) {
	server, err := NewPersistentServer(t.TempDir(), fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, performJSON(t, handler, http.MethodPut, "/api/ui/interaction", `{"token":"missing-game","kind":"rename","gameId":"000000000000000000000000"}`), http.StatusNotFound)
	assertStatus(t, performJSON(t, handler, http.MethodPut, "/api/ui/interaction", `{"token":"missing-node","kind":"review","gameId":"`+state.Game.ID+`","nodeId":"n999"}`), http.StatusNotFound)
	assertStatus(t, performJSON(t, handler, http.MethodPut, "/api/ui/interaction", `{"token":"missing-subtree","kind":"delete-variation","gameId":"`+state.Game.ID+`","nodeId":"root","nodeIds":["n999"]}`), http.StatusNotFound)
}

func Test画面操作の通知は2分で失効する(t *testing.T) {
	server, err := NewPersistentServer(t.TempDir(), fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	server.now = func() time.Time { return now }
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	gameID := state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPut, "/api/ui/interaction", `{"token":"rename-token","kind":"rename","gameId":"`+gameID+`"}`), http.StatusNoContent)
	assertStatus(t, performAgentJSON(t, handler, http.MethodPatch, "/api/games/"+gameID, `{"name":"変更","expectedName":""}`), http.StatusConflict)
	now = now.Add(2 * time.Minute)
	assertStatus(t, performAgentJSON(t, handler, http.MethodPatch, "/api/games/"+gameID, `{"name":"変更","expectedName":""}`), http.StatusOK)
}

func Testメモ編集中は競合するAIagent操作だけを拒否する(t *testing.T) {
	server, err := NewPersistentServer(t.TempDir(), fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	variation := performJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/variations", `{"coordinate":"D4"}`)
	assertStatus(t, variation, http.StatusOK)
	if err := json.Unmarshal(variation.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	variationID := state.Game.CurrentNodeID
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"`+variationID+`"}`), http.StatusNoContent)
	begin := `{"token":"review-token","kind":"review","gameId":"` + state.Game.ID + `","nodeId":"` + variationID + `"}`
	assertStatus(t, performJSON(t, handler, http.MethodPut, "/api/ui/interaction", begin), http.StatusNoContent)

	navigation := performAgentJSON(t, handler, http.MethodPost, "/api/agent/navigation", `{"contextId":"`+state.Game.ID+`:`+variationID+`","nodeId":"root"}`)
	assertStatus(t, navigation, http.StatusConflict)
	update := performAgentJSON(t, handler, http.MethodPatch, "/api/games/"+state.Game.ID+"/nodes/"+variationID, `{"note":"更新","important":false,"expectedNote":"","expectedImportant":false}`)
	assertStatus(t, update, http.StatusConflict)
	playMain := performAgentJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/moves", `{"coordinate":"E5"}`)
	assertStatus(t, playMain, http.StatusConflict)
	playVariation := performAgentJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/variations", `{"coordinate":"E4"}`)
	assertStatus(t, playVariation, http.StatusConflict)
	showVariation := performAgentJSON(t, handler, http.MethodPost, "/api/agent/variations", `{"contextId":"`+state.Game.ID+`:`+variationID+`","moves":["E4"],"title":"別図"}`)
	assertStatus(t, showVariation, http.StatusOK)
	unrelated := performAgentJSON(t, handler, http.MethodPatch, "/api/games/"+state.Game.ID+"/nodes/root", `{"note":"開始局面","important":false,"expectedNote":"","expectedImportant":false}`)
	assertStatus(t, unrelated, http.StatusOK)
	deletion := performAgentJSON(t, handler, http.MethodDelete, "/api/games/"+state.Game.ID+"/nodes/"+variationID, `{"expectedNodeIds":["`+variationID+`"]}`)
	assertStatus(t, deletion, http.StatusConflict)

	assertStatus(t, performJSON(t, handler, http.MethodDelete, "/api/ui/interaction", `{"token":"review-token"}`), http.StatusNoContent)
	navigation = performAgentJSON(t, handler, http.MethodPost, "/api/agent/navigation", `{"contextId":"`+state.Game.ID+`:`+variationID+`","nodeId":"root"}`)
	assertStatus(t, navigation, http.StatusOK)
}

func TestModalと並べ替え中は対象が重なるAIagent操作を拒否する(t *testing.T) {
	server, err := NewPersistentServer(t.TempDir(), fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	gameID := state.Game.ID

	assertStatus(t, performJSON(t, handler, http.MethodPut, "/api/ui/interaction", `{"token":"rename-token","kind":"rename","gameId":"`+gameID+`"}`), http.StatusNoContent)
	assertStatus(t, performAgentJSON(t, handler, http.MethodPatch, "/api/games/"+gameID, `{"name":"変更","expectedName":""}`), http.StatusConflict)
	assertStatus(t, performAgentJSON(t, handler, http.MethodPost, "/api/games/"+gameID+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	assertStatus(t, performAgentJSON(t, handler, http.MethodDelete, "/api/games/"+gameID, ``), http.StatusConflict)
	assertStatus(t, performJSON(t, handler, http.MethodDelete, "/api/ui/interaction", `{"token":"rename-token"}`), http.StatusNoContent)

	assertStatus(t, performJSON(t, handler, http.MethodPut, "/api/ui/interaction", `{"token":"reorder-token","kind":"reorder"}`), http.StatusNoContent)
	assertStatus(t, performAgentJSON(t, handler, http.MethodPut, "/api/games/order", `{"ids":["`+gameID+`"]}`), http.StatusConflict)
	assertStatus(t, performJSON(t, handler, http.MethodDelete, "/api/ui/interaction", `{"token":"reorder-token"}`), http.StatusNoContent)

	assertStatus(t, performJSON(t, handler, http.MethodPut, "/api/ui/interaction", `{"token":"delete-token","kind":"delete-game","gameId":"`+gameID+`"}`), http.StatusNoContent)
	assertStatus(t, performAgentJSON(t, handler, http.MethodPost, "/api/games/"+gameID+"/nodes/root", ``), http.StatusConflict)
	assertStatus(t, performAgentJSON(t, handler, http.MethodPost, "/api/games/"+gameID+"/variations", `{"coordinate":"D4"}`), http.StatusConflict)
	assertStatus(t, performAgentJSON(t, handler, http.MethodPatch, "/api/games/"+gameID, `{"name":"変更","expectedName":""}`), http.StatusConflict)
	assertStatus(t, performAgentJSON(t, handler, http.MethodDelete, "/api/games/"+gameID, ``), http.StatusConflict)
}

func Test変化の削除確認中は対象を変える操作だけを拒否する(t *testing.T) {
	server, err := NewPersistentServer(t.TempDir(), fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	variation := performJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/variations", `{"coordinate":"D4"}`)
	assertStatus(t, variation, http.StatusOK)
	if err := json.Unmarshal(variation.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	variationID := state.Game.CurrentNodeID
	begin := `{"token":"delete-variation-token","kind":"delete-variation","gameId":"` + state.Game.ID + `","nodeId":"` + variationID + `","nodeIds":["` + variationID + `"]}`
	assertStatus(t, performJSON(t, handler, http.MethodPut, "/api/ui/interaction", begin), http.StatusNoContent)

	// 局面を見るだけの操作は、削除対象を変えない。
	assertStatus(t, performAgentJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/nodes/"+variationID, ``), http.StatusOK)
	assertStatus(t, performAgentJSON(t, handler, http.MethodPatch, "/api/games/"+state.Game.ID+"/nodes/"+variationID, `{"note":"更新","important":false,"expectedNote":"","expectedImportant":false}`), http.StatusConflict)
	assertStatus(t, performAgentJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/variations", `{"coordinate":"E4"}`), http.StatusConflict)
	assertStatus(t, performAgentJSON(t, handler, http.MethodDelete, "/api/games/"+state.Game.ID, ``), http.StatusConflict)

	// 既存の変化へ移るだけなら、削除対象自体は変わらない。
	assertStatus(t, performAgentJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/nodes/root", ``), http.StatusOK)
	assertStatus(t, performAgentJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/variations", `{"coordinate":"D4"}`), http.StatusOK)
	// 主分岐にする操作は削除対象を含む主分岐へ変更する。
	assertStatus(t, performAgentJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/moves", `{"coordinate":"D4"}`), http.StatusConflict)
}

func Test変化の削除確認中は対象を通る主分岐への変更を拒否する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	gameID := state.Game.ID
	first := performJSON(t, handler, http.MethodPost, "/api/games/"+gameID+"/variations", `{"coordinate":"D4"}`)
	assertStatus(t, first, http.StatusOK)
	if err := json.Unmarshal(first.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	firstID := state.Game.CurrentNodeID
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+gameID+`","nodeId":"`+firstID+`"}`), http.StatusNoContent)
	added := performAgentJSON(t, handler, http.MethodPost, "/api/agent/variations", `{"contextId":"`+gameID+`:`+firstID+`","moves":["E4","F4"],"title":"削除対象"}`)
	assertStatus(t, added, http.StatusOK)
	loaded := performJSON(t, handler, http.MethodGet, "/api/games/"+gameID, "")
	assertStatus(t, loaded, http.StatusOK)
	if err := json.Unmarshal(loaded.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	var targetIDs []string
	for _, node := range state.Game.TreeNodes {
		if node.Move != nil && (node.Move.Coordinate == "E4" || node.Move.Coordinate == "F4") {
			targetIDs = append(targetIDs, node.ID)
		}
	}
	if len(targetIDs) != 2 {
		t.Fatalf("削除対象 = %v", targetIDs)
	}
	begin := `{"token":"delete-promoted-path","kind":"delete-variation","gameId":"` + gameID + `","nodeId":"` + targetIDs[0] + `","nodeIds":["` + targetIDs[0] + `","` + targetIDs[1] + `"]}`
	assertStatus(t, performJSON(t, handler, http.MethodPut, "/api/ui/interaction", begin), http.StatusNoContent)
	reused := performAgentJSON(t, handler, http.MethodPost, "/api/agent/variations", `{"contextId":"`+gameID+`:`+firstID+`","moves":["E4","F4"],"title":"削除対象"}`)
	assertStatus(t, reused, http.StatusOK)
	promoted := performAgentJSON(t, handler, http.MethodPost, "/api/games/"+gameID+"/nodes/"+firstID+"/main-line", "")
	assertStatus(t, promoted, http.StatusConflict)
}

func performAgentJSON(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set(agentRequestHeader, "1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
