package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-practice/internal/game"
	"go-practice/internal/katago"
)

type countedBlockingAnalyzer struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

type cancellableBlockingAnalyzer struct {
	started  chan struct{}
	release  chan struct{}
	canceled chan struct{}
	calls    atomic.Int32
}

type observedDoneContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (ctx *observedDoneContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}

type restartableAnalyzer struct {
	calls   atomic.Int32
	started chan int
	release chan struct{}
}

type restartableAIAnalyzer struct {
	calls   atomic.Int32
	started chan int
	release chan struct{}
}

func (analyzer *restartableAIAnalyzer) AnalyzePosition(ctx context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	n := int(analyzer.calls.Add(1))
	analyzer.started <- n
	if n == 1 {
		<-ctx.Done()
		return katago.Summary{}, ctx.Err()
	}
	select {
	case <-analyzer.release:
		summary := validAnalysisSummary(position, visits)
		summary.Candidates = []katago.CandidateSummary{{Move: "D5", Visits: visits, Prior: 1, Winrate: 0.5}}
		return summary, nil
	case <-ctx.Done():
		return katago.Summary{}, ctx.Err()
	}
}

func (*restartableAIAnalyzer) AnalysisVersion() string { return "AI再開test" }

type lateResultEngine struct {
	aiStarted        chan struct{}
	aiRelease        chan struct{}
	aiReturned       chan struct{}
	analysisStarted  chan struct{}
	analysisRelease  chan struct{}
	analysisReturned chan struct{}
	analysisCalls    atomic.Int32
}

func (engine *lateResultEngine) AnalyzePosition(_ context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	if visits == 50 {
		close(engine.aiStarted)
		<-engine.aiRelease
		close(engine.aiReturned)
	} else {
		if engine.analysisCalls.Add(1) == 1 {
			close(engine.analysisStarted)
			<-engine.analysisRelease
			close(engine.analysisReturned)
		}
	}
	summary := validAnalysisSummary(position, visits)
	summary.Candidates = []katago.CandidateSummary{{Move: "D5", Visits: visits, Prior: 1, Winrate: 0.5}}
	return summary, nil
}

func (*lateResultEngine) AnalysisVersion() string { return "遅延結果test" }

func (a *restartableAnalyzer) AnalysisVersion() string { return "再開test" }
func (a *restartableAnalyzer) AnalyzePosition(ctx context.Context, position game.Snapshot, _ int) (katago.Summary, error) {
	n := int(a.calls.Add(1))
	a.started <- n
	if n == 1 {
		<-ctx.Done()
		return katago.Summary{}, ctx.Err()
	}
	select {
	case <-a.release:
		return validAnalysisSummary(position, 100), nil
	case <-ctx.Done():
		return katago.Summary{}, ctx.Err()
	}
}

func (analyzer *cancellableBlockingAnalyzer) AnalyzePosition(ctx context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	analyzer.calls.Add(1)
	close(analyzer.started)
	select {
	case <-analyzer.release:
		summary := validAnalysisSummary(position, visits)
		summary.Candidates = []katago.CandidateSummary{{Move: "D5", Visits: visits, Prior: 1, Winrate: 0.5}}
		return summary, nil
	case <-ctx.Done():
		close(analyzer.canceled)
		return katago.Summary{}, ctx.Err()
	}
}

func (analyzer *cancellableBlockingAnalyzer) AnalysisVersion() string { return "取り消し検査" }

func (analyzer *countedBlockingAnalyzer) AnalyzePosition(_ context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	if analyzer.calls.Add(1) == 1 {
		close(analyzer.started)
	}
	<-analyzer.release
	summary := validAnalysisSummary(position, visits)
	summary.Candidates = []katago.CandidateSummary{{Move: "D5", Visits: visits, Prior: 1, Winrate: 0.5}}
	return summary, nil
}

func (analyzer *countedBlockingAnalyzer) AnalysisVersion() string { return "並行処理test" }

func waitForTestSignal[T any](t *testing.T, channel <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("%sを5秒以内に観測できませんでした", description)
		var zero T
		return zero
	}
}

func TestAI思考中に現在の局面を移動すると探索結果を適用しない(t *testing.T) {
	analyzer := &countedBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{})}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	if response := performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`); response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`) }()
	waitForTestSignal(t, analyzer.started, "AI探索の開始")
	if response := performJSON(t, handler, http.MethodPost, path+"/nodes/root", ``); response.Code != http.StatusOK {
		t.Fatalf("現在の局面の移動状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	close(analyzer.release)
	if response := waitForTestSignal(t, finished, "AI要求の完了"); response.Code != http.StatusConflict {
		t.Fatalf("AI着手状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	fetched := performJSON(t, handler, http.MethodGet, path, ``)
	assertStatus(t, fetched, http.StatusOK)
	if err := json.Unmarshal(fetched.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Game.CurrentNodeID != "root" || len(state.Game.Moves) != 0 {
		t.Fatalf("移動後の現在の局面が変わりました：現在=%s、着手列=%v", state.Game.CurrentNodeID, state.Game.Moves)
	}
	if state.Game.MainLineNodeID == "root" || analyzer.calls.Load() != 1 {
		t.Fatalf("人の着手または探索回数が不正です：主分岐=%s、呼出回数=%d", state.Game.MainLineNodeID, analyzer.calls.Load())
	}
}

func Test同時のAI着手要求が1つの探索を共有する(t *testing.T) {
	analyzer := &countedBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{})}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	responses := make(chan *httptest.ResponseRecorder, 2)
	go func() { responses <- performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`) }()
	waitForTestSignal(t, analyzer.started, "AI探索の開始")
	secondArrived := make(chan struct{})
	secondContext := &observedDoneContext{Context: context.Background(), observed: secondArrived}
	go func() {
		request := httptest.NewRequest(http.MethodPost, path+"/ai-moves", strings.NewReader(`{}`)).WithContext(secondContext)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		responses <- response
	}()
	waitForTestSignal(t, secondArrived, "2つ目のAI要求の待機開始")
	close(analyzer.release)
	for range 2 {
		if response := waitForTestSignal(t, responses, "共有AI要求の完了"); response.Code != http.StatusOK {
			t.Fatalf("AI着手状態コード = %d、本文 = %s", response.Code, response.Body.String())
		}
	}
	if analyzer.calls.Load() != 1 {
		t.Fatalf("AI探索回数 = %d、期待値 = 1", analyzer.calls.Load())
	}
}

func Test同時の解析要求が1つの探索を共有する(t *testing.T) {
	analyzer := &countedBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{})}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID + "/analysis"
	responses := make(chan *httptest.ResponseRecorder, 2)
	go func() { responses <- performJSON(t, handler, http.MethodGet, path, ``) }()
	waitForTestSignal(t, analyzer.started, "解析の開始")
	secondArrived := make(chan struct{})
	secondContext, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	observedSecondContext := &observedDoneContext{Context: secondContext, observed: secondArrived}
	go func() {
		request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(observedSecondContext)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		responses <- response
	}()
	waitForTestSignal(t, secondArrived, "2本目の解析要求の待機開始")
	close(analyzer.release)
	for range 2 {
		if response := waitForTestSignal(t, responses, "共有解析要求の完了"); response.Code != http.StatusOK {
			t.Fatalf("解析状態コード = %d、本文 = %s", response.Code, response.Body.String())
		}
	}
	if analyzer.calls.Load() != 1 {
		t.Fatalf("解析回数 = %d、期待値 = 1", analyzer.calls.Load())
	}
}

func TestAI思考中に対局を削除しても対局を復活させない(t *testing.T) {
	analyzer := &countedBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{})}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	aiFinished := make(chan *httptest.ResponseRecorder, 1)
	go func() { aiFinished <- performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`) }()
	waitForTestSignal(t, analyzer.started, "AI探索の開始")
	deleted := performJSON(t, handler, http.MethodDelete, path, ``)
	if deleted.Code != http.StatusOK {
		t.Fatalf("削除状態コード = %d、本文 = %s", deleted.Code, deleted.Body.String())
	}
	close(analyzer.release)
	if response := waitForTestSignal(t, aiFinished, "削除中のAI要求の完了"); response.Code != http.StatusConflict {
		t.Fatalf("削除後のAI着手状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	if response := performJSON(t, handler, http.MethodGet, path, ``); response.Code != http.StatusNotFound {
		t.Fatalf("削除後の取得状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func Test対局を削除すると進行中の局面解析を中止する(t *testing.T) {
	analyzer := &cancellableBlockingAnalyzer{started: make(chan struct{}), canceled: make(chan struct{})}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	analysisDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { analysisDone <- performJSON(t, handler, http.MethodGet, path+"/analysis", ``) }()
	waitForTestSignal(t, analyzer.started, "局面解析の開始")
	assertStatus(t, performJSON(t, handler, http.MethodDelete, path, ``), http.StatusOK)
	waitForTestSignal(t, analyzer.canceled, "対局削除後の局面解析中止")
	response := waitForTestSignal(t, analysisDone, "対局削除後の局面解析要求の終了")
	assertStatus(t, response, http.StatusNotFound)
}

func Testユーザーが中止したAI要求は競合になる(t *testing.T) {
	analyzer := &cancellableBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	handler := newTestServer(t, analyzer).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`) }()
	waitForTestSignal(t, analyzer.started, "AI思考の開始")
	assertStatus(t, performJSON(t, handler, http.MethodDelete, path+"/ai-moves", ``), http.StatusNoContent)
	response := waitForTestSignal(t, finished, "AI要求の中止")
	assertStatus(t, response, http.StatusConflict)
}

func Test解析完了と局面削除が重なっても削除済み局面を返す(t *testing.T) {
	analyzer := &lateResultEngine{
		aiStarted:        make(chan struct{}),
		aiRelease:        make(chan struct{}),
		aiReturned:       make(chan struct{}),
		analysisStarted:  make(chan struct{}),
		analysisRelease:  make(chan struct{}),
		analysisReturned: make(chan struct{}),
	}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	variation := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"D5"}`)
	assertStatus(t, variation, http.StatusOK)
	if err := json.Unmarshal(variation.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	variationID := state.Game.CurrentNodeID

	analysisDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		analysisDone <- performJSON(t, handler, http.MethodGet, path+"/analysis?nodeId="+variationID, ``)
	}()
	waitForTestSignal(t, analyzer.analysisStarted, "局面解析の開始")
	assertStatus(t, performJSON(t, handler, http.MethodDelete, path+"/nodes/"+variationID, expectedNodeIDsBody(t, variationID)), http.StatusOK)
	close(analyzer.analysisRelease)
	waitForTestSignal(t, analyzer.analysisReturned, "削除と重なった局面解析の完了")
	response := waitForTestSignal(t, analysisDone, "削除と重なった局面解析要求の終了")
	assertStatus(t, response, http.StatusNotFound)
}

func Test主分岐局面を削除すると競合を返してAI探索を取り消す(t *testing.T) {
	analyzer := &cancellableBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	moved := performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`)
	assertStatus(t, moved, http.StatusOK)
	fetched := performJSON(t, handler, http.MethodGet, path, ``)
	assertStatus(t, fetched, http.StatusOK)
	if err := json.Unmarshal(fetched.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	nodeID := state.Game.CurrentNodeID
	aiDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { aiDone <- performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`) }()
	waitForTestSignal(t, analyzer.started, "AI思考の開始")
	assertStatus(t, performJSON(t, handler, http.MethodDelete, path+"/nodes/"+nodeID, expectedNodeIDsBody(t, nodeID)), http.StatusOK)
	waitForTestSignal(t, analyzer.canceled, "主分岐局面削除後のAI思考中止")
	response := waitForTestSignal(t, aiDone, "主分岐局面削除後のAI要求終了")
	assertStatus(t, response, http.StatusConflict)
}

func TestAIagentが表示中の局面を移すとAI探索を取り消す(t *testing.T) {
	analyzer := &cancellableBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	handler := newTestServer(t, analyzer).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	played := performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`)
	assertStatus(t, played, http.StatusOK)
	if err := json.Unmarshal(played.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	notifyAgentSelectionForTest(t, handler, state.Game.ID, state.Game.CurrentNodeID)

	aiDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { aiDone <- performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`) }()
	waitForTestSignal(t, analyzer.started, "AI探索の開始")

	navigation := performJSON(
		t,
		handler,
		http.MethodPost,
		"/api/agent/navigation",
		`{"contextId":"`+state.Game.ID+`:`+state.Game.CurrentNodeID+`","nodeId":"root"}`,
	)
	assertStatus(t, navigation, http.StatusOK)
	waitForTestSignal(t, analyzer.canceled, "表示中の局面を移した後のAI探索中止")
	assertStatus(t, waitForTestSignal(t, aiDone, "AI要求の終了"), http.StatusConflict)
}

func Test存在しない局面を対象にした要求はAI探索を取り消さない(t *testing.T) {
	analyzer := &cancellableBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	handler := newTestServer(t, analyzer).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"root"}`), http.StatusNoContent)

	aiDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { aiDone <- performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`) }()
	waitForTestSignal(t, analyzer.started, "AI探索の開始")

	requests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, path + "/nodes/missing", ``},
		{http.MethodPost, path + "/nodes/missing/main-line", ``},
		{http.MethodPost, "/api/agent/navigation", `{"contextId":"` + state.Game.ID + `:missing","nodeId":"root"}`},
		{http.MethodPost, "/api/agent/variations", `{"contextId":"` + state.Game.ID + `:missing","moves":["D5"]}`},
	}
	for _, request := range requests {
		assertStatus(t, performJSON(t, handler, request.method, request.path, request.body), http.StatusNotFound)
		select {
		case <-analyzer.canceled:
			t.Fatal("存在しない局面を対象にした要求がAI探索を取り消しました")
		default:
		}
	}
	close(analyzer.release)
	assertStatus(t, waitForTestSignal(t, aiDone, "AI要求の完了"), http.StatusOK)
}

func Testメモ編集の通知中も共有AI探索を続ける(t *testing.T) {
	analyzer := &cancellableBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	handler := newTestServer(t, analyzer).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"B"}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)

	aiDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { aiDone <- performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`) }()
	waitForTestSignal(t, analyzer.started, "共有AI探索の開始")
	interaction := `{"token":"編集中","kind":"review","gameId":"` + state.Game.ID + `","nodeId":"root"}`
	assertStatus(t, performJSON(t, handler, http.MethodPut, "/api/ui/interaction", interaction), http.StatusNoContent)
	select {
	case <-analyzer.canceled:
		t.Fatal("メモ編集の通知が共有AI探索を中止しました")
	default:
	}
	close(analyzer.release)
	assertStatus(t, waitForTestSignal(t, aiDone, "継続したAI要求の完了"), http.StatusOK)
}

func Test主分岐の昇格で基準局面が外れたAI探索を取り消す(t *testing.T) {
	analyzer := &cancellableBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	variation := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"D5"}`)
	assertStatus(t, variation, http.StatusOK)
	if err := json.Unmarshal(variation.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	variationID := state.Game.CurrentNodeID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/nodes/root", ``), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)

	aiDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { aiDone <- performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`) }()
	waitForTestSignal(t, analyzer.started, "AI思考の開始")
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/nodes/"+variationID+"/main-line", ``), http.StatusOK)
	waitForTestSignal(t, analyzer.canceled, "主分岐昇格後のAI思考中止")
	response := waitForTestSignal(t, aiDone, "主分岐昇格後のAI要求終了")
	assertStatus(t, response, http.StatusConflict)
}

func Test無関係な変化を削除しても進行中のAI処理を取り消す(t *testing.T) {
	analyzer := &countedBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{})}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	variation := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"D5"}`)
	assertStatus(t, variation, http.StatusOK)
	if err := json.Unmarshal(variation.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	variationID := state.Game.CurrentNodeID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/nodes/root", ``), http.StatusOK)
	moved := performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`)
	assertStatus(t, moved, http.StatusOK)
	aiDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { aiDone <- performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`) }()
	waitForTestSignal(t, analyzer.started, "AI思考の開始")
	assertStatus(t, performJSON(t, handler, http.MethodDelete, path+"/nodes/"+variationID, expectedNodeIDsBody(t, variationID)), http.StatusOK)
	close(analyzer.release)
	response := waitForTestSignal(t, aiDone, "無関係な変化削除後のAI要求終了")
	assertStatus(t, response, http.StatusConflict)
}

func TestAI先行要求を取り消すと別の待機要求を探し続ける(t *testing.T) {
	analyzer := &cancellableBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	ctx, cancel := context.WithCancel(context.Background())
	leaderDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPost, path+"/ai-moves", strings.NewReader(`{}`)).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		leaderDone <- response
	}()
	waitForTestSignal(t, analyzer.started, "AI探索の開始")
	waiterDone := make(chan *httptest.ResponseRecorder, 1)
	waiterArrived := make(chan struct{})
	waiterContext := &observedDoneContext{Context: context.Background(), observed: waiterArrived}
	go func() {
		request := httptest.NewRequest(http.MethodPost, path+"/ai-moves", strings.NewReader(`{}`)).WithContext(waiterContext)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		waiterDone <- response
	}()
	waitForTestSignal(t, waiterArrived, "2つ目のAI要求の待機開始")
	cancel()
	assertStatus(t, waitForTestSignal(t, leaderDone, "先行AI要求の取り消し完了"), clientClosedRequestStatus)
	select {
	case <-analyzer.canceled:
		t.Fatal("待機中の要求があるのにAI探索を中止した")
	default:
	}
	close(analyzer.release)
	if response := waitForTestSignal(t, waiterDone, "待機AI要求の完了"); response.Code != http.StatusOK {
		t.Fatalf("待機要求状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func Test取り消したAI探索の結果をKataGoの応答後も適用しない(t *testing.T) {
	engine := &lateResultEngine{
		aiStarted:        make(chan struct{}),
		aiRelease:        make(chan struct{}),
		aiReturned:       make(chan struct{}),
		analysisStarted:  make(chan struct{}),
		analysisRelease:  make(chan struct{}),
		analysisReturned: make(chan struct{}),
	}
	server := newTestServer(t, engine)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	ctx, cancel := context.WithCancel(context.Background())
	requestDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPost, path+"/ai-moves", strings.NewReader(`{}`)).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		requestDone <- response
	}()
	waitForTestSignal(t, engine.aiStarted, "AI探索の開始")
	cancel()
	assertStatus(t, waitForTestSignal(t, requestDone, "取り消ししたAI要求の終了"), clientClosedRequestStatus)
	close(engine.aiRelease)
	waitForTestSignal(t, engine.aiReturned, "取り消し後のAI探索結果の返却")
	fetched := performJSON(t, handler, http.MethodGet, path, ``)
	assertStatus(t, fetched, http.StatusOK)
	if err := json.Unmarshal(fetched.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Game.Moves) != 1 {
		t.Fatalf("取り消し後のAI着手が適用されました：%+v", state.Game.Moves)
	}
}

func TestAI探索完了と要求取り消しが重なっても応答と適用状態が一致する(t *testing.T) {
	engine := &lateResultEngine{
		aiStarted:        make(chan struct{}),
		aiRelease:        make(chan struct{}),
		aiReturned:       make(chan struct{}),
		analysisStarted:  make(chan struct{}),
		analysisRelease:  make(chan struct{}),
		analysisReturned: make(chan struct{}),
	}
	server := newTestServer(t, engine)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)
	ctx, cancel := context.WithCancel(context.Background())
	requestDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPost, path+"/ai-moves", strings.NewReader(`{}`)).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		requestDone <- response
	}()
	waitForTestSignal(t, engine.aiStarted, "AI探索の開始")
	close(engine.aiRelease)
	waitForTestSignal(t, engine.aiReturned, "AI探索結果の返却")
	cancel()
	response := waitForTestSignal(t, requestDone, "AI要求の完了")
	fetched := performJSON(t, handler, http.MethodGet, path, ``)
	assertStatus(t, fetched, http.StatusOK)
	if err := json.Unmarshal(fetched.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if response.Code == http.StatusOK && len(state.Game.Moves) != 2 {
		t.Fatalf("成功したAI着手が適用されていません：%+v", state.Game.Moves)
	}
	if response.Code == clientClosedRequestStatus && len(state.Game.Moves) != 1 {
		t.Fatalf("取り消したAI着手が適用されました：%+v", state.Game.Moves)
	}
	if response.Code != http.StatusOK && response.Code != clientClosedRequestStatus {
		t.Fatalf("AI要求の状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func Test取り消した解析はエンジンが戻った後に結果を保存しない(t *testing.T) {
	engine := &lateResultEngine{
		aiStarted:        make(chan struct{}),
		aiRelease:        make(chan struct{}),
		aiReturned:       make(chan struct{}),
		analysisStarted:  make(chan struct{}),
		analysisRelease:  make(chan struct{}),
		analysisReturned: make(chan struct{}),
	}
	server := newTestServer(t, engine)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	requestDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodGet, "/api/games/"+state.Game.ID+"/analysis", nil).WithContext(ctx)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		requestDone <- response
	}()
	waitForTestSignal(t, engine.analysisStarted, "解析の開始")
	cancel()
	assertStatus(t, waitForTestSignal(t, requestDone, "取り消し後の解析要求の終了"), clientClosedRequestStatus)
	close(engine.analysisRelease)
	waitForTestSignal(t, engine.analysisReturned, "取り消し後の解析結果の返却")
	second := performJSON(t, handler, http.MethodGet, "/api/games/"+state.Game.ID+"/analysis", ``)
	assertStatus(t, second, http.StatusOK)
	if engine.analysisCalls.Load() != 2 {
		t.Fatalf("取り消し後の解析実行回数 = %d、期待値 = 2", engine.analysisCalls.Load())
	}
}

func Test解析完了と要求取り消しが重なっても応答と保存状態が一致する(t *testing.T) {
	engine := &lateResultEngine{
		aiStarted:        make(chan struct{}),
		aiRelease:        make(chan struct{}),
		aiReturned:       make(chan struct{}),
		analysisStarted:  make(chan struct{}),
		analysisRelease:  make(chan struct{}),
		analysisReturned: make(chan struct{}),
	}
	server := newTestServer(t, engine)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID + "/analysis"
	ctx, cancel := context.WithCancel(context.Background())
	requestDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		requestDone <- response
	}()
	waitForTestSignal(t, engine.analysisStarted, "解析の開始")
	close(engine.analysisRelease)
	waitForTestSignal(t, engine.analysisReturned, "解析結果の返却")
	cancel()
	response := waitForTestSignal(t, requestDone, "解析要求の完了")
	assertStatus(t, performJSON(t, handler, http.MethodGet, path, ``), http.StatusOK)
	switch response.Code {
	case http.StatusOK:
		if engine.analysisCalls.Load() != 1 {
			t.Fatalf("成功した解析結果が保存されていません：解析実行回数 = %d", engine.analysisCalls.Load())
		}
	case clientClosedRequestStatus:
		if engine.analysisCalls.Load() != 2 {
			t.Fatalf("取り消した解析結果が保存されました：解析実行回数 = %d", engine.analysisCalls.Load())
		}
	default:
		t.Fatalf("解析要求の状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
}

func Test取り消した解析先行要求がエンジン前に戻る(t *testing.T) {
	engine := &lateResultEngine{
		aiStarted:        make(chan struct{}),
		aiRelease:        make(chan struct{}),
		aiReturned:       make(chan struct{}),
		analysisStarted:  make(chan struct{}),
		analysisRelease:  make(chan struct{}),
		analysisReturned: make(chan struct{}),
	}
	server := newTestServer(t, engine)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	requestDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodGet, "/api/games/"+state.Game.ID+"/analysis", nil).WithContext(ctx)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		requestDone <- response
	}()
	waitForTestSignal(t, engine.analysisStarted, "解析の開始")
	cancel()
	assertStatus(t, waitForTestSignal(t, requestDone, "先頭解析要求の取り消し"), clientClosedRequestStatus)
	select {
	case <-engine.analysisReturned:
		t.Fatal("HTTP要求の取り消し前に解析器が終了した")
	default:
	}
	close(engine.analysisRelease)
	waitForTestSignal(t, engine.analysisReturned, "取り消し後の解析器終了")
}

func Test名前の変更は古い名前を拒否する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	first := performJSON(t, handler, http.MethodPatch, path, `{"name":"先の変更","expectedName":""}`)
	if first.Code != http.StatusOK {
		t.Fatalf("先の変更状態コード = %d、本文 = %s", first.Code, first.Body.String())
	}
	stale := performJSON(t, handler, http.MethodPatch, path, `{"name":"後の変更","expectedName":""}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("古い対局名による変更状態コード = %d、本文 = %s", stale.Code, stale.Body.String())
	}
	fetched := performJSON(t, handler, http.MethodGet, path, ``)
	assertStatus(t, fetched, http.StatusOK)
	if !strings.Contains(fetched.Body.String(), `"name":"先の変更"`) {
		t.Fatalf("競合後の対局名 = %s", fetched.Body.String())
	}
}

func Test選択中のネストした変化を削除すると表示中の局面を解除する(t *testing.T) {
	server := newTestServer(t, fixedAnalyzer{move: "pass"})
	handler := server.Handler()
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
	second := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"D5"}`)
	assertStatus(t, second, http.StatusOK)
	if err := json.Unmarshal(second.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	secondNodeID := state.Game.CurrentNodeID
	assertStatus(t, performJSON(t, handler, http.MethodPost, "/api/agent/selection", `{"gameId":"`+state.Game.ID+`","nodeId":"`+secondNodeID+`"}`), http.StatusNoContent)

	assertStatus(t, performJSON(t, handler, http.MethodDelete, path+"/nodes/"+secondNodeID, expectedNodeIDsBody(t, secondNodeID)), http.StatusOK)
	assertStatus(t, performJSON(t, handler, http.MethodDelete, path+"/nodes/"+firstNodeID, expectedNodeIDsBody(t, firstNodeID)), http.StatusOK)
	contextResponse := performJSON(t, handler, http.MethodGet, "/api/agent/context", ``)
	assertStatus(t, contextResponse, http.StatusConflict)
}

func Test待機要求が取り消されるたびに解析を停止する(t *testing.T) {
	analyzer := &cancellableBlockingAnalyzer{started: make(chan struct{}), canceled: make(chan struct{})}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID + "/analysis"
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx1))
		firstDone <- response
	}()
	waitForTestSignal(t, analyzer.started, "解析の開始")
	secondArrived := make(chan struct{})
	secondDone := make(chan *httptest.ResponseRecorder, 1)
	observedSecondContext := &observedDoneContext{Context: ctx2, observed: secondArrived}
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil).WithContext(observedSecondContext))
		secondDone <- response
	}()
	waitForTestSignal(t, secondArrived, "2本目の解析要求の待機開始")
	cancel1()
	ctx3, cancel3 := context.WithCancel(context.Background())
	thirdArrived := make(chan struct{})
	thirdDone := make(chan *httptest.ResponseRecorder, 1)
	observedThirdContext := &observedDoneContext{Context: ctx3, observed: thirdArrived}
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil).WithContext(observedThirdContext))
		thirdDone <- response
	}()
	waitForTestSignal(t, thirdArrived, "3本目の解析要求の待機開始")
	cancel2()
	assertStatus(t, waitForTestSignal(t, secondDone, "2本目の解析要求の取り消し"), clientClosedRequestStatus)
	select {
	case <-analyzer.canceled:
		t.Fatal("待機中の解析要求があるのに中止した")
	default:
	}
	cancel3()
	waitForTestSignal(t, analyzer.canceled, "全要求の取り消し後の解析中止")
	assertStatus(t, waitForTestSignal(t, firstDone, "1本目の解析要求の終了"), clientClosedRequestStatus)
	assertStatus(t, waitForTestSignal(t, thirdDone, "3本目の解析要求の取り消し"), clientClosedRequestStatus)
	if analyzer.calls.Load() != 1 {
		t.Fatalf("解析回数 = %d、期待値 = 1", analyzer.calls.Load())
	}
}

func Test共有解析の取り消し後に新しい解析を開始できる(t *testing.T) {
	analyzer := &restartableAnalyzer{started: make(chan int, 2), release: make(chan struct{})}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID + "/analysis"
	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
		firstDone <- response
	}()
	if n := waitForTestSignal(t, analyzer.started, "初回解析の開始"); n != 1 {
		t.Fatalf("初回解析 = %d", n)
	}
	cancel()
	select {
	case response := <-firstDone:
		assertStatus(t, response, clientClosedRequestStatus)
	case <-time.After(time.Second):
		t.Fatal("中止した解析要求が終了しなかった")
	}
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() { response <- performJSON(t, handler, http.MethodGet, path, ``) }()
	if n := waitForTestSignal(t, analyzer.started, "再開解析の開始"); n != 2 {
		t.Fatalf("再開解析 = %d", n)
	}
	close(analyzer.release)
	if got := waitForTestSignal(t, response, "再開解析の完了"); got.Code != http.StatusOK {
		t.Fatalf("再開状態コード = %d、本文 = %s", got.Code, got.Body.String())
	}
}

func Test共有AI探索の取り消し後に新しいAI着手を開始できる(t *testing.T) {
	analyzer := &restartableAIAnalyzer{started: make(chan int, 2), release: make(chan struct{})}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	path := "/api/games/" + state.Game.ID
	assertStatus(t, performJSON(t, handler, http.MethodPost, path+"/moves", `{"coordinate":"E5"}`), http.StatusOK)

	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPost, path+"/ai-moves", strings.NewReader(`{}`)).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		firstDone <- response
	}()
	if n := waitForTestSignal(t, analyzer.started, "初回AI探索の開始"); n != 1 {
		t.Fatalf("初回AI探索 = %d", n)
	}
	cancel()
	select {
	case response := <-firstDone:
		assertStatus(t, response, clientClosedRequestStatus)
	case <-time.After(time.Second):
		t.Fatal("中止したAI要求が終了しませんでした")
	}

	secondDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { secondDone <- performJSON(t, handler, http.MethodPost, path+"/ai-moves", `{}`) }()
	if n := waitForTestSignal(t, analyzer.started, "再開AI探索の開始"); n != 2 {
		t.Fatalf("再開AI探索 = %d", n)
	}
	close(analyzer.release)
	response := waitForTestSignal(t, secondDone, "再開AI要求の完了")
	if response.Code != http.StatusOK {
		t.Fatalf("再開状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	var aiResponse aiMoveResponse
	if err := json.Unmarshal(response.Body.Bytes(), &aiResponse); err != nil {
		t.Fatal(err)
	}
	if len(aiResponse.Game.Game.Moves) != 2 || analyzer.calls.Load() != 2 {
		t.Fatalf("再開後の状態：着手列=%v、KataGo呼出回数=%d", aiResponse.Game.Game.Moves, analyzer.calls.Load())
	}
}
