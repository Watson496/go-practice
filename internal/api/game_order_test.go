package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func Test並べ替えていない対局は並べ替えた対局より前に開始日時の新しい順で並ぶ(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	ids := make([]string, 3)
	for index := range ids {
		created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
		var state gameResponse
		if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		ids[index] = state.Game.ID
	}
	listed := performJSON(t, handler, http.MethodGet, "/api/games", "")
	assertGameOrder(t, listed.Body.Bytes(), ids[2], ids[1], ids[0])

	// 指定にない対局（ids[2]）は、まだ並べ替えていない対局として先頭に並ぶ。
	body, err := json.Marshal(map[string]any{"ids": []string{ids[0], ids[1]}})
	if err != nil {
		t.Fatal(err)
	}
	reordered := performJSON(t, handler, http.MethodPut, "/api/games/order", string(body))
	assertStatus(t, reordered, http.StatusOK)
	assertGameOrder(t, reordered.Body.Bytes(), ids[2], ids[0], ids[1])

	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var newest gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &newest); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	listed = performJSON(t, restarted.Handler(), http.MethodGet, "/api/games", "")
	assertGameOrder(t, listed.Body.Bytes(), newest.Game.ID, ids[2], ids[0], ids[1])
}

func Test壊れた並び順を報告して正常な対局を読み込む(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	orderPath := filepath.Join(dataDir, gameOrderFile)
	broken := state.Game.ID + "\n" + state.Game.ID + "\n"
	if err := os.WriteFile(orderPath, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatalf("壊れた並び順により起動できなかった：%v", err)
	}
	listed := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games", "")
	assertGameOrder(t, listed.Body.Bytes(), state.Game.ID)
	failures := performJSON(t, restarted.Handler(), http.MethodGet, "/api/storage-errors", "")
	var storageFailures []storageLoadError
	if err := json.Unmarshal(failures.Body.Bytes(), &storageFailures); err != nil {
		t.Fatal(err)
	}
	if len(storageFailures) != 1 || storageFailures[0].File != gameOrderFile {
		t.Fatalf("並び順の読み込み失敗 = %+v", storageFailures)
	}
	contents, err := os.ReadFile(orderPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != broken {
		t.Fatalf("壊れた並び順を書き換えた：%q", contents)
	}
}

func Test保存した対局の並び順を変更して再起動後に復元する(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	first := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	second := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var firstGame, secondGame gameResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstGame); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondGame); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"ids": []string{firstGame.Game.ID, secondGame.Game.ID}})
	if err != nil {
		t.Fatal(err)
	}
	reordered := performJSON(t, handler, http.MethodPut, "/api/games/order", string(body))
	assertStatus(t, reordered, http.StatusOK)
	assertGameOrder(t, reordered.Body.Bytes(), firstGame.Game.ID, secondGame.Game.ID)

	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	listed := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games", "")
	assertStatus(t, listed, http.StatusOK)
	assertGameOrder(t, listed.Body.Bytes(), firstGame.Game.ID, secondGame.Game.ID)
}

func Test並べ替えは重複を拒否して同時に増減した対局を保つ(t *testing.T) {
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
	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{name: "重複", body: `{"ids":["` + state.Game.ID + `","` + state.Game.ID + `"]}`, want: http.StatusBadRequest},
		{name: "空値", body: `{"ids":null}`, want: http.StatusBadRequest},
		{name: "省略した対局を保つ", body: `{"ids":[]}`, want: http.StatusOK},
		{name: "削除済みの対局を無視する", body: `{"ids":["000000000000000000000000"]}`, want: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertStatus(t, performJSON(t, handler, http.MethodPut, "/api/games/order", test.body), test.want)
		})
	}
}

func Test対局を削除した並び順を再起動後にも保持する(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	first := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	second := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var firstGame, secondGame gameResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstGame); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondGame); err != nil {
		t.Fatal(err)
	}
	order, err := json.Marshal(map[string]any{"ids": []string{firstGame.Game.ID, secondGame.Game.ID}})
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, performJSON(t, handler, http.MethodPut, "/api/games/order", string(order)), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodDelete, "/api/games/"+firstGame.Game.ID, ""), http.StatusOK)

	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	listed := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games", "")
	assertStatus(t, listed, http.StatusOK)
	assertGameOrder(t, listed.Body.Bytes(), secondGame.Game.ID)
}

func assertGameOrder(t *testing.T, body []byte, ids ...string) {
	t.Helper()
	var games []gameResponse
	if err := json.Unmarshal(body, &games); err != nil {
		t.Fatal(err)
	}
	if len(games) != len(ids) {
		t.Fatalf("保存した対局 = %d件、期待値 = %d件", len(games), len(ids))
	}
	for index, id := range ids {
		if games[index].Game.ID != id {
			t.Fatalf("%d番目の対局 = %s、期待値 = %s", index+1, games[index].Game.ID, id)
		}
	}
}
