package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-practice/internal/game"
)

func Test永続サーバーは不正な保存データを隔離する(t *testing.T) {
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
	canonical, err := os.ReadFile(filepath.Join(dataDir, state.Game.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var base savedGame
	if err := json.Unmarshal(canonical, &base); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		id     string
		fileID string
		mutate func(*savedGame)
	}{
		{id: "cycle", mutate: func(saved *savedGame) {
			saved.Game.TreeNodes = append(saved.Game.TreeNodes,
				game.TreeNode{ID: "cycle-a", ParentID: "cycle-b"},
				game.TreeNode{ID: "cycle-b", ParentID: "cycle-a"},
			)
		}},
		{id: "long-note", mutate: func(saved *savedGame) {
			saved.Game.RootProperties = append(saved.Game.RootProperties, game.SGFProperty{Ident: "C", Values: []string{strings.Repeat("長", 4000+1)}})
		}},
		{id: "long-variation-title", mutate: func(saved *savedGame) {
			saved.Game.TreeNodes = append(saved.Game.TreeNodes, game.TreeNode{
				ID:             "long-title-node",
				ParentID:       "root",
				VariationTitle: strings.Repeat("長", 80+1),
			})
		}},
		{id: "name-mismatch", mutate: func(saved *savedGame) {
			saved.Name = "保存名"
			saved.Game.RootProperties = append(saved.Game.RootProperties, game.SGFProperty{Ident: "GN", Values: []string{"別名"}})
		}},
		{id: "name-missing", mutate: func(saved *savedGame) {
			saved.Name = "保存名"
		}},
		{id: "name-values", mutate: func(saved *savedGame) {
			saved.Name = "保存名"
			saved.Game.RootProperties = append(saved.Game.RootProperties, game.SGFProperty{Ident: "GN", Values: []string{"保存名", "別名"}})
		}},
		{id: "name-whitespace", mutate: func(saved *savedGame) {
			saved.Name = "保存名"
			saved.Game.RootProperties = append(saved.Game.RootProperties, game.SGFProperty{Ident: "GN", Values: []string{" 保存名 "}})
		}},
		{id: "date-mismatch", mutate: func(saved *savedGame) {
			saved.StartedAt = "2026-01-01T00:00:00Z"
			replaceRootProperty(saved, "DT", []string{"2026-01-02"})
		}},
		{id: "date-values", mutate: func(saved *savedGame) {
			replaceRootProperty(saved, "DT", []string{"2026-01-01", "2026-01-02"})
		}},
		{id: "date-format", mutate: func(saved *savedGame) {
			replaceRootProperty(saved, "DT", []string{"2026/01/01"})
		}},
	}
	for index := range tests {
		test := &tests[index]
		test.fileID = fmt.Sprintf("%024x", index+1)
		var saved savedGame
		data, err := json.Marshal(base)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		saved.Game.ID = test.fileID
		test.mutate(&saved)
		data, err = json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, test.fileID+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	uppercaseID := "ABCDEFABCDEFABCDEFABCDEF"
	var uppercase savedGame
	if err := json.Unmarshal(canonical, &uppercase); err != nil {
		t.Fatal(err)
	}
	uppercase.Game.ID = uppercaseID
	uppercaseData, err := json.Marshal(uppercase)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, uppercaseID+".json"), uppercaseData, 0o600); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+state.Game.ID, ``), http.StatusOK)
	for _, test := range tests {
		assertStatus(t, performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+test.fileID, ``), http.StatusNotFound)
	}
	errorsResponse := performJSON(t, restarted.Handler(), http.MethodGet, "/api/storage-errors", ``)
	assertStatus(t, errorsResponse, http.StatusOK)
	var failures []storageLoadError
	if err := json.Unmarshal(errorsResponse.Body.Bytes(), &failures); err != nil {
		t.Fatal(err)
	}
	if len(failures) != len(tests)+1 {
		t.Fatalf("隔離した保存ファイル = %+v", failures)
	}
	for _, test := range tests {
		found := false
		for _, failure := range failures {
			found = found || failure.File == test.fileID+".json"
		}
		if !found {
			t.Fatalf("%sの隔離理由がない：%+v", test.id, failures)
		}
	}
	uppercaseFound := false
	for _, failure := range failures {
		uppercaseFound = uppercaseFound || failure.File == uppercaseID+".json"
	}
	if !uppercaseFound {
		t.Fatalf("大文字の識別情報を持つ保存ファイルの隔離理由がない：%+v", failures)
	}
}

func replaceRootProperty(saved *savedGame, ident string, values []string) {
	for index := range saved.Game.RootProperties {
		if saved.Game.RootProperties[index].Ident == ident {
			saved.Game.RootProperties[index].Values = values
			return
		}
	}
	saved.Game.RootProperties = append(saved.Game.RootProperties, game.SGFProperty{Ident: ident, Values: values})
}
