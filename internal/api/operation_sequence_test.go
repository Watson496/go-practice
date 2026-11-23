package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-practice/internal/game"
	"go-practice/internal/sgf"
)

func TestHTTP操作列が失敗時の巻き戻しと往復を保つ(t *testing.T) {
	coordinates := []string{"A1", "B1", "E5", "C7", "I9", "pass", "Z99", "A01", " d4 "}
	sources := []string{
		"",
		"(;FF[4]SZ[9]RE[W+7.5];B[];W[])",
		"(;FF[4]CA[UTF-8]SZ[9];B[];W[];C[終局後のメモ])",
		"(;FF[4]SZ[9]RE[Void];B[aa];W[bb])",
		"(;FF[4]CA[UTF-8]SZ[9]KM[-0.5];B[];W[];C[負のコミ])",
		"(;FF[4]SZ[9]HA[2]AB[cg][gc]PL[W];W[ee];B[])",
	}
	for seed := int64(1); seed <= 20; seed++ {
		dataDir := t.TempDir()
		server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
		if err != nil {
			t.Fatal(err)
		}
		var initial gameResponse
		if source := sources[int(seed)%len(sources)]; source == "" {
			created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
			assertStatus(t, created, http.StatusCreated)
			if err := json.Unmarshal(created.Body.Bytes(), &initial); err != nil {
				t.Fatal(err)
			}
		} else {
			initial = importSGFGame(t, server.Handler(), source)
		}
		gameID := initial.Game.ID
		random := rand.New(rand.NewSource(seed))
		history := make([]string, 0, 15)
		for step := 0; step < 15; step++ {
			path := "/api/games/" + gameID
			before := performJSON(t, server.Handler(), http.MethodGet, path+"/json", "")
			assertStatus(t, before, http.StatusOK)
			stateResponse := performJSON(t, server.Handler(), http.MethodGet, path, "")
			assertStatus(t, stateResponse, http.StatusOK)
			var state gameResponse
			if err := json.Unmarshal(stateResponse.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			method, requestPath, body := randomHTTPGameOperation(t, random, path, state, coordinates)
			response := performJSON(t, server.Handler(), method, requestPath, body)
			history = append(history, fmt.Sprintf("%s %s %s（%d）", method, requestPath, body, response.Code))
			if response.Code >= 500 {
				t.Fatalf("乱数初期値%dの%d操作目でサーバーエラーになりました：%s\n%v", seed, step+1, response.Body.String(), history)
			}
			after := performJSON(t, server.Handler(), http.MethodGet, path+"/json", "")
			assertStatus(t, after, http.StatusOK)
			var afterRecord importGameJSONRequest
			if err := json.Unmarshal(after.Body.Bytes(), &afterRecord); err != nil {
				t.Fatal(err)
			}
			history[len(history)-1] += fmt.Sprintf("→現在%s・主分岐%s・%d手", afterRecord.Game.CurrentNodeID, afterRecord.Game.MainLineNodeID, len(afterRecord.Game.Moves))
			if response.Code < 200 || response.Code >= 300 {
				if !bytes.Equal(before.Body.Bytes(), after.Body.Bytes()) {
					t.Fatalf("乱数初期値%dの%d操作目で失敗した操作が保存状態を変更しました", seed, step+1)
				}
			} else {
				assertSGFRoundTrip(t, server.Handler(), gameID, history)
			}
			restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
			if err != nil {
				t.Fatal(err)
			}
			restored := performJSON(t, restarted.Handler(), http.MethodGet, path+"/json", "")
			assertStatus(t, restored, http.StatusOK)
			if !bytes.Equal(after.Body.Bytes(), restored.Body.Bytes()) {
				t.Fatalf("乱数初期値%dの%d操作目が再起動後に変化しました", seed, step+1)
			}
			server = restarted
		}
	}
}

func randomHTTPGameOperation(t *testing.T, random *rand.Rand, path string, state gameResponse, coordinates []string) (string, string, string) {
	t.Helper()
	node := state.Game.TreeNodes[random.Intn(len(state.Game.TreeNodes))]
	switch random.Intn(9) {
	case 0:
		return http.MethodPost, path + "/moves", `{"coordinate":"` + coordinates[random.Intn(len(coordinates))] + `"}`
	case 1:
		return http.MethodPost, path + "/ai-moves", `{}`
	case 2:
		return http.MethodPost, path + "/variations", `{"coordinate":"` + coordinates[random.Intn(len(coordinates))] + `"}`
	case 3:
		return http.MethodPost, path + "/nodes/" + node.ID, ""
	case 4:
		return http.MethodPost, path + "/nodes/" + node.ID + "/main-line", ""
	case 5:
		expected := testSubtreeNodeIDs(state.Game.TreeNodes, node.ID)
		encoded, err := json.Marshal(map[string]any{"expectedNodeIds": expected})
		if err != nil {
			t.Fatal(err)
		}
		return http.MethodDelete, path + "/nodes/" + node.ID, string(encoded)
	case 6:
		return http.MethodPost, path + "/resign", `{}`
	case 7:
		return http.MethodPost, path + "/undo", `{}`
	default:
		encoded, err := json.Marshal(map[string]any{
			"note": fmt.Sprintf("乱数メモ%d", random.Intn(5)), "important": !node.Important,
			"expectedNote": testNodeComment(state.Game, node.ID), "expectedImportant": node.Important,
		})
		if err != nil {
			t.Fatal(err)
		}
		return http.MethodPatch, path + "/nodes/" + node.ID, string(encoded)
	}
}

func testSubtreeNodeIDs(nodes []game.TreeNode, root string) []string {
	result := []string{root}
	for index := 0; index < len(result); index++ {
		for _, node := range nodes {
			if node.ParentID == result[index] {
				result = append(result, node.ID)
			}
		}
	}
	return result
}

func assertSGFRoundTrip(t *testing.T, handler http.Handler, gameID string, history []string) {
	t.Helper()
	exported := httptest.NewRecorder()
	handler.ServeHTTP(exported, httptest.NewRequest(http.MethodGet, "/api/games/"+gameID+"/sgf", nil))
	assertStatus(t, exported, http.StatusOK)
	first, err := sgf.DecodeGameWithDeadStoneEstimator(exported.Body.String(), "操作列往復", nil)
	if err != nil {
		t.Fatalf("書き出したSGFを読み込めません：%v\n%s\n%v", err, exported.Body.String(), history)
	}
	encoded, err := sgf.Encode(first.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	second, err := sgf.DecodeGameWithDeadStoneEstimator(encoded, "操作列再往復", nil)
	if err != nil {
		t.Fatal(err)
	}
	stable, err := sgf.Encode(second.Snapshot())
	if err != nil || encoded != stable {
		t.Fatalf("SGFが安定していません：%v\n1回目=%s\n2回目=%s", err, encoded, stable)
	}
	if strings.Contains(encoded, "GP") {
		t.Fatalf("書き出したSGFにアプリ固有プロパティがあります：%s", encoded)
	}
}
