package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-practice/internal/game"
)

func Test対局作成とJSONおよびSGFの読み込みでコミを検証する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	tests := map[string]struct {
		request func() *httptest.ResponseRecorder
	}{
		"対局作成": {request: func() *httptest.ResponseRecorder {
			return performJSON(t, server.Handler(), http.MethodPost, "/api/games", `{"boardSize":9,"komi":101,"aiLevel":1}`)
		}},
		"JSON読み込み": {request: func() *httptest.ResponseRecorder {
			session, err := game.NewConfiguredSession("json", 9, 7.5, 0)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := session.Snapshot()
			snapshot.Komi = -101
			contents, err := json.Marshal(importGameJSONRequest{
				SchemaVersion: "1", RecordedAt: "2026-11-04T00:00:00Z", HumanColor: ptr("B"), AIColor: ptr("W"), AILevel: ptr(1), Game: snapshot,
			})
			if err != nil {
				t.Fatal(err)
			}
			return performJSON(t, server.Handler(), http.MethodPost, "/api/games/import-json", string(contents))
		}},
		"SGF読み込み": {request: func() *httptest.ResponseRecorder {
			request := httptest.NewRequest(http.MethodPost, "/api/games/import", bytes.NewBufferString("(;FF[4]SZ[9]KM[NaN])"))
			request.Header.Set("Content-Type", "application/x-go-sgf")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			return response
		}},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := test.request()
			if response.Code != http.StatusBadRequest {
				t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
		})
	}
}

func Test丸めると0点5目になるコミを全入口で拒否する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
	created := performJSON(t, handler, http.MethodPost, "/api/games", `{"boardSize":9,"komi":0.49999999999999999,"aiLevel":1}`)
	assertStatus(t, created, http.StatusBadRequest)

	valid := createGameForTest(t, handler, `{"boardSize":9,"komi":0.5,"aiLevel":1}`)
	var exported map[string]any
	if err := json.Unmarshal(valid.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	gameValue, ok := exported["game"].(map[string]any)
	if !ok {
		t.Fatalf("作成した対局の応答 = %#v", exported)
	}
	gameValue["komi"] = json.RawMessage("0.49999999999999999")
	gameData, err := json.Marshal(gameValue)
	if err != nil {
		t.Fatal(err)
	}
	imported := performJSON(t, handler, http.MethodPost, "/api/games/import-json", `{"schemaVersion":"1","recordedAt":"2026-11-14T00:00:00Z","humanColor":"B","aiColor":"W","aiLevel":1,"game":`+string(gameData)+`}`)
	assertStatus(t, imported, http.StatusBadRequest)

	sgfRequest := httptest.NewRequest(http.MethodPost, "/api/games/import", bytes.NewBufferString("(;FF[4]SZ[9]KM[0.49999999999999999])"))
	sgfRequest.Header.Set("Content-Type", "application/x-go-sgf")
	sgfResponse := httptest.NewRecorder()
	handler.ServeHTTP(sgfResponse, sgfRequest)
	assertStatus(t, sgfResponse, http.StatusBadRequest)
}
