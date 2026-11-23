package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"go-practice/internal/game"
)

func TestSGFを読み込んで書き出しても対局名を保持する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	firstImport := httptest.NewRecorder()
	server.Handler().ServeHTTP(firstImport, sgfImportRequest("(;FF[4]SZ[9]GN[  a\tb\r\nc  ])"))
	assertStatus(t, firstImport, http.StatusCreated)
	var first gameResponse
	if err := json.Unmarshal(firstImport.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Name != "a b c" {
		t.Fatalf("SGFから読み込んだ対局名 = %q", first.Name)
	}

	firstExport := httptest.NewRecorder()
	server.Handler().ServeHTTP(firstExport, httptest.NewRequest(http.MethodGet, "/api/games/"+first.Game.ID+"/sgf", nil))
	assertStatus(t, firstExport, http.StatusOK)
	if !strings.Contains(firstExport.Body.String(), "GN[a b c]") {
		t.Fatalf("SGFに対局名がない：%s", firstExport.Body.String())
	}

	secondImport := httptest.NewRecorder()
	server.Handler().ServeHTTP(secondImport, sgfImportRequest(firstExport.Body.String()))
	assertStatus(t, secondImport, http.StatusCreated)
	var second gameResponse
	if err := json.Unmarshal(secondImport.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.Name != first.Name {
		t.Fatalf("SGF往復後の対局名 = %q、期待値 = %q", second.Name, first.Name)
	}
	secondExport := httptest.NewRecorder()
	server.Handler().ServeHTTP(secondExport, httptest.NewRequest(http.MethodGet, "/api/games/"+second.Game.ID+"/sgf", nil))
	assertStatus(t, secondExport, http.StatusOK)
	if secondExport.Body.String() != firstExport.Body.String() {
		t.Fatalf("対局名を含むSGFが固定点に達していない：\n%s\n%s", firstExport.Body.String(), secondExport.Body.String())
	}
}

func TestSGF書き出しでは現在の対局名が使用される(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	imported := httptest.NewRecorder()
	server.Handler().ServeHTTP(imported, sgfImportRequest("(;FF[4]SZ[9]GN[old])"))
	assertStatus(t, imported, http.StatusCreated)
	var state gameResponse
	if err := json.Unmarshal(imported.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	renamed := performJSON(t, server.Handler(), http.MethodPatch, "/api/games/"+state.Game.ID, `{"name":"new","expectedName":"old"}`)
	assertStatus(t, renamed, http.StatusOK)
	exported := httptest.NewRecorder()
	server.Handler().ServeHTTP(exported, httptest.NewRequest(http.MethodGet, "/api/games/"+state.Game.ID+"/sgf", nil))
	assertStatus(t, exported, http.StatusOK)
	if !strings.Contains(exported.Body.String(), "GN[new]") || strings.Contains(exported.Body.String(), "GN[old]") {
		t.Fatalf("アプリの対局名を正本として書き出していない：%s", exported.Body.String())
	}
}

func TestSGF対局名が空および長すぎる値を処理する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	emptyImport := httptest.NewRecorder()
	server.Handler().ServeHTTP(emptyImport, sgfImportRequest("(;FF[4]SZ[9]GN[\t])"))
	assertStatus(t, emptyImport, http.StatusCreated)
	var empty gameResponse
	if err := json.Unmarshal(emptyImport.Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\d{6}-\d{4}読込$`).MatchString(empty.Name) {
		t.Fatalf("空白だけのGNで対局日もない棋譜の対局名 = %q", empty.Name)
	}
	if empty.StartedAt != "" || empty.ImportedAt == "" {
		t.Fatalf("対局日のない棋譜の開始日時 = %q、読み込んだ時刻 = %q", empty.StartedAt, empty.ImportedAt)
	}
	if empty.StartedAt != "" {
		t.Fatalf("対局日のないSGFの開始日時 = %q", empty.StartedAt)
	}
	emptyExport := httptest.NewRecorder()
	server.Handler().ServeHTTP(emptyExport, httptest.NewRequest(http.MethodGet, "/api/games/"+empty.Game.ID+"/sgf", nil))
	assertStatus(t, emptyExport, http.StatusOK)
	if !strings.Contains(emptyExport.Body.String(), "GN["+empty.Name+"]") || strings.Contains(emptyExport.Body.String(), "DT[") {
		t.Fatalf("開始日時が不明な対局の書き出し = %s", emptyExport.Body.String())
	}
	emptyReimport := httptest.NewRecorder()
	server.Handler().ServeHTTP(emptyReimport, sgfImportRequest(emptyExport.Body.String()))
	assertStatus(t, emptyReimport, http.StatusCreated)
	var emptyAgain gameResponse
	if err := json.Unmarshal(emptyReimport.Body.Bytes(), &emptyAgain); err != nil {
		t.Fatal(err)
	}
	emptyExportAgain := httptest.NewRecorder()
	server.Handler().ServeHTTP(emptyExportAgain, httptest.NewRequest(http.MethodGet, "/api/games/"+emptyAgain.Game.ID+"/sgf", nil))
	assertStatus(t, emptyExportAgain, http.StatusOK)
	if emptyExportAgain.Body.String() != emptyExport.Body.String() {
		t.Fatalf("空のGNが固定点に達していない：\n1回目：%s\n2回目：%s", emptyExport.Body.String(), emptyExportAgain.Body.String())
	}

	longImport := httptest.NewRecorder()
	longName := strings.Repeat("名", 81)
	server.Handler().ServeHTTP(longImport, sgfImportRequest("(;FF[4]CA[UTF-8]SZ[9]GN["+longName+"])"))
	if longImport.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("長すぎるGNの応答 = %d、%s", longImport.Code, longImport.Body.String())
	}
}

func TestJSONが競合するアプリ名とGNを拒否する(t *testing.T) {
	session, err := game.NewConfiguredSession("source", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	snapshot.RootProperties = append(snapshot.RootProperties, game.SGFProperty{Ident: "GN", Values: []string{"棋譜名"}})
	record := importGameJSONRequest{SchemaVersion: userGameRecordSchemaVersion, RecordedAt: "2026-11-05T00:00:00Z", Name: "別名", HumanColor: ptr("B"), AIColor: ptr("W"), AILevel: ptr(1), Game: snapshot}
	contents, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, newTestServer(t, fixedAnalyzer{move: "pass"}).Handler(), http.MethodPost, "/api/games/import-json", string(contents))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}
