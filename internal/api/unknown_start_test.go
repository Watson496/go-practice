package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func importSGFForTest(t *testing.T, handler http.Handler, source string) gameResponse {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, sgfImportRequest(source))
	assertStatus(t, response, http.StatusCreated)
	var state gameResponse
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func Test開始日時が不明の対局は読み込んだ時刻で並び再起動後も保つ(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	dated := importSGFForTest(t, handler, "(;FF[4]SZ[9]DT[2000-01-01])")
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var newGame gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &newGame); err != nil {
		t.Fatal(err)
	}
	undated := importSGFForTest(t, handler, "(;FF[4]SZ[9])")
	if undated.StartedAt != "" || undated.ImportedAt == "" || dated.StartedAt == "" || dated.ImportedAt == "" || newGame.ImportedAt != "" {
		t.Fatalf("開始日時と読み込んだ時刻：対局日なし=%q/%q、対局日あり=%q/%q、作成=%q",
			undated.StartedAt, undated.ImportedAt, dated.StartedAt, dated.ImportedAt, newGame.ImportedAt)
	}

	// 開始日時が不明の対局は、読み込んだ時刻を開始日時の代わりに使うので、後で読み込んだ分だけ新しい。
	listed := performJSON(t, handler, http.MethodGet, "/api/games", "")
	assertGameOrder(t, listed.Body.Bytes(), undated.Game.ID, newGame.Game.ID, dated.Game.ID)

	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	listed = performJSON(t, restarted.Handler(), http.MethodGet, "/api/games", "")
	assertGameOrder(t, listed.Body.Bytes(), undated.Game.ID, newGame.Game.ID, dated.Game.ID)
	restored := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+undated.Game.ID, "")
	assertStatus(t, restored, http.StatusOK)
	var state gameResponse
	if err := json.Unmarshal(restored.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.StartedAt != "" || state.ImportedAt != undated.ImportedAt || state.Name != undated.Name {
		t.Fatalf("再起動後の開始日時 = %q、読み込んだ時刻 = %q、対局名 = %q", state.StartedAt, state.ImportedAt, state.Name)
	}
}

func Test開始日時が不明の対局をJSONで往復しても内容が変わらない(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
	undated := importSGFForTest(t, handler, "(;FF[4]SZ[9])")

	firstResponse := performJSON(t, handler, http.MethodGet, "/api/games/"+undated.Game.ID+"/json", "")
	assertStatus(t, firstResponse, http.StatusOK)
	var first importGameJSONRequest
	if err := json.Unmarshal(firstResponse.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.RecordedAt != "" || first.ImportedAt != undated.ImportedAt || first.Name != undated.Name {
		t.Fatalf("書き出したJSONの開始日時 = %q、読み込んだ時刻 = %q、対局名 = %q", first.RecordedAt, first.ImportedAt, first.Name)
	}
	importedResponse := performJSON(t, handler, http.MethodPost, "/api/games/import-json", firstResponse.Body.String())
	assertStatus(t, importedResponse, http.StatusCreated)
	var imported gameResponse
	if err := json.Unmarshal(importedResponse.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	secondResponse := performJSON(t, handler, http.MethodGet, "/api/games/"+imported.Game.ID+"/json", "")
	assertStatus(t, secondResponse, http.StatusOK)
	var second importGameJSONRequest
	if err := json.Unmarshal(secondResponse.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	first.Game.ID, second.Game.ID = "", ""
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("JSON往復で内容が変化した\n最初=%+v\n再取り込み後=%+v", first, second)
	}
}

func Test開始日時が不明のJSONは読み込んだ時刻と対局名がなければ拒否する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
	undated := importSGFForTest(t, handler, "(;FF[4]SZ[9])")
	exported := performJSON(t, handler, http.MethodGet, "/api/games/"+undated.Game.ID+"/json", "")
	assertStatus(t, exported, http.StatusOK)
	var record importGameJSONRequest
	if err := json.Unmarshal(exported.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}

	withoutImportedAt := record
	withoutImportedAt.ImportedAt = ""
	body, err := json.Marshal(withoutImportedAt)
	if err != nil {
		t.Fatal(err)
	}
	rejected := performJSON(t, handler, http.MethodPost, "/api/games/import-json", string(body))
	assertRejected(t, rejected)

	withoutName := record
	withoutName.Name = ""
	withoutName.Game.RootProperties = nil
	body, err = json.Marshal(withoutName)
	if err != nil {
		t.Fatal(err)
	}
	rejected = performJSON(t, handler, http.MethodPost, "/api/games/import-json", string(body))
	assertRejected(t, rejected)
}

func assertRejected(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	assertStatus(t, response, http.StatusBadRequest)
}
