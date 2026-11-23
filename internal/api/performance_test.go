package api

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"go-practice/internal/apicontract"
	"go-practice/internal/game"
	"go-practice/internal/katago"
)

func Benchmark362件の解析一時保存(benchmark *testing.B) {
	const entries = 362
	cacheEntries := make([]cachedAnalysis, entries)
	for index := range cacheEntries {
		cacheEntries[index] = cachedAnalysis{Version: "benchmark", Summary: katago.Summary{Visits: index + 1}}
	}
	benchmark.ReportAllocs()
	for benchmark.Loop() {
		stored := &storedGame{}
		entries := make(map[string]cachedAnalysis, len(cacheEntries))
		for index, cached := range cacheEntries {
			entries[fmt.Sprintf("n%d:%d", index, index+1)] = cached
		}
		stored.replaceAnalysisCache(entries)
	}
}

func Benchmark362局面の対局に保存した解析を取得する(benchmark *testing.B) {
	session, err := game.NewConfiguredSession("benchmark", 9, 7.5, 0)
	if err != nil {
		benchmark.Fatal(err)
	}
	for index := 1; index < 362; index++ {
		if session.Snapshot().Status == "finished" {
			session.ContinueAfterResult()
		}
		if _, err := session.Play("pass"); err != nil {
			benchmark.Fatal(err)
		}
		if err := session.CommitCurrentNodeToMainLine(); err != nil {
			benchmark.Fatal(err)
		}
	}
	stored := &storedGame{session: session, aiLevel: 1, humanColor: "B", startedAt: time.Now()}
	response := stored.response()
	stored.publishedResponse.Store(&response)
	cacheKey := analysisCacheKey(response.Game.CurrentNodeID, apicontract.DefaultAnalysisVisits)
	stored.replaceAnalysisCache(map[string]cachedAnalysis{cacheKey: {Version: "benchmark", Summary: validAnalysisSummary(response.Game, apicontract.DefaultAnalysisVisits)}})
	server := &Server{games: map[string]*storedGame{"benchmark": stored}, analysisVersion: "benchmark"}
	request := httptest.NewRequest("GET", "/api/games/benchmark/analysis", nil)
	request.SetPathValue("id", "benchmark")

	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for benchmark.Loop() {
		response := httptest.NewRecorder()
		server.getGameAnalysis(response, request)
		if response.Code != 200 {
			benchmark.Fatalf("状態コード = %d", response.Code)
		}
	}
}
