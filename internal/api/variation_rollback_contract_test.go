package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func createResignedGame(t *testing.T, handler http.Handler) gameResponse {
	t.Helper()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/resign", `{}`)
	assertStatus(t, response, http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func createResignedGameAfterPass(t *testing.T, handler http.Handler) gameResponse {
	t.Helper()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`), http.StatusOK)
	resigned := performJSON(t, handler, http.MethodPost, path+"/resign", `{}`)
	assertStatus(t, resigned, http.StatusOK)
	if err := json.Unmarshal(resigned.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func Test終局後の不正な変化は対局状態を変更しない(t *testing.T) {
	setups := []struct {
		name       string
		coordinate string
		status     int
		create     func(*testing.T, http.Handler) gameResponse
	}{
		{
			name:       "盤外の座標",
			coordinate: "Z99",
			status:     http.StatusConflict,
			create:     createResignedGame,
		},
		{
			name:       "石がある点",
			coordinate: "E5",
			status:     http.StatusConflict,
			create:     createResignedGameAfterPass,
		},
	}

	for _, setup := range setups {
		t.Run(setup.name, func(t *testing.T) {
			dataDir := t.TempDir()
			server, err := NewPersistentServer(dataDir, detailedFixedAnalyzer{fixedAnalyzer{move: "pass"}})
			if err != nil {
				t.Fatal(err)
			}
			handler := server.Handler()
			finished := setup.create(t, handler)
			path := "/api/games/" + finished.Game.ID
			canonical := filepath.Join(dataDir, finished.Game.ID+".json")
			fileBefore, err := os.ReadFile(canonical)
			if err != nil {
				t.Fatal(err)
			}
			responseBefore := performJSON(t, handler, http.MethodGet, path, ``)
			assertStatus(t, responseBefore, http.StatusOK)

			failed := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"`+setup.coordinate+`"}`)
			assertStatus(t, failed, setup.status)

			responseAfter := performJSON(t, handler, http.MethodGet, path, ``)
			assertStatus(t, responseAfter, http.StatusOK)
			if !bytes.Equal(responseAfter.Body.Bytes(), responseBefore.Body.Bytes()) {
				t.Fatalf("失敗した変化でメモリ上の対局が変わった\n変更前：%s\n変更後：%s", responseBefore.Body.String(), responseAfter.Body.String())
			}
			fileAfter, err := os.ReadFile(canonical)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(fileAfter, fileBefore) {
				t.Fatal("失敗した変化で保存ファイルが変わった")
			}

			saved := performJSON(t, handler, http.MethodPatch, path+"/nodes/root", `{"note":"確認済み","important":false,"expectedNote":"","expectedImportant":false}`)
			assertStatus(t, saved, http.StatusOK)
			var savedState gameResponse
			if err := json.Unmarshal(saved.Body.Bytes(), &savedState); err != nil {
				t.Fatal(err)
			}
			if savedState.Game.Status != "finished" || savedState.Game.Result == nil || savedState.Game.CurrentNodeID != finished.Game.CurrentNodeID {
				t.Fatalf("後続の保存で壊れた状態を書き込んだ：%+v", savedState.Game)
			}

			restarted, err := NewPersistentServer(dataDir, detailedFixedAnalyzer{fixedAnalyzer{move: "pass"}})
			if err != nil {
				t.Fatal(err)
			}
			restored := performJSON(t, restarted.Handler(), http.MethodGet, path, ``)
			assertStatus(t, restored, http.StatusOK)
			var restoredState gameResponse
			if err := json.Unmarshal(restored.Body.Bytes(), &restoredState); err != nil {
				t.Fatal(err)
			}
			if restoredState.Game.Status != "finished" || restoredState.Game.Result == nil || restoredState.Game.CurrentNodeID != finished.Game.CurrentNodeID {
				t.Fatalf("再起動後に壊れた状態を復元した：%+v", restoredState.Game)
			}
		})
	}
}
