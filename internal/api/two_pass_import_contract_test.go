package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-practice/internal/game"
)

func TestJSONは結果または取り消しなしで2つのパスの主分岐の末端を拒否する(t *testing.T) {
	session, err := game.NewConfiguredSession("source", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	resultID := snapshot.CurrentNodeID
	resultParentID := ""
	nodes := snapshot.TreeNodes[:0]
	for _, node := range snapshot.TreeNodes {
		if node.ID == resultID {
			resultParentID = node.ParentID
			continue
		}
		nodes = append(nodes, node)
	}
	snapshot.TreeNodes = nodes
	snapshot.CurrentNodeID, snapshot.MainLineNodeID = resultParentID, resultParentID
	snapshot.Status, snapshot.Result, snapshot.ConsecutivePasses = "playing", nil, 0
	record := importGameJSONRequest{SchemaVersion: userGameRecordSchemaVersion, RecordedAt: "2026-11-05T00:00:00Z", HumanColor: ptr("B"), AIColor: ptr("W"), AILevel: ptr(1), Game: snapshot}
	contents, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, newTestServer(t, fixedAnalyzer{move: "pass"}).Handler(), http.MethodPost, "/api/games/import-json", string(contents))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func Test二連続パスで終わるSGFを正規の結果付きで往復する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader("(;FF[4]SZ[9];B[];W[])"))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	assertStatus(t, response, http.StatusCreated)
	var state gameResponse
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Game.Status != "finished" || state.Game.Result == nil {
		t.Fatalf("二連続パス SGFの状態 = %+v", state.Game)
	}
	sgfResponse := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID+"/sgf", ``)
	assertStatus(t, sgfResponse, http.StatusOK)
	if strings.Contains(sgfResponse.Body.String(), "GP") || !strings.Contains(sgfResponse.Body.String(), "RE[") {
		t.Fatalf("二連続パス結果のSGF = %s", sgfResponse.Body.String())
	}
	jsonResponse := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID+"/json", ``)
	assertStatus(t, jsonResponse, http.StatusOK)
	assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, "/api/games/import-json", jsonResponse.Body.String()), http.StatusCreated)
}
