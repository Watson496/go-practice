// Package mcp はGo Practiceを操作するMCPサーバーを提供する。
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go-practice/internal/apicontract"
	"go-practice/internal/cli"
	"go-practice/internal/game"
	"go-practice/internal/jsonstructure"
	"go-practice/internal/version"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type transportError struct {
	message string
	cause   error
}

func (err *transportError) Error() string { return err.message }
func (err *transportError) Unwrap() error { return err.cause }

func newTransportError(message string, cause error) error {
	return &transportError{message: message, cause: cause}
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var nullID = json.RawMessage("null")

type server struct {
	baseURL   string
	client    *http.Client
	logger    *slog.Logger
	mu        sync.Mutex
	active    map[string]*activeRequest
	limiter   toolRateLimiter
	lifecycle lifecyclePhase
	now       func() time.Time
}

type lifecyclePhase int

const (
	lifecycleNew lifecyclePhase = iota
	lifecycleAwaitingReady
	lifecycleReady
)

type activeRequest struct {
	method    string
	cancel    context.CancelFunc
	cancelled bool
}

type toolRateLimiter struct {
	tokens     float64
	lastRefill time.Time
}

func concurrentRequest(call request) bool {
	if call.Method == "ping" || call.Method == "tools/list" {
		return true
	}
	// 対局単位の排他と競合の判定はAPI側が行う。MCP接続全体では直列化せず、
	// 別の対局や読み取り専用の要求を、時間のかかる変更要求から独立させる。
	return call.Method == "tools/call"
}

const (
	toolCallsPerMinute          = 60
	toolCallBurst               = 20
	maximumMCPMessageSize       = 64 << 20
	responseWriteFailureMessage = "MCP応答を書き込めません"
)

var errMCPMessageTooLarge = errors.New("MCP要求が大きすぎます")

const supportedProtocolVersion = "2025-11-25"

func Run(input io.Reader, output, diagnosticOutput io.Writer, args []string) error {
	return run(input, output, diagnosticOutput, args, time.Now)
}

func run(input io.Reader, output, diagnosticOutput io.Writer, args []string, now func() time.Time) (returnErr error) {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.Usage = func() {
		cli.PrintHelp(fs, "go-practice mcp", fs.Output())
	}
	baseURL := fs.String("server-url", cli.DefaultServerURL, "Go PracticeアプリのAPI URL")
	logLevel := cli.LogLevelOption(fs)
	if err := cli.ParseFlags(fs, args, diagnosticOutput); err != nil {
		return err
	}
	validatedBaseURL, err := validateServerURL(*baseURL)
	if err != nil {
		return err
	}
	level, err := cli.ParseConfiguredLogLevelOption(fs, *logLevel)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(diagnosticOutput, &slog.HandlerOptions{Level: level}))
	s := &server{
		baseURL: validatedBaseURL,
		client:  &http.Client{Timeout: 3 * time.Minute},
		logger:  logger,
		active:  make(map[string]*activeRequest),
		limiter: toolRateLimiter{tokens: toolCallBurst, lastRefill: now()},
		now:     now,
	}
	logger.Debug("MCPサーバーを起動しました", "server_url", s.baseURL)
	inputCloser, _ := input.(io.Closer)
	reader := bufio.NewReaderSize(input, 64*1024)
	encoder := json.NewEncoder(output)
	var outputMu sync.Mutex
	var outputFailed atomic.Bool
	writeResponse := func(value response) error {
		outputMu.Lock()
		defer outputMu.Unlock()
		if err := encoder.Encode(value); err != nil {
			outputFailed.Store(true)
			s.cancelActiveRequests()
			if inputCloser != nil {
				_ = inputCloser.Close()
			}
			return err
		}
		return nil
	}
	type queuedRequest struct {
		call            request
		active          *activeRequest
		notification    bool
		toolRateLimited bool
	}
	incoming := make(chan queuedRequest)
	requests := make(chan queuedRequest)
	workerErrors := make(chan error, 1)
	var dispatcher sync.WaitGroup
	dispatcher.Add(1)
	go func() {
		defer dispatcher.Done()
		defer close(requests)
		queue := make([]queuedRequest, 0)
		for incoming != nil || len(queue) > 0 {
			var next chan queuedRequest
			var request queuedRequest
			if len(queue) > 0 {
				next = requests
				request = queue[0]
			}
			select {
			case queued, open := <-incoming:
				if !open {
					incoming = nil
					continue
				}
				queue = append(queue, queued)
			case next <- request:
				queue = queue[1:]
			}
		}
	}()
	var launcher sync.WaitGroup
	var readers sync.WaitGroup
	launcher.Add(1)
	go func() {
		defer launcher.Done()
		for queued := range requests {
			call := queued.call
			if outputFailed.Load() {
				if queued.active != nil {
					key, _ := requestIDKey(call.ID)
					s.mu.Lock()
					delete(s.active, key)
					s.mu.Unlock()
				}
				continue
			}
			if queued.notification {
				s.acceptInitializedNotification(call.Params)
				continue
			}
			process := func(call request, active *activeRequest, toolRateLimited bool) {
				key, _ := requestIDKey(call.ID)
				ctx, cancel := context.WithCancel(context.Background())
				s.mu.Lock()
				active.cancel = cancel
				cancelled := active.cancelled
				s.mu.Unlock()
				if cancelled {
					cancel()
					s.mu.Lock()
					delete(s.active, key)
					s.mu.Unlock()
					logger.Debug("MCP要求を取り消しました", "method", call.Method)
					return
				}
				started := time.Now()
				logger.Debug("MCP要求を受信しました", "method", call.Method)
				var result any
				var rpcErr *rpcError
				if toolRateLimited {
					result = toolResult("ツールの呼び出しが多すぎます。しばらく待ってから再試行してください。", true)
				} else {
					result, rpcErr = s.handle(ctx, call)
				}
				s.mu.Lock()
				cancelled = active.cancelled
				delete(s.active, key)
				s.mu.Unlock()
				cancel()
				if cancelled {
					logger.Debug("MCP要求を取り消しました", "method", call.Method)
					return
				}
				if err := writeResponse(response{JSONRPC: "2.0", ID: call.ID, Result: result, Error: rpcErr}); err != nil {
					select {
					case workerErrors <- newTransportError(responseWriteFailureMessage, err):
					default:
					}
					return
				}
				if rpcErr != nil {
					logger.Debug("MCP要求をエラーで終了しました", "method", call.Method, "duration", time.Since(started), "error", rpcErr)
				} else {
					logger.Debug("MCP要求を処理しました", "method", call.Method, "duration", time.Since(started))
				}
			}
			if concurrentRequest(call) {
				readers.Add(1)
				go func() {
					defer readers.Done()
					process(call, queued.active, queued.toolRateLimited)
				}()
				continue
			}
			process(call, queued.active, queued.toolRateLimited)
		}
	}()
	defer func() {
		close(incoming)
		dispatcher.Wait()
		launcher.Wait()
		readers.Wait()
		if returnErr == nil {
			select {
			case returnErr = <-workerErrors:
			default:
			}
		}
	}()
	for {
		line, readErr := readMCPLine(reader, maximumMCPMessageSize)
		if errors.Is(readErr, io.EOF) {
			s.cancelToolRequests()
			break
		}
		if errors.Is(readErr, errMCPMessageTooLarge) {
			if err := writeResponse(response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: errMCPMessageTooLarge.Error()}}); err != nil {
				return newTransportError(responseWriteFailureMessage, err)
			}
			continue
		}
		if readErr != nil {
			if outputFailed.Load() {
				return nil
			}
			s.cancelActiveRequests()
			return newTransportError("MCP要求を読み込めません", readErr)
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if encodingErr := jsonstructure.ValidateEncoding(line); encodingErr != nil {
			if err := writeResponse(response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: encodingErr.Error()}}); err != nil {
				return newTransportError(responseWriteFailureMessage, err)
			}
			continue
		}
		if json.Valid(line) && isJSONRPCResponse(line) {
			logger.Debug("MCP応答を処理せずに受信しました")
			continue
		}
		var call request
		if err := json.Unmarshal(line, &call); err != nil {
			code, message := -32700, "JSONを解析できません"
			var responseID json.RawMessage
			if json.Valid(line) {
				code, message = -32600, "JSON-RPC要求が不正です"
				responseID = requestIDFromEnvelope(line)
			}
			if err := writeResponse(response{JSONRPC: "2.0", ID: responseID, Error: &rpcError{Code: code, Message: message}}); err != nil {
				return newTransportError(responseWriteFailureMessage, err)
			}
			continue
		}
		var envelope map[string]json.RawMessage
		_ = json.Unmarshal(line, &envelope)
		if _, hasMethod := envelope["method"]; hasMethod {
			if _, hasID := envelope["id"]; !hasID {
				if call.JSONRPC != "2.0" || call.Method == "" || validateRequestEnvelope(line) != nil {
					logger.Debug("不正なMCP通知を応答せずに受信しました")
					continue
				}
				if call.Method == "notifications/cancelled" {
					s.cancelRequest(call.Params)
					continue
				}
				if call.Method == "notifications/initialized" {
					incoming <- queuedRequest{call: call, notification: true}
					continue
				}
				logger.Debug("MCP通知を応答せずに受信しました", "method", call.Method)
				continue
			}
		}
		if err := validateRequestEnvelope(line); err != nil {
			if err := writeResponse(response{JSONRPC: "2.0", ID: requestIDFromEnvelope(line), Error: &rpcError{Code: -32600, Message: "JSON-RPC要求が不正です：" + err.Error()}}); err != nil {
				return newTransportError(responseWriteFailureMessage, err)
			}
			continue
		}
		key, _ := requestIDKey(call.ID)
		if call.JSONRPC != "2.0" || call.Method == "" || !validRequestID(call.ID) {
			if err := writeResponse(response{JSONRPC: "2.0", ID: errorResponseID(call.ID), Error: &rpcError{Code: -32600, Message: "JSON-RPC要求が不正です"}}); err != nil {
				return newTransportError(responseWriteFailureMessage, err)
			}
			continue
		}
		active := &activeRequest{method: call.Method}
		s.mu.Lock()
		_, duplicateID := s.active[key]
		if !duplicateID {
			s.active[key] = active
		}
		s.mu.Unlock()
		if duplicateID {
			if err := writeResponse(response{JSONRPC: "2.0", ID: call.ID, Error: &rpcError{Code: -32600, Message: "JSON-RPC要求IDは処理中の要求と重複できません"}}); err != nil {
				return newTransportError(responseWriteFailureMessage, err)
			}
			continue
		}
		toolRateLimited := call.Method == "tools/call" && !s.allowToolCall(s.now())
		incoming <- queuedRequest{call: call, active: active, toolRateLimited: toolRateLimited}
	}
	return nil
}

func readMCPLine(reader *bufio.Reader, maximum int) ([]byte, error) {
	line := make([]byte, 0, min(maximum, 64*1024))
	tooLarge := false
	for {
		fragment, continued, err := reader.ReadLine()
		if err != nil {
			return nil, err
		}
		if !tooLarge {
			if len(line) > maximum-len(fragment) {
				tooLarge = true
				line = nil
			} else {
				line = append(line, fragment...)
			}
		}
		if continued {
			continue
		}
		if tooLarge {
			return nil, errMCPMessageTooLarge
		}
		return line, nil
	}
}

func validateServerURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("--server-urlにはhttpまたはhttpsのAPI URLを指定してください")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validateRequestEnvelope(raw json.RawMessage) error {
	return jsonstructure.ValidateObjectFields(raw, setOf("jsonrpc", "id", "method", "params"))
}

func (s *server) cancelActiveRequests() {
	s.cancelRequests(func(*activeRequest) bool { return true })
}

func (s *server) cancelToolRequests() {
	s.cancelRequests(func(active *activeRequest) bool { return active.method == "tools/call" })
}

func (s *server) cancelRequests(matches func(*activeRequest) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, active := range s.active {
		if !matches(active) {
			continue
		}
		active.cancelled = true
		if active.cancel != nil {
			active.cancel()
		}
	}
}

func (s *server) cancelRequest(params json.RawMessage) {
	var values struct {
		RequestID json.RawMessage `json:"requestId"`
		Reason    string          `json:"reason"`
		Meta      json.RawMessage `json:"_meta"`
	}
	if jsonstructure.ValidateObject(params, setOf("requestId", "reason", "_meta")) != nil || json.Unmarshal(params, &values) != nil || validateProtocolMetadata(values.Meta) != nil || !validRequestID(values.RequestID) || len(values.RequestID) == 0 {
		return
	}
	s.mu.Lock()
	key, _ := requestIDKey(values.RequestID)
	active := s.active[key]
	if active != nil && active.method != "initialize" {
		active.cancelled = true
		if active.cancel != nil {
			active.cancel()
		}
	}
	s.mu.Unlock()
	if active != nil && active.method != "initialize" {
		s.logger.Debug("MCP要求の取り消しを受信しました", "reason", values.Reason)
	}
}

func isJSONRPCResponse(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	if _, hasMethod := fields["method"]; hasMethod {
		return false
	}
	_, hasResult := fields["result"]
	_, hasError := fields["error"]
	return hasResult || hasError
}

func validRequestID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	_, valid := requestIDKey(id)
	return valid
}

func requestIDKey(id json.RawMessage) (string, bool) {
	if len(id) == 0 || bytes.Equal(id, nullID) {
		return "", false
	}
	var text string
	if json.Unmarshal(id, &text) == nil {
		return "s:" + text, true
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(id))
	decoder.UseNumber()
	if decoder.Decode(&number) != nil {
		return "", false
	}
	value, ok := new(big.Rat).SetString(number.String())
	if !ok {
		return "", false
	}
	return "n:" + value.RatString(), true
}

func requestIDFromEnvelope(raw json.RawMessage) json.RawMessage {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil
	}
	var id json.RawMessage
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil
		}
		if name == "id" {
			if len(id) > 0 {
				return nil
			}
			id = value
		}
	}
	return errorResponseID(id)
}

func errorResponseID(id json.RawMessage) json.RawMessage {
	if validRequestID(id) && len(id) > 0 {
		return id
	}
	return nil
}

func (s *server) handle(ctx context.Context, call request) (any, *rpcError) {
	switch call.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string          `json:"protocolVersion"`
			Capabilities    json.RawMessage `json:"capabilities"`
			ClientInfo      json.RawMessage `json:"clientInfo"`
			Meta            json.RawMessage `json:"_meta"`
		}
		if err := jsonstructure.ValidateObject(call.Params, setOf("protocolVersion", "capabilities", "clientInfo", "_meta")); err != nil {
			return nil, &rpcError{Code: -32602, Message: "初期化要求のパラメーターが不正です：" + err.Error()}
		}
		if err := json.Unmarshal(call.Params, &params); err != nil {
			return nil, &rpcError{Code: -32602, Message: "初期化要求の各項目の型が不正です"}
		}
		if err := validateProtocolMetadata(params.Meta); err != nil {
			return nil, &rpcError{Code: -32602, Message: "初期化要求の付随情報が不正です：" + err.Error()}
		}
		var clientInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if len(params.ClientInfo) > 0 {
			if err := validateImplementation(params.ClientInfo); err != nil {
				return nil, &rpcError{Code: -32602, Message: "初期化要求の利用元情報が不正です：" + err.Error()}
			}
			if err := json.Unmarshal(params.ClientInfo, &clientInfo); err != nil {
				return nil, &rpcError{Code: -32602, Message: "初期化要求の利用元情報にはオブジェクトを指定してください"}
			}
		}
		if params.ProtocolVersion == "" || clientInfo.Name == "" || clientInfo.Version == "" || len(params.Capabilities) == 0 {
			return nil, &rpcError{Code: -32602, Message: "初期化には通信規約の版、機能情報、利用元情報が必要です"}
		}
		if err := validateClientCapabilities(params.Capabilities); err != nil {
			return nil, &rpcError{Code: -32602, Message: "初期化要求の機能情報が不正です：" + err.Error()}
		}
		s.mu.Lock()
		if s.lifecycle != lifecycleNew {
			s.mu.Unlock()
			return nil, &rpcError{Code: -32600, Message: "初期化は接続ごとに一度だけ実行できます"}
		}
		s.lifecycle = lifecycleAwaitingReady
		s.mu.Unlock()
		return map[string]any{"protocolVersion": supportedProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": version.Name, "version": version.Current}, "instructions": "局面検討では最初にget_selected_positionを呼び、応答のcontextIdを後続の解析と変化に使ってください。"}, nil
	case "notifications/initialized":
		return nil, &rpcError{Code: -32600, Message: "初期化完了は通知として送信してください"}
	case "ping":
		if err := validateMetadataOnlyParams(call.Params); err != nil {
			return nil, &rpcError{Code: -32602, Message: "稼働確認要求のパラメーターが不正です：" + err.Error()}
		}
		return map[string]any{}, nil
	case "tools/list":
		if !s.operationReady() {
			return nil, &rpcError{Code: -32600, Message: "初期化を完了してからツールを利用してください"}
		}
		if err := validateToolsListParams(call.Params); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		return map[string]any{"tools": toolDefinitions}, nil
	case "tools/call":
		if !s.operationReady() {
			return nil, &rpcError{Code: -32600, Message: "初期化を完了してからツールを利用してください"}
		}
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Meta      json.RawMessage `json:"_meta"`
		}
		if err := jsonstructure.ValidateObjectFields(call.Params, setOf("name", "arguments", "_meta", "task")); err != nil {
			return nil, &rpcError{Code: -32602, Message: "ツール呼び出しのパラメーターが不正です：" + err.Error()}
		}
		if err := json.Unmarshal(call.Params, &params); err != nil || params.Name == "" {
			return nil, &rpcError{Code: -32602, Message: "ツール呼び出しにはツール名を指定してください"}
		}
		if err := validateProtocolMetadata(params.Meta); err != nil {
			return nil, &rpcError{Code: -32602, Message: "ツール呼び出しの付随情報が不正です：" + err.Error()}
		}
		if _, exists := toolDefinitionsByName[params.Name]; !exists {
			return nil, &rpcError{Code: -32602, Message: "指定したツールは存在しません"}
		}
		if len(params.Arguments) > 0 {
			var argumentObject map[string]json.RawMessage
			if json.Unmarshal(params.Arguments, &argumentObject) != nil || argumentObject == nil {
				return nil, &rpcError{Code: -32602, Message: "ツールの引数にはオブジェクトを指定してください"}
			}
		}
		normalized, args, err := validateToolArguments(params.Name, params.Arguments)
		if err != nil {
			return toolResult(err.Error(), true), nil
		}
		s.logger.Debug("MCPツールを呼び出します", "tool", params.Name)
		body, err := s.executeValidatedTool(ctx, params.Name, normalized, args)
		if err != nil {
			s.logger.Debug("MCPツールの呼び出しに失敗しました", "tool", params.Name, "error", err)
			return toolResult(err.Error(), true), nil
		}
		return toolResult(string(body), false), nil
	default:
		return nil, &rpcError{Code: -32601, Message: "メソッドが見つかりません"}
	}
}

func (s *server) acceptInitializedNotification(params json.RawMessage) {
	if validateMetadataOnlyParams(params) != nil {
		return
	}
	s.mu.Lock()
	if s.lifecycle == lifecycleAwaitingReady {
		s.lifecycle = lifecycleReady
	}
	s.mu.Unlock()
}

func (s *server) operationReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lifecycle == lifecycleReady
}

func (s *server) allowToolCall(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.After(s.limiter.lastRefill) {
		elapsed := now.Sub(s.limiter.lastRefill).Seconds()
		s.limiter.tokens = math.Min(toolCallBurst, s.limiter.tokens+elapsed*toolCallsPerMinute/60)
		s.limiter.lastRefill = now
	}
	if s.limiter.tokens < 1 {
		return false
	}
	s.limiter.tokens--
	return true
}

func validateToolsListParams(params json.RawMessage) error {
	if len(params) == 0 {
		return nil
	}
	if err := jsonstructure.ValidateObject(params, setOf("cursor", "_meta")); err != nil {
		return fmt.Errorf("ツール一覧取得のパラメーターが不正です：%w", err)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(params, &values); err != nil {
		return fmt.Errorf("ツール一覧取得のパラメーターにはオブジェクトを指定してください")
	}
	if metadata, exists := values["_meta"]; exists {
		if err := validateProtocolMetadata(metadata); err != nil {
			return fmt.Errorf("ツール一覧取得の付随情報が不正です：%w", err)
		}
	}
	if value, exists := values["cursor"]; exists {
		var cursor string
		if isJSONNull(value) || json.Unmarshal(value, &cursor) != nil {
			return fmt.Errorf("続きの位置には文字列を指定してください")
		}
		return fmt.Errorf("指定した続きの位置は存在しません")
	}
	return nil
}

func validateMetadataOnlyParams(params json.RawMessage) error {
	if len(params) == 0 {
		return nil
	}
	if err := jsonstructure.ValidateObject(params, setOf("_meta")); err != nil {
		return err
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(params, &values); err != nil {
		return fmt.Errorf("オブジェクトを指定してください")
	}
	return validateProtocolMetadata(values["_meta"])
}

func validateProtocolMetadata(metadata json.RawMessage) error {
	if len(metadata) == 0 {
		return nil
	}
	return jsonstructure.ValidateObject(metadata, nil)
}

func validateImplementation(raw json.RawMessage) error {
	allowed := setOf("name", "title", "version", "description", "websiteUrl", "icons")
	if err := jsonstructure.ValidateObject(raw, allowed); err != nil {
		return err
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	stringFields := []struct {
		name  string
		label string
	}{
		{name: "name", label: "名前"},
		{name: "title", label: "表示名"},
		{name: "version", label: "版"},
		{name: "description", label: "説明"},
		{name: "websiteUrl", label: "ウェブサイト"},
	}
	for _, field := range stringFields {
		if value, exists := values[field.name]; exists {
			var text string
			if isJSONNull(value) || json.Unmarshal(value, &text) != nil {
				return fmt.Errorf("利用元情報の%sには文字列を指定してください", field.label)
			}
			if field.name == "websiteUrl" && !validURI(text) {
				return fmt.Errorf("利用元情報のウェブサイトには絶対URLを指定してください")
			}
		}
	}
	if rawIcons, exists := values["icons"]; exists {
		var icons []json.RawMessage
		if json.Unmarshal(rawIcons, &icons) != nil || icons == nil {
			return fmt.Errorf("利用元情報のアイコン一覧には配列を指定してください")
		}
		for _, icon := range icons {
			if err := validateIcon(icon); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateClientCapabilities(raw json.RawMessage) error {
	if err := jsonstructure.ValidateObject(raw, nil); err != nil {
		return err
	}
	var capabilities map[string]json.RawMessage
	if json.Unmarshal(raw, &capabilities) != nil {
		return fmt.Errorf("機能情報を解釈できません")
	}
	names := make([]string, 0, len(capabilities))
	for name := range capabilities {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := capabilities[name]
		var err error
		switch name {
		case "experimental":
			err = validateObjectValues(value)
		case "roots":
			err = validateBooleanObject(value, "listChanged")
		case "sampling":
			err = validateObjectObject(value, "context", "tools")
		case "elicitation":
			err = validateObjectObject(value, "form", "url")
		case "tasks":
			err = validateClientTaskCapabilities(value)
		default:
			continue
		}
		if err != nil {
			return fmt.Errorf("機能情報の%sが不正です：%w", capabilityLabel(name), err)
		}
	}
	return nil
}

func validateObjectValues(raw json.RawMessage) error {
	if err := jsonstructure.ValidateObject(raw, nil); err != nil {
		return err
	}
	var values map[string]json.RawMessage
	_ = json.Unmarshal(raw, &values)
	for _, value := range values {
		if err := jsonstructure.ValidateObject(value, nil); err != nil {
			return err
		}
	}
	return nil
}

func validateBooleanObject(raw json.RawMessage, names ...string) error {
	return validateNamedValues(raw, names, func(value json.RawMessage) error {
		var decoded bool
		if json.Unmarshal(value, &decoded) != nil {
			return fmt.Errorf("真偽値を指定してください")
		}
		return nil
	})
}

func validateObjectObject(raw json.RawMessage, names ...string) error {
	return validateNamedValues(raw, names, func(value json.RawMessage) error {
		return jsonstructure.ValidateObject(value, nil)
	})
}

func validateNamedValues(raw json.RawMessage, names []string, validate func(json.RawMessage) error) error {
	if err := jsonstructure.ValidateObject(raw, nil); err != nil {
		return err
	}
	var values map[string]json.RawMessage
	_ = json.Unmarshal(raw, &values)
	for _, name := range names {
		value, exists := values[name]
		if !exists {
			continue
		}
		if err := validate(value); err != nil {
			return err
		}
	}
	return nil
}

func validateClientTaskCapabilities(raw json.RawMessage) error {
	if err := jsonstructure.ValidateObject(raw, nil); err != nil {
		return err
	}
	var values map[string]json.RawMessage
	_ = json.Unmarshal(raw, &values)
	for _, name := range []string{"list", "cancel"} {
		if value, exists := values[name]; exists {
			if err := jsonstructure.ValidateObject(value, nil); err != nil {
				return err
			}
		}
	}
	requests, exists := values["requests"]
	if !exists {
		return nil
	}
	if err := jsonstructure.ValidateObject(requests, nil); err != nil {
		return err
	}
	var requestValues map[string]json.RawMessage
	_ = json.Unmarshal(requests, &requestValues)
	if sampling, exists := requestValues["sampling"]; exists {
		if err := validateObjectObject(sampling, "createMessage"); err != nil {
			return err
		}
	}
	if elicitation, exists := requestValues["elicitation"]; exists {
		if err := validateObjectObject(elicitation, "create"); err != nil {
			return err
		}
	}
	return nil
}

func validateIcon(raw json.RawMessage) error {
	if err := jsonstructure.ValidateObject(raw, setOf("src", "mimeType", "sizes", "theme")); err != nil {
		return err
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return fmt.Errorf("アイコン情報を解釈できません")
	}
	var src string
	if json.Unmarshal(values["src"], &src) != nil || !validURI(src) {
		return fmt.Errorf("アイコンの参照先には絶対URLを指定してください")
	}
	if mimeType, exists := values["mimeType"]; exists {
		var value string
		if isJSONNull(mimeType) || json.Unmarshal(mimeType, &value) != nil {
			return fmt.Errorf("アイコンのメディア種別には文字列を指定してください")
		}
	}
	if sizes, exists := values["sizes"]; exists {
		var value []string
		if json.Unmarshal(sizes, &value) != nil || value == nil {
			return fmt.Errorf("アイコンの大きさには文字列の配列を指定してください")
		}
		for _, size := range value {
			width, height, found := strings.Cut(size, "x")
			parsedWidth, widthErr := strconv.Atoi(width)
			parsedHeight, heightErr := strconv.Atoi(height)
			if size != "any" && (!found || widthErr != nil || heightErr != nil || parsedWidth <= 0 || parsedHeight <= 0) {
				return fmt.Errorf("アイコンの大きさには幅×高さまたは任意の大きさを指定してください")
			}
		}
	}
	if theme, exists := values["theme"]; exists {
		var value string
		if json.Unmarshal(theme, &value) != nil || value != "light" && value != "dark" {
			return fmt.Errorf("アイコンの表示テーマには明るい画面用または暗い画面用を指定してください")
		}
	}
	return nil
}

func validURI(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme != ""
}

func isJSONNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func capabilityLabel(name string) string {
	switch name {
	case "experimental":
		return "実験的機能"
	case "roots":
		return "参照先一覧"
	case "sampling":
		return "生成要求"
	case "elicitation":
		return "ユーザーへの入力要求"
	case "tasks":
		return "作業管理"
	}
	return name
}

var argumentLabels = map[string]string{
	"contextId":         "局面の識別情報",
	"nodeId":            "棋譜の局面",
	"maxVisits":         "visits",
	"visits":            "visits",
	"sequence":          "着手列",
	"moves":             "着手列",
	"title":             "変化名",
	"gameId":            "対局の識別情報",
	"boardSize":         "盤サイズ",
	"komi":              "コミ",
	"aiLevel":           "AIレベル",
	"handicap":          "置き碁の石数",
	"humanColor":        "人の石の色",
	"name":              "対局名",
	"expectedName":      "変更前の対局名",
	"coordinate":        "着手位置",
	"note":              "メモ",
	"important":         "重要な局面の印",
	"expectedNote":      "変更前のメモ",
	"expectedImportant": "変更前の重要な局面の印",
	"expectedNodeIds":   "削除対象の局面一覧",
	"gameIds":           "並べ替える対局の一覧",
	"contents":          "棋譜の内容",
}

func argumentLabel(name string) string { return argumentLabels[name] }

var argumentDescriptions = map[string]string{
	"contextId":         "get_selected_positionなどが返した局面の識別情報。",
	"nodeId":            "get_gameなどが返した局面の識別情報。",
	"maxVisits":         "KataGoが行う探索の回数。",
	"visits":            "KataGoが行う探索の回数。",
	"sequence":          "基準局面から順に適用する着手。座標またはpassを指定する。",
	"moves":             "基準局面から順に適用する着手。座標またはpassを指定する。",
	"gameId":            "list_gamesやcreate_gameなどが返した対局の識別情報。",
	"boardSize":         "盤サイズを一辺の路数で指定する。",
	"komi":              "白へ加えるコミの目数。",
	"aiLevel":           "対局相手のAIレベル。",
	"handicap":          "固定置き碁で先に置く黒石の数。",
	"humanColor":        "対局する人が担当する石の色。",
	"expectedName":      "競合を検出するための変更前の対局名。",
	"coordinate":        "D4形式の着手位置またはpass。",
	"important":         "局面に重要な局面の印を付けるかどうか。",
	"expectedImportant": "競合を検出するための、変更前の重要な局面の印の有無。",
	"expectedNodeIds":   "競合を検出するための、削除対象に含まれる局面の識別情報の一覧。",
	"gameIds":           "list_gamesで取得した対局の識別情報を、保存する順に指定する。",
	"contents":          "読み込み対象となるSGFまたはJSONの全文。",
}

func argumentDescription(name string) string { return argumentDescriptions[name] }

func setOf(names ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}
	return result
}

var toolDefinitions = buildToolDefinitions()

var toolDefinitionsByName = indexToolDefinitions(toolDefinitions)

func buildToolDefinitions() []map[string]any {
	return []map[string]any{
		tool("get_selected_position", "表示中の局面を取得", "局面検討の最初に呼び、画面で表示中の局面、盤面、既存のKataGo解析を取得する。応答のcontextIdは、その局面を基準にする後続のツール呼び出しに使う。", nil, nil, true, false),
		tool("select_position", "棋譜の局面へ移動", "ユーザーと一緒に確認する既存局面を選び、アプリの盤面も移動する。内部探索には使わない。移動後のツール呼び出しには、このツールが返す新しいcontextIdを使う。", map[string]any{"contextId": stringSchema(), "nodeId": stringSchema()}, []string{"contextId", "nodeId"}, false, false),
		tool("analyze_position", "KataGoで局面または変化を解析", "contextIdで指定した局面からsequenceの着手列を進め、その末尾をKataGoで解析する。", map[string]any{"contextId": stringSchema(), "sequence": map[string]any{"type": "array", "items": stringSchema(), "maxItems": apicontract.MaximumAnalysisSequenceSize, "default": []string{}}, "maxVisits": map[string]any{"type": "integer", "minimum": apicontract.MinimumAnalysisVisits, "maximum": apicontract.MaximumAnalysisVisits, "default": apicontract.DefaultAgentAnalysisVisits}}, []string{"contextId"}, true, false),
		tool("show_variation", "説明した変化を棋譜ツリーへ表示", "回答で実際に示す変化だけを変化として追加する。内部探索には使わない。同一手順は重複しない。", map[string]any{"contextId": stringSchema(), "moves": map[string]any{"type": "array", "items": stringSchema(), "minItems": 1, "maxItems": apicontract.MaximumAnalysisSequenceSize}, "title": map[string]any{"type": "string", "description": "改行と空白を正規化した後で80文字以下の変化名。"}}, []string{"contextId", "moves"}, false, false),
		tool("list_games", "保存した対局の一覧を取得", "保存した並び順で対局の一覧を取得する。", nil, nil, true, false),
		tool("reorder_games", "保存した対局を並べ替え", "指定した対局を指定順で一覧の末尾へ並べる。省略した対局は、まだ並べ替えていない対局として先頭へ並べる。", map[string]any{"gameIds": map[string]any{"type": "array", "items": stringSchema()}}, []string{"gameIds"}, false, false),
		tool("list_storage_errors", "読み込めなかった保存ファイルを取得", "起動時に読み込めなかった保存ファイルと理由を取得する。", nil, nil, true, false),
		tool("get_game", "対局を取得", "対局の棋譜、設定、現在の局面を取得する。", fields("gameId"), []string{"gameId"}, true, false),
		tool("get_analysis", "局面を解析", fmt.Sprintf("対局の指定局面をKataGoで解析する。nodeIdを省略した場合は現在の局面を解析し、visitsを省略した場合は%d回探索する。", apicontract.DefaultAnalysisVisits), map[string]any{"gameId": stringSchema(), "nodeId": stringSchema(), "visits": map[string]any{"type": "integer", "minimum": apicontract.MinimumAnalysisVisits, "maximum": apicontract.MaximumAnalysisVisits, "default": apicontract.DefaultAnalysisVisits}}, []string{"gameId"}, true, false),
		tool("create_game", "対局を作成", fmt.Sprintf("KataGoとの新しい対局を作成する。aiLevelは%dから%dまでで、大きいほど探索量が増える。komiの既定値は互先%.1f目、置き碁%.1f目。", apicontract.MinimumAILevel, apicontract.MaximumAILevel, game.DefaultKomi, game.DefaultHandicapKomi), map[string]any{"boardSize": map[string]any{"type": "integer", "enum": enumValues(game.SupportedBoardSizes())}, "komi": map[string]any{"type": "number", "minimum": apicontract.MinimumKomi, "maximum": apicontract.MaximumKomi, "multipleOf": 0.5}, "aiLevel": map[string]any{"type": "integer", "minimum": apicontract.MinimumAILevel, "maximum": apicontract.MaximumAILevel, "default": apicontract.DefaultAILevel}, "handicap": map[string]any{"type": "integer", "enum": enumValues(game.SupportedHandicaps()), "default": apicontract.DefaultHandicap}, "humanColor": map[string]any{"type": "string", "enum": enumValues(game.SupportedColors()), "default": apicontract.DefaultHumanColor}}, []string{"boardSize"}, false, false),
		tool("rename_game", "対局名を変更", "get_gameまたはlist_gamesで取得した対局名をexpectedNameに指定し、保存した対局の名前を変更する。対局名はSGFで空白とされる前後の文字を除いた後で1文字以上80文字以下とし、別の操作による変更は上書きしない。", map[string]any{"gameId": stringSchema(), "name": map[string]any{"type": "string", "description": "SGFで空白とされる前後の文字を除いた後の対局名。1文字以上80文字以下。"}, "expectedName": stringSchema()}, []string{"gameId", "name", "expectedName"}, false, false),
		tool("update_game_settings", "対局の設定を変更", "対局のAIのレベルと人の石の色を変更する。指定しなかった方は変えない。盤の大きさ、置き碁、コミは変更できない。AIが手を決めている間に変更すると、その処理を中止する。人の石の色を変えると、それまでの手のうち、どれが人の手かも変わる。", map[string]any{"gameId": stringSchema(), "aiLevel": map[string]any{"type": "integer", "minimum": apicontract.MinimumAILevel, "maximum": apicontract.MaximumAILevel}, "humanColor": map[string]any{"type": "string", "enum": enumValues(game.SupportedColors())}}, []string{"gameId"}, false, false),
		tool("delete_game", "対局を削除", "保存した対局を削除する。", fields("gameId"), []string{"gameId"}, false, true),
		tool("play_move", "対局で着手", "現在の局面で人の手を打ち、その手順を主分岐にする。座標はD4の形式で指定し、パスはpassを指定する。AIの手番はrequest_ai_moveで進める。", fields("gameId", "coordinate"), []string{"gameId", "coordinate"}, false, false),
		tool("request_ai_move", "AIに着手を要求", "現在の局面がAIの手番であるとき、KataGoに着手を要求して、その手順を主分岐にする。", fields("gameId"), []string{"gameId"}, false, false),
		tool("resign_game", "投了", "現在の局面が人の手番であるときに投了し、その手順を主分岐にする。", fields("gameId"), []string{"gameId"}, false, false),
		tool("undo_move", "待った", "開始局面から現在の局面までの手順のうち、最も新しい人の操作（人の石の色の着手、パス、投了）と、その後のすべての局面を取り消し、変化として残す。", fields("gameId"), []string{"gameId"}, false, false),
		tool("navigate_game", "現在の局面を移動", "棋譜にある局面を、現在の局面にする。", fields("gameId", "nodeId"), []string{"gameId", "nodeId"}, false, false),
		tool("update_node", "局面のメモを更新", "get_gameで取得した局面のメモと重要な局面を更新する。expectedNoteとexpectedImportantには取得時の値を指定し、別の操作による更新を上書きしない。", map[string]any{"gameId": stringSchema(), "nodeId": stringSchema(), "note": map[string]any{"type": "string", "description": "改行を正規化した後で4000文字以下のメモ。"}, "important": map[string]any{"type": "boolean"}, "expectedNote": map[string]any{"type": "string", "description": "競合を検出するための、改行を正規化した変更前のメモ。"}, "expectedImportant": map[string]any{"type": "boolean"}}, []string{"gameId", "nodeId", "note", "important", "expectedNote", "expectedImportant"}, false, false),
		tool("play_variation", "変化へ着手", "現在の局面から、その局面の手番の側の手として変化を1手進める。", fields("gameId", "coordinate"), []string{"gameId", "coordinate"}, false, false),
		tool("delete_variation", "局面以降を削除", "指定局面と、その後に続く局面をすべて削除する。expectedNodeIdsにはget_gameで確認した削除対象の局面の識別情報をすべて指定し、別の操作による変更を削除しない。", map[string]any{"gameId": stringSchema(), "nodeId": stringSchema(), "expectedNodeIds": map[string]any{"type": "array", "items": stringSchema(), "minItems": 1}}, []string{"gameId", "nodeId", "expectedNodeIds"}, false, true),
		tool("promote_variation", "変化を主分岐にする", "指定局面を通る手順を主分岐にする。", fields("gameId", "nodeId"), []string{"gameId", "nodeId"}, false, false),
		tool("import_sgf", "SGFを読み込む", "SGF文字列を新しい対局として読み込む。", fields("contents"), []string{"contents"}, false, false),
		tool("import_json", "JSONを読み込む", "Go Practiceが書き出したJSON文字列を読み込む。", fields("contents"), []string{"contents"}, false, false),
		tool("export_sgf", "SGFを書き出す", "対局をSGF文字列として書き出す。", fields("gameId"), []string{"gameId"}, true, false),
		tool("export_json", "JSONを書き出す", "対局を再び読み込めるJSON文字列として書き出す。", fields("gameId"), []string{"gameId"}, true, false),
	}
}

func indexToolDefinitions(definitions []map[string]any) map[string]map[string]any {
	indexed := make(map[string]map[string]any, len(definitions))
	for _, definition := range definitions {
		name, _ := definition["name"].(string)
		indexed[name] = definition
	}
	return indexed
}

func stringSchema() map[string]any { return map[string]any{"type": "string"} }
func enumValues[T any](values []T) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}
func fields(names ...string) map[string]any {
	result := make(map[string]any, len(names))
	for _, name := range names {
		result[name] = stringSchema()
	}
	return result
}
func tool(name, title, description string, properties map[string]any, required []string, readOnly, destructive bool) map[string]any {
	return map[string]any{"name": name, "title": title, "description": description, "inputSchema": objectSchema(properties, required), "annotations": map[string]any{"readOnlyHint": readOnly, "destructiveHint": destructive, "openWorldHint": false}}
}
func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	for name, raw := range properties {
		if property, ok := raw.(map[string]any); ok {
			if _, exists := property["description"]; !exists {
				property["description"] = argumentDescription(name)
			}
		}
	}
	result := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

func (s *server) executeValidatedTool(ctx context.Context, name string, arguments json.RawMessage, args map[string]json.RawMessage) ([]byte, error) {
	switch name {
	case "get_selected_position":
		return s.http(ctx, http.MethodGet, "/api/agent/context", nil)
	case "select_position":
		return s.http(ctx, http.MethodPost, "/api/agent/navigation", arguments)
	case "analyze_position":
		return s.http(ctx, http.MethodPost, "/api/agent/analyze", arguments)
	case "show_variation":
		return s.http(ctx, http.MethodPost, "/api/agent/variations", arguments)
	case "list_games":
		return s.http(ctx, http.MethodGet, "/api/games", nil)
	case "reorder_games":
		return s.reorderGames(ctx, args)
	case "list_storage_errors":
		return s.http(ctx, http.MethodGet, "/api/storage-errors", nil)
	case "get_game":
		return s.gameRequest(ctx, http.MethodGet, args, "", nil)
	case "get_analysis":
		return s.getAnalysis(ctx, args)
	case "create_game":
		return s.http(ctx, http.MethodPost, "/api/games", arguments)
	case "rename_game":
		return s.gameRequest(ctx, http.MethodPatch, args, "", []string{"gameId"})
	case "update_game_settings":
		return s.gameRequest(ctx, http.MethodPatch, args, "/settings", []string{"gameId"})
	case "delete_game":
		return s.gameRequest(ctx, http.MethodDelete, args, "", nil)
	case "play_move":
		return s.gameRequest(ctx, http.MethodPost, args, "/moves", []string{"gameId"})
	case "request_ai_move":
		return s.gameRequest(ctx, http.MethodPost, args, "/ai-moves", []string{"gameId"})
	case "resign_game":
		return s.gameRequest(ctx, http.MethodPost, args, "/resign", []string{"gameId"})
	case "undo_move":
		return s.gameRequest(ctx, http.MethodPost, args, "/undo", []string{"gameId"})
	case "navigate_game":
		return s.nodeRequest(ctx, http.MethodPost, args, "", nil)
	case "update_node":
		return s.nodeRequest(ctx, http.MethodPatch, args, "", []string{"gameId", "nodeId"})
	case "play_variation":
		return s.gameRequest(ctx, http.MethodPost, args, "/variations", []string{"gameId"})
	case "delete_variation":
		return s.nodeRequest(ctx, http.MethodDelete, args, "", []string{"gameId", "nodeId"})
	case "promote_variation":
		return s.nodeRequest(ctx, http.MethodPost, args, "/main-line", nil)
	case "import_sgf":
		contents, err := stringArgument(args, "contents")
		if err != nil {
			return nil, err
		}
		return s.httpWithContentType(ctx, http.MethodPost, "/api/games/import", []byte(contents), "application/x-go-sgf")
	case "import_json":
		contents, err := stringArgument(args, "contents")
		if err != nil {
			return nil, err
		}
		return s.http(ctx, http.MethodPost, "/api/games/import-json", []byte(contents))
	case "export_sgf":
		return s.gameRequest(ctx, http.MethodGet, args, "/sgf", nil)
	case "export_json":
		return s.gameRequest(ctx, http.MethodGet, args, "/json", nil)
	}
	return nil, fmt.Errorf("ツール%sの実行処理がありません", name)
}

func (s *server) reorderGames(ctx context.Context, args map[string]json.RawMessage) ([]byte, error) {
	return s.http(ctx, http.MethodPut, "/api/games/order", []byte(`{"ids":`+string(args["gameIds"])+`}`))
}

func validateToolArguments(name string, arguments json.RawMessage) (json.RawMessage, map[string]json.RawMessage, error) {
	definition := toolDefinitionsByName[name]
	args, err := decodeArguments(arguments)
	if err != nil {
		return nil, nil, err
	}
	schema, _ := definition["inputSchema"].(map[string]any)
	properties, _ := schema["properties"].(map[string]any)
	argumentNames := make([]string, 0, len(args))
	for key := range args {
		argumentNames = append(argumentNames, key)
	}
	sort.Strings(argumentNames)
	for _, key := range argumentNames {
		if _, ok := properties[key]; !ok {
			return nil, nil, fmt.Errorf("引数%sは指定できません", key)
		}
	}
	propertyNames := make([]string, 0, len(properties))
	for key := range properties {
		propertyNames = append(propertyNames, key)
	}
	sort.Strings(propertyNames)
	for _, key := range propertyNames {
		rawSchema := properties[key]
		property, _ := rawSchema.(map[string]any)
		if _, ok := args[key]; !ok {
			if value, hasDefault := property["default"]; hasDefault {
				encoded, _ := json.Marshal(value)
				args[key] = encoded
			}
			continue
		}
		if err := validateJSONType(argumentLabel(key), args[key], property); err != nil {
			return nil, nil, err
		}
		if property["type"] == "integer" {
			var value float64
			_ = json.Unmarshal(args[key], &value)
			args[key] = json.RawMessage(strconv.FormatInt(int64(value), 10))
		}
	}
	if required, ok := schema["required"].([]string); ok {
		for _, key := range required {
			if _, exists := args[key]; !exists {
				return nil, nil, fmt.Errorf("%sを指定してください", argumentLabel(key))
			}
		}
	}
	normalized, err := json.Marshal(args)
	if err != nil {
		return nil, nil, fmt.Errorf("引数を解釈できません")
	}
	return normalized, args, nil
}

func validateJSONType(name string, raw json.RawMessage, schema map[string]any) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("%sには空値を指定できません", name)
	}
	switch schema["type"] {
	case "string":
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return fmt.Errorf("%sには文字列を指定してください", name)
		}
	case "integer":
		var value int
		if json.Unmarshal(raw, &value) != nil {
			return fmt.Errorf("%sには整数を指定してください", name)
		}
	case "number":
		var value float64
		if json.Unmarshal(raw, &value) != nil {
			return fmt.Errorf("%sには数値を指定してください", name)
		}
	case "boolean":
		var value bool
		if json.Unmarshal(raw, &value) != nil {
			return fmt.Errorf("%sには真偽値を指定してください", name)
		}
	case "array":
		var value []json.RawMessage
		if json.Unmarshal(raw, &value) != nil {
			return fmt.Errorf("%sには配列を指定してください", name)
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for _, item := range value {
				if err := validateJSONType(name+"の要素", item, itemSchema); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func decodeArguments(arguments json.RawMessage) (map[string]json.RawMessage, error) {
	if len(arguments) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	if err := jsonstructure.ValidateObjectFields(arguments, nil); err != nil {
		return nil, fmt.Errorf("引数が不正です：%w", err)
	}
	var args map[string]json.RawMessage
	_ = json.Unmarshal(arguments, &args)
	return args, nil
}

func requiredString(args map[string]json.RawMessage, name string) (string, error) {
	value, err := stringArgument(args, name)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%sには空でない文字列を指定してください", argumentLabel(name))
	}
	return value, nil
}

func stringArgument(args map[string]json.RawMessage, name string) (string, error) {
	var value string
	if err := json.Unmarshal(args[name], &value); err != nil {
		return "", fmt.Errorf("%sには文字列を指定してください", argumentLabel(name))
	}
	return value, nil
}

func bodyWithout(args map[string]json.RawMessage, excluded ...string) ([]byte, error) {
	remaining := make(map[string]json.RawMessage, len(args))
	for key, value := range args {
		remaining[key] = value
	}
	for _, key := range excluded {
		delete(remaining, key)
	}
	return json.Marshal(remaining)
}

func requestBody(args map[string]json.RawMessage, bodyFields []string) ([]byte, error) {
	if bodyFields == nil {
		return nil, nil
	}
	return bodyWithout(args, bodyFields...)
}

func (s *server) gameRequest(ctx context.Context, method string, args map[string]json.RawMessage, suffix string, bodyFields []string) ([]byte, error) {
	gameID, err := requiredString(args, "gameId")
	if err != nil {
		return nil, err
	}
	body, err := requestBody(args, bodyFields)
	if err != nil {
		return nil, err
	}
	return s.http(ctx, method, "/api/games/"+url.PathEscape(gameID)+suffix, body)
}

func (s *server) nodeRequest(ctx context.Context, method string, args map[string]json.RawMessage, suffix string, bodyFields []string) ([]byte, error) {
	gameID, err := requiredString(args, "gameId")
	if err != nil {
		return nil, err
	}
	nodeID, err := requiredString(args, "nodeId")
	if err != nil {
		return nil, err
	}
	body, err := requestBody(args, bodyFields)
	if err != nil {
		return nil, err
	}
	return s.http(ctx, method, "/api/games/"+url.PathEscape(gameID)+"/nodes/"+url.PathEscape(nodeID)+suffix, body)
}

func (s *server) getAnalysis(ctx context.Context, args map[string]json.RawMessage) ([]byte, error) {
	gameID, err := requiredString(args, "gameId")
	if err != nil {
		return nil, err
	}
	var visits int
	_ = json.Unmarshal(args["visits"], &visits)
	query := url.Values{"visits": {strconv.Itoa(visits)}}
	if _, exists := args["nodeId"]; exists {
		nodeID, err := requiredString(args, "nodeId")
		if err != nil {
			return nil, err
		}
		query.Set("nodeId", nodeID)
	}
	return s.http(ctx, http.MethodGet, "/api/games/"+url.PathEscape(gameID)+"/analysis?"+query.Encode(), nil)
}
func (s *server) http(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	contentType := ""
	if len(body) > 0 {
		contentType = "application/json"
	}
	return s.httpWithContentType(ctx, method, path, body, contentType)
}

func (s *server) httpWithContentType(ctx context.Context, method, path string, body []byte, contentType string) ([]byte, error) {
	started := time.Now()
	s.logger.Debug("Go Practice APIを呼び出します", "method", method, "path", path, "request_bytes", len(body))
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("--server-urlまたはAPIのパスが不正です")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("X-Go-Practice-Agent", "1")
	res, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.logger.Error("Go Practice APIへ接続できません", "method", method, "path", path, "error", err)
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Timeout() {
			return nil, fmt.Errorf("アプリの応答を待つ時間が上限を超えました")
		}
		return nil, fmt.Errorf("アプリ（%s）へ接続できません", s.baseURL)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("アプリの応答を読み込めません")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var apiError struct {
			Error string `json:"error"`
		}
		if res.StatusCode >= 500 {
			s.logger.Error("Go Practice APIが処理に失敗しました", "method", method, "path", path, "status", res.StatusCode, "response_bytes", len(data))
		}
		if json.Unmarshal(data, &apiError) == nil && apiError.Error != "" {
			return nil, errors.New(apiError.Error)
		}
		return nil, fmt.Errorf("アプリの要求に失敗しました（HTTP %d）", res.StatusCode)
	}
	s.logger.Debug("Go Practice APIの応答を受信しました", "method", method, "path", path, "status", res.StatusCode, "response_bytes", len(data), "duration", time.Since(started))
	if len(data) == 0 {
		return []byte(`{}`), nil
	}
	return data, nil
}
func toolResult(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError}
}
