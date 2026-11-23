package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	coord "go-practice/internal/coordinate"
	"go-practice/internal/game"
	"go-practice/internal/katago"
)

type fixedAnalyzer struct {
	move            string
	err             error
	analysisCalls   *int
	analysisVersion string
}

func newTestServer(t *testing.T, analyzer AIEngine) *Server {
	t.Helper()
	server, err := NewPersistentServer(t.TempDir(), analyzer)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

type detailedFixedAnalyzer struct{ fixedAnalyzer }

func (analyzer detailedFixedAnalyzer) AnalyzePosition(_ context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	summary := validAnalysisSummary(position, visits)
	if analyzer.move == "pass" {
		summary.Candidates = []katago.CandidateSummary{{Move: "pass", Visits: visits, Prior: 1, Winrate: 0.5}}
		return summary, analyzer.err
	}
	summary.Candidates = []katago.CandidateSummary{
		{Move: "C3", ScoreLead: 3, Visits: visits, Prior: 0.000001, Winrate: 0.5},
		{Move: analyzer.move, ScoreLead: 0, Visits: visits, Prior: 1, Winrate: 0.5},
	}
	return summary, analyzer.err
}

type deadStoneAnalyzer struct {
	move       string
	coordinate string
}

func (analyzer deadStoneAnalyzer) AnalyzePosition(_ context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	result := validAnalysisSummary(position, visits)
	result.Candidates = []katago.CandidateSummary{{Move: analyzer.move, Visits: visits, Prior: 1, Winrate: 0.5}}
	point, ok := coord.Point(analyzer.coordinate, position.BoardSize)
	if !ok {
		return katago.Summary{}, fmt.Errorf("死石の座標%sが不正です", analyzer.coordinate)
	}
	if position.Stones[analyzer.coordinate] == "B" {
		result.Ownership[point] = -1
	} else {
		result.Ownership[point] = 1
	}
	return result, nil
}

func (deadStoneAnalyzer) AnalysisVersion() string { return "test-analysis-v1" }

type cancellingAnalyzer struct{}

type blockingAnalyzer struct {
	started chan struct{}
	release chan struct{}
}

func (blockingAnalyzer) AnalysisVersion() string { return "test-analysis-v1" }

type moveNumberAnalyzer struct{}

type deadlineAnalyzer struct{}

func requireThirtySecondDeadline(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("30秒の制限時間が設定されていません")
	}
	remaining := time.Until(deadline)
	if remaining < 29*time.Second || remaining > 30*time.Second {
		return fmt.Errorf("制限時間が30秒ではありません：%s", remaining)
	}
	return nil
}

func (deadlineAnalyzer) AnalyzePosition(ctx context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	if err := requireThirtySecondDeadline(ctx); err != nil {
		return katago.Summary{}, err
	}
	summary := validAnalysisSummary(position, visits)
	summary.Candidates = []katago.CandidateSummary{{Move: "D5", Visits: visits, Prior: 1, Winrate: 0.5}}
	return summary, nil
}

func (deadlineAnalyzer) AnalysisVersion() string { return "test-analysis-v1" }

func (cancellingAnalyzer) AnalyzePosition(ctx context.Context, _ game.Snapshot, _ int) (katago.Summary, error) {
	<-ctx.Done()
	return katago.Summary{}, ctx.Err()
}
func (analyzer blockingAnalyzer) AnalyzePosition(_ context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	close(analyzer.started)
	<-analyzer.release
	summary := validAnalysisSummary(position, visits)
	summary.Candidates = []katago.CandidateSummary{{Move: "D5", Visits: visits, Prior: 1, Winrate: 0.5}}
	return summary, nil
}
func (moveNumberAnalyzer) AnalyzePosition(_ context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	moves := []string{"D5", "C5"}
	move := moves[len(position.Moves)/2]
	summary := validAnalysisSummary(position, visits)
	summary.Candidates = []katago.CandidateSummary{{Move: move, Visits: visits, Prior: 1, Winrate: 0.5}}
	return summary, nil
}

func (moveNumberAnalyzer) AnalysisVersion() string { return "test-analysis-v1" }

func (cancellingAnalyzer) AnalysisVersion() string { return "test-analysis-v1" }

func (analyzer fixedAnalyzer) AnalyzePosition(_ context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	if analyzer.analysisCalls != nil {
		*analyzer.analysisCalls++
	}
	summary := validAnalysisSummary(position, visits)
	if analyzer.move != "" {
		summary.Candidates = []katago.CandidateSummary{{Move: analyzer.move, Visits: visits, Prior: 1, Winrate: 0.5}}
	}
	return summary, analyzer.err
}

func validAnalysisSummary(position game.Snapshot, visits int) katago.Summary {
	ownership := make([]float64, position.BoardSize*position.BoardSize)
	if len(ownership) > 0 {
		ownership[0] = 0.75
	}
	return katago.Summary{
		TurnNumber: len(position.Moves), CurrentPlayer: position.CurrentPlayer,
		ScoreLead: 0, Winrate: 0.5, Visits: visits, Ownership: ownership,
	}
}

func (analyzer fixedAnalyzer) AnalysisVersion() string {
	if analyzer.analysisVersion != "" {
		return analyzer.analysisVersion
	}
	return "test-analysis-v1"
}

func Test対局APIで対局作成から着手と棋譜出力まで実行する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"komi":7.5,"aiLevel":2}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("対局作成状態コード = %d、本文 = %s", created.Code, created.Body.String())
	}
	var response gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Game.BoardSize != 9 || response.Game.CurrentPlayer != "B" || value(response.AILevel) != 2 {
		t.Fatalf("想定外の対局：%+v", response)
	}
	if !bytes.Contains(created.Body.Bytes(), []byte(`"moves":[]`)) {
		t.Fatalf("新規対局の着手列は空配列である必要がある：%s", created.Body.String())
	}

	path := "/api/games/" + response.Game.ID + "/moves"
	played := performJSON(t, handler, http.MethodPost, path, `{"coordinate":"E5"}`)
	if played.Code != http.StatusOK {
		t.Fatalf("着手状態コード = %d、本文 = %s", played.Code, played.Body.String())
	}

	fetched := httptest.NewRecorder()
	handler.ServeHTTP(fetched, httptest.NewRequest(http.MethodGet, "/api/games/"+response.Game.ID, nil))
	if fetched.Code != http.StatusOK {
		t.Fatalf("取得状態コード = %d、本文 = %s", fetched.Code, fetched.Body.String())
	}
	if err := json.Unmarshal(fetched.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Game.CurrentPlayer != "W" || response.Game.Stones["E5"] != "B" || len(response.Game.Moves) != 1 {
		t.Fatalf("着手後の状態が想定外：%+v", response)
	}

	sgfResponse := httptest.NewRecorder()
	handler.ServeHTTP(sgfResponse, httptest.NewRequest(http.MethodGet, "/api/games/"+response.Game.ID+"/sgf", nil))
	if sgfResponse.Code != http.StatusOK {
		t.Fatalf("SGF出力状態コード = %d、本文 = %s", sgfResponse.Code, sgfResponse.Body.String())
	}
	if contentType := sgfResponse.Header().Get("Content-Type"); contentType != "application/x-go-sgf; charset=utf-8" {
		t.Fatalf("SGFのContent-Type = %q", contentType)
	}
	if disposition := sgfResponse.Header().Get("Content-Disposition"); disposition != `attachment; filename="go-practice-`+response.Game.ID+`.sgf"` {
		t.Fatalf("SGFのContent-Disposition = %q", disposition)
	}
	if got, want := sgfResponse.Body.String(), ";B[ee]"; !bytes.Contains([]byte(got), []byte(want)) {
		t.Fatalf("SGF %qに%qがない", got, want)
	}
}

func Test対局APIが無効な要求を拒否する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	invalidLevel := performJSON(t, handler, http.MethodPost, "/api/games", `{"boardSize":9,"aiLevel":5}`)
	if invalidLevel.Code != http.StatusBadRequest {
		t.Fatalf("不正なレベルの状態コード = %d", invalidLevel.Code)
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/games/missing", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("存在しない対局の状態コード = %d", missing.Code)
	}
}

func Test対局APIが保存済み対局の一覧を返す(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	for _, size := range []int{9, 13} {
		created := createGameForTest(t, handler, fmt.Sprintf(`{"boardSize":%d,"aiLevel":1}`, size))
		if created.Code != http.StatusCreated {
			t.Fatal(created.Body.String())
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/games", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("応答状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	var games []gameResponse
	if err := json.Unmarshal(response.Body.Bytes(), &games); err != nil {
		t.Fatal(err)
	}
	if len(games) != 2 {
		t.Fatalf("対局一覧 = %#v", games)
	}
}

func Test対局APIは保存された対局の名前を変更し名前を保持する(t *testing.T) {
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
	if state.StartedAt == "" {
		t.Fatal("新規対局に開始時刻がない")
	}
	renamed := performJSON(t, server.Handler(), http.MethodPatch, "/api/games/"+state.Game.ID, `{"name":"  初めての対局  ","expectedName":""}`)
	if renamed.Code != http.StatusOK {
		t.Fatalf("対局名変更状態コード = %d、本文 = %s", renamed.Code, renamed.Body.String())
	}
	if err := json.Unmarshal(renamed.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Name != "初めての対局" {
		t.Fatalf("対局名 = %q", state.Name)
	}
	renamedStartedAt := state.StartedAt

	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	loaded := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+state.Game.ID, "")
	assertStatus(t, loaded, http.StatusOK)
	if err := json.Unmarshal(loaded.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Name != "初めての対局" || state.StartedAt != renamedStartedAt {
		t.Fatalf("再読み込み後の付随情報 = %+v", state)
	}

	invalid := performJSON(t, restarted.Handler(), http.MethodPatch, "/api/games/"+state.Game.ID, `{"name":"   ","expectedName":"初めての対局"}`)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("空の対局名の状態コード = %d", invalid.Code)
	}
}

func Test対局APIが変化を含むSGFを読み込む(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", bytes.NewBufferString(
		"(;GM[1]FF[4]SZ[9]RU[Chinese]KM[7.5]PB[Human];B[ee](;W[de])(;W[ed]C[variation]))",
	))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("応答状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	var imported gameResponse
	if err := json.Unmarshal(response.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if len(imported.Game.Moves) != 2 || len(imported.Game.TreeNodes) != 4 || imported.Game.CurrentNodeID == "root" {
		t.Fatalf("想定外の読み込み結果：%+v", imported.Game)
	}
	sgfResponse := httptest.NewRecorder()
	handler.ServeHTTP(sgfResponse, httptest.NewRequest(http.MethodGet, "/api/games/"+imported.Game.ID+"/sgf", nil))
	assertStatus(t, sgfResponse, http.StatusOK)
	if !strings.Contains(sgfResponse.Body.String(), "C[variation]") || !strings.Contains(sgfResponse.Body.String(), "PB[Human]") {
		t.Fatalf("書き出しで読み込んだ属性が失われた：%s", sgfResponse.Body.String())
	}
}

func Test対局APIはSGF日付を開始日として使用する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader("(;FF[4]SZ[9]DT[2026-10-20])"))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("SGF読み込み状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	var imported gameResponse
	if err := json.Unmarshal(response.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	startedAt, err := time.Parse(time.RFC3339Nano, imported.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	if startedAt.Year() != 2026 || startedAt.Month() != time.October || startedAt.Day() != 20 {
		t.Fatalf("開始日時 = %s", imported.StartedAt)
	}
}

func Test対局APIが書き出したJSONを新しい保存対局として読み込む(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	createdResponse := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":2,"humanColor":"B"}`)
	if createdResponse.Code != http.StatusCreated {
		t.Fatal(createdResponse.Body.String())
	}
	var created gameResponse
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	playedResponse := performJSON(t, handler, http.MethodPost, "/api/games/"+created.Game.ID+"/moves", `{"coordinate":"E5"}`)
	if playedResponse.Code != http.StatusOK {
		t.Fatal(playedResponse.Body.String())
	}
	var played playMoveResponse
	if err := json.Unmarshal(playedResponse.Body.Bytes(), &played); err != nil {
		t.Fatal(err)
	}
	note := performJSON(t, handler, http.MethodPatch, "/api/games/"+created.Game.ID+"/nodes/root", `{"note":"<&>","important":false,"expectedNote":"","expectedImportant":false}`)
	assertStatus(t, note, http.StatusOK)
	exportedResponse := performJSON(t, handler, http.MethodGet, "/api/games/"+created.Game.ID+"/json", "")
	if exportedResponse.Code != http.StatusOK {
		t.Fatalf("応答の状態コード = %d、本文 = %s", exportedResponse.Code, exportedResponse.Body.String())
	}
	if strings.Contains(exportedResponse.Body.String(), `\u003c`) || !strings.Contains(exportedResponse.Body.String(), `"<&>"`) {
		t.Fatalf("JSON書き出しでHTML用のエスケープを行っている：%s", exportedResponse.Body.String())
	}
	var exported importGameJSONRequest
	if err := json.Unmarshal(exportedResponse.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	if exported.SchemaVersion != "1" || exported.RecordedAt == "" || value(exported.HumanColor) != "B" || value(exported.AIColor) != "W" || value(exported.AILevel) != 2 {
		t.Fatalf("想定外のJSON書き出し結果：%+v", exported)
	}
	importedResponse := performJSON(t, handler, http.MethodPost, "/api/games/import-json", exportedResponse.Body.String())
	if importedResponse.Code != http.StatusCreated {
		t.Fatalf("応答状態コード = %d、本文 = %s", importedResponse.Code, importedResponse.Body.String())
	}
	var imported gameResponse
	if err := json.Unmarshal(importedResponse.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Game.ID == played.Game.Game.ID {
		t.Fatal("JSON読み込みで書き出し元の対局の識別情報が再利用された")
	}
	if value(imported.AILevel) != 2 || value(imported.HumanColor) != "B" || len(imported.Game.Moves) != 1 || imported.Game.Moves[0].Coordinate != "E5" {
		t.Fatalf("想定外のJSON読み込み結果：%+v", imported)
	}
}

func TestJSON読み込み後の着手で保留したAI着手を記録する(t *testing.T) {
	// 解析器が返すもう一方の候補を初期配置で塞ぎ、乱数によらずD4を選ぶ状態にする。
	session, err := game.NewSessionWithSetup("exported", 9, 7.5, 0, map[string]string{"C3": "B"}, "W")
	if err != nil {
		t.Fatal(err)
	}
	record := importGameJSONRequest{
		SchemaVersion: "1", RecordedAt: "2026-10-20T00:00:00Z",
		HumanColor: ptr("B"), AIColor: ptr("W"), AILevel: ptr(2), Game: session.Snapshot(),
	}
	contents, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, detailedFixedAnalyzer{fixedAnalyzer: fixedAnalyzer{move: "D4"}})
	response := performJSON(t, server.Handler(), http.MethodPost, "/api/games/import-json", string(contents))
	if response.Code != http.StatusCreated {
		t.Fatalf("応答状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	var imported gameResponse
	if err := json.Unmarshal(response.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	aiResponse := performJSON(t, server.Handler(), http.MethodPost, "/api/games/"+imported.Game.ID+"/ai-moves", `{}`)
	if aiResponse.Code != http.StatusOK {
		t.Fatalf("AI着手状態コード = %d、本文 = %s", aiResponse.Code, aiResponse.Body.String())
	}
	var played aiMoveResponse
	if err := json.Unmarshal(aiResponse.Body.Bytes(), &played); err != nil {
		t.Fatal(err)
	}
	imported = played.Game
	if len(imported.Game.Moves) != 1 || imported.Game.Moves[0].Coordinate != "D4" || imported.Game.MainLineNodeID != imported.Game.CurrentNodeID {
		t.Fatalf("AIの着手が主分岐へ確定されていない：%+v", imported.Game)
	}
	var decision *game.AIDecision
	for _, node := range imported.Game.TreeNodes {
		if node.ID == imported.Game.CurrentNodeID {
			decision = node.AIDecision
		}
	}
	if decision == nil || decision.Level != 2 || decision.SelectedMove != "D4" {
		t.Fatalf("AI判断が記録されていない：%+v", decision)
	}
}

func Test対局APIが無効な棋譜ツリー結果を拒否する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	record := `{
		"schemaVersion":"1",
		"recordedAt":"2026-10-20T00:00:00Z",
		"humanColor":"B",
		"aiColor":"W",
		"aiLevel":1,
		"game":{
			"id":"invalid-result",
			"boardSize":9,
			"rules":"chinese",
			"komi":7.5,
			"handicap":0,
			"setupStones":{},
			"initialPlayer":"B",
			"currentPlayer":"B",
			"status":"finished",
			"consecutivePasses":0,
			"moves":[],
			"stones":{},
			"captures":{"B":0,"W":0},
			"result":{"type":"invalid","cause":"recorded","scoreKnown":false,"deadStones":[]},
			"treeNodes":[{"id":"root"},{"id":"n1","parentId":"root","result":{"type":"invalid","cause":"recorded","scoreKnown":false,"deadStones":[]}}],
			"currentNodeId":"n1",
			"mainLineNodeId":"n1",
			"nextNodeNumber":2
		}
	}`
	response := performJSON(t, handler, http.MethodPost, "/api/games/import-json", record)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("応答状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func Test対局APIが無効なJSON保存データ付随情報を拒否する(t *testing.T) {
	session, err := game.NewConfiguredSession("exported", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	valid := importGameJSONRequest{
		SchemaVersion: "1", RecordedAt: "2026-10-20T00:00:00Z", HumanColor: ptr("B"), AIColor: ptr("W"), AILevel: ptr(1), Game: session.Snapshot(),
	}
	tests := []struct {
		name   string
		status int
		change func(*importGameJSONRequest)
	}{
		{name: "AIの色", status: http.StatusBadRequest, change: func(record *importGameJSONRequest) { record.AIColor = ptr("invalid") }},
		{name: "同じ対局者の色", status: http.StatusBadRequest, change: func(record *importGameJSONRequest) { record.AIColor = ptr("B") }},
		{name: "未設定の対局者の色とAIの色", status: http.StatusBadRequest, change: func(record *importGameJSONRequest) { record.HumanColor = nil }},
		{name: "開始日時", status: http.StatusBadRequest, change: func(record *importGameJSONRequest) { record.RecordedAt = "invalid" }},
		{name: "長すぎる対局名", status: http.StatusRequestEntityTooLarge, change: func(record *importGameJSONRequest) { record.Name = strings.Repeat("名", 81) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := valid
			test.change(&record)
			contents, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			response := performJSON(t, newTestServer(t, fixedAnalyzer{move: "pass"}).Handler(), http.MethodPost, "/api/games/import-json", string(contents))
			if response.Code != test.status {
				t.Fatalf("応答状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
		})
	}
}

func Test対局APIが不明な結果フィールドを拒否する(t *testing.T) {
	contents := `{"schemaVersion":"1","recordedAt":"2026-10-20T00:00:00Z","humanColor":"B","aiColor":"W","aiLevel":1,"game":{"id":"exported","boardSize":9,"rules":"chinese","komi":7.5,"handicap":0,"setupStones":{},"initialPlayer":"B","currentPlayer":"B","status":"finished","consecutivePasses":0,"moves":[],"stones":{},"captures":{"B":0,"W":0},"result":{"type":"points","cause":"recorded","winner":"B","margin":100,"scoreKnown":false,"imported":true},"treeNodes":[{"id":"root"}],"currentNodeId":"root","mainLineNodeId":"root","nextNodeNumber":1}}`
	response := performJSON(t, newTestServer(t, fixedAnalyzer{move: "pass"}).Handler(), http.MethodPost, "/api/games/import-json", contents)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("応答状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func Test対局APIが文字の大小を問わずChineseを受け付ける(t *testing.T) {
	source := []byte("(;FF[4]CA[UTF-8]SZ[9]RU[chinese])")
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", bytes.NewReader(source))
	request.Header.Set("Content-Type", "application/x-go-sgf; charset=utf-8")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("応答状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func Test対局APIが複数の対局名を拒否する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader("(;FF[4]SZ[9]GN[一][二])"))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("応答状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func Test対局APIの読み込みエラーで局面の識別情報を公開しない(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	session, err := game.NewConfiguredSession("source", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	snapshot.TreeNodes = append(snapshot.TreeNodes, game.TreeNode{ID: "ユーザーが指定した秘密のID", ParentID: "存在しない親"})
	record := importGameJSONRequest{
		SchemaVersion: "1", RecordedAt: "2026-11-04T00:00:00Z", HumanColor: ptr("B"), AIColor: ptr("W"), AILevel: ptr(1), Game: snapshot,
	}
	contents, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	jsonResponse := performJSON(t, handler, http.MethodPost, "/api/games/import-json", string(contents))
	if jsonResponse.Code != http.StatusBadRequest {
		t.Fatalf("JSON応答の状態コード = %d、本文 = %s", jsonResponse.Code, jsonResponse.Body.String())
	}

	sgfRequest := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader("(;FF[4]SZ[9];B[ee];B[dd])"))
	sgfRequest.Header.Set("Content-Type", "application/x-go-sgf")
	sgfResponse := httptest.NewRecorder()
	handler.ServeHTTP(sgfResponse, sgfRequest)
	if sgfResponse.Code != http.StatusBadRequest {
		t.Fatalf("SGF応答状態コード = %d、本文 = %s", sgfResponse.Code, sgfResponse.Body.String())
	}
}

func Test対局APIが点数の内訳のないSGF由来のJSONを再読み込みする(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader("(;FF[4]SZ[9]KM[7.5]RE[W+7.5];B[];W[])"))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("SGF読み込み状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	var imported gameResponse
	if err := json.Unmarshal(response.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(importGameJSONRequest{
		SchemaVersion: "1", RecordedAt: imported.StartedAt, ImportedAt: imported.ImportedAt, Name: imported.Name, HumanColor: ptr("B"), AIColor: ptr("W"), AILevel: ptr(1), Game: imported.Game,
	})
	if err != nil {
		t.Fatal(err)
	}

	reimported := performJSON(t, handler, http.MethodPost, "/api/games/import-json", string(record))
	if reimported.Code != http.StatusCreated {
		t.Fatalf("JSON読み込み状態コード = %d、本文 = %s", reimported.Code, reimported.Body.String())
	}
	var restored gameResponse
	if err := json.Unmarshal(reimported.Body.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if result := restored.Game.Result; result == nil || result.Cause != "two_passes" || result.Winner != "W" || result.Margin != 7.5 || result.BlackScore != 0 || result.WhiteScore != 7.5 || !result.ScoreKnown {
		t.Fatalf("想定外の復元結果：%+v", result)
	}
}

func Test対局APIは明示的なゼロコミを保持する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"komi":0,"aiLevel":1}`)
	var response gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Game.Komi != 0 {
		t.Fatalf("コミ = %v、期待値 = 0", response.Game.Komi)
	}
}

func Test対局APIは置き碁の石を配置して白を開始手番にする(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "E5"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":2,"handicap":2}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("応答状態コード = %d、本文 = %s", created.Code, created.Body.String())
	}
	var response gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	aiPlayed := performJSON(t, handler, http.MethodPost, "/api/games/"+response.Game.ID+"/ai-moves", `{}`)
	if aiPlayed.Code != http.StatusOK {
		t.Fatalf("AI初手状態コード = %d、本文 = %s", aiPlayed.Code, aiPlayed.Body.String())
	}
	var aiResponse aiMoveResponse
	if err := json.Unmarshal(aiPlayed.Body.Bytes(), &aiResponse); err != nil {
		t.Fatal(err)
	}
	response = aiResponse.Game
	if response.Game.Handicap != 2 || response.Game.Komi != 0.5 || response.Game.CurrentPlayer != "B" {
		t.Fatalf("想定外の置き碁：%+v", response.Game)
	}
	if len(response.Game.SetupStones) != 2 || len(response.Game.Moves) != 1 || response.Game.Moves[0].Color != "W" {
		t.Fatalf("初期配置後に白番から始まっていない：%+v", response.Game)
	}
}

func Test対局APIは盤サイズを超える置石を保存しない(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	for _, body := range []string{
		`{"boardSize":5,"aiLevel":1,"handicap":6}`,
		`{"boardSize":6,"aiLevel":1,"handicap":5}`,
	} {
		response := performJSON(t, handler, http.MethodPost, "/api/games", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("上限を超える置石への応答 = %d、本文 = %s", response.Code, response.Body.String())
		}
	}
	listed := performJSON(t, handler, http.MethodGet, "/api/games", ``)
	if listed.Code != http.StatusOK || strings.TrimSpace(listed.Body.String()) != "[]" {
		t.Fatalf("拒否後の対局一覧 = %d、本文 = %s", listed.Code, listed.Body.String())
	}
}

func Test人が白なら対局APIでAIが先着手する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "E5"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":2,"humanColor":"W"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("応答状態コード = %d、本文 = %s", created.Code, created.Body.String())
	}
	var response gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	aiPlayed := performJSON(t, handler, http.MethodPost, "/api/games/"+response.Game.ID+"/ai-moves", `{}`)
	if aiPlayed.Code != http.StatusOK {
		t.Fatalf("AI初手状態コード = %d、本文 = %s", aiPlayed.Code, aiPlayed.Body.String())
	}
	var aiResponse aiMoveResponse
	if err := json.Unmarshal(aiPlayed.Body.Bytes(), &aiResponse); err != nil {
		t.Fatal(err)
	}
	response = aiResponse.Game
	if value(response.HumanColor) != "W" || response.Game.CurrentPlayer != "W" || len(response.Game.Moves) != 1 || response.Game.Moves[0].Color != "B" {
		t.Fatalf("想定外の白番対局：%+v", response)
	}
	if response.Game.MainLineNodeID == "root" || response.Game.MainLineNodeID != response.Game.CurrentNodeID {
		t.Fatalf("AI初手後の主分岐の末端 = %s、現在局面 = %s", response.Game.MainLineNodeID, response.Game.CurrentNodeID)
	}
}

func Test対局APIで投了する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var gameState gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &gameState); err != nil {
		t.Fatal(err)
	}
	resigned := performJSON(t, handler, http.MethodPost, "/api/games/"+gameState.Game.ID+"/resign", `{}`)
	if resigned.Code != http.StatusOK {
		t.Fatalf("応答状態コード = %d、本文 = %s", resigned.Code, resigned.Body.String())
	}
	if err := json.Unmarshal(resigned.Body.Bytes(), &gameState); err != nil {
		t.Fatal(err)
	}
	if gameState.Game.Status != "finished" || gameState.Game.Result == nil || gameState.Game.Result.Winner != "W" || gameState.Game.Result.Loser != "B" {
		t.Fatalf("想定外の投了：%+v", gameState.Game)
	}
}

func Test局面移動で投了結果を復元する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	played := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"E5"}`)
	assertStatus(t, played, http.StatusOK)
	if err := json.Unmarshal(played.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	played = performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"D5"}`)
	assertStatus(t, played, http.StatusOK)
	if err := json.Unmarshal(played.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	promoted := performJSON(t, handler, http.MethodPost, path+"/nodes/"+state.Game.CurrentNodeID+"/main-line", ``)
	if promoted.Code != http.StatusOK {
		t.Fatalf("主分岐昇格状態コード = %d、本文 = %s", promoted.Code, promoted.Body.String())
	}
	resigned := performJSON(t, handler, http.MethodPost, path+"/resign", `{}`)
	if resigned.Code != http.StatusOK {
		t.Fatalf("投了状態コード = %d、本文 = %s", resigned.Code, resigned.Body.String())
	}
	if err := json.Unmarshal(resigned.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	resignedNodeID := state.Game.CurrentNodeID
	if state.Game.MainLineNodeID != resignedNodeID || len(state.Game.Moves) != 2 {
		t.Fatalf("投了が着手を持たない主分岐局面として記録されていない：%+v", state.Game)
	}
	resignationNodes := 0
	for _, node := range state.Game.TreeNodes {
		if node.Result != nil && node.Result.Type == "resignation" {
			resignationNodes++
			if node.Move != nil {
				t.Fatalf("投了局面に着手が含まれている：%+v", node)
			}
		}
	}
	if resignationNodes != 1 {
		t.Fatalf("投了局面数 = %d、棋譜ツリー = %+v", resignationNodes, state.Game.TreeNodes)
	}
	if response := performJSON(t, handler, http.MethodPost, path+"/nodes/root", ``); response.Code != http.StatusOK {
		t.Fatalf("開始局面への移動状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	restored := performJSON(t, handler, http.MethodPost, path+"/nodes/"+resignedNodeID, ``)
	if restored.Code != http.StatusOK {
		t.Fatalf("終局局面への移動状態コード = %d、本文 = %s", restored.Code, restored.Body.String())
	}
	if err := json.Unmarshal(restored.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Game.Status != "finished" || state.Game.Result == nil || state.Game.Result.Type != "resignation" || state.Game.Result.Winner != "W" || state.Game.Result.Loser != "B" {
		t.Fatalf("投了が復元されていない：%+v", state.Game)
	}
}

func Test対局APIがAIの応手を適用する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "D5"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":3}`)
	var gameState gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &gameState); err != nil {
		t.Fatal(err)
	}
	played := performJSON(t, handler, http.MethodPost, "/api/games/"+gameState.Game.ID+"/moves", `{"coordinate":"E5"}`)
	if played.Code != http.StatusOK {
		t.Fatalf("応答状態コード = %d、本文 = %s", played.Code, played.Body.String())
	}
	aiPlayed := performJSON(t, handler, http.MethodPost, "/api/games/"+gameState.Game.ID+"/ai-moves", `{}`)
	if aiPlayed.Code != http.StatusOK {
		t.Fatalf("AI着手状態コード = %d、本文 = %s", aiPlayed.Code, aiPlayed.Body.String())
	}
	var response aiMoveResponse
	if err := json.Unmarshal(aiPlayed.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.AIMove.Color != "W" || response.AIMove.Coordinate != "D5" {
		t.Fatalf("想定外のAI着手：%+v", response.AIMove)
	}
	if response.Game.Game.CurrentPlayer != "B" || len(response.Game.Game.Moves) != 2 {
		t.Fatalf("AI応手後の対局が想定外：%+v", response.Game)
	}
}

func Test対局APIはAI応答を延期および再開できる(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "D5"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":3,"humanColor":"W"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Game.Moves) != 0 || state.Game.CurrentPlayer != "B" {
		t.Fatalf("保留したAIの初手が着手された：%+v", state.Game)
	}
	played := performJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/ai-moves", `{}`)
	if played.Code != http.StatusOK {
		t.Fatalf("応答状態コード = %d、本文 = %s", played.Code, played.Body.String())
	}
	var response aiMoveResponse
	if err := json.Unmarshal(played.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.AIMove.Coordinate != "D5" || response.AIMove.Color != "B" || response.Game.Game.CurrentPlayer != "W" {
		t.Fatalf("再開後のAI着手が想定外：%+v", response)
	}
}

func Test延期中のAI応答を取り消しても人の着手を維持する(t *testing.T) {
	server := newTestServer(t, cancellingAnalyzer{})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	played := performJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/moves", `{"coordinate":"E5"}`)
	if played.Code != http.StatusOK {
		t.Fatal(played.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/games/"+state.Game.ID+"/ai-moves", strings.NewReader(`{}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != clientClosedRequestStatus || response.Body.Len() != 0 {
		t.Fatalf("応答状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	fetched := httptest.NewRecorder()
	handler.ServeHTTP(fetched, httptest.NewRequest(http.MethodGet, "/api/games/"+state.Game.ID, nil))
	assertStatus(t, fetched, http.StatusOK)
	if err := json.Unmarshal(fetched.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Game.Moves) != 1 || state.Game.Moves[0].Coordinate != "E5" || state.Game.CurrentPlayer != "W" {
		t.Fatalf("中止後に人の着手が保持されていない：%+v", state.Game)
	}
}

func TestAI思考中も対局を読み取れる(t *testing.T) {
	analyzer := blockingAnalyzer{started: make(chan struct{}), release: make(chan struct{})}
	handler := newTestServer(t, analyzer).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	played := performJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/moves", `{"coordinate":"E5"}`)
	if played.Code != http.StatusOK {
		t.Fatal(played.Body.String())
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/games/"+state.Game.ID+"/ai-moves", strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)
	}()
	waitForTestSignal(t, analyzer.started, "AI探索の開始")
	readFinished := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/games", nil))
		readFinished <- response.Code
	}()
	select {
	case status := <-readFinished:
		if status != http.StatusOK {
			t.Fatalf("一覧状態コード = %d", status)
		}
	case <-time.After(time.Second):
		t.Fatal("AI思考中に対局一覧の取得が待たされた")
	}
	close(analyzer.release)
	waitForTestSignal(t, finished, "AI要求の完了")
}

func Test対局APIが現在の局面を解析する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "D5"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":3}`)
	var gameState gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &gameState); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/games/"+gameState.Game.ID+"/analysis?visits=123", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("応答状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	var analysis katago.Summary
	if err := json.Unmarshal(response.Body.Bytes(), &analysis); err != nil {
		t.Fatal(err)
	}
	if analysis.Visits != 123 || len(analysis.Ownership) != 81 || analysis.Ownership[0] != 0.75 {
		t.Fatalf("想定外の解析結果：%+v", analysis)
	}
}

func Test待った後も取り消した手順を変化として保持する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "D5"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	if state.CanUndo {
		t.Fatal("着手前に待ったが許可されている")
	}
	played := performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`)
	if played.Code != http.StatusOK {
		t.Fatal(played.Body.String())
	}
	aiPlayed := performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`)
	if aiPlayed.Code != http.StatusOK {
		t.Fatalf("AI着手状態コード = %d、本文 = %s", aiPlayed.Code, aiPlayed.Body.String())
	}
	var aiState aiMoveResponse
	if err := json.Unmarshal(aiPlayed.Body.Bytes(), &aiState); err != nil {
		t.Fatal(err)
	}
	if !aiState.Game.CanUndo {
		t.Fatal("人とAIの着手後に待ったが許可されていない")
	}
	undone := performJSON(t, handler, http.MethodPost, path+"/undo", `{}`)
	if undone.Code != http.StatusOK {
		t.Fatalf("待った状態コード = %d、本文 = %s", undone.Code, undone.Body.String())
	}
	if err := json.Unmarshal(undone.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Game.Moves) != 0 || state.Game.CurrentPlayer != "B" || len(state.Game.TreeNodes) != 3 {
		t.Fatalf("待った後の対局が想定外：%+v", state.Game)
	}
	if state.CanUndo {
		t.Fatal("人の着手を取り消した後も待ったが許可されている")
	}
	var originalNode string
	for _, node := range state.Game.TreeNodes {
		if node.Move != nil && node.Move.Coordinate == "E5" {
			originalNode = node.ID
		}
	}
	navigated := performJSON(t, handler, http.MethodPost, path+"/nodes/"+originalNode, ``)
	if navigated.Code != http.StatusOK {
		t.Fatalf("局面移動状態コード = %d、本文 = %s", navigated.Code, navigated.Body.String())
	}
	if err := json.Unmarshal(navigated.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Game.Moves) != 1 || state.Game.Moves[0].Coordinate != "E5" {
		t.Fatalf("選択された局面が想定外：%+v", state.Game)
	}
	rejected := performJSON(t, handler, http.MethodPost, path+"/undo", `{}`)
	assertStatus(t, rejected, http.StatusOK)
	fetched := performJSON(t, handler, http.MethodGet, path, ``)
	assertStatus(t, fetched, http.StatusOK)
	if err := json.Unmarshal(fetched.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Game.CurrentNodeID != "root" || state.Game.MainLineNodeID != "root" {
		t.Fatalf("表示していた人の着手を待ったで取り消していない：%+v", state.Game)
	}
}

func Test現在の変化から対局を進めて主分岐にする(t *testing.T) {
	handler := newTestServer(t, moveNumberAnalyzer{}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/nodes/n1", ``), http.StatusOK)
	variation := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"D4"}`)
	assertStatus(t, variation, http.StatusOK)

	played := performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"C3"}`)
	assertStatus(t, played, http.StatusOK)
	var playedState playMoveResponse
	if err := json.Unmarshal(played.Body.Bytes(), &playedState); err != nil {
		t.Fatal(err)
	}
	if playedState.Game.Game.CurrentNodeID != playedState.Game.Game.MainLineNodeID || len(playedState.Game.Game.Moves) != 3 || mainLineMoveCount(playedState.Game.Game) != 3 {
		t.Fatalf("変化から着手した後の状態 = %+v", playedState.Game.Game)
	}

	secondAI := performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`)
	assertStatus(t, secondAI, http.StatusOK)
	var aiState aiMoveResponse
	if err := json.Unmarshal(secondAI.Body.Bytes(), &aiState); err != nil {
		t.Fatal(err)
	}
	if aiState.Game.Game.CurrentNodeID != aiState.Game.Game.MainLineNodeID || mainLineMoveCount(aiState.Game.Game) != 4 {
		t.Fatalf("変化からAIが着手した後の状態 = %+v", aiState.Game.Game)
	}

	resigned := performJSON(t, handler, http.MethodPost, path+"/resign", `{}`)
	assertStatus(t, resigned, http.StatusOK)
	if err := json.Unmarshal(resigned.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Game.CurrentNodeID != state.Game.MainLineNodeID || state.Game.Result == nil || mainLineResult(state.Game) == nil {
		t.Fatalf("変化から投了した後の状態 = %+v", state.Game)
	}

	undone := performJSON(t, handler, http.MethodPost, path+"/undo", `{}`)
	assertStatus(t, undone, http.StatusOK)
	if err := json.Unmarshal(undone.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Game.CurrentNodeID != state.Game.MainLineNodeID || mainLineResult(state.Game) != nil || mainLineMoveCount(state.Game) != 4 {
		t.Fatalf("変化から待った後の状態 = %+v", state.Game)
	}
}

func Test人が白の場合の待ったは人の着手まで戻す(t *testing.T) {
	handler := newTestServer(t, moveNumberAnalyzer{}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"W"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	if aiPlayed := performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`); aiPlayed.Code != http.StatusOK {
		t.Fatalf("AI初手状態コード = %d、本文 = %s", aiPlayed.Code, aiPlayed.Body.String())
	}
	fetchedInitial := performJSON(t, handler, http.MethodGet, path, ``)
	assertStatus(t, fetchedInitial, http.StatusOK)
	if err := json.Unmarshal(fetchedInitial.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Game.Moves) != 1 || state.Game.Moves[0].Color != "B" {
		t.Fatalf("初期局面が想定外：%+v", state.Game)
	}

	rejected := performJSON(t, handler, http.MethodPost, path+"/undo", `{}`)
	if rejected.Code != http.StatusConflict {
		t.Fatalf("待った状態コード = %d、本文 = %s", rejected.Code, rejected.Body.String())
	}
	fetched := performJSON(t, handler, http.MethodGet, path, ``)
	assertStatus(t, fetched, http.StatusOK)
	if err := json.Unmarshal(fetched.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Game.Moves) != 1 || state.Game.Moves[0].Coordinate != "D5" {
		t.Fatalf("拒否された待ったで局面が変化した：%+v", state.Game)
	}

	played := performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`)
	if played.Code != http.StatusOK {
		t.Fatalf("着手状態コード = %d、本文 = %s", played.Code, played.Body.String())
	}
	if aiPlayed := performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`); aiPlayed.Code != http.StatusOK {
		t.Fatalf("AI着手状態コード = %d、本文 = %s", aiPlayed.Code, aiPlayed.Body.String())
	}
	undone := performJSON(t, handler, http.MethodPost, path+"/undo", `{}`)
	if undone.Code != http.StatusOK {
		t.Fatalf("待った状態コード = %d、本文 = %s", undone.Code, undone.Body.String())
	}
	if err := json.Unmarshal(undone.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Game.Moves) != 1 || state.Game.Moves[0].Coordinate != "D5" || state.Game.CurrentPlayer != "W" {
		t.Fatalf("待ったでAIの初手へ戻っていない：%+v", state.Game)
	}
}

func Test対局APIの待ったで対局の終局を取り消す(t *testing.T) {
	for _, test := range []struct {
		name   string
		finish func(t *testing.T, handler http.Handler, path string)
	}{
		{name: "投了", finish: func(t *testing.T, handler http.Handler, path string) {
			response := performJSON(t, handler, http.MethodPost, path+"/resign", `{}`)
			if response.Code != http.StatusOK {
				t.Fatalf("投了状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
		}},
		{name: "二連続パス", finish: func(t *testing.T, handler http.Handler, path string) {
			if response := performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"pass"}`); response.Code != http.StatusOK {
				t.Fatalf("人のパス 状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
			if response := performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`); response.Code != http.StatusOK {
				t.Fatalf("AIのパス 状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
			created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
			var state gameResponse
			if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			path := "/api/games/" + state.Game.ID
			test.finish(t, handler, path)
			undone := performJSON(t, handler, http.MethodPost, path+"/undo", `{}`)
			if undone.Code != http.StatusOK {
				t.Fatalf("待った状態コード = %d、本文 = %s", undone.Code, undone.Body.String())
			}
			if err := json.Unmarshal(undone.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if state.Game.Status != "playing" || state.Game.Result != nil {
				t.Fatalf("終局を取り消せていない：%+v", state.Game)
			}
		})
	}
}

func Test死石を除いた二連続パス結果を待った後も作れる(t *testing.T) {
	server := newTestServer(t, deadStoneAnalyzer{move: "pass", coordinate: "C3"})
	handler := server.Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader("(;FF[4]CA[UTF-8]GM[1]SZ[5]RU[Chinese]KM[0.5];B[cc];W[])"))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	importedResponse := httptest.NewRecorder()
	handler.ServeHTTP(importedResponse, request)
	assertStatus(t, importedResponse, http.StatusCreated)
	var imported gameResponse
	if err := json.Unmarshal(importedResponse.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + imported.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPatch, path+"/settings", `{"aiLevel":1,"humanColor":"B"}`), http.StatusOK)

	finish := func() gameResponse {
		played := performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"pass"}`)
		assertStatus(t, played, http.StatusOK)
		var response playMoveResponse
		if err := json.Unmarshal(played.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		result := response.Game.Game.Result
		if result == nil || result.Winner != "W" || result.Margin != 0.5 || !reflect.DeepEqual(result.DeadStones, []string{"C3"}) {
			t.Fatalf("死石を除いた結果 = %+v", result)
		}
		return response.Game
	}

	first := finish()
	undone := performJSON(t, handler, http.MethodPost, path+"/undo", `{}`)
	assertStatus(t, undone, http.StatusOK)
	second := finish()
	if !reflect.DeepEqual(first.Game.Result, second.Game.Result) {
		t.Fatalf("打ち直し前後の結果：最初 = %+v、打ち直し後 = %+v", first.Game.Result, second.Game.Result)
	}
	exported := performJSON(t, handler, http.MethodGet, path+"/sgf", "")
	assertStatus(t, exported, http.StatusOK)
	reimportRequest := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader(exported.Body.String()))
	reimportRequest.Header.Set("Content-Type", "application/x-go-sgf")
	reimportedResponse := httptest.NewRecorder()
	handler.ServeHTTP(reimportedResponse, reimportRequest)
	assertStatus(t, reimportedResponse, http.StatusCreated)
	var reimported gameResponse
	if err := json.Unmarshal(reimportedResponse.Body.Bytes(), &reimported); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second.Game.Result, reimported.Game.Result) {
		t.Fatalf("往復前後の結果：書き出し前 = %+v、再読み込み後 = %+v", second.Game.Result, reimported.Game.Result)
	}
}

func Test人の二回目のパスで終局した後に待ったができる(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`), http.StatusOK)
	finished := performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"pass"}`)
	assertStatus(t, finished, http.StatusOK)
	var played playMoveResponse
	if err := json.Unmarshal(finished.Body.Bytes(), &played); err != nil {
		t.Fatal(err)
	}
	state = played.Game
	if state.Game.Status != "finished" || state.Game.Result == nil || len(state.Game.Moves) != 3 {
		t.Fatalf("人の二回目のパスで終局していない：%+v", state.Game)
	}
	undone := performJSON(t, handler, http.MethodPost, path+"/undo", `{}`)
	assertStatus(t, undone, http.StatusOK)
	if err := json.Unmarshal(undone.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Game.Status != "playing" || state.Game.Result != nil || state.Game.CurrentPlayer != "B" || len(state.Game.Moves) != 2 || state.Game.Moves[1].Coordinate != "pass" {
		t.Fatalf("人が終局させたパスだけを待ったできていない：%+v", state.Game)
	}
}

func Test対局APIが入力原因に合う状態コードを返す(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	for _, coordinate := range []string{"invalid", "A01", "A+1"} {
		invalid := performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"`+coordinate+`"}`)
		if invalid.Code != http.StatusBadRequest {
			t.Fatalf("不正な座標%sの状態コード = %d、本文 = %s", coordinate, invalid.Code, invalid.Body.String())
		}
	}
	missing := performJSON(t, handler, http.MethodPost, "/api/games/missing/undo", `{`)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("存在しない対局の状態コード = %d、本文 = %s", missing.Code, missing.Body.String())
	}
}

func Test対局APIがサイズ超過の読み込みを拒否する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	for _, test := range []struct {
		name, path, body, contentType string
	}{
		{name: "SGF", path: "/api/games/import", body: strings.Repeat("x", (10<<20)+1), contentType: "application/x-go-sgf"},
		{name: "JSON", path: "/api/games/import-json", body: `{"schemaVersion":"` + strings.Repeat("x", 10<<20) + `"}`, contentType: "application/json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("応答状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
		})
	}
}

func Test対局APIが変化の部分木を削除する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	first := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"E5"}`)
	assertStatus(t, first, http.StatusOK)
	if err := json.Unmarshal(first.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	variationRoot := state.Game.CurrentNodeID
	second := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"D5"}`)
	if second.Code != http.StatusOK {
		t.Fatalf("2手目の変化状態コード = %d、本文 = %s", second.Code, second.Body.String())
	}
	var secondState gameResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondState); err != nil {
		t.Fatal(err)
	}

	deleted := performJSON(t, handler, http.MethodDelete, path+"/nodes/"+variationRoot, expectedNodeIDsBody(t, variationRoot, secondState.Game.CurrentNodeID))
	if deleted.Code != http.StatusOK {
		t.Fatalf("削除状態コード = %d、本文 = %s", deleted.Code, deleted.Body.String())
	}
	if err := json.Unmarshal(deleted.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Game.CurrentNodeID != "root" || len(state.Game.TreeNodes) != 1 {
		t.Fatalf("削除後も変化の部分木が残っている：%+v", state.Game)
	}
	mainLineDelete := performJSON(t, handler, http.MethodDelete, path+"/nodes/root", expectedNodeIDsBody(t, "root"))
	if mainLineDelete.Code != http.StatusBadRequest {
		t.Fatalf("開始局面削除状態コード = %d、本文 = %s", mainLineDelete.Code, mainLineDelete.Body.String())
	}
}

func Test対局APIが変化を主分岐へ昇格する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	variation := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"E5"}`)
	assertStatus(t, variation, http.StatusOK)
	if err := json.Unmarshal(variation.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	promotedNodeID := state.Game.CurrentNodeID
	promoted := performJSON(t, handler, http.MethodPost, path+"/nodes/"+promotedNodeID+"/main-line", ``)
	if promoted.Code != http.StatusOK {
		t.Fatalf("主分岐昇格状態コード = %d、本文 = %s", promoted.Code, promoted.Body.String())
	}
	if err := json.Unmarshal(promoted.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Game.MainLineNodeID != promotedNodeID {
		t.Fatalf("主分岐の末端 = %s、期待値 = %s", state.Game.MainLineNodeID, promotedNodeID)
	}
}

func Test対局APIが昇格できない局面を状態に合う理由で拒否する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var before gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + before.Game.ID
	nonexistentGame := performJSON(t, handler, http.MethodPost, "/api/games/missing/nodes/root/main-line", ``)
	assertStatus(t, nonexistentGame, http.StatusNotFound)

	missing := performJSON(t, handler, http.MethodPost, path+"/nodes/missing/main-line", ``)
	assertStatus(t, missing, http.StatusNotFound)

	root := performJSON(t, handler, http.MethodPost, path+"/nodes/root/main-line", ``)
	assertStatus(t, root, http.StatusConflict)

	move := performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`)
	assertStatus(t, move, http.StatusOK)
	var moved playMoveResponse
	if err := json.Unmarshal(move.Body.Bytes(), &moved); err != nil {
		t.Fatal(err)
	}
	mainLine := performJSON(t, handler, http.MethodPost, path+"/nodes/"+moved.Game.Game.CurrentNodeID+"/main-line", ``)
	assertStatus(t, mainLine, http.StatusConflict)

	afterResponse := performJSON(t, handler, http.MethodGet, path, ``)
	assertStatus(t, afterResponse, http.StatusOK)
	var after gameResponse
	if err := json.Unmarshal(afterResponse.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.Game.CurrentNodeID != moved.Game.Game.CurrentNodeID || after.Game.MainLineNodeID != moved.Game.Game.MainLineNodeID || len(after.Game.TreeNodes) != 2 {
		t.Fatalf("拒否後に対局状態が変わった：%+v", after.Game)
	}
}

func Test対局APIで局面のメモを更新する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	updated := performJSON(t, handler, http.MethodPatch, "/api/games/"+state.Game.ID+"/nodes/root", `{"note":"要確認","important":true,"expectedNote":"","expectedImportant":false}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("応答状態コード = %d、本文 = %s", updated.Code, updated.Body.String())
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	foundNote := false
	for _, property := range state.Game.RootProperties {
		foundNote = foundNote || property.Ident == "C" && len(property.Values) == 1 && property.Values[0] == "要確認"
	}
	if !foundNote {
		t.Fatalf("開始局面のメモが保存されていない：%+v", state.Game)
	}
	var root game.TreeNode
	for _, node := range state.Game.TreeNodes {
		if node.ID == "root" {
			root = node
		}
	}
	if !root.Important {
		t.Fatalf("開始局面の重要な局面が保存されていない：%+v", root)
	}
}

func Test対局APIが変更前の値と食い違う局面メモを拒否する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID + "/nodes/root"
	first := performJSON(t, handler, http.MethodPatch, path, `{"note":"先の更新","important":true,"expectedNote":"","expectedImportant":false}`)
	if first.Code != http.StatusOK {
		t.Fatal(first.Body.String())
	}
	stale := performJSON(t, handler, http.MethodPatch, path, `{"note":"後の更新","important":false,"expectedNote":"","expectedImportant":false}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("競合状態コード = %d、本文 = %s", stale.Code, stale.Body.String())
	}
	fetched := performJSON(t, handler, http.MethodGet, "/api/games/"+state.Game.ID, ``)
	assertStatus(t, fetched, http.StatusOK)
	if !strings.Contains(fetched.Body.String(), "先の更新") || strings.Contains(fetched.Body.String(), "後の更新") {
		t.Fatalf("競合後の内容 = %s", fetched.Body.String())
	}
}

func Test対局APIがAIの応手なしで変化を追加する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "D5"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	response := performJSON(t, handler, http.MethodPost, "/api/games/"+state.Game.ID+"/variations", `{"coordinate":"E5"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("応答状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Game.Moves) != 1 || state.Game.Moves[0].Color != "B" || state.Game.CurrentPlayer != "W" {
		t.Fatalf("変化にAI応手が誤って含まれている：%+v", state.Game)
	}
}

func Test対局APIが人とAIのパス後に点数を計算する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"komi":7.5,"aiLevel":1}`)
	var gameState gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &gameState); err != nil {
		t.Fatal(err)
	}

	played := performJSON(t, handler, http.MethodPost, "/api/games/"+gameState.Game.ID+"/moves", `{"coordinate":"pass"}`)
	if played.Code != http.StatusOK {
		t.Fatalf("応答状態コード = %d、本文 = %s", played.Code, played.Body.String())
	}
	aiPlayed := performJSON(t, handler, http.MethodPost, "/api/games/"+gameState.Game.ID+"/ai-moves", `{}`)
	if aiPlayed.Code != http.StatusOK {
		t.Fatalf("AI着手状態コード = %d、本文 = %s", aiPlayed.Code, aiPlayed.Body.String())
	}
	var response aiMoveResponse
	if err := json.Unmarshal(aiPlayed.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	result := response.Game.Game.Result
	if response.Game.Game.Status != "finished" || result == nil || result.Type != "points" || result.Winner != "W" || result.Margin != 7.5 {
		t.Fatalf("得点計算後の対局が想定外：%+v", response.Game.Game)
	}
}

func Test対局APIが人の着手と不正なAI着手を区別する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "E5"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var gameState gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &gameState); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + gameState.Game.ID
	played := performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`)
	if played.Code != http.StatusOK {
		t.Fatalf("人の着手状態コード = %d、本文 = %s", played.Code, played.Body.String())
	}
	aiPlayed := performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`)
	if aiPlayed.Code != http.StatusBadGateway {
		t.Fatalf("AIの着手状態コード = %d、本文 = %s", aiPlayed.Code, aiPlayed.Body.String())
	}
	fetched := httptest.NewRecorder()
	handler.ServeHTTP(fetched, httptest.NewRequest(http.MethodGet, path, nil))
	assertStatus(t, fetched, http.StatusOK)
	if err := json.Unmarshal(fetched.Body.Bytes(), &gameState); err != nil {
		t.Fatal(err)
	}
	if len(gameState.Game.Moves) != 1 || gameState.Game.Moves[0].Coordinate != "E5" || gameState.Game.CurrentPlayer != "W" {
		t.Fatalf("失敗したAI着手で人の着手が変化した：%+v", gameState.Game)
	}
}

func Test対局APIが手番を変えずに不正な着手を報告する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":5,"aiLevel":1}`)
	var response gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + response.Game.ID + "/moves"
	if result := performJSON(t, handler, http.MethodPost, path, `{"coordinate":"C3"}`); result.Code != http.StatusOK {
		t.Fatal(result.Body.String())
	}
	conflict := performJSON(t, handler, http.MethodPost, path, `{"coordinate":"C3"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("占有点への着手状態コード = %d", conflict.Code)
	}
}

func Test対局を進める操作は実際の手番と一致するときだけ受け付ける(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()

	black := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var blackState gameResponse
	if err := json.Unmarshal(black.Body.Bytes(), &blackState); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + blackState.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`), http.StatusConflict)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"D4"}`), http.StatusConflict)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`), http.StatusOK)

	white := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"W"}`)
	var whiteState gameResponse
	if err := json.Unmarshal(white.Body.Bytes(), &whiteState); err != nil {
		t.Fatal(err)
	}
	whitePath := "/api/games/" + whiteState.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, whitePath+"/moves", `{"coordinate":"E5"}`), http.StatusConflict)
	assertStatus(t, performJSON(t, handler, http.MethodPost, whitePath+"/ai-moves", `{}`), http.StatusOK)
}

func Test解析器の制限時間超過を504として返す(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass", err: context.DeadlineExceeded})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodGet, path+"/analysis?visits=1", ``), http.StatusGatewayTimeout)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`), http.StatusGatewayTimeout)
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`), http.StatusNoContent)
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/analyze", `{"contextId":"`+state.Game.ID+`:root","sequence":[],"maxVisits":1}`), http.StatusGatewayTimeout)
}

func Test解析とAIの着手は30秒の制限時間を設定する(t *testing.T) {
	server := newTestServer(t, deadlineAnalyzer{})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodGet, path+"/analysis?visits=1", ``), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`), http.StatusOK)
}

func Test表示中の局面を通知し直すまでAIagentは移動できない(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	gameID := state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/navigation", `{"contextId":"`+gameID+`:root","nodeId":"root"}`), http.StatusConflict)
	variation := performJSON(t, handler, http.MethodPost, "/api/games/"+gameID+"/variations", `{"coordinate":"D4"}`)
	assertStatus(t, variation, http.StatusOK)
	if err := json.Unmarshal(variation.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	variationID := state.Game.CurrentNodeID
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+gameID+`","nodeId":"`+variationID+`"}`), http.StatusNoContent)
	assertStatus(t, performJSON(t, handler, http.MethodDelete, "/api/games/"+gameID+"/nodes/"+variationID, `{"expectedNodeIds":["`+variationID+`"]}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/navigation", `{"contextId":"`+gameID+`:`+variationID+`","nodeId":"root"}`), http.StatusConflict)
}

func TestAIagentは画面で開いていない対局へ移動できない(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	first := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	second := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var firstState, secondState gameResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstState); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondState); err != nil {
		t.Fatal(err)
	}
	variation := performJSON(t, handler, http.MethodPost, "/api/games/"+firstState.Game.ID+"/variations", `{"coordinate":"D4"}`)
	assertStatus(t, variation, http.StatusOK)
	if err := json.Unmarshal(variation.Body.Bytes(), &firstState); err != nil {
		t.Fatal(err)
	}
	variationID := firstState.Game.CurrentNodeID
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/games/"+firstState.Game.ID+"/nodes/root", `{}`), http.StatusOK)
	notifyAgentSelectionForTest(t, handler, secondState.Game.ID, "root")
	navigation := performJSON(t, handler, http.MethodPost, "/api/agent/navigation", `{"contextId":"`+firstState.Game.ID+`:root","nodeId":"`+variationID+`"}`)
	assertStatus(t, navigation, http.StatusConflict)
	selected := performJSON(t, handler, http.MethodGet, "/api/agent/context", ``)
	assertStatus(t, selected, http.StatusOK)
	if !bytes.Contains(selected.Body.Bytes(), []byte(`"contextId":"`+secondState.Game.ID+`:root"`)) {
		t.Fatalf("拒否後の表示中の局面 = %s", selected.Body.String())
	}
	loaded := performJSON(t, handler, http.MethodGet, "/api/games/"+firstState.Game.ID, ``)
	assertStatus(t, loaded, http.StatusOK)
	if err := json.Unmarshal(loaded.Body.Bytes(), &firstState); err != nil {
		t.Fatal(err)
	}
	if firstState.Game.CurrentNodeID != "root" {
		t.Fatalf("拒否後の別の対局の現在の局面 = %s", firstState.Game.CurrentNodeID)
	}
	afterRejection := performJSON(t, handler, http.MethodPost, "/api/games/"+firstState.Game.ID+"/variations", `{"coordinate":"E5"}`)
	assertStatus(t, afterRejection, http.StatusOK)
	if err := json.Unmarshal(afterRejection.Body.Bytes(), &firstState); err != nil {
		t.Fatal(err)
	}
	for _, node := range firstState.Game.TreeNodes {
		if node.ID == firstState.Game.CurrentNodeID && node.ParentID != "root" {
			t.Fatalf("拒否後に作った変化の親 = %s、期待値 = root", node.ParentID)
		}
	}
}

func Test永続サーバーは再起動後に対局を復元する(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"komi":7.5,"aiLevel":3}`)
	var initial gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	played := performJSON(t, server.Handler(), http.MethodPost, "/api/games/"+initial.Game.ID+"/moves", `{"coordinate":"E5"}`)
	if played.Code != http.StatusOK {
		t.Fatalf("着手状態コード = %d、本文 = %s", played.Code, played.Body.String())
	}
	var playedState playMoveResponse
	if err := json.Unmarshal(played.Body.Bytes(), &playedState); err != nil {
		t.Fatal(err)
	}
	aiPlayed := performJSON(t, server.Handler(), http.MethodPost, "/api/games/"+initial.Game.ID+"/ai-moves", `{}`)
	if aiPlayed.Code != http.StatusOK {
		t.Fatalf("AI着手状態コード = %d、本文 = %s", aiPlayed.Code, aiPlayed.Body.String())
	}
	var aiState aiMoveResponse
	if err := json.Unmarshal(aiPlayed.Body.Bytes(), &aiState); err != nil {
		t.Fatal(err)
	}
	playedState.Game = aiState.Game
	reviewed := performJSON(t, server.Handler(), http.MethodPatch, "/api/games/"+initial.Game.ID+"/nodes/"+playedState.Game.Game.CurrentNodeID, `{"note":"復元するメモ","important":true,"expectedNote":"","expectedImportant":false}`)
	if reviewed.Code != http.StatusOK {
		t.Fatalf("検討情報保存状態コード = %d、本文 = %s", reviewed.Code, reviewed.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, initial.Game.ID+".sgf")); !os.IsNotExist(err) {
		t.Fatalf("永続化用ではないSGFが保存されている：%v", err)
	}
	savedData, err := os.ReadFile(filepath.Join(dataDir, initial.Game.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(savedData, []byte(`"game"`)) || !bytes.Contains(savedData, []byte(`"schemaVersion": "1"`)) {
		t.Fatalf("想定外の保存内容：%s", savedData)
	}

	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "D5"})
	if err != nil {
		t.Fatal(err)
	}
	fetched := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(fetched, httptest.NewRequest(http.MethodGet, "/api/games/"+initial.Game.ID, nil))
	if fetched.Code != http.StatusOK {
		t.Fatalf("取得状態コード = %d、本文 = %s", fetched.Code, fetched.Body.String())
	}
	var restored gameResponse
	if err := json.Unmarshal(fetched.Body.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if value(restored.AILevel) != 3 || len(restored.Game.Moves) != 2 || restored.Game.Stones["E5"] != "B" || restored.Game.Stones["D5"] != "W" {
		t.Fatalf("復元した対局が想定外：%+v", restored)
	}
	current := restored.Game.TreeNodes[0]
	for _, node := range restored.Game.TreeNodes {
		if node.ID == restored.Game.CurrentNodeID {
			current = node
			break
		}
	}
	if !current.Important || testNodeComment(restored.Game, current.ID) != "復元するメモ" {
		t.Fatalf("検討情報が復元されていない：%+v", current)
	}
}

func Test再起動後に読み込んだ投了対局を復元する(t *testing.T) {
	for _, source := range []string{
		"(;FF[4]SZ[9]RE[W+R])",
		"(;FF[4]SZ[9]RE[B+R];B[ee])",
		"(;FF[4]SZ[9]RE[W+R];B[ee];W[dd])",
	} {
		t.Run(source, func(t *testing.T) {
			dataDir := t.TempDir()
			server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader(source))
			request.Header.Set("Content-Type", "application/x-go-sgf")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusCreated {
				t.Fatalf("SGF読み込み状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
			var imported gameResponse
			if err := json.Unmarshal(response.Body.Bytes(), &imported); err != nil {
				t.Fatal(err)
			}
			path := "/api/games/" + imported.Game.ID
			before := performJSON(t, server.Handler(), http.MethodGet, path+"/sgf", ``)
			assertStatus(t, before, http.StatusOK)
			restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
			if err != nil {
				t.Fatalf("再起動時に投了棋譜を復元できない：%v", err)
			}
			restored := performJSON(t, restarted.Handler(), http.MethodGet, path, ``)
			assertStatus(t, restored, http.StatusOK)
			var restoredState gameResponse
			if err := json.Unmarshal(restored.Body.Bytes(), &restoredState); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(restoredState.Game, imported.Game) {
				t.Fatalf("再起動後の投了棋譜が一致しない\n再起動前=%+v\n再起動後=%+v", imported.Game, restoredState.Game)
			}
			after := performJSON(t, restarted.Handler(), http.MethodGet, path+"/sgf", ``)
			assertStatus(t, after, http.StatusOK)
			if after.Body.String() != before.Body.String() {
				t.Fatalf("再起動後に投了SGFが変化した\n再起動前=%s\n再起動後=%s", before.Body.String(), after.Body.String())
			}
		})
	}
}

func Test永続サーバーが対局と保存ファイルを削除する(t *testing.T) {
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
	if _, err := os.Stat(filepath.Join(dataDir, state.Game.ID+".json")); err != nil {
		t.Fatalf("保存ファイルがない：%v", err)
	}

	deleted := performJSON(t, server.Handler(), http.MethodDelete, "/api/games/"+state.Game.ID, ``)
	if deleted.Code != http.StatusOK {
		t.Fatalf("削除状態コード = %d、本文 = %s", deleted.Code, deleted.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, state.Game.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("削除後も保存ファイルが残っている：%v", err)
	}
	fetched := httptest.NewRecorder()
	server.Handler().ServeHTTP(fetched, httptest.NewRequest(http.MethodGet, "/api/games/"+state.Game.ID, nil))
	if fetched.Code != http.StatusNotFound {
		t.Fatalf("削除した対局の取得状態コード = %d、本文 = %s", fetched.Code, fetched.Body.String())
	}
}

func Test永続サーバーは変化とそのメモを復元する(t *testing.T) {
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
	first := performJSON(t, server.Handler(), http.MethodPost, path+"/variations", `{"coordinate":"E5"}`)
	assertStatus(t, first, http.StatusOK)
	if err := json.Unmarshal(first.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	firstNodeID := state.Game.CurrentNodeID
	reviewed := performJSON(t, server.Handler(), http.MethodPatch, path+"/nodes/"+firstNodeID, `{"note":"第一optionのメモ","important":true,"expectedNote":"","expectedImportant":false}`)
	if reviewed.Code != http.StatusOK {
		t.Fatal(reviewed.Body.String())
	}
	if response := performJSON(t, server.Handler(), http.MethodPost, path+"/nodes/root", ``); response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	second := performJSON(t, server.Handler(), http.MethodPost, path+"/variations", `{"coordinate":"D5"}`)
	if second.Code != http.StatusOK {
		t.Fatal(second.Body.String())
	}

	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	fetched := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(fetched, httptest.NewRequest(http.MethodGet, path, nil))
	assertStatus(t, fetched, http.StatusOK)
	if err := json.Unmarshal(fetched.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	coordinates := make(map[string]game.TreeNode)
	for _, node := range state.Game.TreeNodes {
		if node.Move != nil {
			coordinates[node.Move.Coordinate] = node
		}
	}
	if len(coordinates) != 2 || coordinates["E5"].ParentID != "root" || coordinates["D5"].ParentID != "root" {
		t.Fatalf("復元した変化 = %+v", state.Game.TreeNodes)
	}
	firstNode := coordinates["E5"]
	if !firstNode.Important || testNodeComment(state.Game, firstNode.ID) != "第一optionのメモ" {
		t.Fatalf("復元した変化のメモ = %+v", firstNode)
	}
	if current := coordinates["D5"]; state.Game.CurrentNodeID != current.ID {
		t.Fatalf("現在局面 = %s、期待値 = %s", state.Game.CurrentNodeID, current.ID)
	}
}

func TestAIの決定は着手局面に保存される(t *testing.T) {
	dataDir := t.TempDir()
	analyzer := detailedFixedAnalyzer{fixedAnalyzer{move: "D4"}}
	server, err := NewPersistentServer(dataDir, analyzer)
	if err != nil {
		t.Fatal(err)
	}
	// C3を置石で塞ぎ、解析の第2候補であるD4だけを打てる候補にする。
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"handicap":2,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	played := performJSON(t, server.Handler(), http.MethodPost, "/api/games/"+state.Game.ID+"/ai-moves", `{}`)
	if played.Code != http.StatusOK {
		t.Fatalf("AI着手状態コード = %d、本文 = %s", played.Code, played.Body.String())
	}
	var playedState aiMoveResponse
	if err := json.Unmarshal(played.Body.Bytes(), &playedState); err != nil {
		t.Fatal(err)
	}
	state = playedState.Game
	var decision *game.AIDecision
	for _, node := range state.Game.TreeNodes {
		if node.AIDecision != nil {
			decision = node.AIDecision
		}
	}
	if decision == nil || decision.EngineMove != "C3" || decision.SelectedMove != "D4" || decision.Visits != 50 {
		t.Fatalf("AIの選択情報 = %#v", decision)
	}
	restarted, err := NewPersistentServer(dataDir, analyzer)
	if err != nil {
		t.Fatal(err)
	}
	restored := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(restored, httptest.NewRequest(http.MethodGet, "/api/games/"+state.Game.ID, nil))
	assertStatus(t, restored, http.StatusOK)
	if !bytes.Contains(restored.Body.Bytes(), []byte(`"engineMove":"C3"`)) {
		t.Fatalf("復元した対局にAIの選択情報がない：%s", restored.Body.String())
	}
}

func Test解析キャッシュを復元し解析版が変われば無効にする(t *testing.T) {
	dataDir := t.TempDir()
	firstCalls := 0
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{
		move: "D5", analysisCalls: &firstCalls, analysisVersion: "model-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":2}`)
	var gameState gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &gameState); err != nil {
		t.Fatal(err)
	}
	analysisPath := "/api/games/" + gameState.Game.ID + "/analysis?visits=123"
	if response := performJSON(t, server.Handler(), http.MethodGet, analysisPath, ""); response.Code != http.StatusOK {
		t.Fatalf("初回解析状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	if response := performJSON(t, server.Handler(), http.MethodGet, analysisPath, ""); response.Code != http.StatusOK {
		t.Fatalf("キャッシュ利用時の解析状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	if firstCalls != 1 {
		t.Fatalf("再起動前の解析回数 = %d、期待値 = 1", firstCalls)
	}

	restoredCalls := 0
	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{
		move: "D5", analysisCalls: &restoredCalls, analysisVersion: "model-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response := performJSON(t, restarted.Handler(), http.MethodGet, analysisPath, ""); response.Code != http.StatusOK {
		t.Fatalf("キャッシュ復元後の解析状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	if restoredCalls != 0 {
		t.Fatalf("キャッシュ復元後の解析回数 = %d、期待値 = 0", restoredCalls)
	}

	changedCalls := 0
	changed, err := NewPersistentServer(dataDir, fixedAnalyzer{
		move: "D5", analysisCalls: &changedCalls, analysisVersion: "model-v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response := performJSON(t, changed.Handler(), http.MethodGet, analysisPath, ""); response.Code != http.StatusOK {
		t.Fatalf("無効化後のキャッシュの状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	if changedCalls != 1 {
		t.Fatalf("解析版の変更後の解析回数 = %d、期待値 = 1", changedCalls)
	}
	saved, err := os.ReadFile(filepath.Join(dataDir, gameState.Game.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(saved, []byte("model-v1")) || !bytes.Contains(saved, []byte("model-v2")) {
		t.Fatalf("解析版を変更した後の保存内容に古い解析結果が残っている：%s", saved)
	}
}

func Test変化を削除しても残る局面の解析を再利用する(t *testing.T) {
	analysisCalls := 0
	server := newTestServer(t, fixedAnalyzer{move: "pass", analysisCalls: &analysisCalls})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	assertStatus(t, created, http.StatusCreated)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodGet, path+"/analysis?nodeId=root&visits=100", ``), http.StatusOK)
	added := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"E5"}`)
	assertStatus(t, added, http.StatusOK)
	if err := json.Unmarshal(added.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	variationID := state.Game.CurrentNodeID
	deleted := performJSON(t, handler, http.MethodDelete, path+"/nodes/"+variationID, `{"expectedNodeIds":["`+variationID+`"]}`)
	assertStatus(t, deleted, http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodGet, path+"/analysis?nodeId=root&visits=100", ``), http.StatusOK)
	if analysisCalls != 1 {
		t.Fatalf("残る開始局面の解析回数 = %d、期待値 = 1", analysisCalls)
	}
}

func Test解析では対局を移動せずに棋譜ツリーの局面を対象にできる(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "D5"})
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
	var initial gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	played := performJSON(t, server.Handler(), http.MethodPost, "/api/games/"+initial.Game.ID+"/moves", `{"coordinate":"E5"}`)
	assertStatus(t, played, http.StatusOK)
	var response playMoveResponse
	if err := json.Unmarshal(played.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	var humanNodeID string
	for _, node := range response.Game.Game.TreeNodes {
		if node.Move != nil && node.Move.Coordinate == "E5" {
			humanNodeID = node.ID
		}
	}
	analysisResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(analysisResponse, httptest.NewRequest(http.MethodGet, "/api/games/"+initial.Game.ID+"/analysis?nodeId="+humanNodeID, nil))
	assertStatus(t, analysisResponse, http.StatusOK)
	var analysis katago.Summary
	if err := json.Unmarshal(analysisResponse.Body.Bytes(), &analysis); err != nil {
		t.Fatal(err)
	}
	if analysis.CurrentPlayer != "W" {
		t.Fatalf("対象局面解析の手番 = %s、期待値 = W", analysis.CurrentPlayer)
	}
	fetched := httptest.NewRecorder()
	server.Handler().ServeHTTP(fetched, httptest.NewRequest(http.MethodGet, "/api/games/"+initial.Game.ID, nil))
	assertStatus(t, fetched, http.StatusOK)
	var unchanged gameResponse
	if err := json.Unmarshal(fetched.Body.Bytes(), &unchanged); err != nil {
		t.Fatal(err)
	}
	if unchanged.Game.CurrentNodeID != response.Game.Game.CurrentNodeID {
		t.Fatalf("解析によって対局が%sへ移動した", unchanged.Game.CurrentNodeID)
	}
}

func TestAIagentの局面選択と変化追加が表示中の局面を保持する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "D5"})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	selection := performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`)
	if selection.Code != http.StatusNoContent {
		t.Fatalf("局面共有状態コード = %d：%s", selection.Code, selection.Body.String())
	}
	contextResponse := performJSON(t, handler, http.MethodGet, "/api/agent/context", "")
	if contextResponse.Code != http.StatusOK || !bytes.Contains(contextResponse.Body.Bytes(), []byte(`"contextId":"`+state.Game.ID+`:root"`)) {
		t.Fatalf("表示中の局面取得状態コード = %d：%s", contextResponse.Code, contextResponse.Body.String())
	}
	variation := performJSON(t, handler, http.MethodPost, "/api/agent/variations", `{"contextId":"`+state.Game.ID+`:root","moves":["E5","D5"],"title":"回答で示した変化"}`)
	if variation.Code != http.StatusOK {
		t.Fatalf("変化追加状態コード = %d：%s", variation.Code, variation.Body.String())
	}
	loaded := performJSON(t, handler, http.MethodGet, "/api/games/"+state.Game.ID, "")
	assertStatus(t, loaded, http.StatusOK)
	var after gameResponse
	if err := json.Unmarshal(loaded.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.Game.CurrentNodeID != "root" {
		t.Fatalf("表示中の局面が%sへ変化した", after.Game.CurrentNodeID)
	}
	found := false
	targetNodeID := ""
	for _, node := range after.Game.TreeNodes {
		if node.Origin == "agent" && node.VariationTitle == "回答で示した変化" && node.Highlighted {
			found = true
		}
		if node.Origin == "agent" && node.Move != nil && node.Move.Coordinate == "D5" {
			targetNodeID = node.ID
		}
	}
	if !found {
		t.Fatalf("AI agent由来の変化の情報がない：%+v", after.Game.TreeNodes)
	}
	if targetNodeID == "" {
		t.Fatalf("AI agentの変化末端がない：%+v", after.Game.TreeNodes)
	}
	navigation := performJSON(t, handler, http.MethodPost, "/api/agent/navigation", `{"contextId":"`+state.Game.ID+`:root","nodeId":"`+targetNodeID+`"}`)
	if navigation.Code != http.StatusOK || !bytes.Contains(navigation.Body.Bytes(), []byte(`"nodeId":"`+targetNodeID+`"`)) {
		t.Fatalf("局面移動状態コード = %d：%s", navigation.Code, navigation.Body.String())
	}
	loaded = performJSON(t, handler, http.MethodGet, "/api/games/"+state.Game.ID, "")
	if err := json.Unmarshal(loaded.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.Game.CurrentNodeID != targetNodeID {
		t.Fatalf("AI agentの局面移動後も表示中の局面が%sのまま", after.Game.CurrentNodeID)
	}
}

func TestAIagentの前提条件エラーに原因に合う状態を返す(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, path string
		want       int
	}{
		{name: "contextIdの形式", path: "/api/agent/context?contextId=invalid", want: http.StatusBadRequest},
		{name: "存在しない対局", path: "/api/agent/context?contextId=missing:root", want: http.StatusNotFound},
		{name: "存在しない局面", path: "/api/agent/context?contextId=" + state.Game.ID + ":missing", want: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performJSON(t, handler, http.MethodGet, test.path, ``)
			if response.Code != test.want {
				t.Fatalf("状態コード = %d、期待値 = %d、本文 = %s", response.Code, test.want, response.Body.String())
			}
		})
	}
	unselected := newTestServer(t, fixedAnalyzer{move: "pass"})
	if response := performJSON(t, unselected.Handler(), http.MethodGet, "/api/agent/context", ``); response.Code != http.StatusConflict {
		t.Fatalf("未選択時状態コード = %d、期待値 = %d、本文 = %s", response.Code, http.StatusConflict, response.Body.String())
	}
}

func TestAIagentの局面移動が不正な前提条件を状態に合う理由で拒否する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	notifyAgentSelectionForTest(t, handler, state.Game.ID, "root")
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{name: "指定なし", body: `{}`, status: http.StatusBadRequest},
		{name: "表示中の局面の形式", body: `{"contextId":"invalid","nodeId":"root"}`, status: http.StatusBadRequest},
		{name: "存在しない対局", body: `{"contextId":"missing:root","nodeId":"root"}`, status: http.StatusNotFound},
		{name: "存在しない表示中の局面", body: `{"contextId":"` + state.Game.ID + `:missing","nodeId":"root"}`, status: http.StatusNotFound},
		{name: "存在しない移動先", body: `{"contextId":"` + state.Game.ID + `:root","nodeId":"missing"}`, status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performJSON(t, handler, http.MethodPost, "/api/agent/navigation", test.body)
			assertStatus(t, response, test.status)
		})
	}
	loaded := performJSON(t, handler, http.MethodGet, "/api/games/"+state.Game.ID, ``)
	assertStatus(t, loaded, http.StatusOK)
	if err := json.Unmarshal(loaded.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Game.CurrentNodeID != "root" {
		t.Fatalf("拒否後の表示中の局面 = %q、期待値 = 開始局面", state.Game.CurrentNodeID)
	}
}

func TestAIagentの変化も死石を除いて終局する(t *testing.T) {
	server := newTestServer(t, deadStoneAnalyzer{move: "pass", coordinate: "C3"})
	handler := server.Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/games/import", strings.NewReader("(;FF[4]CA[UTF-8]GM[1]SZ[5]RU[Chinese]KM[0.5];B[cc];W[])"))
	request.Header.Set("Content-Type", "application/x-go-sgf")
	importedResponse := httptest.NewRecorder()
	handler.ServeHTTP(importedResponse, request)
	assertStatus(t, importedResponse, http.StatusCreated)
	var imported gameResponse
	if err := json.Unmarshal(importedResponse.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	originID := imported.Game.CurrentNodeID
	selection := performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+imported.Game.ID+`","nodeId":"`+originID+`"}`)
	assertStatus(t, selection, http.StatusNoContent)
	variation := performJSON(t, handler, http.MethodPost, "/api/agent/variations", `{"contextId":"`+imported.Game.ID+`:`+originID+`","moves":["pass"],"title":"終局図"}`)
	assertStatus(t, variation, http.StatusOK)

	loaded := performJSON(t, handler, http.MethodGet, "/api/games/"+imported.Game.ID, "")
	assertStatus(t, loaded, http.StatusOK)
	var after gameResponse
	if err := json.Unmarshal(loaded.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	var result *game.Result
	for _, node := range after.Game.TreeNodes {
		if node.Result != nil && node.Result.Cause == "two_passes" {
			result = node.Result
		}
	}
	if result == nil || result.Winner != "W" || result.Margin != 0.5 || !reflect.DeepEqual(result.DeadStones, []string{"C3"}) {
		t.Fatalf("変化の死石を除いた結果 = %+v", result)
	}
}

func Test投了結果の局面から始める変化は状態の不一致として返す(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":5,"aiLevel":1,"humanColor":"B","komi":7.5}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"B1"}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`), http.StatusOK)
	resigned := performJSON(t, handler, http.MethodPost, path+"/resign", `{}`)
	assertStatus(t, resigned, http.StatusOK)
	if err := json.Unmarshal(resigned.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"`+state.Game.CurrentNodeID+`"}`), http.StatusNoContent)
	variation := performJSON(t, handler, http.MethodPost, "/api/agent/variations", `{"contextId":"`+state.Game.ID+`:`+state.Game.CurrentNodeID+`","moves":["pass"]}`)
	assertStatus(t, variation, http.StatusConflict)
}

func Test変化を削除すると削除されたときのみ表示中の局面が変更される(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID

	first := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"E5"}`)
	assertStatus(t, first, http.StatusOK)
	if err := json.Unmarshal(first.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	firstNodeID := state.Game.CurrentNodeID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/nodes/root", ``), http.StatusOK)
	second := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"F5"}`)
	assertStatus(t, second, http.StatusOK)
	if err := json.Unmarshal(second.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	secondNodeID := state.Game.CurrentNodeID

	selection := performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"`+firstNodeID+`"}`)
	if selection.Code != http.StatusNoContent {
		t.Fatalf("局面共有状態コード = %d、本文 = %s", selection.Code, selection.Body.String())
	}
	deleted := performJSON(t, handler, http.MethodDelete, path+"/nodes/"+secondNodeID, expectedNodeIDsBody(t, secondNodeID))
	if deleted.Code != http.StatusOK {
		t.Fatalf("別の変化の削除状態コード = %d、本文 = %s", deleted.Code, deleted.Body.String())
	}
	shared := performJSON(t, handler, http.MethodGet, "/api/agent/context", ``)
	if shared.Code != http.StatusOK || !bytes.Contains(shared.Body.Bytes(), []byte(`"nodeId":"`+firstNodeID+`"`)) {
		t.Fatalf("残った表示中の局面状態コード = %d、本文 = %s", shared.Code, shared.Body.String())
	}

	deleted = performJSON(t, handler, http.MethodDelete, path+"/nodes/"+firstNodeID, expectedNodeIDsBody(t, firstNodeID))
	if deleted.Code != http.StatusOK {
		t.Fatalf("共有中の変化の削除状態コード = %d、本文 = %s", deleted.Code, deleted.Body.String())
	}
	shared = performJSON(t, handler, http.MethodGet, "/api/agent/context", ``)
	if shared.Code != http.StatusConflict {
		t.Fatalf("削除後の表示中の局面状態コード = %d、本文 = %s", shared.Code, shared.Body.String())
	}
}

func Test永続サーバーは破損した保存された対局を隔離する(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dataDir, "broken.json"), []byte(`{"game":`), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatalf("破損した保存ファイルで起動に失敗した：%v", err)
	}
	if response := performJSON(t, restarted.Handler(), http.MethodGet, "/api/games/"+state.Game.ID, ""); response.Code != http.StatusOK {
		t.Fatalf("正常な対局を読み込めない：状態コード=%d 本文=%s", response.Code, response.Body.String())
	}
	errors := performJSON(t, restarted.Handler(), http.MethodGet, "/api/storage-errors", "")
	if errors.Code != http.StatusOK || !bytes.Contains(errors.Body.Bytes(), []byte(`"file":"broken.json"`)) {
		t.Fatalf("読み込みエラーがユーザー向けに公開されていない：状態コード=%d 本文=%s", errors.Code, errors.Body.String())
	}
}

func Test永続サーバーは未知のファイルや不完全なファイルを個別に拒否する(t *testing.T) {
	dataDir := t.TempDir()
	inputs := map[string]string{
		"unknown.json":    `{"schemaVersion":"1","startedAt":"2026-11-04T00:00:00Z","game":{},"aiLevel":1,"humanColor":"B","unknown":true}`,
		"incomplete.json": `{"aiLevel":1,"humanColor":"B","game":{}}`,
	}
	for name, input := range inputs {
		if err := os.WriteFile(filepath.Join(dataDir, name), []byte(input), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	errors := performJSON(t, server.Handler(), http.MethodGet, "/api/storage-errors", "")
	assertStatus(t, errors, http.StatusOK)
	var failures []storageLoadError
	if err := json.Unmarshal(errors.Body.Bytes(), &failures); err != nil {
		t.Fatal(err)
	}
	if len(failures) != len(inputs) {
		t.Fatalf("読み込みエラー = %+v", failures)
	}
}

func Test保存データを安定した正規JSONで書き込む(t *testing.T) {
	dataDir := t.TempDir()
	analyzer := fixedAnalyzer{move: "pass"}
	server, err := NewPersistentServer(dataDir, analyzer)
	if err != nil {
		t.Fatal(err)
	}
	created := createGameForTest(t, server.Handler(), `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, state.Game.ID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPersistentServer(dataDir, analyzer)
	if err != nil {
		t.Fatal(err)
	}
	updated := performJSON(t, restarted.Handler(), http.MethodPatch, "/api/games/"+state.Game.ID+"/nodes/root", `{"note":"","important":false,"expectedNote":"","expectedImportant":false}`)
	assertStatus(t, updated, http.StatusOK)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("再保存で正本が変化した\n再起動前：\n%s\n再起動後：\n%s", before, after)
	}
}

func Test局面欠落誤りによって内部IDが公開されない(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/games/" + state.Game.ID + "/nodes/n999",
		"/api/games/" + state.Game.ID + "/analysis?nodeId=n999",
	} {
		response := performJSON(t, handler, http.MethodPost, path, ``)
		if strings.Contains(path, "/analysis") {
			response = performJSON(t, handler, http.MethodGet, path, ``)
		}
		if response.Code != http.StatusNotFound {
			t.Fatalf("経路 = %s、状態コード = %d、本文 = %s", path, response.Code, response.Body.String())
		}
	}
}

func Test表示中の局面の選択で入力不足と存在しない局面を区別する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "対局が空", body: `{"gameId":"","nodeId":"root"}`, status: http.StatusBadRequest},
		{name: "局面が空", body: `{"gameId":"` + state.Game.ID + `","nodeId":""}`, status: http.StatusBadRequest},
		{name: "局面が存在しない", body: `{"gameId":"` + state.Game.ID + `","nodeId":"missing"}`, status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performJSON(t, handler, http.MethodPost, "/api/agent/selection", test.body)
			if response.Code != test.status {
				t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
		})
	}
}

func performJSON(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func notifyAgentSelectionForTest(t *testing.T, handler http.Handler, gameID, nodeID string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"gameId": gameID, "nodeId": nodeID})
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", string(body)), http.StatusNoContent)
}

func expectedNodeIDsBody(t *testing.T, nodeIDs ...string) string {
	t.Helper()
	body, err := json.Marshal(map[string][]string{"expectedNodeIds": nodeIDs})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func createGameForTest(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	response := performJSON(t, handler, http.MethodPost, "/api/games", body)
	assertStatus(t, response, http.StatusCreated)
	return response
}

func testNodeComment(snapshot game.Snapshot, nodeID string) string {
	properties := snapshot.RootProperties
	if nodeID != "root" {
		for _, node := range snapshot.TreeNodes {
			if node.ID == nodeID {
				properties = node.Properties
				break
			}
		}
	}
	for _, property := range properties {
		if property.Ident == "C" && len(property.Values) > 0 {
			return property.Values[0]
		}
	}
	return ""
}

func mainLineMoveCount(snapshot game.Snapshot) int {
	nodes := make(map[string]game.TreeNode, len(snapshot.TreeNodes))
	for _, node := range snapshot.TreeNodes {
		nodes[node.ID] = node
	}
	count := 0
	for nodeID := snapshot.MainLineNodeID; nodeID != ""; nodeID = nodes[nodeID].ParentID {
		if nodes[nodeID].Move != nil {
			count++
		}
	}
	return count
}

func mainLineResult(snapshot game.Snapshot) *game.Result {
	for _, node := range snapshot.TreeNodes {
		if node.ID == snapshot.MainLineNodeID {
			return node.Result
		}
	}
	return nil
}
