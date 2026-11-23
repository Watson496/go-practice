package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"time"

	"go-practice/internal/api"
	"go-practice/internal/cli"
	"go-practice/internal/httpboundary"
	"go-practice/internal/katago"
	appMCP "go-practice/internal/mcp"
)

const usage = `Go Practiceは、KataGoとの対局と棋譜の検討を1つの画面で行うローカルアプリです。

使い方：
  go-practice serve [オプション]
  go-practice mcp [オプション]
`

type kataGoDefaults struct {
	Binary string
	Model  string
	Config string
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe(os.Args[2:])
	case "mcp":
		err = appMCP.Run(os.Stdin, os.Stdout, os.Stderr, os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprint(os.Stderr, usage)
		err = fmt.Errorf("不明なコマンド%q", os.Args[1])
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "エラー：%v\n", err)
		os.Exit(1)
	}
}

func runServe(args []string) error {
	defaults := discoverKataGo()
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.Usage = func() {
		cli.PrintHelp(fs, "go-practice serve", fs.Output())
	}
	address := fs.String("address", cli.DefaultAddress, "HTTPの待受アドレス")
	dataDir := fs.String("data-dir", "data/games", "保存した対局を置くディレクトリ")
	webDir := fs.String("web-dir", "web/dist", "フロントエンドのビルド成果物を置くディレクトリ")
	apiOnly := fs.Bool("api-only", false, "フロントエンドを配信せずAPIだけを起動する")
	katagoPath := fs.String("katago", defaults.Binary, "KataGo実行ファイルのパス")
	modelPath := fs.String("model", defaults.Model, "KataGoモデルのパス")
	configPath := fs.String("config", defaults.Config, "KataGo解析設定のパス")
	overrideConfig := fs.String("override-config", "", "KataGoへ追加する設定")
	logLevel := cli.LogLevelOption(fs)
	if err := cli.ParseFlags(fs, args, os.Stderr); err != nil {
		return err
	}
	webDirSpecified := false
	fs.Visit(func(option *flag.Flag) {
		if option.Name == "web-dir" {
			webDirSpecified = true
		}
	})
	if *apiOnly && webDirSpecified {
		return fmt.Errorf("--api-onlyと--web-dirは同時に指定できません")
	}
	if !*apiOnly && *webDir == "" {
		return fmt.Errorf("--web-dirには空でないディレクトリを指定してください")
	}
	level, err := cli.ParseConfiguredLogLevelOption(fs, *logLevel)
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	dependencies := []struct{ name, path string }{
		{name: "KataGo実行ファイル", path: *katagoPath},
		{name: "KataGoモデル", path: *modelPath},
		{name: "KataGo設定", path: *configPath},
	}
	for _, dependency := range dependencies {
		if dependency.path == "" {
			return fmt.Errorf("%sが見つかりません。明示的に指定してください", dependency.name)
		}
		info, err := os.Stat(dependency.path)
		if err != nil {
			return fmt.Errorf("%s%qを読み込めません：%w", dependency.name, dependency.path, cli.LocalizeError(err))
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s%qにはファイルを指定してください", dependency.name, dependency.path)
		}
		if dependency.name == "KataGo実行ファイル" && info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("KataGo実行ファイル%qには実行可能なファイルを指定してください", dependency.path)
		}
	}
	appHandler, err := prepareAppHandler(*webDir, *apiOnly)
	if err != nil {
		return err
	}
	player := katago.BeginnerPlayer{Options: katago.Options{
		BinaryPath: *katagoPath, ModelPath: *modelPath, ConfigPath: *configPath, OverrideConfig: *overrideConfig,
	}}
	server, err := api.NewPersistentServer(*dataDir, player)
	if err != nil {
		return fmt.Errorf("保存機能を開始できません：%w", cli.LocalizeError(err))
	}
	handler := appHandler(server.Handler())
	httpServer := &http.Server{
		Addr:              *address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return fmt.Errorf("HTTPサーバーを開始できません：%w", cli.LocalizeError(err))
	}
	slog.Info("HTTPサーバーを起動します", "address", listener.Addr().String(), "data_dir", *dataDir)
	slog.Debug("KataGo設定を読み込みました", "binary", *katagoPath, "model", *modelPath, "config", *configPath, "override_configured", *overrideConfig != "")
	if err := httpServer.Serve(listener); err != nil {
		return fmt.Errorf("HTTPサーバーを実行できません：%w", cli.LocalizeError(err))
	}
	return nil
}

func prepareAppHandler(webDir string, apiOnly bool) (func(http.Handler) http.Handler, error) {
	if apiOnly {
		return func(apiHandler http.Handler) http.Handler {
			return apiHandler
		}, nil
	}
	if err := validateWebDirectory(webDir); err != nil {
		return nil, err
	}
	return func(apiHandler http.Handler) http.Handler {
		return newWebAppHandler(apiHandler, webDir)
	}, nil
}

func newWebAppHandler(apiHandler http.Handler, webDir string) http.Handler {
	indexPath := filepath.Join(webDir, "index.html")

	fileServer := http.FileServer(http.Dir(webDir))
	mux := http.NewServeMux()
	mux.Handle("/api", apiHandler)
	mux.Handle("/api/", apiHandler)
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			http.Error(writer, "許可されていないHTTPメソッドです", http.StatusMethodNotAllowed)
			return
		}
		cleanPath := strings.TrimPrefix(pathpkg.Clean("/"+request.URL.Path), "/")
		staticPath := filepath.Join(webDir, filepath.FromSlash(cleanPath))
		if info, err := os.Stat(staticPath); err == nil && !info.IsDir() {
			serveLocalizedStatic(writer, func(localized http.ResponseWriter) {
				fileServer.ServeHTTP(localized, request)
			})
			return
		}
		serveLocalizedStatic(writer, func(localized http.ResponseWriter) {
			http.ServeFile(localized, request, indexPath)
		})
	})
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !httpboundary.ValidURLPath(request.URL.Path) {
			http.Error(writer, "URLのパスが不正です", http.StatusBadRequest)
			return
		}
		mux.ServeHTTP(writer, request)
	})
}

func serveLocalizedStatic(writer http.ResponseWriter, serve func(http.ResponseWriter)) {
	localized := &localizedStaticResponseWriter{ResponseWriter: writer}
	serve(localized)
	localized.finish()
}

func validateWebDirectory(webDir string) error {
	indexPath := filepath.Join(webDir, "index.html")
	info, err := os.Stat(indexPath)
	if err != nil {
		return fmt.Errorf("フロントエンドのビルド成果物%qを開けません：%w", indexPath, cli.LocalizeError(err))
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("フロントエンドのビルド成果物%qにはファイルが必要です", indexPath)
	}
	return nil
}

type localizedStaticResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (writer *localizedStaticResponseWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.wroteHeader = true
	writer.status = status
	if status < http.StatusBadRequest {
		writer.ResponseWriter.WriteHeader(status)
	}
}

func (writer *localizedStaticResponseWriter) Write(data []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	if writer.status >= http.StatusBadRequest {
		writer.writeLocalizedError()
		return len(data), nil
	}
	if writer.status >= http.StatusMultipleChoices {
		return len(data), nil
	}
	return writer.ResponseWriter.Write(data)
}

func (writer *localizedStaticResponseWriter) finish() {
	if writer.status >= http.StatusBadRequest {
		writer.writeLocalizedError()
	}
}

func (writer *localizedStaticResponseWriter) writeLocalizedError() {
	if writer.status == 0 {
		return
	}
	status := writer.status
	writer.status = 0
	writer.Header().Del("Content-Length")
	message := "静的ファイルを配信できません"
	if status == http.StatusRequestedRangeNotSatisfiable {
		message = "指定された範囲を読み取れません"
	}
	http.Error(writer.ResponseWriter, message, status)
}

func discoverKataGo() kataGoDefaults {
	prefixes := []string{"/opt/homebrew/opt/katago", "/usr/local/opt/katago"}
	var defaults kataGoDefaults
	for _, prefix := range prefixes {
		if defaults.Binary == "" {
			defaults.Binary = firstExisting(filepath.Join(prefix, "bin", "katago"))
		}
		share := filepath.Join(prefix, "share", "katago")
		if defaults.Model == "" {
			defaults.Model = firstExisting(
				filepath.Join(share, "kata1-b18c384nbt-s9996604416-d4316597426.bin.gz"),
				filepath.Join(share, "g170e-b20c256x2-s5303129600-d1228401921.bin.gz"),
				filepath.Join(share, "g170-b40c256x2-s5095420928-d1229425124.bin.gz"),
			)
		}
		if defaults.Config == "" {
			defaults.Config = firstExisting(filepath.Join(share, "configs", "analysis_example.cfg"))
		}
	}
	return defaults
}

func firstExisting(paths ...string) string {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}
