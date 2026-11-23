package main

import (
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newAppHandler(apiHandler http.Handler, webDir string, apiOnly bool) (http.Handler, error) {
	build, err := prepareAppHandler(webDir, apiOnly)
	if err != nil {
		return nil, err
	}
	return build(apiHandler), nil
}

func Testサーバーを起動せずにヘルプを返す(t *testing.T) {
	if err := runServe([]string{"-h"}); err != flag.ErrHelp {
		t.Fatalf("エラー = %v、期待値 = %v", err, flag.ErrHelp)
	}
}

func Test画面配信の指定が競合すると起動を拒否する(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "API専用と画面用ディレクトリ", args: []string{"--api-only", "--web-dir", "web/dist"}},
		{name: "空の画面用ディレクトリ", args: []string{"--web-dir="}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := runServe(test.args); err == nil {
				t.Fatal("競合する画面配信の指定を受け付けました")
			}
		})
	}
}

func Test起動処理は不正なログレベルを報告する(t *testing.T) {
	if err := runServe([]string{"--log-level", "connection refused"}); err == nil {
		t.Fatal("不正なログレベルを受け付けました")
	}
}

func Test起動処理が最初に欠落しているKataGo依存関係を報告する(t *testing.T) {
	err := runServe([]string{"--katago=", "--model=", "--config=", "--api-only"})
	if err == nil {
		t.Fatalf("依存ファイルのエラー = %v", err)
	}
}

func Test起動処理が保存先の検証理由を保持する(t *testing.T) {
	dependencies := makeTestDependencies(t)
	err := runServe(append(dependencies, "--data-dir", "   ", "--api-only"))
	if err == nil {
		t.Fatalf("起動時のエラー = %v", err)
	}
}

func Test起動処理が不正な待受アドレスを日本語で報告する(t *testing.T) {
	dependencies := makeTestDependencies(t)
	err := runServe(append(dependencies, "--data-dir", t.TempDir(), "--address", "abc", "--api-only"))
	if err == nil {
		t.Fatalf("起動時のエラー = %v", err)
	}
}

func makeTestDependencies(t *testing.T) []string {
	t.Helper()
	directory := t.TempDir()
	paths := make([]string, 3)
	for index, name := range []string{"katago", "model", "config"} {
		paths[index] = filepath.Join(directory, name)
		writeTestFile(t, paths[index], "テスト用")
	}
	if err := os.Chmod(paths[0], 0o700); err != nil {
		t.Fatal(err)
	}
	return []string{"--katago", paths[0], "--model", paths[1], "--config", paths[2]}
}

func TestKataGoの依存物には用途に合うファイルが必要になる(t *testing.T) {
	dependencies := makeTestDependencies(t)
	directory := t.TempDir()
	for _, option := range []string{"--katago", "--model", "--config"} {
		args := append([]string(nil), dependencies...)
		for index := range args {
			if args[index] == option {
				args[index+1] = directory
			}
		}
		err := runServe(append(args, "--data-dir", t.TempDir(), "--api-only"))
		if err == nil {
			t.Fatalf("%sにディレクトリを指定したエラー = %v", option, err)
		}
	}

	nonExecutable := filepath.Join(t.TempDir(), "katago")
	writeTestFile(t, nonExecutable, "実行できない内容")
	for index := range dependencies {
		if dependencies[index] == "--katago" {
			dependencies[index+1] = nonExecutable
		}
	}
	err := runServe(append(dependencies, "--data-dir", t.TempDir(), "--api-only"))
	if err == nil {
		t.Fatalf("実行できないKataGoのエラー = %v", err)
	}
}

func Test画面の成果物が不正な場合は保存先を作らない(t *testing.T) {
	dependencies := makeTestDependencies(t)
	parent := t.TempDir()
	dataDir := filepath.Join(parent, "games")
	err := runServe(append(dependencies, "--data-dir", dataDir, "--web-dir", filepath.Join(parent, "missing")))
	if err == nil {
		t.Fatalf("画面の成果物に対するエラー = %v", err)
	}
	if _, statErr := os.Stat(dataDir); !os.IsNotExist(statErr) {
		t.Fatalf("保存先が作成された：%v", statErr)
	}
}

func Testアプリの処理系が画面とAPIを配信する(t *testing.T) {
	webDir := t.TempDir()
	writeTestFile(t, filepath.Join(webDir, "index.html"), "画面の開始点")
	writeTestFile(t, filepath.Join(webDir, "assets", "app.js"), "画面の静的ファイル")
	apiHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, "APIの応答")
	})
	handler, err := newAppHandler(apiHandler, webDir, false)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		path string
		want string
	}{
		{name: "開始画面", path: "/", want: "画面の開始点"},
		{name: "静的ファイル", path: "/assets/app.js", want: "画面の静的ファイル"},
		{name: "画面の代替応答", path: "/games/current", want: "画面の開始点"},
		{name: "APIの開始位置", path: "/api", want: "APIの応答"},
		{name: "API一覧", path: "/api/games", want: "APIの応答"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK || response.Body.String() != test.want {
				t.Fatalf("応答状態コード = %d、本文 = %q", response.Code, response.Body.String())
			}
		})
	}
}

func Test画面配信には構築済みの成果物が必要になる(t *testing.T) {
	if _, err := newAppHandler(http.NotFoundHandler(), t.TempDir(), false); err == nil {
		t.Fatal("「index.html」がなくても処理部を作成できた")
	}
}

func TestAPI専用設定では画面の成果物がなくても起動できる(t *testing.T) {
	apiHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler, err := newAppHandler(apiHandler, t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/games", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("応答状態コード = %d", response.Code)
	}
}

func Test画面配信がGET以外の要求を拒否する(t *testing.T) {
	webDir := t.TempDir()
	writeTestFile(t, filepath.Join(webDir, "index.html"), "画面の開始点")
	handler, err := newAppHandler(http.NotFoundHandler(), webDir, false)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/games/current", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("応答状態コード = %d", response.Code)
	}
	if allow := response.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Fatalf("許可されるHTTPメソッド = %q", allow)
	}
}

func Test画面配信は配信先の外を参照しない(t *testing.T) {
	root := t.TempDir()
	webDir := filepath.Join(root, "web")
	writeTestFile(t, filepath.Join(webDir, "index.html"), "画面の開始点")
	writeTestFile(t, filepath.Join(root, "outside.txt"), "配信してはならない内容")
	handler, err := newAppHandler(http.NotFoundHandler(), webDir, false)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/%2e%2e/outside.txt", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("配信先の外を指す要求への応答：状態コード = %d、本文 = %q", response.Code, response.Body.String())
	}
}

func Test静的ファイルの範囲外を拒否する(t *testing.T) {
	webDir := t.TempDir()
	writeTestFile(t, filepath.Join(webDir, "index.html"), "画面の開始点")
	writeTestFile(t, filepath.Join(webDir, "assets", "app.js"), "短い内容")
	handler, err := newAppHandler(http.NotFoundHandler(), webDir, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/assets/app.js", "/存在しない画面"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Range", "bytes=100-")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("%sの応答状態コード = %d", path, response.Code)
		}
	}
}

func Test静的ファイルの転送応答に英語の本文を付けない(t *testing.T) {
	webDir := t.TempDir()
	writeTestFile(t, filepath.Join(webDir, "index.html"), "画面の開始点")
	handler, err := newAppHandler(http.NotFoundHandler(), webDir, false)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if response.Code < 300 || response.Code >= 400 || response.Body.Len() != 0 {
		t.Fatalf("静的ファイルの転送応答：状態コード = %d、本文 = %q", response.Code, response.Body.String())
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
