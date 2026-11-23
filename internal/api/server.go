// Package api はGo PracticeのHTTP APIと永続化を提供する。
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go-practice/internal/ailevel"
	"go-practice/internal/apicontract"
	"go-practice/internal/game"
	"go-practice/internal/gamerecord"
	"go-practice/internal/httpboundary"
	"go-practice/internal/jsonstructure"
	"go-practice/internal/katago"
	"go-practice/internal/sgf"
)

type Server struct {
	mu                sync.RWMutex
	games             map[string]*storedGame
	gameOrder         []string
	analyzer          AIEngine
	dataDir           string
	analysisVersion   string
	activeGameID      string
	activeNodeID      string
	storageLoadErrors []storageLoadError
	interactionMu     sync.Mutex
	uiInteractions    map[string]uiInteraction
	requestSequence   atomic.Uint64
	now               func() time.Time
}

const (
	kataGoOperationTimeout         = 30 * time.Second
	maximumRequestBodySize         = 10 << 20
	userGameRecordSchemaVersion    = gamerecord.JSONSchemaVersion
	clientClosedRequestStatus      = 499
	gameNotFoundMessage            = "対局が見つかりません"
	positionNotFoundMessage        = "局面が見つかりません"
	gameSaveFailureMessage         = "対局を保存できません"
	analysisPositionChangedMessage = "解析中に局面が変更されました"
	invalidAnalysisResultMessage   = "解析結果が不正です"
	invalidAnalysisVisitsMessage   = "解析結果のvisitsが不正です"
	aiGameChangedMessage           = "AIの手を決める処理は対局の変更により中止されました"
	mainLineUpdateFailureMessage   = "主分岐を更新できません"
	gameIDCreationFailureMessage   = "対局の識別情報を作成できません"
	invalidJSONSyntaxMessage       = "JSONの文法が不正です"
	invalidJSONValueTypeMessage    = "JSONの値の型が不正です"
)

type AIEngine = katago.PositionAnalyzer

type cachedAnalysis struct {
	Version string         `json:"version"`
	Summary katago.Summary `json:"summary"`
}

type positionAnalysisResult struct {
	summary katago.Summary
	status  int
	message string
	cause   error
}

func (server *Server) analyzePosition(ctx context.Context, position game.Snapshot, visits int) positionAnalysisResult {
	analysisContext, cancel := context.WithTimeout(ctx, kataGoOperationTimeout)
	defer cancel()
	analysis, err := server.analyzer.AnalyzePosition(analysisContext, position, visits)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled) || errors.Is(analysisContext.Err(), context.Canceled):
			return positionAnalysisResult{status: http.StatusConflict, message: analysisPositionChangedMessage}
		case errors.Is(err, context.DeadlineExceeded) || errors.Is(analysisContext.Err(), context.DeadlineExceeded):
			return positionAnalysisResult{status: http.StatusGatewayTimeout, message: "局面解析が制限時間内に完了しませんでした", cause: err}
		default:
			return positionAnalysisResult{status: http.StatusBadGateway, message: "KataGoで局面を解析できません", cause: err}
		}
	}
	if err := katago.ValidateSummaryForPosition(analysis, position.BoardSize, len(position.Moves), position.CurrentPlayer); err != nil {
		message := invalidAnalysisResultMessage
		if analysis.Visits < katago.MinimumResultVisits {
			message = invalidAnalysisVisitsMessage
		}
		return positionAnalysisResult{status: http.StatusBadGateway, message: message, cause: err}
	}
	if analysis.Candidates == nil {
		analysis.Candidates = []katago.CandidateSummary{}
	}
	return positionAnalysisResult{summary: analysis, status: http.StatusOK}
}

func analysisCacheKey(nodeID string, visits int) string {
	return nodeID + ":" + strconv.Itoa(visits)
}

func parseAnalysisCacheKey(cacheKey string) (string, int, bool) {
	separator := strings.LastIndexByte(cacheKey, ':')
	if separator <= 0 {
		return "", 0, false
	}
	visits, err := strconv.Atoi(cacheKey[separator+1:])
	return cacheKey[:separator], visits, err == nil
}

type storedGame struct {
	mu                sync.Mutex
	persistenceMu     sync.Mutex
	deleted           atomic.Bool
	session           *game.Session
	aiLevel           int
	humanColor        string
	startedAt         time.Time
	importedAt        time.Time
	name              string
	analysisCacheMu   sync.RWMutex
	analysisCache     map[string]cachedAnalysis
	aiFlights         map[string]*aiMoveFlight
	analysisMu        sync.Mutex
	analysisCalls     map[string]*analysisFlight
	publishedResponse atomic.Pointer[gameResponse]
	searchOnce        sync.Once
	searchContext     context.Context
	cancelSearches    context.CancelFunc
}

func (server *Server) acquireGameAnalysis(
	requestContext context.Context,
	stored *storedGame,
	cacheKey string,
	snapshot game.Snapshot,
	visits int,
) (katago.Summary, bool, *analysisFlight, uint64) {
	stored.analysisMu.Lock()
	if cached, ok := stored.cachedAnalysis(cacheKey, server.analysisVersion); ok {
		stored.analysisMu.Unlock()
		return cached.Summary, true, nil, 0
	}
	if stored.analysisCalls == nil {
		stored.analysisCalls = make(map[string]*analysisFlight)
	}
	flight, waiting := stored.analysisCalls[cacheKey]
	if waiting && flight.ctx.Err() != nil {
		delete(stored.analysisCalls, cacheKey)
		waiting = false
	}
	if !waiting {
		flightContext, cancel := context.WithCancel(stored.searchLifetime())
		flight = &analysisFlight{
			nodeID:         snapshot.CurrentNodeID,
			ctx:            flightContext,
			cancel:         cancel,
			done:           make(chan struct{}),
			waiterContexts: make(map[uint64]context.Context),
		}
		stored.analysisCalls[cacheKey] = flight
	}
	waiterID := flight.addWaiter(requestContext)
	stored.analysisMu.Unlock()
	if !waiting {
		go server.runGameAnalysis(stored, cacheKey, flight, snapshot, visits)
	}
	return katago.Summary{}, false, flight, waiterID
}

func (stored *storedGame) searchLifetime() context.Context {
	stored.searchOnce.Do(func() {
		stored.searchContext, stored.cancelSearches = context.WithCancel(context.Background())
	})
	return stored.searchContext
}

func (stored *storedGame) stopSearches() {
	stored.searchLifetime()
	stored.cancelSearches()
}

// cancelSearchesForNodes はstored.muを保持して呼び出す。
func (stored *storedGame) cancelSearchesForNodes(nodeIDs map[string]struct{}) {
	stored.analysisMu.Lock()
	for key, flight := range stored.analysisCalls {
		if _, deleted := nodeIDs[flight.nodeID]; deleted {
			flight.cancel()
			delete(stored.analysisCalls, key)
		}
	}
	stored.analysisMu.Unlock()
	for key, flight := range stored.aiFlights {
		if _, deleted := nodeIDs[flight.positionNodeID]; deleted {
			flight.cancel()
			delete(stored.aiFlights, key)
		}
	}
}

// cancelAIFlights は、その対局のAIが手を決める処理をすべて中止する。stored.muを保持して呼び出す。
func (stored *storedGame) cancelAIFlights() {
	for key, flight := range stored.aiFlights {
		flight.cancel()
		delete(stored.aiFlights, key)
	}
}

func aiMoveFlightKey(nodeID, player string) string { return nodeID + "\x00" + player }

func (stored *storedGame) lockUnlessDeleted() bool {
	stored.mu.Lock()
	if stored.deleted.Load() {
		stored.mu.Unlock()
		return false
	}
	return true
}

type aiMoveFlight struct {
	mu              sync.Mutex
	positionNodeID  string
	player          string
	ctx             context.Context
	cancel          context.CancelFunc
	nextWaiterID    uint64
	waiterContexts  map[uint64]context.Context
	applying        bool
	done            chan struct{}
	response        aiMoveResponse
	status          int
	err             string
	cause           error
	cancelledByUser bool
}

type aiMoveApplication struct {
	response aiMoveResponse
	status   int
	message  string
	cause    error
}

type analysisFlight struct {
	nodeID         string
	ctx            context.Context
	cancel         context.CancelFunc
	nextWaiterID   uint64
	waiterContexts map[uint64]context.Context
	applying       bool
	done           chan struct{}
	analysis       katago.Summary
	status         int
	err            string
	cause          error
}

func (flight *aiMoveFlight) beginApplication() bool {
	flight.mu.Lock()
	defer flight.mu.Unlock()
	for _, waiterContext := range flight.waiterContexts {
		if waiterContext.Err() == nil {
			flight.applying = true
			return true
		}
	}
	return false
}

func (flight *aiMoveFlight) cancelByUser() {
	flight.mu.Lock()
	flight.cancelledByUser = true
	flight.cancel()
	flight.mu.Unlock()
}

func (flight *aiMoveFlight) cancellationMessage() string {
	flight.mu.Lock()
	defer flight.mu.Unlock()
	if flight.cancelledByUser {
		return "AIの思考を中止しました"
	}
	return aiGameChangedMessage
}

func (flight *aiMoveFlight) addWaiter(waiterContext context.Context) (uint64, bool) {
	flight.mu.Lock()
	defer flight.mu.Unlock()
	if flight.ctx.Err() != nil {
		return 0, false
	}
	flight.nextWaiterID++
	waiterID := flight.nextWaiterID
	flight.waiterContexts[waiterID] = waiterContext
	return waiterID, true
}

func (flight *aiMoveFlight) removeWaiter(waiterID uint64) (int, bool) {
	flight.mu.Lock()
	defer flight.mu.Unlock()
	if _, exists := flight.waiterContexts[waiterID]; !exists {
		return len(flight.waiterContexts), flight.applying
	}
	delete(flight.waiterContexts, waiterID)
	remaining := len(flight.waiterContexts)
	if remaining == 0 && !flight.applying {
		flight.cancel()
	}
	return remaining, flight.applying
}

// analysisFlightの状態はstoredGame.analysisMuで保護する。
func (flight *analysisFlight) addWaiter(waiterContext context.Context) uint64 {
	flight.nextWaiterID++
	waiterID := flight.nextWaiterID
	flight.waiterContexts[waiterID] = waiterContext
	return waiterID
}

func (flight *analysisFlight) removeWaiter(waiterID uint64) (int, bool) {
	if _, exists := flight.waiterContexts[waiterID]; !exists {
		return len(flight.waiterContexts), flight.applying
	}
	delete(flight.waiterContexts, waiterID)
	return len(flight.waiterContexts), flight.applying
}

func (flight *analysisFlight) beginApplication() bool {
	for _, waiterContext := range flight.waiterContexts {
		if waiterContext.Err() == nil {
			flight.applying = true
			return true
		}
	}
	return false
}

type gameResponse struct {
	Game       game.Snapshot `json:"game"`
	AILevel    *int          `json:"aiLevel"`
	HumanColor *string       `json:"humanColor"`
	StartedAt  string        `json:"startedAt"`
	ImportedAt string        `json:"importedAt,omitempty"`
	Name       string        `json:"name,omitempty"`
	CanUndo    bool          `json:"canUndo"`
}

type createGameRequest struct {
	BoardSize  int      `json:"boardSize" required:"true"`
	Komi       *float64 `json:"komi" null:"reject" value:"komi"`
	AILevel    *int     `json:"aiLevel" null:"reject"`
	Handicap   int      `json:"handicap"`
	HumanColor *string  `json:"humanColor" null:"reject"`
}

type importGameJSONRequest = gamerecord.JSONRecord

func (stored *storedGame) response() gameResponse {
	snapshot := stored.session.Snapshot()
	_, undoErr := undoTarget(snapshot, stored)
	return gameResponse{Game: snapshot, AILevel: optionalAILevel(stored.aiLevel), HumanColor: optionalColor(stored.humanColor), StartedAt: gamerecord.FormatTime(stored.startedAt), ImportedAt: gamerecord.FormatTime(stored.importedAt), Name: stored.name, CanUndo: undoErr == nil}
}

type renameGameRequest struct {
	Name         string  `json:"name"`
	ExpectedName *string `json:"expectedName"`
}

type playMoveRequest struct {
	Coordinate string `json:"coordinate"`
}

type variationMoveRequest struct {
	Coordinate string `json:"coordinate"`
}

type emptyRequest struct{}

type updateNodeRequest struct {
	Note              *string `json:"note"`
	Important         *bool   `json:"important"`
	ExpectedNote      *string `json:"expectedNote"`
	ExpectedImportant *bool   `json:"expectedImportant"`
}

type deleteVariationRequest struct {
	ExpectedNodeIDs []string `json:"expectedNodeIds"`
}

func reviewAt(snapshot game.Snapshot, nodeID string) (string, bool, bool) {
	properties := snapshot.RootProperties
	important, found := false, nodeID == "root"
	for _, node := range snapshot.TreeNodes {
		if node.ID == nodeID {
			important, found = node.Important, true
			if nodeID != "root" {
				properties = node.Properties
			}
			break
		}
	}
	note := ""
	for _, property := range properties {
		if property.Ident == "C" && len(property.Values) > 0 {
			note = property.Values[0]
			break
		}
	}
	return note, important, found
}

type playMoveResponse struct {
	Game gameResponse `json:"game"`
}

type aiMoveResponse struct {
	AIMove game.Move    `json:"aiMove"`
	Game   gameResponse `json:"game"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func newServer(dataDir string, analyzer AIEngine) *Server {
	if analyzer == nil {
		panic("局面解析器は必須です")
	}
	server := &Server{
		games:           make(map[string]*storedGame),
		analyzer:        analyzer,
		dataDir:         dataDir,
		analysisVersion: analyzer.AnalysisVersion(),
		uiInteractions:  make(map[string]uiInteraction),
		now:             time.Now,
	}
	return server
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/games", server.createGame)
	mux.HandleFunc("GET /api/games", server.listGames)
	mux.HandleFunc("PUT /api/games/order", server.reorderGames)
	mux.HandleFunc("GET /api/storage-errors", server.listStorageErrors)
	mux.HandleFunc("PUT /api/ui/interaction", server.beginUIInteraction)
	mux.HandleFunc("DELETE /api/ui/interaction", server.endUIInteraction)
	mux.HandleFunc("POST /api/games/import", server.importGame)
	mux.HandleFunc("POST /api/games/import-json", server.importGameJSON)
	mux.HandleFunc("GET /api/games/{id}", server.getGame)
	mux.HandleFunc("GET /api/games/{id}/json", server.getGameJSON)
	mux.HandleFunc("PATCH /api/games/{id}", server.renameGame)
	mux.HandleFunc("DELETE /api/games/{id}", server.deleteGame)
	mux.HandleFunc("PATCH /api/games/{id}/settings", server.updateGameSettings)
	mux.HandleFunc("GET /api/games/{id}/sgf", server.getGameSGF)
	mux.HandleFunc("GET /api/games/{id}/analysis", server.getGameAnalysis)
	mux.HandleFunc("POST /api/games/{id}/moves", server.playMove)
	mux.HandleFunc("POST /api/games/{id}/ai-moves", server.playAIMove)
	mux.HandleFunc("DELETE /api/games/{id}/ai-moves", server.cancelAIMoves)
	mux.HandleFunc("POST /api/games/{id}/resign", server.resignGame)
	mux.HandleFunc("POST /api/games/{id}/undo", server.undoMove)
	mux.HandleFunc("POST /api/games/{id}/nodes/{nodeID}", server.navigateGameTree)
	mux.HandleFunc("PATCH /api/games/{id}/nodes/{nodeID}", server.updateGameTreeNode)
	mux.HandleFunc("DELETE /api/games/{id}/nodes/{nodeID}", server.deleteGameTreeVariation)
	mux.HandleFunc("POST /api/games/{id}/nodes/{nodeID}/main-line", server.promoteGameTreeVariation)
	mux.HandleFunc("POST /api/games/{id}/variations", server.playVariation)
	mux.HandleFunc("POST /api/agent/selection", server.setAgentSelection)
	mux.HandleFunc("POST /api/agent/navigation", server.navigateAgentPosition)
	mux.HandleFunc("GET /api/agent/context", server.getAgentContext)
	mux.HandleFunc("POST /api/agent/analyze", server.analyzeAgentPosition)
	mux.HandleFunc("POST /api/agent/variations", server.addAgentVariation)
	validPaths := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !httpboundary.ValidURLPath(request.URL.Path) {
			writeError(writer, http.StatusBadRequest, "URLのパスが不正です")
			return
		}
		mux.ServeHTTP(writer, request)
	})
	return server.logRequests(normalizeRoutingErrors(limitRequestBody(validPaths)))
}

type routingErrorResponse struct {
	http.ResponseWriter
	normalized bool
}

type errorRecorder interface {
	recordError(string)
}

type internalCauseRecorder interface {
	recordInternalCause(error)
}

func (response *routingErrorResponse) recordError(message string) {
	if recorder, ok := response.ResponseWriter.(errorRecorder); ok {
		recorder.recordError(message)
	}
}

func (response *routingErrorResponse) recordInternalCause(cause error) {
	if recorder, ok := response.ResponseWriter.(internalCauseRecorder); ok {
		recorder.recordInternalCause(cause)
	}
}

func (response *routingErrorResponse) WriteHeader(status int) {
	if strings.HasPrefix(response.Header().Get("Content-Type"), "text/plain") {
		message := ""
		normalizedStatus := status
		switch status {
		case http.StatusNotFound:
			message = "APIが見つかりません"
		case http.StatusMethodNotAllowed:
			normalizedStatus = http.StatusNotFound
			message = "APIが見つかりません"
		}
		if message != "" {
			response.Header().Del("Content-Type")
			response.normalized = true
			writeError(response.ResponseWriter, normalizedStatus, message)
			return
		}
	}
	response.ResponseWriter.WriteHeader(status)
}

func (response *routingErrorResponse) Write(data []byte) (int, error) {
	if response.normalized {
		return len(data), nil
	}
	return response.ResponseWriter.Write(data)
}

func normalizeRoutingErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		next.ServeHTTP(&routingErrorResponse{ResponseWriter: writer}, request)
	})
}

func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(writer, request.Body, maximumRequestBodySize)
		next.ServeHTTP(writer, request)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status        int
	bytes         int
	errorMessage  string
	internalCause error
}

func (writer *responseRecorder) recordError(message string) { writer.errorMessage = message }

func (writer *responseRecorder) recordInternalCause(cause error) { writer.internalCause = cause }

func (writer *responseRecorder) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *responseRecorder) Write(data []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	written, err := writer.ResponseWriter.Write(data)
	writer.bytes += written
	return written, err
}

func (server *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := server.requestSequence.Add(1)
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: writer}
		slog.Debug("HTTP要求を受信しました", "request_id", requestID, "method", request.Method, "path", request.URL.Path, "remote_addr", request.RemoteAddr)
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				recorder.recordInternalCause(fmt.Errorf("予期しない停止：%v", recovered))
				if recorder.status == 0 {
					writeError(recorder, http.StatusInternalServerError, "サーバー内部でエラーが発生しました")
				}
			}
			status := recorder.status
			if status == 0 {
				if request.Context().Err() != nil {
					status = clientClosedRequestStatus
				} else {
					status = http.StatusOK
				}
			}
			level := slog.LevelDebug
			if status >= 500 || recorder.internalCause != nil {
				level = slog.LevelError
			} else if status >= 400 {
				level = slog.LevelInfo
			}
			attributes := []any{"request_id", requestID, "method", request.Method, "path", request.URL.Path, "status", status, "bytes", recorder.bytes, "duration", time.Since(started)}
			if recorder.errorMessage != "" {
				attributes = append(attributes, "error", recorder.errorMessage)
			}
			if recorder.internalCause != nil {
				attributes = append(attributes, "cause", recorder.internalCause)
			}
			slog.Log(request.Context(), level, "HTTP要求を処理しました", attributes...)
		}()
		next.ServeHTTP(recorder, request)
	})
}

func (server *Server) playVariation(writer http.ResponseWriter, request *http.Request) {
	gameID := request.PathValue("id")
	stored, ok := server.findGame(request.PathValue("id"))
	if !ok {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	var input variationMoveRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, decodeJSONErrorStatus(err), err.Error())
		return
	}
	if !stored.lockUnlessDeleted() {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	defer stored.mu.Unlock()
	preview := stored.session.Clone()
	if _, err := preview.PlayVariationWithDeadStones(input.Coordinate, nil); err != nil {
		status := playInputErrorStatus(err)
		if status != http.StatusBadRequest && status != http.StatusNotFound {
			stored.cancelAIFlights()
		}
		writeError(writer, status, err.Error())
		return
	}
	stored.cancelAIFlights()
	before := stored.session.Clone()
	deadStones, completesTwoPasses, err := server.deadStonesForMove(request.Context(), stored.session, input.Coordinate, true)
	if err != nil {
		writeAnalysisFailure(writer, err)
		return
	}
	if _, err := stored.session.PlayVariationWithDeadStones(input.Coordinate, deadStones); err != nil {
		writeError(writer, playInputErrorStatus(err), err.Error())
		return
	}
	if completesTwoPasses {
		slog.Debug("二連続パスの死石を反映しました", "game_id", gameID, "dead_stones", len(deadStones))
	}
	after := stored.session.Snapshot()
	if server.rejectAgentConflictLocked(writer, request, stored, "play-variation", gameID, addedMutationNodeIDs(before.Snapshot(), after)...) {
		stored.session = before
		return
	}
	if err := server.persistSessionMutation(stored, before); err != nil {
		writeInternalError(writer, http.StatusInternalServerError, gameSaveFailureMessage, err)
		return
	}
	snapshot := stored.session.Snapshot()
	slog.Debug("検討中の着手を追加しました", "game_id", snapshot.ID, "node_id", snapshot.CurrentNodeID, "coordinate", input.Coordinate)
	writeJSON(writer, http.StatusOK, stored.response())
}

func (server *Server) listGames(writer http.ResponseWriter, _ *http.Request) {
	server.mu.RLock()
	storedGames := make(map[string]*storedGame, len(server.games))
	for id, stored := range server.games {
		storedGames[id] = stored
	}
	order := append([]string(nil), server.gameOrder...)
	server.mu.RUnlock()
	type listEntry struct {
		response  gameResponse
		startedAt time.Time
	}
	entries := make(map[string]listEntry, len(storedGames))
	for id, stored := range storedGames {
		if response, ok := publishedGameResponse(stored); ok {
			entries[id] = listEntry{response: response, startedAt: stored.orderingTime()}
		}
	}
	orderedEntries := make([]gameResponse, 0, len(entries))
	for _, id := range order {
		if entry, ok := entries[id]; ok {
			orderedEntries = append(orderedEntries, entry.response)
			delete(entries, id)
		}
	}
	remaining := make([]listEntry, 0, len(entries))
	for _, entry := range entries {
		remaining = append(remaining, entry)
	}
	sort.Slice(remaining, func(i, j int) bool {
		if !remaining[i].startedAt.Equal(remaining[j].startedAt) {
			return remaining[i].startedAt.After(remaining[j].startedAt)
		}
		return remaining[i].response.Game.ID > remaining[j].response.Game.ID
	})
	responses := make([]gameResponse, 0, len(entries)+len(orderedEntries))
	for _, entry := range remaining {
		responses = append(responses, entry.response)
	}
	responses = append(responses, orderedEntries...)
	writeJSON(writer, http.StatusOK, responses)
}

type reorderGamesRequest struct {
	IDs []string `json:"ids" required:"true"`
}

func (server *Server) reorderGames(writer http.ResponseWriter, request *http.Request) {
	var input reorderGamesRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, decodeJSONErrorStatus(err), err.Error())
		return
	}
	if input.IDs == nil {
		writeError(writer, http.StatusBadRequest, "並び順には対局の一覧を指定してください")
		return
	}
	if server.rejectAgentConflict(writer, request, "reorder", "") {
		return
	}
	seen := make(map[string]struct{}, len(input.IDs))
	for _, id := range input.IDs {
		if !validGameID(id) {
			writeError(writer, http.StatusBadRequest, "対局の識別情報が不正です")
			return
		}
		if _, duplicate := seen[id]; duplicate {
			writeError(writer, http.StatusBadRequest, "同じ対局を並び順へ2回指定することはできません")
			return
		}
		seen[id] = struct{}{}
	}
	server.mu.Lock()
	// 並べ替えた順は、そのとき残っている対局にだけ当てはめる。指定にない対局は、
	// まだ並べ替えていない対局として、一覧の先頭に開始日時の新しい順で並ぶ。
	merged := make([]string, 0, len(input.IDs))
	for _, id := range input.IDs {
		if _, exists := server.games[id]; exists {
			merged = append(merged, id)
		}
	}
	before := append([]string(nil), server.gameOrder...)
	server.gameOrder = merged
	if err := server.persistGameOrder(); err != nil {
		server.gameOrder = before
		server.mu.Unlock()
		writeInternalError(writer, http.StatusInternalServerError, "保存した対局の並び順を保存できません", err)
		return
	}
	server.mu.Unlock()
	slog.Debug("保存した対局を並べ替えました", "count", len(input.IDs))
	server.listGames(writer, request)
}

func (server *Server) deleteGame(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if server.rejectAgentConflict(writer, request, "delete-game", id) {
		return
	}
	stored, ok := server.findGame(id)
	if !ok {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	stored.mu.Lock()
	defer stored.mu.Unlock()
	stored.cancelAIFlights()
	server.mu.Lock()
	if current, exists := server.games[id]; !exists || current != stored {
		server.mu.Unlock()
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	stored.persistenceMu.Lock()
	defer stored.persistenceMu.Unlock()
	wasActive := server.activeGameID == id
	activeNodeID := server.activeNodeID
	previousOrder := append([]string(nil), server.gameOrder...)
	stored.deleted.Store(true)
	delete(server.games, id)
	server.gameOrder = slices.DeleteFunc(server.gameOrder, func(orderedID string) bool {
		return orderedID == id
	})
	if wasActive {
		server.activeGameID = ""
		server.activeNodeID = ""
	}
	if err := server.persistGameOrder(); err != nil {
		stored.deleted.Store(false)
		server.games[id] = stored
		server.gameOrder = previousOrder
		if wasActive {
			server.activeGameID = id
			server.activeNodeID = activeNodeID
		}
		server.mu.Unlock()
		writeInternalError(writer, http.StatusInternalServerError, "対局を削除できません", err)
		return
	}
	if err := server.removePersistedGame(id); err != nil {
		stored.deleted.Store(false)
		server.games[id] = stored
		server.gameOrder = previousOrder
		if wasActive && server.activeGameID == "" && server.activeNodeID == "" {
			server.activeGameID = id
			server.activeNodeID = activeNodeID
		}
		rollbackErr := server.persistGameOrder()
		server.mu.Unlock()
		if rollbackErr != nil {
			err = transactionRollbackError("対局の削除", err, rollbackErr)
		}
		writeInternalError(writer, http.StatusInternalServerError, "対局を削除できません", err)
		return
	}
	server.mu.Unlock()
	stored.stopSearches()
	slog.Debug("対局を一覧から削除しました", "game_id", id)
	writeJSON(writer, http.StatusOK, map[string]bool{"deleted": true})
}

func (server *Server) updateGameTreeNode(writer http.ResponseWriter, request *http.Request) {
	if server.rejectAgentConflict(writer, request, "update-review", request.PathValue("id"), request.PathValue("nodeID")) {
		return
	}
	stored, ok := server.findGame(request.PathValue("id"))
	if !ok {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	var input updateNodeRequest
	if !stored.lockUnlessDeleted() {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	_, _, found := reviewAt(stored.session.Snapshot(), request.PathValue("nodeID"))
	stored.mu.Unlock()
	if !found {
		writeError(writer, http.StatusNotFound, positionNotFoundMessage)
		return
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, decodeJSONErrorStatus(err), err.Error())
		return
	}
	if input.Note == nil || input.Important == nil || input.ExpectedNote == nil || input.ExpectedImportant == nil {
		writeError(writer, http.StatusBadRequest, "メモ、重要な局面、編集開始時の値を指定してください")
		return
	}
	note := game.NormalizeSGFPropertyValue("C", *input.Note)
	normalizedExpectedNote := game.NormalizeSGFPropertyValue("C", *input.ExpectedNote)
	input.ExpectedNote = &normalizedExpectedNote
	if err := apicontract.ValidateNote(note); err != nil {
		writeError(writer, inputValueErrorStatus(err), err.Error())
		return
	}
	if err := apicontract.ValidateNote(*input.ExpectedNote); err != nil {
		writeError(writer, inputValueErrorStatus(err), err.Error())
		return
	}
	if !stored.lockUnlessDeleted() {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	defer stored.mu.Unlock()
	currentNote, currentImportant, found := reviewAt(stored.session.Snapshot(), request.PathValue("nodeID"))
	if !found {
		writeError(writer, http.StatusNotFound, positionNotFoundMessage)
		return
	}
	if currentNote != *input.ExpectedNote || currentImportant != *input.ExpectedImportant {
		writeError(writer, http.StatusConflict, "メモは別の操作で更新されています。最新の内容を確認してください。")
		return
	}
	before := stored.session.Clone()
	if err := stored.session.UpdateNodeReview(request.PathValue("nodeID"), note, *input.Important); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if err := server.persistSessionMutation(stored, before); err != nil {
		writeInternalError(writer, http.StatusInternalServerError, gameSaveFailureMessage, err)
		return
	}
	slog.Debug("棋譜の局面のメモを更新しました", "game_id", request.PathValue("id"), "node_id", request.PathValue("nodeID"), "note_length", len([]rune(note)), "important", *input.Important)
	writeJSON(writer, http.StatusOK, stored.response())
}

func (server *Server) deleteGameTreeVariation(writer http.ResponseWriter, request *http.Request) {
	stored, ok := server.findGame(request.PathValue("id"))
	if !ok {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	published, ok := publishedGameResponse(stored)
	if !ok || !snapshotHasNode(published.Game, request.PathValue("nodeID")) {
		writeError(writer, http.StatusNotFound, positionNotFoundMessage)
		return
	}
	var input deleteVariationRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, decodeJSONErrorStatus(err), err.Error())
		return
	}
	if err := apicontract.ValidateDeletionNodeIDs(input.ExpectedNodeIDs); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if server.rejectAgentConflict(writer, request, "delete-variation", request.PathValue("id"), input.ExpectedNodeIDs...) {
		return
	}
	if !stored.lockUnlessDeleted() {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	stored.cancelAIFlights()
	before := stored.session.Clone()
	previousAnalysisCache := stored.analysisCacheEntries()
	preview := stored.session.Clone()
	actualNodeIDs, previewErr := preview.DeleteVariation(request.PathValue("nodeID"))
	if previewErr != nil {
		stored.mu.Unlock()
		writeGameTreeMutationError(writer, previewErr)
		return
	}
	if !sameNodeIDSet(input.ExpectedNodeIDs, actualNodeIDs) {
		stored.mu.Unlock()
		writeError(writer, http.StatusConflict, "削除対象の変化は別の操作で変更されています。最新の棋譜を確認してください。")
		return
	}
	stored.session = preview
	deletedNodes := make(map[string]struct{}, len(actualNodeIDs))
	for _, nodeID := range actualNodeIDs {
		deletedNodes[nodeID] = struct{}{}
	}
	retainedAnalysisCache := make(map[string]cachedAnalysis, len(previousAnalysisCache))
	for cacheKey, cached := range previousAnalysisCache {
		retainedAnalysisCache[cacheKey] = cached
	}
	for cacheKey := range retainedAnalysisCache {
		nodeID, _, valid := parseAnalysisCacheKey(cacheKey)
		if _, deleted := deletedNodes[nodeID]; valid && deleted {
			delete(retainedAnalysisCache, cacheKey)
		}
	}
	stored.replaceAnalysisCache(retainedAnalysisCache)
	if err := server.persistSessionMutation(stored, before); err != nil {
		stored.replaceAnalysisCache(previousAnalysisCache)
		stored.mu.Unlock()
		writeInternalError(writer, http.StatusInternalServerError, gameSaveFailureMessage, err)
		return
	}
	response := stored.response()
	stored.cancelSearchesForNodes(deletedNodes)
	currentNodeID := response.Game.CurrentNodeID
	server.mu.Lock()
	if server.activeGameID == request.PathValue("id") && slices.Contains(actualNodeIDs, server.activeNodeID) {
		server.activeGameID = ""
		server.activeNodeID = ""
	}
	server.mu.Unlock()
	stored.mu.Unlock()
	slog.Debug("変化を削除しました", "game_id", request.PathValue("id"), "node_id", request.PathValue("nodeID"), "current_node_id", currentNodeID, "deleted_analysis_count", len(previousAnalysisCache)-len(retainedAnalysisCache))
	writeJSON(writer, http.StatusOK, response)
}

func sameNodeIDSet(expected, actual []string) bool {
	if len(expected) != len(actual) {
		return false
	}
	seen := make(map[string]struct{}, len(expected))
	for _, nodeID := range expected {
		seen[nodeID] = struct{}{}
	}
	for _, nodeID := range actual {
		if _, ok := seen[nodeID]; !ok {
			return false
		}
	}
	return true
}

func (server *Server) promoteGameTreeVariation(writer http.ResponseWriter, request *http.Request) {
	stored, ok := server.lockStoredGame(writer, request.PathValue("id"))
	if !ok {
		return
	}
	defer stored.mu.Unlock()
	before := stored.session.Clone()
	next := stored.session.Clone()
	if err := next.PromoteVariation(request.PathValue("nodeID")); err != nil {
		if !game.IsNodeNotFound(err) {
			stored.cancelAIFlights()
		}
		writeGameTreeMutationError(writer, err)
		return
	}
	after := next.Snapshot()
	if server.rejectAgentConflictLocked(writer, request, stored, "promote", request.PathValue("id"), mutationPathNodeIDs(after, "root", after.MainLineNodeID)...) {
		return
	}
	stored.cancelAIFlights()
	stored.session = next
	if err := server.persistSessionMutation(stored, before); err != nil {
		writeInternalError(writer, http.StatusInternalServerError, gameSaveFailureMessage, err)
		return
	}
	slog.Debug("変化を主分岐へ昇格しました", "game_id", request.PathValue("id"), "node_id", request.PathValue("nodeID"))
	writeJSON(writer, http.StatusOK, stored.response())
}

func (server *Server) importGame(writer http.ResponseWriter, request *http.Request) {
	if !hasMediaType(request, "application/x-go-sgf") {
		writeError(writer, http.StatusUnsupportedMediaType, "本文の形式にはUTF-8のSGFを指定してください")
		return
	}
	contents, err := io.ReadAll(request.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(writer, http.StatusRequestEntityTooLarge, "SGFの容量が大きすぎます")
			return
		}
		writeError(writer, http.StatusBadRequest, "SGFの本文を読み込めません")
		return
	}
	id, err := newID()
	if err != nil {
		writeInternalError(writer, http.StatusInternalServerError, gameIDCreationFailureMessage, err)
		return
	}
	session, err := sgf.DecodeGameWithDeadStoneEstimator(string(contents), id, func(position game.Snapshot) ([]string, error) {
		return server.deadStonesForPosition(request.Context(), position)
	})
	if err != nil {
		if sgf.IsDeadStoneEstimationError(err) {
			if errors.Is(err, context.DeadlineExceeded) {
				writeInternalError(writer, http.StatusGatewayTimeout, "KataGoの解析が制限時間内に完了しませんでした", err)
				return
			}
			writeInternalError(writer, http.StatusBadGateway, "KataGoの解析結果を取得できません", err)
			return
		}
		writeError(writer, inputValueErrorStatus(err), sgf.PublicDecodeError(err))
		return
	}
	prepared, err := gamerecord.PrepareSGF(session, time.Now(), time.Local)
	if err != nil {
		writeError(writer, inputValueErrorStatus(err), err.Error())
		return
	}
	// 読み込んだ棋譜からは、どちらが人の手かを決めないので、AIのレベルと人の石の色は未設定にする。
	stored := &storedGame{session: prepared.Session, startedAt: prepared.StartedAt, importedAt: prepared.ImportedAt, name: prepared.Name}
	if err := server.persistAndRegisterGame(id, stored); err != nil {
		writeInternalError(writer, http.StatusInternalServerError, gameSaveFailureMessage, err)
		return
	}
	snapshot := prepared.Session.Snapshot()
	slog.Debug("SGFから対局を読み込みました", "game_id", id, "bytes", len(contents), "board_size", snapshot.BoardSize, "moves", len(snapshot.Moves), "tree_nodes", len(snapshot.TreeNodes))
	writeJSON(writer, http.StatusCreated, stored.response())
}

func (server *Server) persistAndRegisterGame(id string, stored *storedGame) error {
	if err := server.persist(stored); err != nil {
		return err
	}
	// 新しい対局は、まだ並べ替えていない対局として一覧に出るので、並び順には加えない。
	server.mu.Lock()
	server.games[id] = stored
	server.mu.Unlock()
	return nil
}

func (server *Server) importGameJSON(writer http.ResponseWriter, request *http.Request) {
	var input importGameJSONRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, decodeJSONErrorStatus(err), err.Error())
		return
	}
	id, err := newID()
	if err != nil {
		writeInternalError(writer, http.StatusInternalServerError, gameIDCreationFailureMessage, err)
		return
	}
	prepared, err := gamerecord.PrepareJSON(input, id)
	if err != nil {
		writeError(writer, inputValueErrorStatus(err), err.Error())
		return
	}
	stored := &storedGame{session: prepared.Session, aiLevel: prepared.AILevel, humanColor: prepared.HumanColor, startedAt: prepared.StartedAt, importedAt: prepared.ImportedAt, name: prepared.Name}
	if err := server.persistAndRegisterGame(id, stored); err != nil {
		writeInternalError(writer, http.StatusInternalServerError, gameSaveFailureMessage, err)
		return
	}
	snapshot := prepared.Session.Snapshot()
	slog.Debug("JSONから対局を読み込みました", "game_id", id, "schema_version", input.SchemaVersion, "board_size", snapshot.BoardSize, "moves", len(snapshot.Moves), "tree_nodes", len(snapshot.TreeNodes))
	writeJSON(writer, http.StatusCreated, stored.response())
}

func writeGameTreeMutationError(writer http.ResponseWriter, err error) {
	if game.IsInvalidTreeMutationInput(err) {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if game.IsNodeNotFound(err) {
		writeError(writer, http.StatusNotFound, positionNotFoundMessage)
		return
	}
	if game.IsInvalidTreeMutation(err) {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeInternalError(writer, http.StatusInternalServerError, "棋譜を変更できません", err)
}

func (server *Server) navigateGameTree(writer http.ResponseWriter, request *http.Request) {
	if server.rejectAgentConflict(writer, request, "navigate", request.PathValue("id"), request.PathValue("nodeID")) {
		return
	}
	stored, ok := server.lockStoredGame(writer, request.PathValue("id"))
	if !ok {
		return
	}
	defer stored.mu.Unlock()
	before := stored.session.Clone()
	next := stored.session.Clone()
	if err := next.Navigate(request.PathValue("nodeID")); err != nil {
		if game.IsNodeNotFound(err) {
			slog.Debug("表示する棋譜の局面が見つかりません", "game_id", request.PathValue("id"), "node_id", request.PathValue("nodeID"), "error", err)
			writeError(writer, http.StatusNotFound, positionNotFoundMessage)
			return
		}
		writeInternalError(writer, http.StatusInternalServerError, "局面へ移動できません", err)
		return
	}
	stored.cancelAIFlights()
	stored.session = next
	if err := server.persistSessionMutation(stored, before); err != nil {
		writeInternalError(writer, http.StatusInternalServerError, gameSaveFailureMessage, err)
		return
	}
	slog.Debug("棋譜の局面へ移動しました", "game_id", request.PathValue("id"), "node_id", request.PathValue("nodeID"))
	writeJSON(writer, http.StatusOK, stored.response())
}

func (server *Server) gameForMutationRequest(writer http.ResponseWriter, request *http.Request) (*storedGame, bool) {
	stored, ok := server.findGame(request.PathValue("id"))
	if !ok {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return nil, false
	}
	var input emptyRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, decodeJSONErrorStatus(err), err.Error())
		return nil, false
	}
	return stored, true
}

func (server *Server) lockGameForMutationRequest(writer http.ResponseWriter, request *http.Request) (*storedGame, bool) {
	stored, ok := server.gameForMutationRequest(writer, request)
	if !ok {
		return nil, false
	}
	if !stored.lockUnlessDeleted() {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return nil, false
	}
	return stored, true
}

func (server *Server) undoMove(writer http.ResponseWriter, request *http.Request) {
	gameID := request.PathValue("id")
	if server.rejectAgentConflict(writer, request, "play-main", gameID, server.publishedMutationNode(gameID)) {
		return
	}
	stored, ok := server.lockGameForMutationRequest(writer, request)
	if !ok {
		return
	}
	defer stored.mu.Unlock()
	stored.cancelAIFlights()
	before := stored.session.Clone()
	snapshot := stored.session.Snapshot()
	targetNodeID, err := undoTarget(snapshot, stored)
	if err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	for stored.session.Cursor().NodeID != targetNodeID {
		if err := stored.session.Undo(); err != nil {
			stored.session = before
			writeInternalError(writer, http.StatusInternalServerError, "待ったを適用できません", err)
			return
		}
	}
	if err := stored.session.CommitCurrentNodeToMainLine(); err != nil {
		stored.session = before
		writeInternalError(writer, http.StatusInternalServerError, mainLineUpdateFailureMessage, err)
		return
	}
	if err := server.persistSessionMutation(stored, before); err != nil {
		writeInternalError(writer, http.StatusInternalServerError, gameSaveFailureMessage, err)
		return
	}
	snapshot = stored.session.Snapshot()
	slog.Debug("待ったを適用しました", "game_id", request.PathValue("id"), "current_node_id", snapshot.CurrentNodeID, "main_line_node_id", snapshot.MainLineNodeID, "moves", len(snapshot.Moves))
	writeJSON(writer, http.StatusOK, stored.response())
}

func (server *Server) getGameAnalysis(writer http.ResponseWriter, request *http.Request) {
	stored, ok := server.findGame(request.PathValue("id"))
	if !ok {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	visits := apicontract.DefaultAnalysisVisits
	if value := request.URL.Query().Get("visits"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			writeError(writer, http.StatusBadRequest, apicontract.AnalysisVisitsTypeError().Error())
			return
		}
		if err := apicontract.ValidateAnalysisVisits(parsed); err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		visits = parsed
	}
	response, ok := publishedGameResponse(stored)
	if !ok {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	nodeID := response.Game.CurrentNodeID
	requestedNodeID, nodeIDSpecified := request.URL.Query()["nodeId"]
	if nodeIDSpecified && (len(requestedNodeID) != 1 || strings.TrimSpace(requestedNodeID[0]) == "") {
		writeError(writer, http.StatusBadRequest, "棋譜の局面には空でない文字列を指定してください")
		return
	}
	if nodeIDSpecified {
		nodeID = requestedNodeID[0]
		if !snapshotHasNode(response.Game, nodeID) {
			slog.Debug("解析する棋譜の局面が見つかりません", "game_id", request.PathValue("id"), "node_id", nodeID)
			writeError(writer, http.StatusNotFound, positionNotFoundMessage)
			return
		}
	}
	cacheKey := analysisCacheKey(nodeID, visits)
	if cached, ok := stored.cachedAnalysis(cacheKey, server.analysisVersion); ok {
		slog.Debug("保存済みの局面解析を使用します", "game_id", response.Game.ID, "node_id", nodeID, "visits", visits)
		writeJSON(writer, http.StatusOK, cached.Summary)
		return
	}
	analysisSession, err := game.RestoreSession(response.Game)
	if err != nil {
		writeInternalError(writer, http.StatusInternalServerError, "対局を読み取れません", err)
		return
	}
	if nodeID != response.Game.CurrentNodeID {
		if err := analysisSession.Navigate(nodeID); err != nil {
			slog.Debug("解析する棋譜の局面が見つかりません", "game_id", request.PathValue("id"), "node_id", nodeID, "error", err)
			writeError(writer, http.StatusNotFound, positionNotFoundMessage)
			return
		}
	}
	snapshot := analysisSession.Snapshot()
	if cached, found, flight, waiterID := server.acquireGameAnalysis(request.Context(), stored, cacheKey, snapshot, visits); found {
		writeJSON(writer, http.StatusOK, cached)
	} else {
		server.waitForAnalysisFlight(writer, request, stored, cacheKey, flight, waiterID)
	}
}

func (server *Server) runGameAnalysis(stored *storedGame, cacheKey string, flight *analysisFlight, snapshot game.Snapshot, visits int) {
	complete := func(status int, message string, analysis katago.Summary, cause error) {
		stored.analysisMu.Lock()
		flight.status, flight.err, flight.analysis, flight.cause = status, message, analysis, cause
		if stored.analysisCalls[cacheKey] == flight {
			delete(stored.analysisCalls, cacheKey)
		}
		close(flight.done)
		stored.analysisMu.Unlock()
		flight.cancel()
	}
	slog.Debug("局面解析を実行します", "game_id", snapshot.ID, "node_id", snapshot.CurrentNodeID, "visits", visits)
	result := server.analyzePosition(flight.ctx, snapshot, visits)
	if result.message != "" {
		if stored.deleted.Load() {
			complete(http.StatusNotFound, gameNotFoundMessage, katago.Summary{}, nil)
			return
		}
		published, available := publishedGameResponse(stored)
		if !available || !snapshotHasNode(published.Game, snapshot.CurrentNodeID) {
			complete(http.StatusNotFound, positionNotFoundMessage, katago.Summary{}, nil)
			return
		}
		complete(result.status, result.message, katago.Summary{}, result.cause)
		return
	}
	analysis := result.summary
	stored.mu.Lock()
	stored.analysisMu.Lock()
	applicationStarted := flight.beginApplication()
	stored.analysisMu.Unlock()
	if stored.deleted.Load() {
		stored.mu.Unlock()
		complete(http.StatusNotFound, gameNotFoundMessage, katago.Summary{}, nil)
		return
	}
	published, available := publishedGameResponse(stored)
	if !available || !snapshotHasNode(published.Game, snapshot.CurrentNodeID) {
		stored.mu.Unlock()
		complete(http.StatusNotFound, positionNotFoundMessage, katago.Summary{}, nil)
		return
	}
	if !applicationStarted {
		stored.mu.Unlock()
		complete(clientClosedRequestStatus, "局面解析の要求が中止されました", katago.Summary{}, nil)
		return
	}
	currentSession := stored.session.Clone()
	if err := currentSession.Navigate(snapshot.CurrentNodeID); err != nil {
		stored.mu.Unlock()
		complete(http.StatusConflict, analysisPositionChangedMessage, katago.Summary{}, nil)
		return
	}
	nextAnalysisCache := stored.analysisCacheEntries()
	if nextAnalysisCache == nil {
		nextAnalysisCache = make(map[string]cachedAnalysis)
	}
	nextAnalysisCache[cacheKey] = cachedAnalysis{Version: server.analysisVersion, Summary: analysis}
	slog.Debug("局面解析結果を一時保存しました", "game_id", snapshot.ID, "node_id", snapshot.CurrentNodeID, "visits", visits, "candidates", len(analysis.Candidates))
	if err := server.persistWithAnalysisCache(stored, nextAnalysisCache); err != nil {
		stored.mu.Unlock()
		complete(http.StatusInternalServerError, "局面解析を保存できません", katago.Summary{}, err)
		return
	}
	stored.replaceAnalysisCache(nextAnalysisCache)
	stored.mu.Unlock()
	complete(http.StatusOK, "", analysis, nil)
}

func snapshotHasNode(snapshot game.Snapshot, nodeID string) bool {
	for _, node := range snapshot.TreeNodes {
		if node.ID == nodeID {
			return true
		}
	}
	return false
}

func releaseAnalysisWaiter(stored *storedGame, cacheKey string, flight *analysisFlight, waiterID uint64) (int, bool) {
	stored.analysisMu.Lock()
	remaining, applying := flight.removeWaiter(waiterID)
	if remaining == 0 && !applying {
		flight.cancel()
	}
	if stored.analysisCalls[cacheKey] == flight {
		if remaining == 0 && !applying {
			delete(stored.analysisCalls, cacheKey)
		}
	}
	stored.analysisMu.Unlock()
	return remaining, applying
}

func writeAnalysisFlightResult(writer http.ResponseWriter, flight *analysisFlight) {
	if flight.err != "" {
		writeFlightError(writer, flight.status, flight.err, flight.cause)
		return
	}
	writeJSON(writer, http.StatusOK, flight.analysis)
}

func (server *Server) waitForAnalysisFlight(writer http.ResponseWriter, request *http.Request, stored *storedGame, cacheKey string, flight *analysisFlight, waiterID uint64) {
	select {
	case <-request.Context().Done():
		remaining, applying := releaseAnalysisWaiter(stored, cacheKey, flight, waiterID)
		if remaining == 0 && applying {
			<-flight.done
			writeAnalysisFlightResult(writer, flight)
			return
		}
		writer.WriteHeader(clientClosedRequestStatus)
	case <-flight.done:
		releaseAnalysisWaiter(stored, cacheKey, flight, waiterID)
		writeAnalysisFlightResult(writer, flight)
	}
}

func (server *Server) getGameSGF(writer http.ResponseWriter, request *http.Request) {
	response, ok := server.publishedResponseForRequest(writer, request)
	if !ok {
		return
	}
	contents, err := sgf.Encode(response.Game)
	if err != nil {
		writeInternalError(writer, http.StatusInternalServerError, "SGFを書き出せません", err)
		return
	}
	if len(contents) > maximumRequestBodySize {
		writeError(writer, http.StatusUnprocessableEntity, fmt.Sprintf("書き出すSGFは%dMiB以下にしてください", maximumRequestBodySize>>20))
		return
	}
	slog.Debug("対局をSGFへ書き出しました", "game_id", response.Game.ID, "moves", len(response.Game.Moves), "tree_nodes", len(response.Game.TreeNodes), "bytes", len(contents))
	writer.Header().Set("Content-Type", "application/x-go-sgf; charset=utf-8")
	writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="go-practice-%s.sgf"`, response.Game.ID))
	writer.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(writer, contents)
}

func (server *Server) resignGame(writer http.ResponseWriter, request *http.Request) {
	gameID := request.PathValue("id")
	if server.rejectAgentConflict(writer, request, "play-main", gameID, server.publishedMutationNode(gameID)) {
		return
	}
	stored, ok := server.lockGameForMutationRequest(writer, request)
	if !ok {
		return
	}
	defer stored.mu.Unlock()
	stored.cancelAIFlights()
	before := stored.session.Clone()
	snapshot := stored.session.Snapshot()
	if err := validateHumanAction(snapshot, stored); err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	if err := stored.session.Resign(); err != nil {
		stored.session = before
		writeError(writer, playInputErrorStatus(err), err.Error())
		return
	}
	if err := stored.session.CommitCurrentNodeToMainLine(); err != nil {
		stored.session = before
		writeInternalError(writer, http.StatusInternalServerError, mainLineUpdateFailureMessage, err)
		return
	}
	if err := server.persistSessionMutation(stored, before); err != nil {
		writeInternalError(writer, http.StatusInternalServerError, gameSaveFailureMessage, err)
		return
	}
	snapshot = stored.session.Snapshot()
	slog.Debug("対局を投了で終了しました", "game_id", request.PathValue("id"), "current_player", snapshot.CurrentPlayer, "result", snapshot.Result)
	writeJSON(writer, http.StatusOK, stored.response())
}

func (server *Server) createGame(writer http.ResponseWriter, request *http.Request) {
	var input createGameRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, decodeJSONErrorStatus(err), err.Error())
		return
	}
	komi := game.DefaultKomi
	if input.Handicap > 0 {
		komi = game.DefaultHandicapKomi
	}
	if input.Komi != nil {
		komi = *input.Komi
	}
	aiLevel := apicontract.DefaultAILevel
	if input.AILevel != nil {
		aiLevel = *input.AILevel
	}
	if err := ailevel.Validate(aiLevel); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	humanColor := apicontract.DefaultHumanColor
	if input.HumanColor != nil {
		humanColor = *input.HumanColor
	}
	if err := apicontract.ValidateHumanColor(humanColor); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	id, err := newID()
	if err != nil {
		writeInternalError(writer, http.StatusInternalServerError, gameIDCreationFailureMessage, err)
		return
	}
	session, err := game.NewConfiguredSession(id, input.BoardSize, komi, input.Handicap)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	startedAt := time.Now()
	if err := session.SetSGFProperties("root", []game.SGFProperty{
		{Ident: "DT", Values: []string{startedAt.Format("2006-01-02")}},
	}); err != nil {
		writeInternalError(writer, http.StatusInternalServerError, "開始日時を記録できません", err)
		return
	}
	stored := &storedGame{session: session, aiLevel: aiLevel, humanColor: humanColor, startedAt: startedAt}
	if err := server.persistAndRegisterGame(id, stored); err != nil {
		writeInternalError(writer, http.StatusInternalServerError, gameSaveFailureMessage, err)
		return
	}
	slog.Debug("対局を作成しました", "game_id", id, "board_size", input.BoardSize, "komi", komi, "handicap", input.Handicap, "ai_level", aiLevel, "human_color", humanColor)
	writeJSON(writer, http.StatusCreated, stored.response())
}

func (server *Server) getGame(writer http.ResponseWriter, request *http.Request) {
	response, ok := server.publishedResponseForRequest(writer, request)
	if !ok {
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (server *Server) getGameJSON(writer http.ResponseWriter, request *http.Request) {
	response, ok := server.publishedResponseForRequest(writer, request)
	if !ok {
		return
	}
	contents, err := marshalJSON(exportGameRecord(response), "")
	if err != nil {
		writeInternalError(writer, http.StatusInternalServerError, "JSONを書き出せません", err)
		return
	}
	if len(contents) > maximumRequestBodySize {
		writeError(writer, http.StatusUnprocessableEntity, fmt.Sprintf("書き出すJSONは%dMiB以下にしてください", maximumRequestBodySize>>20))
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(contents)
}

func (server *Server) publishedResponseForRequest(writer http.ResponseWriter, request *http.Request) (gameResponse, bool) {
	stored, ok := server.findGame(request.PathValue("id"))
	if ok {
		if response, available := publishedGameResponse(stored); available {
			return response, true
		}
	}
	writeError(writer, http.StatusNotFound, gameNotFoundMessage)
	return gameResponse{}, false
}

func publishedGameResponse(stored *storedGame) (gameResponse, bool) {
	response := stored.publishedResponse.Load()
	if response != nil && !stored.deleted.Load() {
		return *response, true
	}
	return gameResponse{}, false
}

func exportGameRecord(response gameResponse) importGameJSONRequest {
	return exportGameRecordValues(response.Game, response.AILevel, response.HumanColor, response.StartedAt, response.ImportedAt, response.Name)
}

func exportGameRecordValues(snapshot game.Snapshot, aiLevel *int, humanColor *string, recordedAt, importedAt, name string) importGameJSONRequest {
	var aiColor *string
	if humanColor != nil {
		aiColor = optionalColor(game.OtherColor(*humanColor))
	}
	return importGameJSONRequest{
		SchemaVersion: userGameRecordSchemaVersion, RecordedAt: recordedAt, ImportedAt: importedAt, Name: name,
		HumanColor: humanColor, AIColor: aiColor, AILevel: aiLevel, Game: snapshot,
	}
}

func (server *Server) renameGame(writer http.ResponseWriter, request *http.Request) {
	if server.rejectAgentConflict(writer, request, "rename", request.PathValue("id")) {
		return
	}
	stored, ok := server.findGame(request.PathValue("id"))
	if !ok {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	var input renameGameRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, decodeJSONErrorStatus(err), err.Error())
		return
	}
	name, err := gamerecord.CanonicalName(input.Name)
	if err != nil {
		writeError(writer, inputValueErrorStatus(err), err.Error())
		return
	}
	if err := apicontract.ValidateRequiredGameName(name); err != nil {
		writeError(writer, inputValueErrorStatus(err), err.Error())
		return
	}
	if input.ExpectedName == nil {
		writeError(writer, http.StatusBadRequest, "変更前の対局名を指定してください")
		return
	}
	normalizedExpectedName, err := gamerecord.CanonicalName(*input.ExpectedName)
	if err != nil {
		writeError(writer, inputValueErrorStatus(err), err.Error())
		return
	}
	if !stored.lockUnlessDeleted() {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	defer stored.mu.Unlock()
	if stored.name != normalizedExpectedName {
		writeError(writer, http.StatusConflict, "対局名は別の操作で更新されています。最新の内容を確認してください。")
		return
	}
	previous := stored.name
	previousSession := stored.session.Clone()
	stored.name = name
	stored.session.SetGameName(name)
	if err := server.persist(stored); err != nil {
		stored.name = previous
		stored.session = previousSession
		writeInternalError(writer, http.StatusInternalServerError, "対局名を変更できません", err)
		return
	}
	slog.Debug("対局名を変更しました", "game_id", request.PathValue("id"), "name_length", len([]rune(name)))
	writeJSON(writer, http.StatusOK, stored.response())
}

type updateGameSettingsRequest struct {
	AILevel    *int    `json:"aiLevel" null:"reject"`
	HumanColor *string `json:"humanColor" null:"reject"`
}

// updateGameSettings は、対局のAIのレベルと人の石の色を変える。盤の大きさ、置き碁、コミは変えない。
// AIが手を決める処理の間に変えたときは、その処理を中止してから変える。
func (server *Server) updateGameSettings(writer http.ResponseWriter, request *http.Request) {
	if server.rejectAgentConflict(writer, request, "update-settings", request.PathValue("id")) {
		return
	}
	var input updateGameSettingsRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, decodeJSONErrorStatus(err), err.Error())
		return
	}
	if input.AILevel == nil && input.HumanColor == nil {
		writeError(writer, http.StatusBadRequest, "AIのレベルか人の石の色を指定してください")
		return
	}
	aiLevel, humanColor, err := gamerecord.Settings(input.AILevel, input.HumanColor)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	stored, ok := server.lockStoredGame(writer, request.PathValue("id"))
	if !ok {
		return
	}
	defer stored.mu.Unlock()
	stored.cancelAIFlights()
	previousAILevel, previousHumanColor := stored.aiLevel, stored.humanColor
	if input.AILevel != nil {
		stored.aiLevel = aiLevel
	}
	if input.HumanColor != nil {
		stored.humanColor = humanColor
	}
	if err := server.persist(stored); err != nil {
		stored.aiLevel, stored.humanColor = previousAILevel, previousHumanColor
		writeInternalError(writer, http.StatusInternalServerError, "対局の設定を変更できません", err)
		return
	}
	slog.Debug("対局の設定を変更しました", "game_id", request.PathValue("id"), "ai_level", stored.aiLevel, "human_color", stored.humanColor)
	writeJSON(writer, http.StatusOK, stored.response())
}

func (server *Server) playMove(writer http.ResponseWriter, request *http.Request) {
	gameID := request.PathValue("id")
	if server.rejectAgentConflict(writer, request, "play-main", gameID, server.publishedMutationNode(gameID)) {
		return
	}
	stored, ok := server.findGame(request.PathValue("id"))
	if !ok {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	var input playMoveRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, decodeJSONErrorStatus(err), err.Error())
		return
	}
	if strings.TrimSpace(input.Coordinate) == "" {
		writeError(writer, http.StatusBadRequest, "着手座標を指定してください")
		return
	}
	if !stored.lockUnlessDeleted() {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	before := stored.session.Clone()
	snapshot := stored.session.Snapshot()
	if err := validateHumanAction(snapshot, stored); err != nil {
		stored.cancelAIFlights()
		stored.mu.Unlock()
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	preview := stored.session.Clone()
	if _, err := preview.PlayWithDeadStones(input.Coordinate, nil); err != nil {
		status := playInputErrorStatus(err)
		if status != http.StatusBadRequest && status != http.StatusNotFound {
			stored.cancelAIFlights()
		}
		stored.mu.Unlock()
		writeError(writer, status, err.Error())
		return
	}
	stored.cancelAIFlights()
	deadStones, completesTwoPasses, deadStoneErr := server.deadStonesForMove(request.Context(), stored.session, input.Coordinate, false)
	if deadStoneErr != nil {
		stored.mu.Unlock()
		writeAnalysisFailure(writer, deadStoneErr)
		return
	}
	move, err := stored.session.PlayWithDeadStones(input.Coordinate, deadStones)
	if err != nil {
		stored.session = before
		stored.mu.Unlock()
		writeError(writer, playInputErrorStatus(err), err.Error())
		return
	}
	if completesTwoPasses {
		slog.Debug("二連続パスの死石を反映しました", "game_id", gameID, "dead_stones", len(deadStones))
	}
	after := stored.session.Snapshot()
	if server.rejectAgentConflictLocked(writer, request, stored, "play-main", gameID, mutationPathNodeIDs(after, snapshot.CurrentNodeID, after.CurrentNodeID)...) {
		stored.session = before
		stored.mu.Unlock()
		return
	}
	slog.Debug("あなたの着手を適用しました", "game_id", request.PathValue("id"), "color", move.Color, "coordinate", move.Coordinate, "captured", len(move.Captured))
	if err := stored.session.CommitCurrentNodeToMainLine(); err != nil {
		stored.session = before
		stored.mu.Unlock()
		writeInternalError(writer, http.StatusInternalServerError, mainLineUpdateFailureMessage, err)
		return
	}
	response := playMoveResponse{}
	if err := server.persistSessionMutation(stored, before); err != nil {
		stored.mu.Unlock()
		writeInternalError(writer, http.StatusInternalServerError, gameSaveFailureMessage, err)
		return
	}
	response.Game = stored.response()
	if response.Game.Game.Status != "playing" {
		slog.Debug("対局が終了しました", "game_id", request.PathValue("id"), "status", response.Game.Game.Status, "result", response.Game.Game.Result)
	}
	stored.mu.Unlock()
	writeJSON(writer, http.StatusOK, response)
}

func (server *Server) playAIMove(writer http.ResponseWriter, request *http.Request) {
	gameID := request.PathValue("id")
	if server.rejectAgentConflict(writer, request, "play-main", gameID, server.publishedMutationNode(gameID)) {
		return
	}
	stored, ok := server.gameForMutationRequest(writer, request)
	if !ok {
		return
	}
	if err := request.Context().Err(); err != nil {
		writer.WriteHeader(clientClosedRequestStatus)
		return
	}
	stored.mu.Lock()
	if stored.deleted.Load() {
		stored.mu.Unlock()
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	snapshot := stored.session.Snapshot()
	if err := validateAIAction(snapshot, stored); err != nil {
		stored.mu.Unlock()
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	aiLevel := stored.aiLevel
	var flight *aiMoveFlight
	var waiterID uint64
	started := false
	flightKey := aiMoveFlightKey(snapshot.CurrentNodeID, snapshot.CurrentPlayer)
	if stored.aiFlights == nil {
		stored.aiFlights = make(map[string]*aiMoveFlight)
	}
	for {
		flight = stored.aiFlights[flightKey]
		if flight == nil {
			flightCtx, cancel := context.WithCancel(stored.searchLifetime())
			flight = &aiMoveFlight{positionNodeID: snapshot.CurrentNodeID, player: snapshot.CurrentPlayer, ctx: flightCtx, cancel: cancel, done: make(chan struct{}), waiterContexts: make(map[uint64]context.Context)}
			stored.aiFlights[flightKey] = flight
			started = true
		}
		var joined bool
		waiterID, joined = flight.addWaiter(request.Context())
		if joined {
			break
		}
		if stored.aiFlights[flightKey] == flight {
			delete(stored.aiFlights, flightKey)
		}
		started = false
	}
	stored.mu.Unlock()
	if started {
		go func() {
			result := server.applyAIMove(flight, stored, snapshot, aiLevel)
			flight.cancel()
			stored.mu.Lock()
			flight.response, flight.status, flight.err, flight.cause = result.response, result.status, result.message, result.cause
			if stored.aiFlights[flightKey] == flight {
				delete(stored.aiFlights, flightKey)
			}
			close(flight.done)
			stored.mu.Unlock()
		}()
	}
	select {
	case <-request.Context().Done():
		remaining, applying := flight.removeWaiter(waiterID)
		if remaining == 0 && applying {
			<-flight.done
			stored.mu.Lock()
			status, message, response, cause := flight.status, flight.err, flight.response, flight.cause
			stored.mu.Unlock()
			if message != "" {
				writeFlightError(writer, status, message, cause)
				return
			}
			writeJSON(writer, http.StatusOK, response)
			return
		}
		stored.mu.Lock()
		if remaining == 0 {
			if stored.aiFlights[flightKey] == flight {
				delete(stored.aiFlights, flightKey)
			}
		}
		stored.mu.Unlock()
		writer.WriteHeader(clientClosedRequestStatus)
		return
	case <-flight.done:
		stored.mu.Lock()
		flight.removeWaiter(waiterID)
		status, message, response, cause := flight.status, flight.err, flight.response, flight.cause
		stored.mu.Unlock()
		if message != "" {
			writeFlightError(writer, status, message, cause)
			return
		}
		writeJSON(writer, http.StatusOK, response)
		return
	}
}

func (server *Server) cancelAIMoves(writer http.ResponseWriter, request *http.Request) {
	stored, ok := server.findGame(request.PathValue("id"))
	if !ok {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	stored.mu.Lock()
	if stored.deleted.Load() {
		stored.mu.Unlock()
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	for key, flight := range stored.aiFlights {
		flight.cancelByUser()
		delete(stored.aiFlights, key)
	}
	stored.mu.Unlock()
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) applyAIMove(flight *aiMoveFlight, stored *storedGame, snapshot game.Snapshot, aiLevel int) aiMoveApplication {
	failure := func(status int, message string, cause error) aiMoveApplication {
		return aiMoveApplication{status: status, message: message, cause: cause}
	}
	ctx := flight.ctx
	aiCoordinate, decision, err := server.selectAIMove(ctx, snapshot, aiLevel)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return failure(http.StatusConflict, flight.cancellationMessage(), nil)
		}
		if stored.deleted.Load() {
			return failure(http.StatusNotFound, gameNotFoundMessage, nil)
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return failure(http.StatusGatewayTimeout, "AIの思考が制限時間内に完了しませんでした", err)
		}
		if errors.Is(err, katago.ErrNoPlayableCandidate) {
			return failure(http.StatusBadGateway, "KataGoの候補手からAIの着手を選べません", err)
		}
		return failure(http.StatusBadGateway, "KataGoの解析結果からAIの着手を選べません", err)
	}
	if ctx.Err() != nil {
		return failure(http.StatusConflict, flight.cancellationMessage(), nil)
	}
	stored.mu.Lock()
	defer stored.mu.Unlock()
	if stored.deleted.Load() {
		return failure(http.StatusNotFound, gameNotFoundMessage, nil)
	}
	if ctx.Err() != nil {
		return failure(http.StatusConflict, flight.cancellationMessage(), nil)
	}
	if !flight.beginApplication() {
		return failure(clientClosedRequestStatus, "AIの着手要求が中止されました", nil)
	}
	latest := stored.session.Snapshot()
	if latest.MainLineNodeID != snapshot.MainLineNodeID {
		return failure(http.StatusConflict, aiGameChangedMessage, nil)
	}
	before := stored.session.Clone()
	if latest.CurrentNodeID != snapshot.CurrentNodeID {
		if err := stored.session.Navigate(snapshot.CurrentNodeID); err != nil {
			stored.session = before
			return failure(http.StatusConflict, aiGameChangedMessage, err)
		}
	}
	deadStones, completesTwoPasses, deadStoneErr := server.deadStonesForMove(ctx, stored.session, aiCoordinate, false)
	if deadStoneErr != nil {
		stored.session = before
		if errors.Is(deadStoneErr, context.DeadlineExceeded) {
			return failure(http.StatusGatewayTimeout, "KataGoの解析が制限時間内に完了しませんでした", deadStoneErr)
		}
		return failure(http.StatusBadGateway, "KataGoの解析結果を取得できません", deadStoneErr)
	}
	aiMove, err := stored.session.PlayWithDeadStones(aiCoordinate, deadStones)
	if err != nil {
		stored.session = before
		if game.IsTreeLimit(err) {
			return failure(http.StatusUnprocessableEntity, err.Error(), nil)
		}
		return failure(http.StatusBadGateway, "AIが不正な手を選びました", err)
	}
	if completesTwoPasses {
		slog.Debug("二連続パスの死石を反映しました", "game_id", snapshot.ID, "dead_stones", len(deadStones))
	}
	if err := stored.session.SetCurrentNodeAIDecision(decision); err != nil {
		stored.session = before
		return failure(http.StatusInternalServerError, "AIの判断を記録できません", err)
	}
	if err := stored.session.CommitCurrentNodeToMainLine(); err != nil {
		stored.session = before
		return failure(http.StatusInternalServerError, mainLineUpdateFailureMessage, err)
	}
	if err := server.persistSessionMutation(stored, before); err != nil {
		return failure(http.StatusInternalServerError, gameSaveFailureMessage, err)
	}
	response := stored.response()
	slog.Debug("AIの着手を適用しました", "game_id", snapshot.ID, "color", aiMove.Color, "coordinate", aiMove.Coordinate, "captured", len(aiMove.Captured), "status", response.Game.Status, "result", response.Game.Result)
	return aiMoveApplication{response: aiMoveResponse{AIMove: aiMove, Game: response}, status: http.StatusOK}
}

func (server *Server) selectAIMove(ctx context.Context, position game.Snapshot, level int) (string, game.AIDecision, error) {
	ctx, cancel := context.WithTimeout(ctx, kataGoOperationTimeout)
	defer cancel()
	selection, err := katago.SelectMoveDetails(ctx, server.analyzer, position, level)
	if err != nil {
		return "", game.AIDecision{}, err
	}
	if selection.AnalysisVisits < katago.MinimumResultVisits {
		return "", game.AIDecision{}, fmt.Errorf("AIのvisitsが不正です")
	}
	if strings.TrimSpace(selection.EngineMove) == "" || strings.TrimSpace(selection.SelectedMove) == "" {
		return "", game.AIDecision{}, fmt.Errorf("AIの着手が不正です")
	}
	candidates := selection.Candidates()
	if len(candidates) == 0 && selection.SelectedMove != "pass" {
		return "", game.AIDecision{}, fmt.Errorf("AIの候補手がありません")
	}
	return selection.SelectedMove, game.AIDecision{
		Level: level, Visits: selection.AnalysisVisits,
		EngineMove: selection.EngineMove, SelectedMove: selection.SelectedMove,
		Candidates: candidates,
	}, nil
}

func (server *Server) deadStonesForPosition(ctx context.Context, position game.Snapshot) ([]string, error) {
	analysisContext, cancel := context.WithTimeout(ctx, kataGoOperationTimeout)
	defer cancel()
	analysis, err := server.analyzer.AnalyzePosition(analysisContext, position, 500)
	if err != nil {
		return nil, err
	}
	return game.DeadStonesFromOwnership(position, analysis.Ownership)
}

func (server *Server) deadStonesForMove(ctx context.Context, session *game.Session, coordinate string, variation bool) ([]string, bool, error) {
	position, completesTwoPasses, err := session.PreviewTwoPassMove(coordinate, variation)
	if err != nil || !completesTwoPasses {
		return nil, false, err
	}
	deadStones, err := server.deadStonesForPosition(ctx, position)
	return deadStones, true, err
}

func writeAnalysisFailure(writer http.ResponseWriter, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		writeInternalError(writer, http.StatusGatewayTimeout, "KataGoの解析が制限時間内に完了しませんでした", err)
		return
	}
	writeInternalError(writer, http.StatusBadGateway, "KataGoの解析結果を取得できません", err)
}

func (server *Server) findGame(id string) (*storedGame, bool) {
	server.mu.RLock()
	stored, ok := server.games[id]
	server.mu.RUnlock()
	return stored, ok
}

func (server *Server) lockStoredGame(writer http.ResponseWriter, id string) (*storedGame, bool) {
	stored, ok := server.findGame(id)
	if !ok || !stored.lockUnlessDeleted() {
		writeError(writer, http.StatusNotFound, gameNotFoundMessage)
		return nil, false
	}
	return stored, true
}

func decodeJSON(request *http.Request, target any) error {
	if !hasMediaType(request, "application/json") {
		return errUnsupportedMediaType
	}
	if err := decodeStrictJSON(request.Body, target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errRequestBodyTooLarge
		}
		var input *jsonInputError
		if errors.As(err, &input) {
			return input
		}
		return fmt.Errorf("JSONが不正です：%w", err)
	}
	return nil
}

func hasMediaType(request *http.Request, expected string) bool {
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != expected {
		return false
	}
	charset, specified := parameters["charset"]
	return !specified || strings.EqualFold(charset, "utf-8")
}

func decodeJSONErrorStatus(err error) int {
	if errors.Is(err, errRequestBodyTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	if errors.Is(err, errUnsupportedMediaType) {
		return http.StatusUnsupportedMediaType
	}
	return http.StatusBadRequest
}

func inputValueErrorStatus(err error) int {
	if apicontract.IsInputSizeLimit(err) || sgf.IsInputSizeLimit(err) || game.IsTreeLimit(err) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func playInputErrorStatus(err error) int {
	if game.IsTreeLimit(err) {
		return http.StatusUnprocessableEntity
	}
	if game.IsInvalidMoveInput(err) {
		return http.StatusBadRequest
	}
	return http.StatusConflict
}

var errRequestBodyTooLarge = errors.New("JSONの容量が大きすぎます")
var errUnsupportedMediaType = errors.New("本文の形式にはUTF-8のJSONを指定してください")

func decodeStrictJSON(reader io.Reader, target any) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return err
		}
		return &jsonInputError{message: "JSONの本文を読み込めません"}
	}
	if err := jsonstructure.ValidateEncoding(data); err != nil {
		return &jsonInputError{message: err.Error()}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return publicJSONDecodeError(err, data)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return &jsonInputError{message: "JSONは1つだけ指定してください"}
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return &jsonInputError{message: "JSONの最上位にはオブジェクトを指定してください"}
	}
	if err := validateJSONKeys(raw, reflect.TypeOf(target)); err != nil {
		return publicJSONDecodeError(err, raw)
	}
	valueDecoder := json.NewDecoder(bytes.NewReader(raw))
	valueDecoder.DisallowUnknownFields()
	if err := valueDecoder.Decode(target); err != nil {
		return publicJSONDecodeError(err, data)
	}
	return nil
}

type jsonInputError struct{ message string }

func (err *jsonInputError) Error() string { return err.message }

func publicJSONDecodeError(err error, source []byte) error {
	sourceLength := len(source)
	if len(bytes.TrimSpace(source)) == 0 {
		return &jsonInputError{message: "JSONの本文が空です"}
	}
	var input *jsonInputError
	if errors.As(err, &input) {
		return input
	}
	var publicInput interface{ PublicInputMessage() string }
	if errors.As(err, &publicInput) {
		return &jsonInputError{message: "JSONの" + publicInput.PublicInputMessage()}
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		trimmed := bytes.TrimSpace(source)
		if syntax.Offset >= int64(sourceLength) && (len(trimmed) == 0 || (trimmed[len(trimmed)-1] != '}' && trimmed[len(trimmed)-1] != ']')) {
			return &jsonInputError{message: "JSONが途中で終わっています"}
		}
		return &jsonInputError{message: fmt.Sprintf("JSONの%dバイト目に文法上の誤りがあります", syntax.Offset)}
	}
	var valueType *json.UnmarshalTypeError
	if errors.As(err, &valueType) {
		if valueType.Field != "" {
			return &jsonInputError{message: fmt.Sprintf("JSONの項目%qの値の型が不正です", valueType.Field)}
		}
		return &jsonInputError{message: invalidJSONValueTypeMessage}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return &jsonInputError{message: "JSONが途中で終わっています"}
	}
	return &jsonInputError{message: invalidJSONSyntaxMessage}
}

func validateJSONKeys(data []byte, targetType reflect.Type) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := jsonstructure.ValidateValue(decoder); err != nil {
		var structureError *jsonstructure.Error
		if errors.As(err, &structureError) {
			return &jsonInputError{message: err.Error()}
		}
		return err
	}
	return validateJSONValueShape(data, targetType, "")
}

func validateJSONValueShape(raw json.RawMessage, targetType reflect.Type, path string) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	for targetType.Kind() == reflect.Pointer {
		targetType = targetType.Elem()
	}
	if targetType == reflect.TypeOf(json.RawMessage(nil)) {
		return nil
	}
	switch targetType.Kind() {
	case reflect.Struct:
		if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
			if path == "" {
				return &jsonInputError{message: "JSONの最上位にはオブジェクトを指定してください"}
			}
			return &jsonInputError{message: fmt.Sprintf("JSONの項目%qにはオブジェクトを指定してください", path)}
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return &jsonInputError{message: invalidJSONSyntaxMessage}
		}
		fields := make(map[string]reflect.StructField)
		for index := 0; index < targetType.NumField(); index++ {
			field := targetType.Field(index)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				fields[name] = field
			}
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := object[key]
			fieldPath := key
			if path != "" {
				fieldPath = path + "." + key
			}
			field, ok := fields[key]
			if !ok {
				return &jsonInputError{message: fmt.Sprintf("JSONに不明な項目%qがあります", fieldPath)}
			}
			fieldType := field.Type
			kind := fieldType.Kind()
			nullable := kind == reflect.Pointer || kind == reflect.Map || kind == reflect.Slice || kind == reflect.Interface
			if field.Tag.Get("null") == "reject" {
				nullable = false
			}
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) && !nullable {
				return &jsonInputError{message: fmt.Sprintf("JSONの項目%qにはnullを指定できません", fieldPath)}
			}
			if err := validateJSONValueShape(value, fieldType, fieldPath); err != nil {
				var inputError *jsonInputError
				if errors.As(err, &inputError) && inputError.message == invalidJSONValueTypeMessage {
					return &jsonInputError{message: fmt.Sprintf("JSONの項目%qの値の型が不正です", fieldPath)}
				}
				return err
			}
			if field.Tag.Get("value") == "komi" && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) && !game.IsValidKomiLiteral(string(value)) {
				return &jsonInputError{message: fmt.Sprintf("JSONの項目%qのコミは%.0f以上%.0f以下の0.5目単位で指定してください", fieldPath, game.MinimumKomi, game.MaximumKomi)}
			}
		}
		for index := 0; index < targetType.NumField(); index++ {
			field := targetType.Field(index)
			if field.Tag.Get("required") != "true" {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if _, exists := object[name]; exists {
				continue
			}
			fieldPath := name
			if path != "" {
				fieldPath = path + "." + name
			}
			return &jsonInputError{message: fmt.Sprintf("JSONに必須の項目%qがありません", fieldPath)}
		}
	case reflect.Slice, reflect.Array:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return &jsonInputError{message: fmt.Sprintf("JSONの項目%qには配列を指定してください", path)}
		}
		for index, value := range values {
			elementPath := fmt.Sprintf("%s[%d]", path, index)
			if err := validateJSONValueShape(value, targetType.Elem(), elementPath); err != nil {
				return err
			}
		}
	case reflect.Map:
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return &jsonInputError{message: fmt.Sprintf("JSONの項目%qにはオブジェクトを指定してください", path)}
		}
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			elementPath := key
			if path != "" {
				elementPath = path + "." + key
			}
			if err := validateJSONValueShape(values[key], targetType.Elem(), elementPath); err != nil {
				return err
			}
		}
	default:
		value := reflect.New(targetType).Interface()
		if err := json.Unmarshal(raw, value); err != nil {
			return &jsonInputError{message: invalidJSONValueTypeMessage}
		}
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, message string) {
	if recorder, ok := writer.(errorRecorder); ok {
		recorder.recordError(message)
	}
	writeJSON(writer, status, errorResponse{Error: message})
}

func writeInternalError(writer http.ResponseWriter, status int, message string, cause error) {
	var tooLarge *gameRecordTooLargeError
	if errors.As(cause, &tooLarge) {
		writeError(writer, http.StatusUnprocessableEntity, tooLarge.Error())
		return
	}
	var sgfTooLarge *gameSGFTooLargeError
	if errors.As(cause, &sgfTooLarge) {
		writeError(writer, http.StatusUnprocessableEntity, sgfTooLarge.Error())
		return
	}
	if recorder, ok := writer.(internalCauseRecorder); ok {
		recorder.recordInternalCause(cause)
	}
	writeError(writer, status, message)
}

func writeFlightError(writer http.ResponseWriter, status int, message string, cause error) {
	if cause != nil && status >= http.StatusInternalServerError {
		writeInternalError(writer, status, message, cause)
		return
	}
	writeError(writer, status, message)
}

func newID() (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func validGameID(id string) bool {
	if len(id) != 24 || id != strings.ToLower(id) {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
