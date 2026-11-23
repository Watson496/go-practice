package api

import (
	"net/http"
	"strings"
	"time"

	"go-practice/internal/game"
)

const agentRequestHeader = "X-Go-Practice-Agent"
const uiInteractionLifetime = 2 * time.Minute

const (
	interactionReview          = "review"
	interactionRename          = "rename"
	interactionReorder         = "reorder"
	interactionDeleteGame      = "delete-game"
	interactionDeleteVariation = "delete-variation"
)

type uiInteraction struct {
	kind      string
	gameID    string
	nodeID    string
	nodeIDs   map[string]struct{}
	expiresAt time.Time
}

type uiInteractionRequest struct {
	Token   string   `json:"token" required:"true"`
	Kind    string   `json:"kind" required:"true"`
	GameID  string   `json:"gameId,omitempty"`
	NodeID  string   `json:"nodeId,omitempty"`
	NodeIDs []string `json:"nodeIds,omitempty"`
}

type endUIInteractionRequest struct {
	Token string `json:"token" required:"true"`
}

func (server *Server) beginUIInteraction(writer http.ResponseWriter, request *http.Request) {
	var input uiInteractionRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, decodeJSONErrorStatus(err), err.Error())
		return
	}
	if strings.TrimSpace(input.Token) == "" || len(input.Token) > 200 {
		writeError(writer, http.StatusBadRequest, "画面操作の識別情報が不正です")
		return
	}
	interaction := uiInteraction{
		kind: input.Kind, gameID: input.GameID, nodeID: input.NodeID,
		nodeIDs:   make(map[string]struct{}, len(input.NodeIDs)),
		expiresAt: server.now().Add(uiInteractionLifetime),
	}
	for _, nodeID := range input.NodeIDs {
		if strings.TrimSpace(nodeID) == "" {
			writeError(writer, http.StatusBadRequest, "操作対象の局面が不正です")
			return
		}
		interaction.nodeIDs[nodeID] = struct{}{}
	}
	switch input.Kind {
	case interactionReorder:
		if input.GameID != "" || input.NodeID != "" || len(input.NodeIDs) != 0 {
			writeError(writer, http.StatusBadRequest, "並べ替えの操作対象が不正です")
			return
		}
	case interactionReview:
		if input.GameID == "" || input.NodeID == "" || len(input.NodeIDs) != 0 {
			writeError(writer, http.StatusBadRequest, "メモ編集の対象を指定してください")
			return
		}
	case interactionRename, interactionDeleteGame:
		if input.GameID == "" || input.NodeID != "" || len(input.NodeIDs) != 0 {
			writeError(writer, http.StatusBadRequest, "対局の操作対象を指定してください")
			return
		}
	case interactionDeleteVariation:
		if input.GameID == "" || input.NodeID == "" || len(interaction.nodeIDs) == 0 {
			writeError(writer, http.StatusBadRequest, "変化の削除対象を指定してください")
			return
		}
	default:
		writeError(writer, http.StatusBadRequest, "画面操作の種類が不正です")
		return
	}
	if input.Kind != interactionReorder {
		stored, found := server.findGame(input.GameID)
		if !found {
			writeError(writer, http.StatusNotFound, gameNotFoundMessage)
			return
		}
		response, found := publishedGameResponse(stored)
		if !found {
			writeError(writer, http.StatusNotFound, gameNotFoundMessage)
			return
		}
		existing := make(map[string]struct{}, len(response.Game.TreeNodes))
		for _, node := range response.Game.TreeNodes {
			existing[node.ID] = struct{}{}
		}
		if input.NodeID != "" {
			if _, found := existing[input.NodeID]; !found {
				writeError(writer, http.StatusNotFound, positionNotFoundMessage)
				return
			}
		}
		for nodeID := range interaction.nodeIDs {
			if _, found := existing[nodeID]; !found {
				writeError(writer, http.StatusNotFound, positionNotFoundMessage)
				return
			}
		}
	}
	server.interactionMu.Lock()
	server.uiInteractions[input.Token] = interaction
	server.interactionMu.Unlock()
	if input.Kind == interactionDeleteGame || input.Kind == interactionDeleteVariation {
		stored, found := server.findGame(input.GameID)
		if found {
			stored.mu.Lock()
			if !stored.deleted.Load() {
				stored.cancelAIFlights()
			}
			stored.mu.Unlock()
		}
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) endUIInteraction(writer http.ResponseWriter, request *http.Request) {
	var input endUIInteractionRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, decodeJSONErrorStatus(err), err.Error())
		return
	}
	if strings.TrimSpace(input.Token) == "" {
		writeError(writer, http.StatusBadRequest, "画面操作の識別情報を指定してください")
		return
	}
	server.interactionMu.Lock()
	delete(server.uiInteractions, input.Token)
	server.interactionMu.Unlock()
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) rejectAgentConflict(writer http.ResponseWriter, request *http.Request, action, gameID string, nodeIDs ...string) bool {
	if request.Header.Get(agentRequestHeader) != "1" {
		return false
	}
	if !server.agentConflict(action, gameID, nodeIDs...) {
		return false
	}
	server.cancelGameAIFlights(gameID)
	writeError(writer, http.StatusConflict, "画面で操作中の対象と競合するため、AI agentの操作を実行できません")
	return true
}

// rejectAgentConflictLocked はstored.muを保持している変更処理で使う。
func (server *Server) rejectAgentConflictLocked(writer http.ResponseWriter, request *http.Request, stored *storedGame, action, gameID string, nodeIDs ...string) bool {
	if request.Header.Get(agentRequestHeader) != "1" || !server.agentConflict(action, gameID, nodeIDs...) {
		return false
	}
	stored.cancelAIFlights()
	writeError(writer, http.StatusConflict, "画面で操作中の対象と競合するため、AI agentの操作を実行できません")
	return true
}

func (server *Server) cancelGameAIFlights(gameID string) {
	stored, found := server.findGame(gameID)
	if !found {
		return
	}
	stored.mu.Lock()
	if !stored.deleted.Load() {
		stored.cancelAIFlights()
	}
	stored.mu.Unlock()
}

func (server *Server) agentConflict(action, gameID string, nodeIDs ...string) bool {
	targets := make(map[string]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if nodeID != "" {
			targets[nodeID] = struct{}{}
		}
	}
	server.interactionMu.Lock()
	conflict := false
	now := server.now()
	for token, interaction := range server.uiInteractions {
		if !interaction.expiresAt.After(now) {
			delete(server.uiInteractions, token)
			continue
		}
		if interactionConflicts(interaction, action, gameID, targets) {
			conflict = true
			break
		}
	}
	server.interactionMu.Unlock()
	return conflict
}

func mutationPathNodeIDs(snapshot game.Snapshot, originNodeID, destinationNodeID string) []string {
	parentByID := make(map[string]string, len(snapshot.TreeNodes))
	for _, node := range snapshot.TreeNodes {
		parentByID[node.ID] = node.ParentID
	}
	result := make([]string, 0)
	for nodeID := destinationNodeID; nodeID != ""; nodeID = parentByID[nodeID] {
		result = append(result, nodeID)
		if nodeID == originNodeID {
			return result
		}
	}
	return append(result, originNodeID)
}

func addedMutationNodeIDs(before, after game.Snapshot) []string {
	existing := make(map[string]struct{}, len(before.TreeNodes))
	for _, node := range before.TreeNodes {
		existing[node.ID] = struct{}{}
	}
	targets := make(map[string]struct{})
	for _, node := range after.TreeNodes {
		if _, found := existing[node.ID]; found {
			continue
		}
		for _, nodeID := range mutationPathNodeIDs(after, node.ParentID, node.ID) {
			targets[nodeID] = struct{}{}
		}
	}
	result := make([]string, 0, len(targets))
	for nodeID := range targets {
		result = append(result, nodeID)
	}
	return result
}

func interactionConflicts(interaction uiInteraction, action, gameID string, targets map[string]struct{}) bool {
	sameGame := interaction.gameID != "" && interaction.gameID == gameID
	switch interaction.kind {
	case interactionReview:
		if !sameGame {
			return false
		}
		if action == "navigate" {
			_, remainsDisplayed := targets[interaction.nodeID]
			return !remainsDisplayed
		}
		if action == "play-main" || action == "play-variation" || action == "delete-game" {
			return true
		}
		if action == "update-review" {
			_, found := targets[interaction.nodeID]
			return found
		}
		if action == "delete-variation" {
			_, found := targets[interaction.nodeID]
			return found
		}
	case interactionRename:
		return sameGame && (action == "rename" || action == "delete-game")
	case interactionReorder:
		return action == "reorder"
	case interactionDeleteGame:
		return sameGame
	case interactionDeleteVariation:
		if !sameGame {
			return false
		}
		if action == "delete-game" {
			return true
		}
		if action == "update-review" || action == "play-main" || action == "play-variation" || action == "show-variation" || action == "promote" || action == "delete-variation" {
			for nodeID := range targets {
				if _, found := interaction.nodeIDs[nodeID]; found {
					return true
				}
			}
		}
	}
	return false
}

func (server *Server) publishedMutationNode(gameID string) string {
	stored, ok := server.findGame(gameID)
	if !ok {
		return ""
	}
	response, ok := publishedGameResponse(stored)
	if !ok {
		return ""
	}
	return response.Game.CurrentNodeID
}
