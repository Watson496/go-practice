package katago

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"slices"

	"go-practice/internal/ailevel"
	"go-practice/internal/coordinate"
	"go-practice/internal/game"
)

var ErrNoPlayableCandidate = errors.New("打てる候補手がありません")

type beginnerAIProfile struct {
	AnalysisVisits       int
	MaxScoreLoss         float64
	Temperature          float64
	CandidateCap         int
	MinVisits            int
	TacticalBias         float64
	TargetLoss           float64
	LossSpread           float64
	LocalityBias         float64
	LocalityMaxScoreLoss float64
}

type moveSelectionContext struct {
	BoardSize  int
	FocusMoves []string
}

const (
	passScoreLossTolerance = 0.25
	minimumCandidatePrior  = 1e-6
)

var beginnerAIProfiles = map[int]beginnerAIProfile{
	1: {AnalysisVisits: 50, MaxScoreLoss: 15, Temperature: 4, CandidateCap: 20, MinVisits: 1, TacticalBias: 1, TargetLoss: 3, LossSpread: 1.5, LocalityBias: 4, LocalityMaxScoreLoss: 5},
	2: {AnalysisVisits: 150, MaxScoreLoss: 3, Temperature: 1.2, CandidateCap: 10, MinVisits: 3, TacticalBias: 2, LocalityBias: 2, LocalityMaxScoreLoss: 2},
	3: {AnalysisVisits: 300, MaxScoreLoss: 1.5, Temperature: 0.8, CandidateCap: 8, MinVisits: 5, TacticalBias: 1.3, LocalityBias: 1.25, LocalityMaxScoreLoss: 1},
	4: {AnalysisVisits: 500, MaxScoreLoss: 0.5, Temperature: 0.45, CandidateCap: 5, MinVisits: 8, TacticalBias: 1},
}

type aiMoveOption struct {
	Move                  string
	Visits                int
	ScoreLoss             float64
	Prior                 float64
	Weight                float64
	CapturedStones        []string
	SavedOwnAtariGroups   int
	OwnGroupsStillInAtari int
	LocalityMultiplier    float64
}

type AIMoveSelection struct {
	seed             int64
	AnalysisVisits   int
	EngineMove       string
	SelectedMove     string
	CandidateRecords []game.AICandidate
	options          []aiMoveOption
}

// Candidates は選択条件を満たした候補手と、選択に用いた重みを返す。
func (selection AIMoveSelection) Candidates() []game.AICandidate {
	return slices.DeleteFunc(slices.Clone(selection.CandidateRecords), func(candidate game.AICandidate) bool {
		return candidate.Coordinate == "" || candidate.Coordinate == "pass"
	})
}

func candidateRecords(options []aiMoveOption) []game.AICandidate {
	result := make([]game.AICandidate, len(options))
	for index, option := range options {
		if option.Move == "pass" {
			continue
		}
		result[index] = game.AICandidate{Coordinate: option.Move, Weight: option.Weight}
	}
	return slices.DeleteFunc(result, func(candidate game.AICandidate) bool { return candidate.Coordinate == "" })
}

func beginnerProfile(level int) (beginnerAIProfile, error) {
	if err := ailevel.Validate(level); err != nil {
		return beginnerAIProfile{}, err
	}
	return beginnerAIProfiles[level], nil
}

func selectBeginnerMoveWithContext(summary Summary, tactics []candidateTactics, profile beginnerAIProfile, moveContext moveSelectionContext, seed int64) (AIMoveSelection, error) {
	if len(summary.Candidates) == 0 {
		return AIMoveSelection{}, fmt.Errorf("解析結果に候補手がありません")
	}
	if !game.IsColor(summary.CurrentPlayer) {
		return AIMoveSelection{}, fmt.Errorf("現在の手番%qには対応していません", summary.CurrentPlayer)
	}
	tacticsByMove := make(map[string]candidateTactics, len(tactics))
	for _, value := range tactics {
		tacticsByMove[value.Move] = value
	}
	isLegal := func(move string) bool {
		tactical, found := tacticsByMove[move]
		return !found || tactical.Legal
	}
	localityEnabled := profile.LocalityBias > 1 && moveContext.BoardSize > 0 && len(moveContext.FocusMoves) > 0
	utility := func(candidate CandidateSummary) float64 {
		return candidate.ScoreLead
	}
	// 規則で打てない候補手を先に除き、最善手、パスの判定、候補の数の上限は、残った候補手で数える。
	legalCandidates := slices.DeleteFunc(slices.Clone(summary.Candidates), func(candidate CandidateSummary) bool {
		return !isLegal(candidate.Move)
	})
	if len(legalCandidates) == 0 {
		return AIMoveSelection{}, ErrNoPlayableCandidate
	}
	engineMove := summary.Candidates[0].Move
	baseline := legalCandidates[0]
	selectPass := baseline.Move == "pass"
	for _, candidate := range legalCandidates {
		if selectPass {
			break
		}
		if candidate.Move != "pass" {
			continue
		}
		loss := utility(baseline) - utility(candidate)
		if loss <= passScoreLossTolerance {
			selectPass = true
		}
		break
	}

	limit := min(len(legalCandidates), profile.CandidateCap)
	options := make([]aiMoveOption, 0, limit)
	for _, candidate := range legalCandidates[:limit] {
		if candidate.Move == "pass" {
			continue
		}
		if candidate.Visits < profile.MinVisits && candidate.Move != baseline.Move {
			continue
		}
		loss := utility(baseline) - utility(candidate)
		if loss > profile.MaxScoreLoss {
			continue
		}
		weight := math.Pow(math.Max(candidate.Prior, minimumCandidatePrior), 1/profile.Temperature)
		if profile.TargetLoss > 0 {
			weight *= math.Exp(-math.Abs(loss-profile.TargetLoss) / profile.LossSpread)
		}
		option := aiMoveOption{
			Move: candidate.Move, Visits: candidate.Visits,
			ScoreLoss: loss, Prior: candidate.Prior, Weight: weight,
		}
		if tactical, ok := tacticsByMove[candidate.Move]; ok {
			option.CapturedStones = tactical.CapturedStones
			option.SavedOwnAtariGroups = len(tactical.SavedOwnAtariGroups)
			option.OwnGroupsStillInAtari = len(tactical.OwnGroupsStillInAtari)
			if len(tactical.CapturedStones) > 0 || len(tactical.SavedOwnAtariGroups) > 0 {
				option.Weight *= profile.TacticalBias
			}
		}
		options = append(options, option)
	}
	if len(options) == 0 {
		return AIMoveSelection{seed: seed, AnalysisVisits: summary.Visits, EngineMove: engineMove, SelectedMove: "pass", CandidateRecords: candidateRecords(options), options: options}, nil
	}
	for _, option := range options {
		if option.SavedOwnAtariGroups > 0 {
			localityEnabled = false
			break
		}
	}
	if localityEnabled {
		for index := range options {
			if options[index].ScoreLoss <= profile.LocalityMaxScoreLoss && moveIsNearFocus(options[index].Move, moveContext.FocusMoves, moveContext.BoardSize) {
				options[index].LocalityMultiplier = profile.LocalityBias
				options[index].Weight *= profile.LocalityBias
			}
		}
	}

	random := rand.New(rand.NewSource(seed))
	total := 0.0
	for _, option := range options {
		total += option.Weight
	}
	threshold := random.Float64() * total
	selected := options[len(options)-1].Move
	for _, option := range options {
		threshold -= option.Weight
		if threshold <= 0 {
			selected = option.Move
			break
		}
	}
	if selectPass {
		selected = "pass"
	}
	return AIMoveSelection{
		seed: seed, AnalysisVisits: summary.Visits, EngineMove: engineMove,
		SelectedMove: selected, CandidateRecords: candidateRecords(options), options: options,
	}, nil
}

func beginnerMoveContextFromMoves(boardSize int, currentPlayer string, moves []move) moveSelectionContext {
	moveContext := moveSelectionContext{BoardSize: boardSize}
	if len(moves) < 2 {
		return moveContext
	}
	previousAI := moves[len(moves)-2]
	latestHuman := moves[len(moves)-1]
	if previousAI[0] != currentPlayer || latestHuman[0] == currentPlayer || previousAI[1] == "pass" {
		return moveContext
	}
	moveContext.FocusMoves = append(moveContext.FocusMoves, previousAI[1])
	if moveIsNearFocus(latestHuman[1], moveContext.FocusMoves, boardSize) {
		moveContext.FocusMoves = append(moveContext.FocusMoves, latestHuman[1])
	}
	return moveContext
}

func moveIsNearFocus(move string, focuses []string, boardSize int) bool {
	x, y, ok := movePoint(move, boardSize)
	if !ok {
		return false
	}
	radius := max(2, (boardSize+3)/4)
	for _, focus := range focuses {
		focusX, focusY, ok := movePoint(focus, boardSize)
		if ok && max(abs(x-focusX), abs(y-focusY)) <= radius {
			return true
		}
	}
	return false
}

func movePoint(move string, boardSize int) (int, int, bool) {
	point, ok := coordinate.Point(move, boardSize)
	if !ok {
		return 0, 0, false
	}
	return point % boardSize, boardSize - 1 - point/boardSize, true
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
