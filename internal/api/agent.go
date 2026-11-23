package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"go-practice/internal/apicontract"
	"go-practice/internal/game"
	"go-practice/internal/katago"
)

type agentPositionError struct {
	status  int
	message string
}

func (e *agentPositionError) Error() string { return e.message }

type agentSelectionRequest struct {
	GameID string `json:"gameId"`
	NodeID string `json:"nodeId"`
}

type agentNavigationRequest struct {
	ContextID string `json:"contextId"`
	NodeID    string `json:"nodeId"`
}

type agentContext struct {
	ContextID string        `json:"contextId"`
	GameID    string        `json:"gameId"`
	NodeID    string        `json:"nodeId"`
	Position  game.Snapshot `json:"position"`
	Analysis  any           `json:"analysis,omitempty"`
}

type selectedAgentPosition struct {
	stored  *storedGame
	session *game.Session
	gameID  string
	nodeID  string
}

func (position selectedAgentPosition) contextID() string {
	return formatAgentContextID(position.gameID, position.nodeID)
}

func formatAgentContextID(gameID, nodeID string) string { return gameID + ":" + nodeID }

func (server *Server) setAgentSelection(w http.ResponseWriter, r *http.Request) {
	var input agentSelectionRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, decodeJSONErrorStatus(err), err.Error())
		return
	}
	if r.Context().Err() != nil {
		w.WriteHeader(clientClosedRequestStatus)
		return
	}
	if strings.TrimSpace(input.GameID) == "" || strings.TrimSpace(input.NodeID) == "" {
		writeError(w, http.StatusBadRequest, "対局と局面を指定してください")
		return
	}
	stored, ok := server.findGame(input.GameID)
	if !ok || !stored.lockUnlessDeleted() {
		writeError(w, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	defer stored.mu.Unlock()
	if r.Context().Err() != nil {
		w.WriteHeader(clientClosedRequestStatus)
		return
	}
	if !snapshotHasNode(stored.session.Snapshot(), input.NodeID) {
		writeError(w, http.StatusNotFound, positionNotFoundMessage)
		return
	}
	server.mu.Lock()
	server.activeGameID, server.activeNodeID = input.GameID, input.NodeID
	server.mu.Unlock()
	slog.Debug("AI agentの表示中の局面を更新しました", "game_id", input.GameID, "node_id", input.NodeID)
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) navigateAgentPosition(w http.ResponseWriter, r *http.Request) {
	r.Header.Set(agentRequestHeader, "1")
	var input agentNavigationRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, decodeJSONErrorStatus(err), err.Error())
		return
	}
	if strings.TrimSpace(input.ContextID) == "" || strings.TrimSpace(input.NodeID) == "" {
		writeError(w, http.StatusBadRequest, "表示中の局面と移動先を指定してください")
		return
	}
	server.mu.RLock()
	selectionNotified := server.activeGameID != "" && server.activeNodeID != ""
	server.mu.RUnlock()
	if !selectionNotified {
		writeError(w, http.StatusConflict, "表示中の局面が画面から通知されていません")
		return
	}
	position, err := server.selectedPosition(input.ContextID)
	if err != nil {
		writeError(w, err.status, err.Error())
		return
	}
	stored, gameID := position.stored, position.gameID
	if server.rejectAgentConflict(w, r, "navigate", gameID, input.NodeID) {
		return
	}
	stored.mu.Lock()
	if stored.deleted.Load() {
		stored.mu.Unlock()
		writeError(w, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	snapshot := stored.session.Snapshot()
	if !snapshotHasNode(snapshot, position.nodeID) || !snapshotHasNode(snapshot, input.NodeID) {
		stored.mu.Unlock()
		writeError(w, http.StatusNotFound, positionNotFoundMessage)
		return
	}
	stored.cancelAIFlights()
	before := stored.session.Clone()
	if err := stored.session.Navigate(input.NodeID); err != nil {
		stored.session = before
		stored.mu.Unlock()
		if game.IsNodeNotFound(err) {
			slog.Debug("AI agentの移動先が見つかりません", "game_id", gameID, "node_id", input.NodeID, "error", err)
			writeError(w, http.StatusNotFound, positionNotFoundMessage)
			return
		}
		writeInternalError(w, http.StatusInternalServerError, "局面へ移動できません", err)
		return
	}
	server.mu.Lock()
	if server.activeGameID != gameID {
		server.mu.Unlock()
		stored.session = before
		stored.mu.Unlock()
		writeError(w, http.StatusConflict, "画面で開いている対局が変わりました")
		return
	}
	if err := server.persistSessionMutation(stored, before); err != nil {
		server.mu.Unlock()
		stored.mu.Unlock()
		writeInternalError(w, http.StatusInternalServerError, gameSaveFailureMessage, err)
		return
	}
	snapshot = stored.session.Snapshot()
	analysis, hasAnalysis := server.cachedAgentAnalysis(stored, snapshot.CurrentNodeID)
	server.activeGameID, server.activeNodeID = gameID, input.NodeID
	server.mu.Unlock()
	stored.mu.Unlock()
	slog.Debug("AI agentから棋譜の局面へ移動しました", "game_id", gameID, "node_id", input.NodeID, "analysis_cache_hit", hasAnalysis)

	result := agentContext{ContextID: formatAgentContextID(gameID, input.NodeID), GameID: gameID, NodeID: input.NodeID, Position: snapshot}
	if hasAnalysis {
		result.Analysis = analysis
	}
	writeJSON(w, http.StatusOK, result)
}

func (server *Server) selectedPosition(contextID string) (*selectedAgentPosition, *agentPositionError) {
	server.mu.RLock()
	gameID, nodeID := server.activeGameID, server.activeNodeID
	server.mu.RUnlock()
	if contextID != "" {
		contextGameID, contextNodeID, ok := parseAgentContextID(contextID)
		if !ok {
			return nil, &agentPositionError{http.StatusBadRequest, "表示中の局面の指定が不正です"}
		}
		gameID, nodeID = contextGameID, contextNodeID
	}
	if gameID == "" {
		return nil, &agentPositionError{http.StatusConflict, "表示中の局面が画面から通知されていません"}
	}
	stored, ok := server.findGame(gameID)
	if !ok {
		return nil, &agentPositionError{http.StatusNotFound, "選択した対局が見つかりません"}
	}
	response, ok := publishedGameResponse(stored)
	if !ok {
		return nil, &agentPositionError{http.StatusNotFound, "選択した対局が見つかりません"}
	}
	session, restoreErr := game.RestoreSession(response.Game)
	if restoreErr != nil {
		slog.Error("表示中の局面を復元できません", "game_id", gameID, "node_id", nodeID, "error", restoreErr)
		return nil, &agentPositionError{http.StatusInternalServerError, "表示中の局面を読み取れません"}
	}
	if err := session.Navigate(nodeID); err != nil {
		slog.Debug("AI agentの表示中の局面が見つかりません", "game_id", gameID, "node_id", nodeID, "error", err)
		return nil, &agentPositionError{http.StatusNotFound, positionNotFoundMessage}
	}
	return &selectedAgentPosition{stored: stored, session: session, gameID: gameID, nodeID: nodeID}, nil
}

func (server *Server) getAgentContext(w http.ResponseWriter, r *http.Request) {
	position, err := server.selectedPosition(r.URL.Query().Get("contextId"))
	if err != nil {
		writeError(w, err.status, err.Error())
		return
	}
	stored, session, id := position.stored, position.session, position.contextID()
	snapshot := session.Snapshot()
	analysis, ok := server.cachedAgentAnalysis(stored, snapshot.CurrentNodeID)
	result := agentContext{ContextID: id, GameID: snapshot.ID, NodeID: snapshot.CurrentNodeID, Position: snapshot}
	if ok {
		result.Analysis = analysis
	}
	slog.Debug("AI agentへ表示中の局面を返します", "game_id", snapshot.ID, "node_id", snapshot.CurrentNodeID, "analysis_cache_hit", result.Analysis != nil)
	writeJSON(w, http.StatusOK, result)
}

func parseAgentContextID(contextID string) (string, string, bool) {
	gameID, nodeID, found := strings.Cut(contextID, ":")
	return gameID, nodeID, found && gameID != "" && nodeID != ""
}

func channelClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

func (server *Server) cachedAgentAnalysis(stored *storedGame, nodeID string) (katago.Summary, bool) {
	cached, found := stored.cachedAnalysis(analysisCacheKey(nodeID, apicontract.DefaultAnalysisVisits), server.analysisVersion)
	return cached.Summary, found
}

type agentAnalyzeRequest struct {
	ContextID string          `json:"contextId"`
	Sequence  []string        `json:"sequence"`
	MaxVisits json.RawMessage `json:"maxVisits"`
}

func (server *Server) analyzeAgentPosition(w http.ResponseWriter, r *http.Request) {
	var input agentAnalyzeRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, decodeJSONErrorStatus(err), err.Error())
		return
	}
	maxVisits := apicontract.DefaultAgentAnalysisVisits
	if input.MaxVisits != nil {
		if string(input.MaxVisits) == "null" || json.Unmarshal(input.MaxVisits, &maxVisits) != nil {
			writeError(w, http.StatusBadRequest, apicontract.AnalysisVisitsTypeError().Error())
			return
		}
	}
	if err := apicontract.ValidateAnalysisVisits(maxVisits); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := apicontract.ValidateAnalysisSequenceLength(len(input.Sequence)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(input.ContextID) == "" {
		writeError(w, http.StatusBadRequest, "表示中の局面を指定してください")
		return
	}
	position, positionErr := server.selectedPosition(input.ContextID)
	if positionErr != nil {
		writeError(w, positionErr.status, positionErr.Error())
		return
	}
	stored, session, id := position.stored, position.session, position.contextID()
	for index, move := range input.Sequence {
		if _, err := session.PlayVariation(move); err != nil {
			writeError(w, playInputErrorStatus(err), "解析手順の"+strconv.Itoa(index+1)+"手目："+err.Error())
			return
		}
	}
	gameID, nodeID := position.gameID, position.nodeID
	gameAvailable, nodeAvailable := server.agentAnalysisTargetAvailable(stored, gameID, nodeID)
	if !gameAvailable {
		writeError(w, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	if !nodeAvailable {
		writeError(w, http.StatusNotFound, positionNotFoundMessage)
		return
	}
	analysisPosition := session.Snapshot()
	normalizedSequence := make([]string, len(input.Sequence))
	for index, move := range analysisPosition.Moves[len(analysisPosition.Moves)-len(input.Sequence):] {
		normalizedSequence[index] = move.Coordinate
	}
	var analysis katago.Summary
	var status int
	var message string
	var cause error
	var canceled bool
	if len(normalizedSequence) == 0 {
		analysis, status, message, cause, canceled = server.sharedGameAnalysis(r.Context(), stored, analysisPosition, maxVisits)
	} else {
		key := "agent\x00" + id + "\x00" + strconv.Itoa(maxVisits) + "\x00" + strings.Join(normalizedSequence, "\x00")
		analysis, status, message, cause, canceled = server.sharedAgentAnalysis(r.Context(), stored, key, nodeID, analysisPosition, maxVisits)
	}
	if canceled {
		w.WriteHeader(clientClosedRequestStatus)
		return
	}
	gameAvailable, nodeAvailable = server.agentAnalysisTargetAvailable(stored, gameID, nodeID)
	if !gameAvailable {
		writeError(w, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	if !nodeAvailable {
		writeError(w, http.StatusNotFound, positionNotFoundMessage)
		return
	}
	if message != "" {
		writeInternalError(w, status, message, cause)
		return
	}
	slog.Debug("AI agentの追加解析が完了しました", "context_id", id, "sequence_length", len(input.Sequence), "candidates", len(analysis.Candidates))
	writeJSON(w, http.StatusOK, map[string]any{"contextId": id, "sequence": input.Sequence, "position": session.Snapshot(), "analysis": analysis})
}

func (server *Server) sharedGameAnalysis(requestContext context.Context, stored *storedGame, position game.Snapshot, visits int) (katago.Summary, int, string, error, bool) {
	cacheKey := analysisCacheKey(position.CurrentNodeID, visits)
	cached, found, flight, waiterID := server.acquireGameAnalysis(requestContext, stored, cacheKey, position, visits)
	if found {
		return cached, http.StatusOK, "", nil, false
	}
	select {
	case <-requestContext.Done():
		releaseAnalysisWaiter(stored, cacheKey, flight, waiterID)
		return katago.Summary{}, 0, "", nil, true
	case <-flight.done:
		releaseAnalysisWaiter(stored, cacheKey, flight, waiterID)
		return flight.analysis, flight.status, flight.err, flight.cause, false
	}
}

func (server *Server) sharedAgentAnalysis(requestContext context.Context, stored *storedGame, key, nodeID string, position game.Snapshot, visits int) (katago.Summary, int, string, error, bool) {
	stored.analysisMu.Lock()
	if stored.analysisCalls == nil {
		stored.analysisCalls = make(map[string]*analysisFlight)
	}
	for existingKey, existing := range stored.analysisCalls {
		if existingKey != key && channelClosed(existing.done) && len(existing.waiterContexts) == 0 {
			existing.cancel()
			delete(stored.analysisCalls, existingKey)
		}
	}
	flight, waiting := stored.analysisCalls[key]
	if waiting && flight.ctx.Err() != nil && !channelClosed(flight.done) {
		delete(stored.analysisCalls, key)
		waiting = false
	}
	if !waiting {
		flightContext, cancel := context.WithCancel(stored.searchLifetime())
		flight = &analysisFlight{nodeID: nodeID, ctx: flightContext, cancel: cancel, done: make(chan struct{}), waiterContexts: make(map[uint64]context.Context)}
		stored.analysisCalls[key] = flight
	}
	waiterID := flight.addWaiter(requestContext)
	stored.analysisMu.Unlock()
	if !waiting {
		go server.runAgentAnalysis(stored, flight, position, visits)
	}
	select {
	case <-requestContext.Done():
		stored.analysisMu.Lock()
		remaining, _ := flight.removeWaiter(waiterID)
		if remaining == 0 {
			flight.cancel()
			if stored.analysisCalls[key] == flight {
				delete(stored.analysisCalls, key)
			}
		}
		stored.analysisMu.Unlock()
		return katago.Summary{}, 0, "", nil, true
	case <-flight.done:
		stored.analysisMu.Lock()
		remaining, _ := flight.removeWaiter(waiterID)
		analysis, status, message, cause := flight.analysis, flight.status, flight.err, flight.cause
		if remaining == 0 && message != "" && stored.analysisCalls[key] == flight {
			delete(stored.analysisCalls, key)
			flight.cancel()
		}
		stored.analysisMu.Unlock()
		return analysis, status, message, cause, false
	}
}

func (server *Server) runAgentAnalysis(stored *storedGame, flight *analysisFlight, position game.Snapshot, visits int) {
	complete := func(status int, message string, analysis katago.Summary, cause error) {
		stored.analysisMu.Lock()
		flight.status, flight.err, flight.analysis, flight.cause = status, message, analysis, cause
		close(flight.done)
		stored.analysisMu.Unlock()
	}
	slog.Debug("AI agentから追加解析を開始します", "node_id", position.CurrentNodeID, "max_visits", visits)
	result := server.analyzePosition(flight.ctx, position, visits)
	complete(result.status, result.message, result.summary, result.cause)
}

func (server *Server) agentAnalysisTargetAvailable(stored *storedGame, gameID, nodeID string) (bool, bool) {
	server.mu.RLock()
	current, exists := server.games[gameID]
	gameAvailable := exists && current == stored && !stored.deleted.Load()
	server.mu.RUnlock()
	if !gameAvailable {
		return false, false
	}
	published, available := publishedGameResponse(stored)
	return true, available && snapshotHasNode(published.Game, nodeID)
}

type agentVariationRequest struct {
	ContextID string   `json:"contextId"`
	Moves     []string `json:"moves"`
	Title     string   `json:"title"`
}

func (server *Server) addAgentVariation(w http.ResponseWriter, r *http.Request) {
	r.Header.Set(agentRequestHeader, "1")
	var input agentVariationRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, decodeJSONErrorStatus(err), err.Error())
		return
	}
	if err := apicontract.ValidateVariationSequenceLength(len(input.Moves)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(input.ContextID) == "" {
		writeError(w, http.StatusBadRequest, "表示中の局面を指定してください")
		return
	}
	input.Title = game.NormalizeVariationTitle(input.Title)
	if err := apicontract.ValidateVariationTitle(input.Title); err != nil {
		writeError(w, inputValueErrorStatus(err), err.Error())
		return
	}
	position, positionErr := server.selectedPosition(input.ContextID)
	if positionErr != nil {
		writeError(w, positionErr.status, positionErr.Error())
		return
	}
	stored, session, id := position.stored, position.session, position.contextID()
	snapshot := session.Snapshot()
	originNodeID := snapshot.CurrentNodeID
	deadStonesByMove := make(map[int][]string)
	preview := session.Clone()
	for index, coordinate := range input.Moves {
		if _, err := preview.PreviewMove(coordinate, true); err != nil {
			writeError(w, playInputErrorStatus(err), fmt.Sprintf("変化の%d手目：%v", index+1, err))
			return
		}
		deadStones, completesTwoPasses, err := server.deadStonesForMove(r.Context(), preview, coordinate, true)
		if err != nil {
			writeAnalysisFailure(w, err)
			return
		}
		if completesTwoPasses {
			deadStonesByMove[index] = deadStones
		}
		if _, err := preview.PlayVariationWithDeadStones(coordinate, deadStones); err != nil {
			writeError(w, playInputErrorStatus(err), fmt.Sprintf("変化の%d手目：%v", index+1, err))
			return
		}
	}
	stored.mu.Lock()
	if stored.deleted.Load() {
		stored.mu.Unlock()
		writeError(w, http.StatusNotFound, gameNotFoundMessage)
		return
	}
	before := stored.session.Clone()
	nodes, err := stored.session.AddVariation(originNodeID, input.Moves, deadStonesByMove, game.AgentOrigin, input.Title)
	if err != nil {
		stored.mu.Unlock()
		if game.IsNodeNotFound(err) {
			writeError(w, http.StatusNotFound, positionNotFoundMessage)
			return
		}
		writeError(w, playInputErrorStatus(err), err.Error())
		return
	}
	if server.rejectAgentConflictLocked(w, r, stored, "show-variation", position.gameID, addedMutationNodeIDs(before.Snapshot(), stored.session.Snapshot())...) {
		stored.session = before
		stored.mu.Unlock()
		return
	}
	if err := server.persistSessionMutation(stored, before); err != nil {
		stored.mu.Unlock()
		writeInternalError(w, http.StatusInternalServerError, gameSaveFailureMessage, err)
		return
	}
	response := stored.response()
	stored.mu.Unlock()
	slog.Debug("AI agentの変化を保存しました", "context_id", id, "origin_node_id", originNodeID, "moves", len(input.Moves), "created_node_ids", nodes, "title", input.Title)
	writeJSON(w, http.StatusOK, map[string]any{"contextId": id, "nodeIds": nodes, "game": response})
}
