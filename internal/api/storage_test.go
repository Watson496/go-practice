package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-practice/internal/game"
	"go-practice/internal/katago"
)

func Test原子的書き込みで完全なファイルを置き換える(t *testing.T) {
	path := filepath.Join(t.TempDir(), "game.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, []byte("new record\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new record\n" {
		t.Fatalf("保存内容 = %q", data)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".go-practice-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("一時ファイルが残っている：%v", matches)
	}
}

func Test保存データの読み込みエラーがない場合は空配列を返す(t *testing.T) {
	server, err := NewPersistentServer(t.TempDir(), fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, server.Handler(), http.MethodGet, "/api/storage-errors", ``)
	assertStatus(t, response, http.StatusOK)
	if response.Body.String() != "[]\n" {
		t.Fatalf("保存読み込みエラー = %s", response.Body.String())
	}
}

func Test再起動後も削除した局面の識別情報を再利用しない(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
	var initial gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + initial.Game.ID
	added := performJSON(t, server.Handler(), http.MethodPost, path+"/variations", `{"coordinate":"E5"}`)
	assertStatus(t, added, http.StatusOK)
	var withVariation gameResponse
	if err := json.Unmarshal(added.Body.Bytes(), &withVariation); err != nil {
		t.Fatal(err)
	}
	deletedID := withVariation.Game.CurrentNodeID
	deleted := performJSON(t, server.Handler(), http.MethodDelete, path+"/nodes/"+deletedID, `{"expectedNodeIds":["`+deletedID+`"]}`)
	assertStatus(t, deleted, http.StatusOK)

	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	addedAfterRestart := performJSON(t, restarted.Handler(), http.MethodPost, path+"/variations", `{"coordinate":"D4"}`)
	assertStatus(t, addedAfterRestart, http.StatusOK)
	var afterRestart gameResponse
	if err := json.Unmarshal(addedAfterRestart.Body.Bytes(), &afterRestart); err != nil {
		t.Fatal(err)
	}
	if afterRestart.Game.CurrentNodeID == deletedID {
		t.Fatalf("削除した局面の識別情報 %q が別の局面に再利用されました", deletedID)
	}
	staleUpdate := performJSON(t, restarted.Handler(), http.MethodPatch, path+"/nodes/"+deletedID, `{"note":"古い局面のメモ","important":false,"expectedNote":"","expectedImportant":false}`)
	assertStatus(t, staleUpdate, http.StatusNotFound)
}

type reportedVisitsAnalyzer struct {
	fixedAnalyzer
	visits int
	calls  *int
}

func (analyzer reportedVisitsAnalyzer) AnalyzePosition(_ context.Context, position game.Snapshot, _ int) (katago.Summary, error) {
	*analyzer.calls++
	return katago.Summary{
		TurnNumber:    len(position.Moves),
		CurrentPlayer: position.CurrentPlayer,
		Visits:        analyzer.visits,
		Ownership:     make([]float64, position.BoardSize*position.BoardSize),
	}, analyzer.err
}

func Test複数局面の解析キャッシュを局面ごとに照合して復元する(t *testing.T) {
	dataDir := t.TempDir()
	calls := 0
	analyzer := fixedAnalyzer{move: "pass", analysisCalls: &calls}
	server, err := NewPersistentServer(dataDir, analyzer)
	if err != nil {
		t.Fatal(err)
	}
	state := importSGFGame(t, server.Handler(), "(;FF[4]SZ[9];B[ee];W[dd])")
	path := "/api/games/" + state.Game.ID + "/analysis?visits=100&nodeId="
	for _, nodeID := range []string{"root", "n1"} {
		assertStatus(t, performJSON(t, server.Handler(), http.MethodGet, path+nodeID, ``), http.StatusOK)
	}
	if calls != 2 {
		t.Fatalf("保存前の解析器呼出回数 = %d、期待値 = 2", calls)
	}
	saved, err := os.ReadFile(filepath.Join(dataDir, state.Game.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(saved, []byte(`"candidates": [`)) {
		t.Fatalf("保存した候補手の一覧が配列ではありません：%s", saved)
	}

	restarted, err := NewPersistentServer(dataDir, analyzer)
	if err != nil {
		t.Fatal(err)
	}
	for _, nodeID := range []string{"root", "n1"} {
		assertStatus(t, performJSON(t, restarted.Handler(), http.MethodGet, path+nodeID, ``), http.StatusOK)
	}
	if calls != 2 {
		t.Fatalf("復元後の解析器呼出回数 = %d、期待値 = 2", calls)
	}
}

func TestKataGoの探索数が要求値と異なっても解析キャッシュへ保存する(t *testing.T) {
	dataDir := t.TempDir()
	calls := 0
	analyzer := reportedVisitsAnalyzer{
		fixedAnalyzer: fixedAnalyzer{move: "pass", analysisVersion: "model"},
		visits:        80,
		calls:         &calls,
	}
	server, err := NewPersistentServer(dataDir, analyzer)
	if err != nil {
		t.Fatal(err)
	}
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	analysis := performJSON(t, server.Handler(), http.MethodGet, "/api/games/"+state.Game.ID+"/analysis?visits=100", ``)
	if analysis.Code != http.StatusOK || !strings.Contains(analysis.Body.String(), `"visits":80`) {
		t.Fatalf("解析結果 = 状態コード %d、本文 %s", analysis.Code, analysis.Body.String())
	}
	restarted, err := NewPersistentServer(dataDir, analyzer)
	if err != nil {
		t.Fatal(err)
	}
	restored := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+state.Game.ID+"/analysis?visits=100", ``)
	if restored.Code != http.StatusOK || !strings.Contains(restored.Body.String(), `"visits":80`) || calls != 1 {
		t.Fatalf("復元したキャッシュ = 状態コード %d、本文 %s、エンジン呼出回数 %d", restored.Code, restored.Body.String(), calls)
	}
}

func Test保存データに現在形式の全項目を必須とする(t *testing.T) {
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
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	gameData := record["game"].(map[string]any)
	delete(gameData, "moves")
	data, err = json.Marshal(record)
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
	loaded := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+state.Game.ID, ``)
	assertStatus(t, loaded, http.StatusNotFound)
	errors := performJSON(t, restarted.Handler(), http.MethodGet, "/api/storage-errors", ``)
	assertStatus(t, errors, http.StatusOK)
	var failures []storageLoadError
	if err := json.Unmarshal(errors.Body.Bytes(), &failures); err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].File != filepath.Base(path) {
		t.Fatalf("隔離した保存ファイル = %+v", failures)
	}
}

func Test保存領域ロード誤りにより絶対パスが非表示になる(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "読み込み失敗.json")
	if err := os.Symlink(filepath.Join(dataDir, "存在しない正本"), path); err != nil {
		t.Fatal(err)
	}
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	errors := performJSON(t, server.Handler(), http.MethodGet, "/api/storage-errors", ``)
	assertStatus(t, errors, http.StatusOK)
	var failures []storageLoadError
	if err := json.Unmarshal(errors.Body.Bytes(), &failures); err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 {
		t.Fatalf("公開した保存読み込みエラー = %s", errors.Body.String())
	}
}

func TestAI思考中の永続的な削除が再起動後に戻らない(t *testing.T) {
	dataDir := t.TempDir()
	analyzer := &countedBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{})}
	server, err := NewPersistentServer(dataDir, analyzer)
	if err != nil {
		t.Fatal(err)
	}
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	if response := performJSON(t, server.Handler(), http.MethodPost, path+"/moves", `{"coordinate":"E5"}`); response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		finished <- performJSON(t, server.Handler(), http.MethodPost, path+"/ai-moves", `{}`)
	}()
	waitForTestSignal(t, analyzer.started, "AI探索の開始")
	if response := performJSON(t, server.Handler(), http.MethodDelete, path, ``); response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	close(analyzer.release)
	if response := waitForTestSignal(t, finished, "削除中のAI要求の完了"); response.Code != http.StatusConflict {
		t.Fatalf("削除後のAI着手状態コード = %d", response.Code)
	}
	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	if response := performJSON(t, restarted.Handler(), http.MethodGet, path, ``); response.Code != http.StatusNotFound {
		t.Fatalf("再起動後の取得状態コード = %d", response.Code)
	}
}

func Test永続サーバーは終了を元に戻した後に継続した対局を復元する(t *testing.T) {
	tests := map[string]func(*testing.T, http.Handler, string){
		"二連続パス": func(t *testing.T, handler http.Handler, path string) {
			assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"pass"}`), http.StatusOK)
			assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`), http.StatusOK)
		},
		"投了": func(t *testing.T, handler http.Handler, path string) {
			assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/resign", `{}`), http.StatusOK)
		},
	}
	for name, finish := range tests {
		t.Run(name, func(t *testing.T) {
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
			path := "/api/games/" + state.Game.ID
			finish(t, server.Handler(), path)
			assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/undo", `{}`), http.StatusOK)
			assertStatus(t, performJSON(t, server.Handler(), http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
			restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
			if err != nil {
				t.Fatal(err)
			}
			loaded := performJSON(t, restarted.Handler(), http.MethodGet, path, ``)
			assertStatus(t, loaded, http.StatusOK)
			if err := json.Unmarshal(loaded.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if state.Game.Status != "playing" || state.Game.Moves[len(state.Game.Moves)-1].Coordinate != "E5" {
				t.Fatalf("再起動後の対局 = %+v", state.Game)
			}
		})
	}
}

func Test不正な解析キャッシュを保存データの読み込みエラーとして報告する(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
	var response gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, response.Game.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved savedGame
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	saved.AnalysisCache = map[string]cachedAnalysis{"missing:100": {Version: "model", Summary: katago.Summary{Visits: 100}}}
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
	loaded := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+response.Game.ID, ``)
	assertStatus(t, loaded, http.StatusNotFound)
	errors := performJSON(t, restarted.Handler(), http.MethodGet, "/api/storage-errors", ``)
	assertStatus(t, errors, http.StatusOK)
	var failures []storageLoadError
	if err := json.Unmarshal(errors.Body.Bytes(), &failures); err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 {
		t.Fatalf("隔離した保存ファイル = %+v", failures)
	}
}

func Test保存先の大文字拡張子を読み込みエラーとして報告する(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
	var response gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	lowerPath := filepath.Join(dataDir, response.Game.ID+".json")
	upperPath := filepath.Join(dataDir, response.Game.ID+".JSON")
	if err := os.Rename(lowerPath, upperPath); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	loaded := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+response.Game.ID, ``)
	assertStatus(t, loaded, http.StatusNotFound)
	errorsResponse := performJSON(t, restarted.Handler(), http.MethodGet, "/api/storage-errors", ``)
	assertStatus(t, errorsResponse, http.StatusOK)
	var failures []storageLoadError
	if err := json.Unmarshal(errorsResponse.Body.Bytes(), &failures); err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].File != filepath.Base(upperPath) {
		t.Fatalf("隔離した保存ファイル = %+v", failures)
	}
}

func Test容量の大きい正しい保存ファイルを読み込む(t *testing.T) {
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
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents = append(contents, bytes.Repeat([]byte(" "), (10<<20)+1)...)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	loaded := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+state.Game.ID, ``)
	assertStatus(t, loaded, http.StatusOK)
}

func Test保存先に残った一時ファイルを起動時に削除する(t *testing.T) {
	dataDir := t.TempDir()
	temporaryFiles := []string{
		".go-practice-123.tmp",
		".go-practice-123.tmp.previous",
		".go-practice-delete-456.tmp",
	}
	for _, name := range temporaryFiles {
		if err := os.WriteFile(filepath.Join(dataDir, name), []byte("削除済みの保存内容"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range temporaryFiles {
		if _, err := os.Stat(filepath.Join(dataDir, name)); !os.IsNotExist(err) {
			t.Fatalf("一時ファイル %q が残っています：%v", name, err)
		}
	}
}

func Test保存ファイル名を持つディレクトリを読み込みエラーとして報告する(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dataDir, "broken.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	errorsResponse := performJSON(t, server.Handler(), http.MethodGet, "/api/storage-errors", ``)
	assertStatus(t, errorsResponse, http.StatusOK)
	var failures []storageLoadError
	if err := json.Unmarshal(errorsResponse.Body.Bytes(), &failures); err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].File != "broken.json" {
		t.Fatalf("隔離した保存ファイル = %+v", failures)
	}
}

func Test保存ファイル名を持つシンボリックリンクを開かずに報告する(t *testing.T) {
	dataDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(target, []byte(`{"secret":"保存先外"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dataDir, "linked.json")); err != nil {
		t.Fatal(err)
	}
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	errorsResponse := performJSON(t, server.Handler(), http.MethodGet, "/api/storage-errors", ``)
	assertStatus(t, errorsResponse, http.StatusOK)
	var failures []storageLoadError
	if err := json.Unmarshal(errorsResponse.Body.Bytes(), &failures); err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 {
		t.Fatalf("読み込みエラー = %s", errorsResponse.Body.String())
	}
}
