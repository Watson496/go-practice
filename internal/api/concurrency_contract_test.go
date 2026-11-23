package api

import (
	"context"
	"encoding/json"
	"errors"
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

type agentBlockingAnalyzer struct {
	started  chan struct{}
	release  chan struct{}
	canceled chan struct{}
}

type agentRetryAnalyzer struct{ calls atomic.Int32 }

func (analyzer *agentRetryAnalyzer) AnalyzePosition(_ context.Context, position game.Snapshot, visits int) (katago.Summary, error) {
	if analyzer.calls.Add(1) == 1 {
		return katago.Summary{}, errors.New("解析器の一時的な失敗")
	}
	return validAnalysisSummary(position, visits), nil
}

func (*agentRetryAnalyzer) AnalysisVersion() string { return "AI agent再試行検査" }

func (analyzer *agentBlockingAnalyzer) AnalyzePosition(ctx context.Context, position game.Snapshot, _ int) (katago.Summary, error) {
	close(analyzer.started)
	select {
	case <-analyzer.release:
		return validAnalysisSummary(position, 100), nil
	case <-ctx.Done():
		close(analyzer.canceled)
		return katago.Summary{}, ctx.Err()
	}
}

func (*agentBlockingAnalyzer) AnalysisVersion() string {
	return "AI agent削除競合検査"
}

func TestAIagent解析により検索中に削除された局面が拒否される(t *testing.T) {
	analyzer := &agentBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
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
	nodeID := state.Game.CurrentNodeID
	notifyAgentSelectionForTest(t, handler, state.Game.ID, nodeID)
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response <- performJSON(t, handler, http.MethodPost, "/api/agent/analyze", `{"contextId":"`+state.Game.ID+`:`+nodeID+`","maxVisits":100}`)
	}()
	waitForTestSignal(t, analyzer.started, "AI agent解析の開始")
	assertStatus(t, performJSON(t, handler, http.MethodDelete, path+"/nodes/"+nodeID, `{"expectedNodeIds":["`+nodeID+`"]}`), http.StatusOK)
	waitForTestSignal(t, analyzer.canceled, "削除した局面のAI agent解析中止")
	result := waitForTestSignal(t, response, "削除した局面のAI agent解析完了")
	if result.Code != http.StatusNotFound {
		t.Fatalf("削除した局面の解析状態コード = %d、本文 = %s", result.Code, result.Body.String())
	}
}

func Test同じ局面と正規化後の手順のAIagent解析を共有する(t *testing.T) {
	analyzer := &countedBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{})}
	server := newTestServer(t, analyzer)
	var entered atomic.Int32
	secondEntered := make(chan struct{})
	var enteredOnce sync.Once
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/agent/analyze" {
			if entered.Add(1) == 2 {
				enteredOnce.Do(func() { close(secondEntered) })
			}
			<-secondEntered
		}
		server.Handler().ServeHTTP(writer, request)
	})
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	notifyAgentSelectionForTest(t, handler, state.Game.ID, "root")
	upper := `{"contextId":"` + state.Game.ID + `:root","sequence":["D5"],"maxVisits":100}`
	lower := `{"contextId":"` + state.Game.ID + `:root","sequence":["d5"],"maxVisits":100}`
	responses := make(chan *httptest.ResponseRecorder, 2)
	go func() { responses <- performJSON(t, handler, http.MethodPost, "/api/agent/analyze", upper) }()
	go func() { responses <- performJSON(t, handler, http.MethodPost, "/api/agent/analyze", lower) }()
	waitForTestSignal(t, secondEntered, "2つ目の解析要求の到着")
	waitForTestSignal(t, analyzer.started, "共有する解析の開始")
	close(analyzer.release)
	assertStatus(t, waitForTestSignal(t, responses, "1つ目の解析応答"), http.StatusOK)
	assertStatus(t, waitForTestSignal(t, responses, "2つ目の解析応答"), http.StatusOK)
	if calls := analyzer.calls.Load(); calls != 1 {
		t.Fatalf("同じ解析の実行回数 = %d、期待値 = 1", calls)
	}
}

func Test通常解析と空手順のAIagent解析を共有する(t *testing.T) {
	analyzer := &countedBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{})}
	server := newTestServer(t, analyzer)
	var entered atomic.Int32
	bothEntered := make(chan struct{})
	var enteredOnce sync.Once
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/agent/analyze" || strings.HasSuffix(request.URL.Path, "/analysis") {
			if entered.Add(1) == 2 {
				enteredOnce.Do(func() { close(bothEntered) })
			}
			<-bothEntered
		}
		server.Handler().ServeHTTP(writer, request)
	})
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	notifyAgentSelectionForTest(t, handler, state.Game.ID, "root")
	responses := make(chan *httptest.ResponseRecorder, 2)
	go func() {
		responses <- performJSON(t, handler, http.MethodGet, "/api/games/"+state.Game.ID+"/analysis?visits=100", "")
	}()
	go func() {
		responses <- performJSON(t, handler, http.MethodPost, "/api/agent/analyze", `{"contextId":"`+state.Game.ID+`:root","maxVisits":100}`)
	}()
	waitForTestSignal(t, bothEntered, "2種類の解析要求の到着")
	waitForTestSignal(t, analyzer.started, "共有する解析の開始")
	time.Sleep(20 * time.Millisecond)
	close(analyzer.release)
	assertStatus(t, waitForTestSignal(t, responses, "通常解析の応答"), http.StatusOK)
	assertStatus(t, waitForTestSignal(t, responses, "AI agent解析の応答"), http.StatusOK)
	if calls := analyzer.calls.Load(); calls != 1 {
		t.Fatalf("同じ局面の解析実行回数 = %d、期待値 = 1", calls)
	}
}

func Test失敗したAIagent解析を同じ条件で再試行できる(t *testing.T) {
	analyzer := &agentRetryAnalyzer{}
	handler := newTestServer(t, analyzer).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	notifyAgentSelectionForTest(t, handler, state.Game.ID, "root")
	body := `{"contextId":"` + state.Game.ID + `:root","maxVisits":100}`
	first := performJSON(t, handler, http.MethodPost, "/api/agent/analyze", body)
	if first.Code != http.StatusBadGateway {
		t.Fatalf("最初の解析状態コード = %d、本文 = %s", first.Code, first.Body.String())
	}
	second := performJSON(t, handler, http.MethodPost, "/api/agent/analyze", body)
	assertStatus(t, second, http.StatusOK)
	if calls := analyzer.calls.Load(); calls != 2 {
		t.Fatalf("解析器の呼出回数 = %d、期待値 = 2", calls)
	}
}

func Test対局が削除されるとAIagent解析が停止する(t *testing.T) {
	analyzer := &agentBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	server := newTestServer(t, analyzer)
	handler := server.Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	notifyAgentSelectionForTest(t, handler, state.Game.ID, "root")
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response <- performJSON(t, handler, http.MethodPost, "/api/agent/analyze", `{"contextId":"`+state.Game.ID+`:root","maxVisits":100}`)
	}()
	waitForTestSignal(t, analyzer.started, "AI agent解析の開始")
	assertStatus(t, performJSON(t, handler, http.MethodDelete, "/api/games/"+state.Game.ID, ``), http.StatusOK)
	waitForTestSignal(t, analyzer.canceled, "削除した対局のAI agent解析中止")
	result := waitForTestSignal(t, response, "削除した対局のAI agent解析完了")
	if result.Code != http.StatusNotFound {
		t.Fatalf("削除した対局の解析状態コード = %d、本文 = %s", result.Code, result.Body.String())
	}
}

func TestAIagent解析を要求元が中止すると499を記録する(t *testing.T) {
	analyzer := &agentBlockingAnalyzer{started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	handler := newTestServer(t, analyzer).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	notifyAgentSelectionForTest(t, handler, state.Game.ID, "root")
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/api/agent/analyze", strings.NewReader(`{"contextId":"`+state.Game.ID+`:root","maxVisits":100}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(finished)
	}()
	waitForTestSignal(t, analyzer.started, "AI agent解析の開始")
	cancel()
	waitForTestSignal(t, analyzer.canceled, "要求元が中止したAI agent解析")
	waitForTestSignal(t, finished, "AI agent解析要求の完了")
	if response.Code != clientClosedRequestStatus {
		t.Fatalf("中止した解析の状態コード = %d、期待値 = %d", response.Code, clientClosedRequestStatus)
	}
}

func Test中止したAIagent解析を同じ条件で再開できる(t *testing.T) {
	analyzer := &restartableAnalyzer{started: make(chan int, 2), release: make(chan struct{})}
	handler := newTestServer(t, analyzer).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	notifyAgentSelectionForTest(t, handler, state.Game.ID, "root")
	body := `{"contextId":"` + state.Game.ID + `:root","maxVisits":100}`
	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPost, "/api/agent/analyze", strings.NewReader(body)).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		firstDone <- response
	}()
	if call := waitForTestSignal(t, analyzer.started, "初回AI agent解析の開始"); call != 1 {
		t.Fatalf("初回解析 = %d", call)
	}
	cancel()
	assertStatus(t, waitForTestSignal(t, firstDone, "中止したAI agent解析の完了"), clientClosedRequestStatus)

	secondDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		secondDone <- performJSON(t, handler, http.MethodPost, "/api/agent/analyze", body)
	}()
	if call := waitForTestSignal(t, analyzer.started, "再開AI agent解析の開始"); call != 2 {
		t.Fatalf("再開解析 = %d", call)
	}
	close(analyzer.release)
	assertStatus(t, waitForTestSignal(t, secondDone, "再開AI agent解析の完了"), http.StatusOK)
}

func Test表示中の局面の選択前に要求元が中止すると499を記録する(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/selection", strings.NewReader(`{"gameId":"game","nodeId":"root"}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != clientClosedRequestStatus {
		t.Fatalf("中止した表示中の局面選択の状態コード = %d、期待値 = %d", response.Code, clientClosedRequestStatus)
	}
}

func Test変化削除により確認後に追加された局面が拒否される(t *testing.T) {
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
	confirmedNodeID := state.Game.CurrentNodeID
	second := performJSON(t, handler, http.MethodPost, path+"/variations", `{"coordinate":"D5"}`)
	assertStatus(t, second, http.StatusOK)
	if err := json.Unmarshal(second.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	addedNodeID := state.Game.CurrentNodeID

	deleted := performJSON(t, handler, http.MethodDelete, path+"/nodes/"+confirmedNodeID, `{"expectedNodeIds":["`+confirmedNodeID+`"]}`)
	assertStatus(t, deleted, http.StatusConflict)
	latest := performJSON(t, handler, http.MethodGet, path, ``)
	assertStatus(t, latest, http.StatusOK)
	if err := json.Unmarshal(latest.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !snapshotHasNode(state.Game, confirmedNodeID) || !snapshotHasNode(state.Game, addedNodeID) {
		t.Fatalf("競合した削除後の棋譜ツリー = %+v", state.Game.TreeNodes)
	}
}

func Test共有検索の継続中に取り消したAI待機要求が削除される(t *testing.T) {
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

	firstContext, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPost, path+"/ai-moves", strings.NewReader(`{}`)).WithContext(firstContext)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		firstDone <- response
	}()
	waitForTestSignal(t, analyzer.started, "AI探索の開始")
	secondDone := make(chan *httptest.ResponseRecorder, 1)
	secondArrived := make(chan struct{})
	secondContext := &observedDoneContext{Context: context.Background(), observed: secondArrived}
	go func() {
		request := httptest.NewRequest(http.MethodPost, path+"/ai-moves", strings.NewReader(`{}`)).WithContext(secondContext)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		secondDone <- response
	}()
	waitForTestSignal(t, secondArrived, "2つ目のAI要求の待機開始")
	cancelFirst()
	assertStatus(t, waitForTestSignal(t, firstDone, "取り消ししたAI要求の終了"), clientClosedRequestStatus)
	select {
	case <-analyzer.canceled:
		t.Fatal("待機中の要求があるのに共有AI探索を中止しました")
	default:
	}
	close(analyzer.release)
	response := waitForTestSignal(t, secondDone, "共有AI探索の完了")
	assertStatus(t, response, http.StatusOK)
	var aiResponse aiMoveResponse
	if err := json.Unmarshal(response.Body.Bytes(), &aiResponse); err != nil {
		t.Fatal(err)
	}
	if len(aiResponse.Game.Game.Moves) != 2 {
		t.Fatalf("共有AI探索後の着手 = %+v", aiResponse.Game.Game.Moves)
	}
	if calls := analyzer.calls.Load(); calls != 1 {
		t.Fatalf("AI探索回数 = %d、期待値 = 1", calls)
	}
}
