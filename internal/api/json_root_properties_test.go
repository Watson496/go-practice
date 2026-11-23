package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"go-practice/internal/game"
)

func TestJSON取り込みは局面番号の整数オーバーフローを拒否する(t *testing.T) {
	session, err := game.NewConfiguredSession("source", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	snapshot.NextNodeNumber = math.MaxInt
	record := importGameJSONRequest{
		SchemaVersion: userGameRecordSchemaVersion,
		RecordedAt:    "2023-01-01T00:00:00Z",
		HumanColor:    ptr("B"),
		AIColor:       ptr("W"),
		AILevel:       ptr(1),
		Game:          snapshot,
	}
	contents, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, newTestServer(t, fixedAnalyzer{move: "pass"}).Handler(), http.MethodPost, "/api/games/import-json", string(contents))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func TestJSON取り込みは着手のない局面の着手注釈を拒否する(t *testing.T) {
	session, err := game.NewConfiguredSession("source", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	snapshot.RootProperties = []game.SGFProperty{{Ident: "BM", Values: []string{"1"}}}
	record := importGameJSONRequest{
		SchemaVersion: userGameRecordSchemaVersion,
		RecordedAt:    "2023-01-01T00:00:00Z",
		HumanColor:    ptr("B"),
		AIColor:       ptr("W"),
		AILevel:       ptr(1),
		Game:          snapshot,
	}
	contents, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, newTestServer(t, fixedAnalyzer{move: "pass"}).Handler(), http.MethodPost, "/api/games/import-json", string(contents))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func TestJSON取り込みは局面数の上限を超えた棋譜を拒否する(t *testing.T) {
	session, err := game.NewConfiguredSession("source", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	for number := 1; number <= 4096; number++ {
		snapshot.TreeNodes = append(snapshot.TreeNodes, game.TreeNode{ID: "n" + strconv.Itoa(number), ParentID: "root"})
	}
	snapshot.NextNodeNumber = 4097
	record := importGameJSONRequest{
		SchemaVersion: userGameRecordSchemaVersion,
		RecordedAt:    "2023-01-01T00:00:00Z",
		HumanColor:    ptr("B"),
		AIColor:       ptr("W"),
		AILevel:       ptr(1),
		Game:          snapshot,
	}
	contents, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, newTestServer(t, fixedAnalyzer{move: "pass"}).Handler(), http.MethodPost, "/api/games/import-json", string(contents))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func TestJSON取り込みは完成形にない局面の識別情報を拒否する(t *testing.T) {
	session, err := game.NewConfiguredSession("source", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	snapshot.TreeNodes[0].ID = "node"
	record := importGameJSONRequest{
		SchemaVersion: userGameRecordSchemaVersion,
		RecordedAt:    "2023-01-01T00:00:00Z",
		HumanColor:    ptr("B"),
		AIColor:       ptr("W"),
		AILevel:       ptr(1),
		Game:          snapshot,
	}
	contents, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, newTestServer(t, fixedAnalyzer{move: "pass"}).Handler(), http.MethodPost, "/api/games/import-json", string(contents))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func TestJSON往復は日付と開始局面属性の順序を維持する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
	imported := httptest.NewRecorder()
	source := "(;FF[4]CA[UTF-8]SZ[9]PB[黒]GN[対局]DT[2023-01-01]PW[白])"
	handler.ServeHTTP(imported, sgfImportRequest(source))
	assertStatus(t, imported, http.StatusCreated)
	var first gameResponse
	if err := json.Unmarshal(imported.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	exportedJSON := performJSON(t, handler, http.MethodGet, "/api/games/"+first.Game.ID+"/json", ``)
	assertStatus(t, exportedJSON, http.StatusOK)
	reimported := performJSON(t, handler, http.MethodPost, "/api/games/import-json", exportedJSON.Body.String())
	assertStatus(t, reimported, http.StatusCreated)
	var second gameResponse
	if err := json.Unmarshal(reimported.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	exportedSGF := httptest.NewRecorder()
	handler.ServeHTTP(exportedSGF, httptest.NewRequest(http.MethodGet, "/api/games/"+second.Game.ID+"/sgf", nil))
	assertStatus(t, exportedSGF, http.StatusOK)
	wantOrder := "PB[黒]GN[対局]DT[2023-01-01]PW[白]"
	if !strings.Contains(exportedSGF.Body.String(), wantOrder) {
		t.Fatalf("JSON往復後の開始局面の属性 = %s", exportedSGF.Body.String())
	}
}

func TestJSONが無効または競合する開始局面日付を拒否する(t *testing.T) {
	session, err := game.NewConfiguredSession("source", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		date string
	}{
		{date: "2023/01/01"},
		{date: "2023-01-02"},
	} {
		snapshot := session.Snapshot()
		snapshot.RootProperties = append(snapshot.RootProperties, game.SGFProperty{Ident: "DT", Values: []string{test.date}})
		record := importGameJSONRequest{SchemaVersion: userGameRecordSchemaVersion, RecordedAt: "2023-01-01T00:00:00Z", HumanColor: ptr("B"), AIColor: ptr("W"), AILevel: ptr(1), Game: snapshot}
		contents, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		response := performJSON(t, newTestServer(t, fixedAnalyzer{move: "pass"}).Handler(), http.MethodPost, "/api/games/import-json", string(contents))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("DT[%s]：状態コード = %d、本文 = %s", test.date, response.Code, response.Body.String())
		}
	}
}

func TestJSON開始局面日付は開始日時に含まれる時差を使用する(t *testing.T) {
	for _, test := range []struct {
		name, recordedAt, date string
	}{
		{name: "協定世界時", recordedAt: "2023-01-01T00:30:00Z", date: "2023-01-01"},
		{name: "正の最大時差", recordedAt: "2023-01-01T00:30:00+14:00", date: "2023-01-01"},
		{name: "負の最大時差", recordedAt: "2023-12-31T23:30:00-12:00", date: "2023-12-31"},
		{name: "日本標準時", recordedAt: "2023-06-15T23:30:00+09:00", date: "2023-06-15"},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, err := game.NewConfiguredSession("source", 9, 7.5, 0)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := session.Snapshot()
			snapshot.RootProperties = append(snapshot.RootProperties, game.SGFProperty{Ident: "DT", Values: []string{test.date}})
			record := importGameJSONRequest{SchemaVersion: userGameRecordSchemaVersion, RecordedAt: test.recordedAt, HumanColor: ptr("B"), AIColor: ptr("W"), AILevel: ptr(1), Game: snapshot}
			contents, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			response := performJSON(t, newTestServer(t, fixedAnalyzer{move: "pass"}).Handler(), http.MethodPost, "/api/games/import-json", string(contents))
			if response.Code != http.StatusCreated {
				t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
		})
	}
}
