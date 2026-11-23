package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-practice/internal/apicontract"
	"go-practice/internal/game"
	"go-practice/internal/katago"
	"go-practice/internal/sgf"
)

type failingRequestBody struct{}

func (failingRequestBody) Read([]byte) (int, error) {
	return 0, errors.New("秘密の通信詳細")
}
func (failingRequestBody) Close() error { return nil }

type zeroVisitAnalysisAnalyzer struct{ fixedAnalyzer }

func (zeroVisitAnalysisAnalyzer) AnalyzePosition(_ context.Context, position game.Snapshot, _ int) (katago.Summary, error) {
	return validAnalysisSummary(position, 0), nil
}

type invalidWinrateAnalysisAnalyzer struct{ fixedAnalyzer }

func (invalidWinrateAnalysisAnalyzer) AnalyzePosition(_ context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	result := validAnalysisSummary(position, visits)
	result.Winrate = 2
	return result, nil
}

type invalidCoordinateAnalysisAnalyzer struct{ fixedAnalyzer }

func (invalidCoordinateAnalysisAnalyzer) AnalyzePosition(_ context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	result := validAnalysisSummary(position, visits)
	result.Candidates = []katago.CandidateSummary{{Move: "Z99", Winrate: 0.5, Prior: 0.5}}
	return result, nil
}

type mismatchedPositionAnalysisAnalyzer struct{ fixedAnalyzer }

func (mismatchedPositionAnalysisAnalyzer) AnalyzePosition(_ context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	result := validAnalysisSummary(position, visits)
	result.TurnNumber++
	return result, nil
}

type missingOwnershipAnalysisAnalyzer struct{ fixedAnalyzer }

func (missingOwnershipAnalysisAnalyzer) AnalyzePosition(_ context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	result := validAnalysisSummary(position, visits)
	result.Ownership = nil
	return result, nil
}

func Test経路とHTTPメソッドの誤りをJSONで返す(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	for _, test := range []struct {
		name, method, path string
		status             int
	}{
		{name: "不正なURLパス", method: http.MethodGet, path: "/%2e%2e/unknown", status: http.StatusBadRequest},
		{name: "存在しないAPI", method: http.MethodGet, path: "/api/unknown", status: http.StatusNotFound},
		{name: "存在しないメソッドとパスの組", method: http.MethodPut, path: "/api/games", status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performJSON(t, handler, test.method, test.path, "")
			if response.Code != test.status || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("状態コード = %d、Content-Type = %q、本文 = %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
			}
		})
	}
}

func Test文字を黙って置き換えるJSONを状態変更前に拒否する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "不正なUTF-8バイト列", body: []byte(`{"boardSize":9,"name":"` + string([]byte{0xff}) + `","aiLevel":1}`)},
		{name: "単独のサロゲート", body: []byte(`{"boardSize":9,"name":"\ud800","aiLevel":1}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/games", bytes.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
		})
	}
	games := performJSON(t, server.Handler(), http.MethodGet, "/api/games", "")
	if games.Code != http.StatusOK || strings.Contains(games.Body.String(), "\"id\"") {
		t.Fatalf("拒否後の保存対局一覧：状態コード = %d、本文 = %s", games.Code, games.Body.String())
	}
}

func Test開始局面を削除できない(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, handler, http.MethodDelete, "/api/games/"+state.Game.ID+"/nodes/root", expectedNodeIDsBody(t, "root"))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func Test変化削除で空と重複の確認が拒否される(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	variation := performJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/variations", `{"coordinate":"D4"}`)
	assertStatus(t, variation, http.StatusOK)
	if err := json.Unmarshal(variation.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID + "/nodes/" + state.Game.CurrentNodeID
	empty := performJSON(t, handler, http.MethodDelete, path, "")
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("空の本文の状態コード = %d、本文 = %s", empty.Code, empty.Body.String())
	}
	whitespace := performJSON(t, handler, http.MethodDelete, path, " \n\t")
	if whitespace.Code != http.StatusBadRequest {
		t.Fatalf("空白だけの本文の状態コード = %d、本文 = %s", whitespace.Code, whitespace.Body.String())
	}
	duplicate := performJSON(t, handler, http.MethodDelete, path, `{"expectedNodeIds":["`+state.Game.CurrentNodeID+`","`+state.Game.CurrentNodeID+`"]}`)
	if duplicate.Code != http.StatusBadRequest {
		t.Fatalf("重複確認の状態コード = %d、本文 = %s", duplicate.Code, duplicate.Body.String())
	}
}

func Test欠落しているメモ局面が不正なボディよりも優先される(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, handler, http.MethodPatch, "/api/games/"+state.Game.ID+"/nodes/missing", "{")
	if response.Code != http.StatusNotFound {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func Testメモ更新は新旧のメモと重要な局面をすべて必要とする(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID + "/nodes/root"
	for _, body := range []string{
		`{"important":false,"expectedNote":"","expectedImportant":false}`,
		`{"note":"","expectedNote":"","expectedImportant":false}`,
		`{"note":"","important":false,"expectedImportant":false}`,
		`{"note":"","important":false,"expectedNote":""}`,
	} {
		response := performJSON(t, handler, http.MethodPatch, path, body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("不足する項目を受け付けました：状態コード = %d、本文 = %s", response.Code, response.Body.String())
		}
	}
}

func Test欠落した変化局面が不正なボディよりも優先される(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, handler, http.MethodDelete, "/api/games/"+state.Game.ID+"/nodes/missing", "{")
	if response.Code != http.StatusNotFound {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func TestJSON本文とメモに文字数制限を適用する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	valid := `{"boardSize":9,"aiLevel":1}`
	maximum := valid + strings.Repeat(" ", (10<<20)-len(valid))
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/games", maximum), http.StatusCreated)
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/games", maximum+" "), http.StatusRequestEntityTooLarge)

	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	note, err := json.Marshal(strings.Repeat("あ", 4000+1))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"note":` + string(note) + `,"important":false,"expectedNote":"","expectedImportant":false}`
	response := performJSON(t, handler, http.MethodPatch, "/api/games/"+state.Game.ID+"/nodes/root", body)
	assertStatus(t, response, http.StatusRequestEntityTooLarge)
}

func TestKataGoの不正な解析値を保存前に拒否する(t *testing.T) {
	for _, test := range []struct {
		name   string
		engine AIEngine
	}{
		{name: "勝率", engine: invalidWinrateAnalysisAnalyzer{fixedAnalyzer{move: "pass"}}},
		{name: "候補手の座標", engine: invalidCoordinateAnalysisAnalyzer{fixedAnalyzer{move: "pass"}}},
		{name: "対象局面との不一致", engine: mismatchedPositionAnalysisAnalyzer{fixedAnalyzer{move: "pass"}}},
		{name: "領域予測の欠落", engine: missingOwnershipAnalysisAnalyzer{fixedAnalyzer{move: "pass"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newTestServer(t, test.engine)
			created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
			var state gameResponse
			if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			response := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID+"/analysis", "")
			if response.Code != http.StatusBadGateway {
				t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
			data, err := os.ReadFile(filepath.Join(server.dataDir, state.Game.ID+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte(`"analysisCache"`)) {
				t.Fatal("不正な解析結果が保存されています")
			}
		})
	}
}

func Test書き出すJSONが上限を超えるSGFを保存しない(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	source := "(;FF[4]SZ[9]XX[" + strings.Repeat(`"`, 6<<20) + "])"
	if len(source) >= 10<<20 {
		t.Fatalf("検査用SGFが入力上限を超えています：%dバイト", len(source))
	}
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader(source))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	games := performJSON(t, server.Handler(), http.MethodGet, "/api/games", "")
	if games.Code != http.StatusOK || strings.TrimSpace(games.Body.String()) != "[]" {
		t.Fatalf("保存失敗後の一覧：状態コード = %d、本文 = %s", games.Code, games.Body.String())
	}
	entries, err := os.ReadDir(server.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("保存失敗後に%d個のファイルが残っています", len(entries))
	}
}

func Test書き出すSGFが上限を超えるJSONを保存しない(t *testing.T) {
	const recordedAt = "2026-01-02T03:04:05Z"
	session, err := game.NewConfiguredSession("000000000000000000000000", 9, game.DefaultKomi, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.SetSGFProperties("root", []game.SGFProperty{
		{Ident: "DT", Values: []string{"2026-01-02"}},
		{Ident: "XX", Values: []string{strings.Repeat("]", 6<<20)}},
	}); err != nil {
		t.Fatal(err)
	}
	record := exportGameRecordValues(session.Snapshot(), ptr(apicontract.DefaultAILevel), ptr(apicontract.DefaultHumanColor), recordedAt, "", "")
	contents, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) >= 10<<20 {
		t.Fatalf("検査用JSONが入力上限を超えています：%dバイト", len(contents))
	}
	encoded, err := sgf.Encode(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) <= 10<<20 {
		t.Fatalf("書き出すSGFが上限を超える検査データになっていません：%dバイト", len(encoded))
	}

	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	response := performJSON(t, server.Handler(), http.MethodPost, "/api/games/import-json", string(contents))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	games := performJSON(t, server.Handler(), http.MethodGet, "/api/games", "")
	if games.Code != http.StatusOK || strings.TrimSpace(games.Body.String()) != "[]" {
		t.Fatalf("保存失敗後の一覧：状態コード = %d、本文 = %s", games.Code, games.Body.String())
	}
	entries, err := os.ReadDir(server.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("保存失敗後に%d個のファイルが残っています", len(entries))
	}

	startupDir := t.TempDir()
	savedContents, err := json.MarshalIndent(savedGame{
		SchemaVersion: savedGameSchemaVersion,
		StartedAt:     recordedAt,
		Game:          session.Snapshot(),
		AILevel:       ptr(apicontract.DefaultAILevel),
		HumanColor:    ptr(apicontract.DefaultHumanColor),
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(savedContents)+1 > 10<<20 {
		t.Fatalf("検査用保存ファイルがJSONの上限を超えています：%dバイト", len(savedContents)+1)
	}
	if err := os.WriteFile(filepath.Join(startupDir, session.Snapshot().ID+".json"), append(savedContents, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPersistentServer(startupDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	loaded := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games", "")
	if loaded.Code != http.StatusOK || strings.TrimSpace(loaded.Body.String()) != "[]" {
		t.Fatalf("起動時にSGFの上限を超えた対局を読み込みました：状態コード = %d、本文 = %s", loaded.Code, loaded.Body.String())
	}
	failures := performJSON(t, restarted.Handler(), http.MethodGet, "/api/storage-errors", "")
	var storageFailures []storageLoadError
	if failures.Code != http.StatusOK || json.Unmarshal(failures.Body.Bytes(), &storageFailures) != nil || len(storageFailures) != 1 {
		t.Fatalf("起動時の保存エラー：状態コード = %d、本文 = %s", failures.Code, failures.Body.String())
	}
}

func Test操作後のJSONが上限を超えるとメモリと保存内容を戻す(t *testing.T) {
	const recordedAt = "2026-01-02T03:04:05Z"
	const gameID = "000000000000000000000000"
	small, err := sgf.DecodeGameWithDeadStoneEstimator(`(;FF[4]SZ[9]DT[2026-01-02]XX["])`, gameID, nil)
	if err != nil {
		t.Fatal(err)
	}
	baseRecord := exportGameRecordValues(small.Snapshot(), ptr(apicontract.DefaultAILevel), ptr(apicontract.DefaultHumanColor), recordedAt, "", "")
	baseJSON, err := json.Marshal(baseRecord)
	if err != nil {
		t.Fatal(err)
	}
	baseSize := len(baseJSON) - 2
	quoteCount := ((10 << 20) - 1 - 1024 - baseSize) / 2
	source := `(;FF[4]SZ[9]DT[2026-01-02]XX[` + strings.Repeat(`"`, quoteCount) + `])`

	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader(source))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	imported := httptest.NewRecorder()
	server.Handler().ServeHTTP(imported, request)
	assertStatus(t, imported, http.StatusCreated)
	var identity struct {
		Game struct {
			ID string `json:"id"`
		} `json:"game"`
	}
	if err := json.Unmarshal(imported.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(server.dataDir, identity.Game.ID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	note, err := json.Marshal(strings.Repeat("x", 2000))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"note":` + string(note) + `,"important":false,"expectedNote":"","expectedImportant":false}`
	rejected := performJSON(t, server.Handler(), http.MethodPatch, "/api/games/"+identity.Game.ID+"/nodes/root", body)
	if rejected.Code != http.StatusUnprocessableEntity {
		t.Fatalf("状態コード = %d、本文 = %s", rejected.Code, rejected.Body.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("拒否したメモ更新で保存内容が変わりました")
	}
	state := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+identity.Game.ID, "")
	if state.Code != http.StatusOK || strings.Contains(state.Body.String(), strings.Repeat("x", 2000)) {
		t.Fatalf("拒否したメモ更新がメモリに残っています：状態コード = %d", state.Code)
	}
}

func Test局面数上限を超える対局操作は保存せず拒否する(t *testing.T) {
	session, err := game.NewSessionWithSetup("node-limit", 5, 7.5, 0, nil, "B")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	for number := 1; number < 4096; number++ {
		snapshot.TreeNodes = append(snapshot.TreeNodes, game.TreeNode{
			ID:       fmt.Sprintf("n%d", number),
			ParentID: "root",
			Properties: []game.SGFProperty{{
				Ident: "C", Values: []string{"局面数上限の検査"},
			}},
		})
	}
	snapshot.CurrentNodeID = "n1"
	snapshot.MainLineNodeID = "n1"
	snapshot.NextNodeNumber = 4096
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	for _, testCase := range []struct {
		name       string
		humanColor string
		path       string
	}{
		{name: "着手", humanColor: "B", path: "/moves"},
		{name: "AI着手", humanColor: "W", path: "/ai-moves"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			record := exportGameRecordValues(snapshot, ptr(apicontract.DefaultAILevel), ptr(testCase.humanColor), "2026-11-11T00:00:00Z", "", "")
			contents, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			imported := performJSON(t, server.Handler(), http.MethodPost, "/api/games/import-json", string(contents))
			assertStatus(t, imported, http.StatusCreated)
			var state gameResponse
			if err := json.Unmarshal(imported.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			body := `{}`
			if testCase.path == "/moves" {
				body = `{"coordinate":"A1"}`
			}
			response := performJSON(t, server.Handler(), http.MethodPost, "/api/games/"+state.Game.ID+testCase.path, body)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
			after := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID, ``)
			assertStatus(t, after, http.StatusOK)
			if after.Body.String() != imported.Body.String() {
				t.Fatalf("拒否後に対局が変わった\n読み込み直後：%s\n拒否後：%s", imported.Body.String(), after.Body.String())
			}
		})
	}
}

func Test読み込む棋譜の局面数と変化の深さの超過は413になる(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	tooManyNodes := "(;FF[4]SZ[9]" + strings.Repeat(";C[x]", 4096) + ")"
	tooDeep := strings.Repeat("(;", 4097) + strings.Repeat(")", 4097)
	for _, source := range []string{tooManyNodes, tooDeep} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, sgfImportRequest(source))
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("上限を超えたSGFへの応答 = %d、本文 = %s", response.Code, response.Body.String())
		}
	}
}

func Test対局名の文字数超過は413になる(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	name, err := json.Marshal(strings.Repeat("名", 80+1))
	if err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, handler, http.MethodPatch, "/api/games/"+state.Game.ID, `{"name":`+string(name)+`,"expectedName":""}`)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("長すぎる対局名への応答 = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func TestJSONの媒体型を厳密に解析する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	for _, test := range []struct {
		name, contentType string
		status            int
	}{
		{name: "JSON", contentType: "application/json", status: http.StatusCreated},
		{name: "文字コード付きJSON", contentType: "application/json; charset=utf-8", status: http.StatusCreated},
		{name: "異なる文字コード付きJSON", contentType: "application/json; charset=Shift_JIS", status: http.StatusUnsupportedMediaType},
		{name: "前方一致する別形式", contentType: "application/jsonx", status: http.StatusUnsupportedMediaType},
		{name: "壊れた引数", contentType: "application/json; charset", status: http.StatusUnsupportedMediaType},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/games", strings.NewReader(`{"boardSize":9,"aiLevel":1}`))
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestSGFの媒体型を厳密に解析する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	for _, test := range []struct {
		name, contentType string
		status            int
	}{
		{name: "SGF", contentType: "application/x-go-sgf", status: http.StatusCreated},
		{name: "文字コード付きSGF", contentType: "application/x-go-sgf; charset=utf-8", status: http.StatusCreated},
		{name: "異なる文字コード付きSGF", contentType: "application/x-go-sgf; charset=Shift_JIS", status: http.StatusUnsupportedMediaType},
		{name: "媒体型なし", status: http.StatusUnsupportedMediaType},
		{name: "別形式", contentType: "text/plain", status: http.StatusUnsupportedMediaType},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader("(;FF[4]SZ[9])"))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
		})
	}
}

func Test名前はSGFの空白だけを正規化する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, handler, http.MethodPatch, "/api/games/"+state.Game.ID, `{"name":"\u3000対局名\u3000","expectedName":""}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"　対局名　"`) {
		t.Fatalf("対局名の状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	response = performJSON(t, handler, http.MethodPatch, "/api/games/"+state.Game.ID, `{"name":"次の名前","expectedName":"\u3000対局名\u3000"}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"次の名前"`) {
		t.Fatalf("正規化した変更前対局名の状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	response = performJSON(t, handler, http.MethodPatch, "/api/games/"+state.Game.ID, `{"name":"\u0085対局名\u0085","expectedName":"次の名前"}`)
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Name != "\u0085対局名\u0085" {
		t.Fatalf("NELを含む対局名 = %q", state.Name)
	}
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`), http.StatusNoContent)
	response = performJSON(t, handler, http.MethodPost, "/api/agent/variations", `{"contextId":"`+state.Game.ID+`:root","moves":["D4"],"title":"\u0085変化名\u0085"}`)
	var variation struct {
		Game gameResponse `json:"game"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &variation); err != nil {
		t.Fatal(err)
	}
	state = variation.Game
	if response.Code != http.StatusOK || state.Game.TreeNodes[len(state.Game.TreeNodes)-1].VariationTitle != "\u0085変化名\u0085" {
		t.Fatalf("変化名の状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func TestメモのSGF空白を正規化する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, handler, http.MethodPatch, "/api/games/"+state.Game.ID+"/nodes/root", `{"note":"一\t二\u3000三\n四","important":false,"expectedNote":"","expectedImportant":false}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"values":["一 二　三\n四"]`) {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func TestAIagent解析が探索数の境界を検査する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`), http.StatusNoContent)
	for _, value := range []string{"0", "null"} {
		response := performJSON(t, handler, http.MethodPost, "/api/agent/analyze", `{"contextId":"`+state.Game.ID+`:root","maxVisits":`+value+`}`)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("探索数%sの状態コード = %d、本文 = %s", value, response.Code, response.Body.String())
		}
	}
	for _, value := range []int{apicontract.MinimumAnalysisVisits, apicontract.MaximumAnalysisVisits} {
		response := performJSON(t, handler, http.MethodPost, "/api/agent/analyze", fmt.Sprintf(`{"contextId":"%s:root","maxVisits":%d}`, state.Game.ID, value))
		assertStatus(t, response, http.StatusOK)
	}
}

func TestAIagent解析が手順の上限を境界値で検査する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	contextID := state.Game.ID + ":root"
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`), http.StatusNoContent)

	maximum := []string{"E5", "D5", "F5", "C5", "G5", "B5", "H5", "A5", "J5", "E4", "D4", "F4"}
	if len(maximum) != apicontract.MaximumAnalysisSequenceSize {
		t.Fatal("解析手順の境界値が要件と一致しません")
	}
	encodedMaximum, err := json.Marshal(map[string]any{"contextId": contextID, "sequence": maximum, "maxVisits": 1})
	if err != nil {
		t.Fatal(err)
	}
	accepted := performJSON(t, handler, http.MethodPost, "/api/agent/analyze", string(encodedMaximum))
	assertStatus(t, accepted, http.StatusOK)

	overLimit := append(append([]string(nil), maximum...), "pass")
	encodedOverLimit, err := json.Marshal(map[string]any{"contextId": contextID, "sequence": overLimit, "maxVisits": 1})
	if err != nil {
		t.Fatal(err)
	}
	rejected := performJSON(t, handler, http.MethodPost, "/api/agent/analyze", string(encodedOverLimit))
	assertStatus(t, rejected, http.StatusBadRequest)
}

func TestAIagent解析が占有点への着手を競合として返す(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	played := performJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/moves", `{"coordinate":"D4"}`)
	assertStatus(t, played, http.StatusOK)
	var moveResult playMoveResponse
	if err := json.Unmarshal(played.Body.Bytes(), &moveResult); err != nil {
		t.Fatal(err)
	}
	state = moveResult.Game
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"`+state.Game.CurrentNodeID+`"}`), http.StatusNoContent)
	response := performJSON(t, handler, http.MethodPost, "/api/agent/analyze", `{"contextId":"`+state.Game.ID+`:`+state.Game.CurrentNodeID+`","sequence":["D4"]}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func TestKataGoの探索数ゼロを解析キャッシュの保存前に拒否する(t *testing.T) {
	handler := newTestServer(t, zeroVisitAnalysisAnalyzer{fixedAnalyzer{move: "pass"}}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, method, path, body string
	}{
		{name: "HTTP解析", method: http.MethodGet, path: "/api/games/" + state.Game.ID + "/analysis?visits=1"},
		{name: "AI agent解析", method: http.MethodPost, path: "/api/agent/analyze", body: `{"contextId":"` + state.Game.ID + `:root","maxVisits":1}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "AI agent解析" {
				assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`), http.StatusNoContent)
			}
			response := performJSON(t, handler, test.method, test.path, test.body)
			if response.Code != http.StatusBadGateway {
				t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
		})
	}
}

func Test局面解析が探索数の境界を検査する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"0", "abc", "1.5"} {
		response := performJSON(t, handler, http.MethodGet, "/api/games/"+state.Game.ID+"/analysis?visits="+value, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("探索数%sの状態コード = %d、本文 = %s", value, response.Code, response.Body.String())
		}
	}
	for _, visits := range []int{apicontract.MinimumAnalysisVisits, apicontract.MaximumAnalysisVisits} {
		accepted := performJSON(t, handler, http.MethodGet, fmt.Sprintf("/api/games/%s/analysis?visits=%d", state.Game.ID, visits), "")
		assertStatus(t, accepted, http.StatusOK)
	}
}

func Test対局作成では盤サイズを必須にする(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	response := performJSON(t, handler, http.MethodPost, "/api/games", `{}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func Test局面解析では空の局面識別情報を拒否する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"nodeId=", "nodeId=%20", "nodeId=root&nodeId=n1"} {
		response := performJSON(t, handler, http.MethodGet, "/api/games/"+state.Game.ID+"/analysis?"+query, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("query = %q、状態コード = %d、本文 = %s", query, response.Code, response.Body.String())
		}
	}
}

func TestAIagentの変化が入力局面の状態を使う(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	selected := performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`)
	if selected.Code != http.StatusNoContent {
		t.Fatalf("共有状態コード = %d、本文 = %s", selected.Code, selected.Body.String())
	}
	for _, test := range []struct {
		moves  string
		status int
	}{
		{moves: `"invalid"`, status: http.StatusBadRequest},
		{moves: `"E5","E5"`, status: http.StatusConflict},
	} {
		response := performJSON(t, handler, http.MethodPost, "/api/agent/variations", `{"contextId":"`+state.Game.ID+`:root","moves":[`+test.moves+`]}`)
		if response.Code != test.status {
			t.Fatalf("着手列 = %s、状態コード = %d、本文 = %s", test.moves, response.Code, response.Body.String())
		}
	}
}

func Test保存ファイルの内部識別子をユーザーへ表示しない(t *testing.T) {
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
	path := filepath.Join(dataDir, state.Game.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved savedGame
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	saved.Game.TreeNodes = append(saved.Game.TreeNodes, game.TreeNode{ID: "ユーザーの秘密ID", ParentID: "存在しない親"})
	data, err = json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, restarted.Handler(), http.MethodGet, "/api/storage-errors", "")
	if response.Code != http.StatusOK {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func TestSGF読み込み誤りにより入力と通信の詳細が非表示になる(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", nil)
	request.Body = failingRequestBody{}
	request.Header.Set("Content-Type", "application/x-go-sgf")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("本文読み込み状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader("(;FF[4]SZ[9]DT[秘密の日時])"))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("対局日状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

var _ io.ReadCloser = failingRequestBody{}

type zeroVisitAnalyzer struct{ fixedAnalyzer }

func (zeroVisitAnalyzer) AnalyzePosition(_ context.Context, position game.Snapshot, _ int) (katago.Summary, error) {
	summary := validAnalysisSummary(position, 0)
	summary.Candidates = []katago.CandidateSummary{{Move: "D4", Prior: 1, Winrate: 0.5}}
	return summary, nil
}

func Test存在しないメソッドとパスの組でも許可ヘッダーを返す(t *testing.T) {
	response := performJSON(t, newTestServer(t, fixedAnalyzer{move: "pass"}).Handler(), http.MethodPut, "/api/games", "")
	if response.Code != http.StatusNotFound || response.Header().Get("Allow") == "" {
		t.Fatalf("状態コード = %d、Allow = %q", response.Code, response.Header().Get("Allow"))
	}
}

func TestAIagentの変化は状態変更の前に長いタイトルを拒否する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`), http.StatusNoContent)
	title, err := json.Marshal(strings.Repeat("長", 80+1))
	if err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, handler, http.MethodPost, "/api/agent/variations", `{"contextId":"`+state.Game.ID+`:root","moves":["D4"],"title":`+string(title)+`}`)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	loaded := performJSON(t, handler, http.MethodGet, "/api/games/"+state.Game.ID, "")
	assertStatus(t, loaded, http.StatusOK)
	if strings.Contains(loaded.Body.String(), "D4") {
		t.Fatalf("拒否した変化が残った：%s", loaded.Body.String())
	}
}

func TestJSON読み込みが長いメモを拒否する(t *testing.T) {
	session, err := game.NewConfiguredSession("source", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	snapshot.RootProperties = append(snapshot.RootProperties, game.SGFProperty{Ident: "C", Values: []string{strings.Repeat("長", 4000+1)}})
	record := importGameJSONRequest{SchemaVersion: "1", RecordedAt: "2026-11-05T00:00:00Z", HumanColor: ptr("B"), AIColor: ptr("W"), AILevel: ptr(1), Game: snapshot}
	contents, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, newTestServer(t, fixedAnalyzer{move: "pass"}).Handler(), http.MethodPost, "/api/games/import-json", string(contents))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func Test書き出しが上限内なら内部保存ファイルの大きさで拒否しない(t *testing.T) {
	session, err := game.NewConfiguredSession("source", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	const nodeCount = 450
	for range nodeCount - 1 {
		if _, err := session.AppendSGFNode(nil); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := session.Snapshot()
	recordedAt := "2026-11-05T00:00:00Z"
	var contents []byte
	low, high := 0, 4000
	for low <= high {
		length := low + (high-low)/2
		note := strings.Repeat("<", length)
		for index := range snapshot.TreeNodes {
			if snapshot.TreeNodes[index].ID != "root" {
				snapshot.TreeNodes[index].Properties = []game.SGFProperty{{Ident: "C", Values: []string{note}}}
			}
		}
		record := importGameJSONRequest{SchemaVersion: userGameRecordSchemaVersion, RecordedAt: recordedAt, HumanColor: ptr("B"), AIColor: ptr("W"), AILevel: ptr(1), Game: snapshot}
		candidate, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if len(candidate) <= 10<<20 {
			contents = candidate
			low = length + 1
		} else {
			high = length - 1
		}
	}
	if len(contents) == 0 {
		t.Fatal("要求上限内のJSONを作成できません")
	}
	var record importGameJSONRequest
	if err := json.Unmarshal(contents, &record); err != nil {
		t.Fatal(err)
	}
	storedBytes, err := json.MarshalIndent(savedGame{
		SchemaVersion: savedGameSchemaVersion,
		StartedAt:     recordedAt,
		Game:          record.Game,
		AILevel:       record.AILevel,
		HumanColor:    record.HumanColor,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(storedBytes)+1 <= 10<<20 {
		t.Fatalf("保存ファイルが上限を超える検査データになっていません：要求=%d、保存=%d", len(contents), len(storedBytes)+1)
	}

	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, server.Handler(), http.MethodPost, "/api/games/import-json", string(contents))
	if response.Code != http.StatusCreated {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("対局の保存ファイルだけが必要です：%v", entries)
	}
}

func TestJSON読み込みが文章中のSGF空白を正規化する(t *testing.T) {
	session, err := game.NewConfiguredSession("source", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	snapshot.RootProperties = append(snapshot.RootProperties,
		game.SGFProperty{Ident: "GN", Values: []string{"一 局"}},
		game.SGFProperty{Ident: "C", Values: []string{"一\t二\u3000三"}},
	)
	record := importGameJSONRequest{SchemaVersion: "1", RecordedAt: "2026-11-05T00:00:00Z", Name: "一\n局", HumanColor: ptr("B"), AIColor: ptr("W"), AILevel: ptr(1), Game: snapshot}
	contents, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, newTestServer(t, fixedAnalyzer{move: "pass"}).Handler(), http.MethodPost, "/api/games/import-json", string(contents))
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"name":"一 局"`) || !strings.Contains(response.Body.String(), `"values":["一 二　三"]`) {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func Test無効なAIの決定は状態変更の前に拒否される(t *testing.T) {
	handler := newTestServer(t, zeroVisitAnalyzer{fixedAnalyzer{move: "D4"}}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"W"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/ai-moves", `{}`)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	loaded := performJSON(t, handler, http.MethodGet, "/api/games/"+state.Game.ID, "")
	assertStatus(t, loaded, http.StatusOK)
	var after gameResponse
	if err := json.Unmarshal(loaded.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if len(after.Game.Moves) != 0 || len(after.Game.TreeNodes) != 1 {
		t.Fatalf("不正なAI判断で状態が変化した：%+v", after.Game)
	}
}
