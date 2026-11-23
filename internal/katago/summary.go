package katago

import (
	"fmt"
	"math"

	"go-practice/internal/coordinate"
)

type Summary struct {
	TurnNumber    int                `json:"turnNumber"`
	CurrentPlayer string             `json:"currentPlayer"`
	ScoreLead     float64            `json:"scoreLead"`
	Winrate       float64            `json:"winrate"`
	Visits        int                `json:"visits"`
	Candidates    []CandidateSummary `json:"candidates"`
	Ownership     []float64          `json:"ownership,omitempty"`
}

type CandidateSummary struct {
	Move      string   `json:"move"`
	Visits    int      `json:"visits"`
	ScoreLead float64  `json:"scoreLead"`
	Winrate   float64  `json:"winrate"`
	Prior     float64  `json:"prior"`
	PV        []string `json:"pv"`
}

func summarize(result result, candidateLimit int) (Summary, error) {
	root, ok := result["rootInfo"].(map[string]any)
	if !ok {
		return Summary{}, fmt.Errorf("解析結果に全体情報がありません")
	}
	moveInfos, ok := result["moveInfos"].([]any)
	if !ok {
		return Summary{}, fmt.Errorf("解析結果に候補手情報がありません")
	}
	if candidateLimit <= 0 || candidateLimit > len(moveInfos) {
		candidateLimit = len(moveInfos)
	}

	turnNumber, err := requiredInteger(result, "turnNumber", 0)
	if err != nil {
		return Summary{}, err
	}
	currentPlayer, err := requiredString(root, "currentPlayer")
	if err != nil || (currentPlayer != "B" && currentPlayer != "W") {
		return Summary{}, fmt.Errorf("解析結果の手番が不正です")
	}
	scoreLead, err := requiredFiniteNumber(root, "scoreLead")
	if err != nil {
		return Summary{}, err
	}
	winrate, err := requiredRangedNumber(root, "winrate", 0, 1)
	if err != nil {
		return Summary{}, err
	}
	visits, err := requiredInteger(root, "visits", MinimumResultVisits)
	if err != nil {
		return Summary{}, err
	}
	ownership, err := optionalNumberSlice(result, "ownership", -1, 1)
	if err != nil {
		return Summary{}, err
	}
	summary := Summary{
		TurnNumber: turnNumber, CurrentPlayer: currentPlayer, ScoreLead: scoreLead,
		Winrate: winrate, Visits: visits, Ownership: ownership,
		Candidates: make([]CandidateSummary, 0, candidateLimit),
	}
	for _, raw := range moveInfos[:candidateLimit] {
		info, ok := raw.(map[string]any)
		if !ok {
			return Summary{}, fmt.Errorf("解析候補の一覧にオブジェクト以外の値があります")
		}
		candidate, err := candidateSummary(info)
		if err != nil {
			return Summary{}, err
		}
		if currentPlayer == "W" {
			candidate.ScoreLead = -candidate.ScoreLead
			candidate.Winrate = 1 - candidate.Winrate
		}
		summary.Candidates = append(summary.Candidates, candidate)
	}
	if err := validateSummary(summary, 0); err != nil {
		return Summary{}, err
	}
	return summary, nil
}

// validateSummary は解析結果を画面表示や保存へ渡せるか検証する。
// boardPointsが正なら、ownershipの交点数も検証する。
func validateSummary(summary Summary, boardPoints int) error {
	if summary.TurnNumber < 0 {
		return fmt.Errorf("解析結果の手数が不正です")
	}
	if summary.CurrentPlayer != "B" && summary.CurrentPlayer != "W" {
		return fmt.Errorf("解析結果の手番が不正です")
	}
	if !finite(summary.ScoreLead) {
		return fmt.Errorf("解析結果の目差が不正です")
	}
	if !normalizedRange(summary.Winrate, 0) {
		return fmt.Errorf("解析結果の勝率が不正です")
	}
	if summary.Visits < MinimumResultVisits {
		return fmt.Errorf("解析結果のvisitsが不正です：%d", summary.Visits)
	}
	if boardPoints > 0 && len(summary.Ownership) != boardPoints {
		return fmt.Errorf("解析結果のownershipに含まれる交点数が不正です")
	}
	for _, ownership := range summary.Ownership {
		if !normalizedRange(ownership, -1) {
			return fmt.Errorf("解析結果のownershipが不正です")
		}
	}
	boardSize := 0
	if boardPoints > 0 {
		boardSize = int(math.Sqrt(float64(boardPoints)))
		if boardSize*boardSize != boardPoints {
			return fmt.Errorf("解析対象の盤の交点数が不正です")
		}
	}
	for _, candidate := range summary.Candidates {
		if err := validateCandidateSummary(candidate, boardSize); err != nil {
			return err
		}
	}
	return nil
}

// ValidateSummaryForPosition は解析結果が要求した局面と一致するか検証する。
func ValidateSummaryForPosition(summary Summary, boardSize, turnNumber int, currentPlayer string) error {
	if err := validateSummary(summary, boardSize*boardSize); err != nil {
		return err
	}
	if summary.TurnNumber != turnNumber {
		return fmt.Errorf("解析結果の手数が対象局面と一致しません")
	}
	if summary.CurrentPlayer != currentPlayer {
		return fmt.Errorf("解析結果の手番が対象局面と一致しません")
	}
	return nil
}

func validateCandidateSummary(candidate CandidateSummary, boardSize int) error {
	if candidate.Move == "" || candidate.Visits < 0 || !finite(candidate.ScoreLead) || !normalizedRange(candidate.Winrate, 0) || !normalizedRange(candidate.Prior, 0) {
		return fmt.Errorf("解析結果の候補手が不正です")
	}
	if boardSize > 0 && !validAnalysisMove(candidate.Move, boardSize) {
		return fmt.Errorf("解析結果の候補手の座標が不正です：%s", candidate.Move)
	}
	for _, move := range candidate.PV {
		if move == "" {
			return fmt.Errorf("解析結果の候補手の読み筋が不正です")
		}
		if boardSize > 0 && !validAnalysisMove(move, boardSize) {
			return fmt.Errorf("解析結果の読み筋の座標が不正です：%s", move)
		}
	}
	return nil
}

func validAnalysisMove(move string, boardSize int) bool {
	if move == "pass" {
		return true
	}
	_, ok := coordinate.Point(move, boardSize)
	return ok
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func normalizedRange(value, minimum float64) bool {
	return finite(value) && value >= minimum && value <= 1
}

func candidateSummary(info map[string]any) (CandidateSummary, error) {
	move, err := requiredString(info, "move")
	if err != nil {
		return CandidateSummary{}, err
	}
	visits, err := requiredInteger(info, "visits", 0)
	if err != nil {
		return CandidateSummary{}, err
	}
	scoreLead, err := requiredFiniteNumber(info, "scoreLead")
	if err != nil {
		return CandidateSummary{}, err
	}
	winrate, err := requiredRangedNumber(info, "winrate", 0, 1)
	if err != nil {
		return CandidateSummary{}, err
	}
	prior, err := requiredRangedNumber(info, "prior", 0, 1)
	if err != nil {
		return CandidateSummary{}, err
	}
	pv, err := requiredStringSlice(info, "pv")
	if err != nil {
		return CandidateSummary{}, err
	}
	return CandidateSummary{Move: move, Visits: visits, ScoreLead: scoreLead, Winrate: winrate, Prior: prior, PV: pv}, nil
}

func requiredString(values map[string]any, key string) (string, error) {
	value, ok := values[key].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("解析結果の%sに文字列がありません", key)
	}
	return value, nil
}

func requiredFiniteNumber(values map[string]any, key string) (float64, error) {
	value, ok := values[key].(float64)
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("解析結果の%sに有限の数がありません", key)
	}
	return value, nil
}

func requiredRangedNumber(values map[string]any, key string, minimum, maximum float64) (float64, error) {
	value, err := requiredFiniteNumber(values, key)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("解析結果の%sは%g以上%g以下で指定してください", key, minimum, maximum)
	}
	return value, nil
}

func requiredInteger(values map[string]any, key string, minimum int) (int, error) {
	value, err := requiredFiniteNumber(values, key)
	if err != nil || value != math.Trunc(value) || value < float64(minimum) || value > float64(math.MaxInt) {
		return 0, fmt.Errorf("解析結果の%sは%d以上の整数で指定してください", key, minimum)
	}
	return int(value), nil
}

func optionalNumberSlice(object map[string]any, key string, minimum, maximum float64) ([]float64, error) {
	raw, exists := object[key]
	if !exists {
		return nil, nil
	}
	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("解析結果の%sには数値の配列を指定してください", key)
	}
	result := make([]float64, 0, len(values))
	for _, value := range values {
		number, ok := value.(float64)
		if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < minimum || number > maximum {
			return nil, fmt.Errorf("解析結果の%sには%g以上%g以下の数だけを指定してください", key, minimum, maximum)
		}
		result = append(result, number)
	}
	return result, nil
}

func requiredStringSlice(object map[string]any, key string) ([]string, error) {
	values, ok := object[key].([]any)
	if !ok {
		return nil, fmt.Errorf("解析結果の%sには文字列の配列を指定してください", key)
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("解析結果の%sには空でない文字列だけを指定してください", key)
		}
		result = append(result, text)
	}
	return result, nil
}
