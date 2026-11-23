package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-practice/internal/apicontract"
)

func Test新規対局の既定値は項目を省略したときだけ使う(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()

	omitted := performJSON(t, handler, http.MethodPost, "/api/games", `{"boardSize":9}`)
	assertStatus(t, omitted, http.StatusCreated)
	var created gameResponse
	if err := json.Unmarshal(omitted.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if value(created.AILevel) != apicontract.MinimumAILevel || value(created.HumanColor) != "B" {
		t.Fatalf("省略時の既定値 = AIレベル%d、人%s", value(created.AILevel), value(created.HumanColor))
	}

	for _, body := range []string{
		`{"boardSize":9,"aiLevel":0}`,
		`{"boardSize":9,"humanColor":""}`,
	} {
		response := performJSON(t, handler, http.MethodPost, "/api/games", body)
		assertStatus(t, response, http.StatusBadRequest)
	}
}

func Test要求ログに処理系誤りが含まれる(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	response := performJSON(t, newTestServer(t, fixedAnalyzer{move: "pass"}).Handler(), http.MethodPost, "/api/games", `{"boardSize":"9"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
	}
	decoder := json.NewDecoder(bytes.NewReader(logs.Bytes()))
	found := false
	for decoder.More() {
		var entry map[string]any
		if err := decoder.Decode(&entry); err != nil {
			t.Fatalf("要求ログをJSONとして読み取れません：%v", err)
		}
		if reason, ok := entry["error"].(string); ok && reason != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("要求ログに処理理由がありません：%s", logs.String())
	}
}

func Test応答開始後の予期しない停止をエラーとして記録する(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	server := &Server{}
	handler := server.logRequests(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		panic("処理の障害")
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/test", nil))
	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil || entry["level"] != "ERROR" || entry["cause"] == "" {
		t.Fatalf("予期しない停止のエラーログがない：%s", logs.String())
	}
}

func TestHTTP処理の中止を回復処理で握りつぶさない(t *testing.T) {
	server := &Server{}
	handler := server.logRequests(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Fatalf("回復した値 = %v", recovered)
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/test", nil))
}

func Test共有検索の失敗は原因とともに一度ログに記録される(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		path   func(string) string
		body   string
	}{
		{name: "局面解析", method: http.MethodGet, path: func(id string) string { return "/api/games/" + id + "/analysis" }},
		{name: "AI着手", method: http.MethodPost, path: func(id string) string { return "/api/games/" + id + "/ai-moves" }, body: `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			handler := newTestServer(t, fixedAnalyzer{move: "pass", err: errors.New("解析器との通信に失敗")}).Handler()
			created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1,"humanColor":"W"}`)
			var state gameResponse
			if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			response := performJSON(t, handler, test.method, test.path(state.Game.ID), test.body)
			if response.Code != http.StatusBadGateway {
				t.Fatalf("状態コード = %d、本文 = %s", response.Code, response.Body.String())
			}
			if count := strings.Count(logs.String(), `"level":"ERROR"`); count != 1 {
				t.Fatalf("エラーログの件数 = %d、ログ = %s", count, logs.String())
			}
			if !strings.Contains(logs.String(), `"cause":"解析器との通信に失敗"`) {
				t.Fatalf("要求ログに原因がない：%s", logs.String())
			}
		})
	}
}

func Test保存に失敗した対局を作成済みとして記録しない(t *testing.T) {
	dataDir := t.TempDir()
	server, err := NewPersistentServer(dataDir, fixedAnalyzer{move: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataDir, []byte("保存先を利用できない状態にする"), 0o600); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	response := performJSON(t, server.Handler(), http.MethodPost, "/api/games", `{"boardSize":9,"aiLevel":1}`)
	assertStatus(t, response, http.StatusInternalServerError)
	if strings.Contains(logs.String(), "対局を作成しました") {
		t.Fatalf("保存に失敗した対局が作成済みとして記録された：%s", logs.String())
	}
	if entries, err := filepath.Glob(filepath.Join(dataDir, "*.json")); err != nil || len(entries) != 0 {
		t.Fatalf("保存失敗後の対局ファイル = %v、検査エラー = %v", entries, err)
	}
}

func Test取り消した要求ログが成功を報告しない(t *testing.T) {
	handler := newTestServer(t, fixedAnalyzer{move: "pass"}).Handler()
	created := createGameForTest(t, handler, `{"boardSize":9,"aiLevel":1}`)
	var state gameResponse
	if err := json.Unmarshal(created.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/games/"+state.Game.ID+"/ai-moves", strings.NewReader(`{}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != clientClosedRequestStatus {
		t.Fatalf("状態コード = %d", response.Code)
	}
	if !strings.Contains(logs.String(), `"status":499`) {
		t.Fatalf("取り消した要求が成功として記録された：%s", logs.String())
	}
	if lines := strings.Count(strings.TrimSpace(logs.String()), "\n") + 1; lines != 1 {
		t.Fatalf("取り消した要求のログ件数 = %d、ログ = %s", lines, logs.String())
	}
}

func TestJSON読み取り誤りによって通信の詳細が公開されない(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/games", io.NopCloser(failingRequestBody{}))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	newTestServer(t, fixedAnalyzer{move: "pass"}).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("状態コード = %d", response.Code)
	}
}
