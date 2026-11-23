package katago

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"sort"
	"strings"

	"go-practice/internal/game"
)

type BeginnerPlayer struct {
	Options Options
}

// PositionAnalyzer はKataGoへ局面解析を要求する境界を表す。
type PositionAnalyzer interface {
	AnalyzePosition(context.Context, game.Snapshot, int) (Summary, error)
	AnalysisVersion() string
}

const (
	// MinimumResultVisitsは有効なKataGo解析結果に必要な探索数を表す。
	MinimumResultVisits = 1
)

func (player BeginnerPlayer) AnalysisVersion() string {
	parts := []string{
		fmt.Sprintf(
			"perspective=black;ko=%s;scoring=%s;suicide=%t;whiteHandicapBonus=%s",
			applicationRules.Ko,
			applicationRules.Scoring,
			applicationRules.Suicide,
			applicationRules.WhiteHandicapBonus,
		),
		analysisFileVersion("binary", player.Options.BinaryPath),
		analysisFileVersion("model", player.Options.ModelPath),
		analysisFileVersion("config", player.Options.ConfigPath),
		analysisTextVersion("override", player.Options.OverrideConfig),
	}
	return strings.Join(parts, "|")
}

func analysisTextVersion(kind, value string) string {
	fingerprint := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%s:%x", kind, fingerprint[:8])
}

func analysisFileVersion(kind, path string) string {
	prefix := analysisTextVersion(kind, path)
	info, err := os.Stat(path)
	if err != nil {
		return prefix + ":unavailable"
	}
	return fmt.Sprintf("%s:%d:%d", prefix, info.Size(), info.ModTime().UnixNano())
}

// SelectMoveDetails はKataGoの解析結果から初心者向けAIの着手を選ぶ。
func SelectMoveDetails(ctx context.Context, analyzer PositionAnalyzer, position game.Snapshot, level int) (AIMoveSelection, error) {
	profile, err := beginnerProfile(level)
	if err != nil {
		return AIMoveSelection{}, err
	}
	visits := profile.AnalysisVisits
	slog.Debug("AI応手の選択を開始します", "game_id", position.ID, "current_player", position.CurrentPlayer, "level", level, "visits", visits, "moves", len(position.Moves))
	summary, err := analyzer.AnalyzePosition(ctx, position, visits)
	if err != nil {
		return AIMoveSelection{}, err
	}
	moves, initialStones := positionMoves(position)
	candidateMoves := make([]string, 0, len(summary.Candidates))
	for _, candidate := range summary.Candidates {
		candidateMoves = append(candidateMoves, candidate.Move)
	}
	tactics, err := analyzeTactics(position.BoardSize, initialStones, moves, candidateMoves, position.CurrentPlayer)
	if err != nil {
		return AIMoveSelection{}, fmt.Errorf("AI候補の戦術情報を計算できません：%w", err)
	}
	moveContext := beginnerMoveContext(position)
	slog.Debug("初心者AIの候補を評価します", "game_id", position.ID, "analysis_candidates", len(summary.Candidates), "tactical_candidates", len(tactics), "profile", profile, "focus_moves", moveContext.FocusMoves)
	selection, err := selectBeginnerMoveWithContext(summary, tactics, profile, moveContext, positionSeed(position))
	if err != nil {
		return AIMoveSelection{}, err
	}
	logMoveSelection(position.ID, selection, "weighted_candidates")
	return selection, nil
}

func logMoveSelection(gameID string, selection AIMoveSelection, reason string) {
	slog.Debug("AI応手を選択しました", "game_id", gameID, "reason", reason, "selected_move", selection.SelectedMove, "engine_move", selection.EngineMove, "candidates", len(selection.options), "visits", selection.AnalysisVisits, "seed", selection.seed, "options", selection.options)
}

func beginnerMoveContext(position game.Snapshot) moveSelectionContext {
	moves, _ := positionMoves(position)
	return beginnerMoveContextFromMoves(position.BoardSize, position.CurrentPlayer, moves)
}

func (player BeginnerPlayer) AnalyzePosition(ctx context.Context, position game.Snapshot, visits int) (Summary, error) {
	slog.Debug("局面解析要求を作成します", "game_id", position.ID, "node_id", position.CurrentNodeID, "current_player", position.CurrentPlayer, "moves", len(position.Moves), "visits", visits)
	if visits < MinimumResultVisits {
		return Summary{}, fmt.Errorf("visitsは%d以上にしてください", MinimumResultVisits)
	}
	moves, initialStones := positionMoves(position)
	result, err := analyze(ctx, player.Options, query{
		ID: "game-position", InitialStones: initialStones, InitialPlayer: position.InitialPlayer, Moves: moves, Rules: applicationRules, Komi: position.Komi,
		BoardXSize: position.BoardSize, BoardYSize: position.BoardSize,
		AnalyzeTurns: []int{len(moves)}, MaxVisits: visits,
		IncludeOwnership: true,
	})
	if err != nil {
		return Summary{}, err
	}
	summary, err := summarize(result, 0)
	if err != nil {
		return Summary{}, fmt.Errorf("局面の解析結果を要約できません：%w", err)
	}
	if err := ValidateSummaryForPosition(summary, position.BoardSize, len(position.Moves), position.CurrentPlayer); err != nil {
		return Summary{}, fmt.Errorf("局面の解析結果が要求と一致しません：%w", err)
	}
	return summary, nil
}

func positionMoves(position game.Snapshot) ([]move, []move) {
	moves := make([]move, 0, len(position.Moves))
	for _, played := range position.Moves {
		moves = append(moves, move{played.Color, played.Coordinate})
	}
	initialStones := make([]move, 0, len(position.SetupStones))
	for coordinate, color := range position.SetupStones {
		initialStones = append(initialStones, move{color, coordinate})
	}
	sort.Slice(initialStones, func(i, j int) bool { return initialStones[i][1] < initialStones[j][1] })
	return moves, initialStones
}

func positionSeed(position game.Snapshot) int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(position.ID))
	for _, move := range position.Moves {
		_, _ = hash.Write([]byte(move.Color + ":" + move.Coordinate + ","))
	}
	return int64(hash.Sum64())
}
