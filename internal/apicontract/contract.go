// Package apicontract はHTTP APIとMCPが共有する入力制約を定義する。
package apicontract

import (
	"errors"
	"fmt"

	"go-practice/internal/ailevel"
	"go-practice/internal/game"
)

type inputSizeLimitError struct {
	message string
}

type snapshotTextLimitError struct {
	field   string
	maximum int
}

func (err *snapshotTextLimitError) Error() string {
	return fmt.Sprintf("%sは%d文字以下にしてください", err.field, err.maximum)
}

// SnapshotTextLimit は棋譜中の文章が上限を超えた誤りから対象と上限を返す。
func SnapshotTextLimit(err error) (string, int, bool) {
	var limit *snapshotTextLimitError
	if !errors.As(err, &limit) {
		return "", 0, false
	}
	return limit.field, limit.maximum, true
}

func (err *inputSizeLimitError) Error() string { return err.message }

// IsInputSizeLimit は入力中の文字列や要素数が上限を超えた誤りかを返す。
func IsInputSizeLimit(err error) bool {
	var limit *inputSizeLimitError
	var textLimit *snapshotTextLimitError
	return errors.As(err, &limit) || errors.As(err, &textLimit)
}

// NormalizeSnapshotUserText はJSONから読み込んだ棋譜の文章を正規化する。
func NormalizeSnapshotUserText(snapshot game.Snapshot) game.Snapshot {
	normalizeProperties := func(properties []game.SGFProperty) {
		for propertyIndex := range properties {
			for valueIndex := range properties[propertyIndex].Values {
				properties[propertyIndex].Values[valueIndex] = game.NormalizeSGFPropertyValue(properties[propertyIndex].Ident, properties[propertyIndex].Values[valueIndex])
			}
		}
	}
	normalizeProperties(snapshot.RootProperties)
	for nodeIndex := range snapshot.TreeNodes {
		normalizeProperties(snapshot.TreeNodes[nodeIndex].Properties)
		snapshot.TreeNodes[nodeIndex].VariationTitle = game.NormalizeVariationTitle(snapshot.TreeNodes[nodeIndex].VariationTitle)
	}
	return snapshot
}

// ValidateSnapshotUserText は棋譜中のメモと変化名の長さを検証する。
func ValidateSnapshotUserText(snapshot game.Snapshot) error {
	validateProperties := func(properties []game.SGFProperty) error {
		for _, property := range properties {
			if property.Ident != "C" {
				continue
			}
			for _, value := range property.Values {
				if ValidateNote(value) != nil {
					return &snapshotTextLimitError{field: "メモ", maximum: maximumNoteLength}
				}
			}
		}
		return nil
	}
	if err := validateProperties(snapshot.RootProperties); err != nil {
		return err
	}
	for _, node := range snapshot.TreeNodes {
		if ValidateVariationTitle(node.VariationTitle) != nil {
			return &snapshotTextLimitError{field: "変化名", maximum: maximumVariationTitleLength}
		}
		if err := validateProperties(node.Properties); err != nil {
			return err
		}
	}
	return nil
}

func newInputSizeLimitError(format string, values ...any) error {
	return &inputSizeLimitError{message: fmt.Sprintf(format, values...)}
}

const (
	MinimumAnalysisVisits       = 1
	MaximumAnalysisVisits       = 2000
	DefaultAnalysisVisits       = 100
	DefaultAgentAnalysisVisits  = 200
	DefaultAILevel              = MinimumAILevel
	DefaultHandicap             = 0
	DefaultHumanColor           = "B"
	MaximumAnalysisSequenceSize = 12
	maximumNoteLength           = 4000
	maximumGameNameLength       = 80
	maximumVariationTitleLength = 80
	MinimumAILevel              = ailevel.Minimum
	MaximumAILevel              = ailevel.Maximum
	MinimumKomi                 = game.MinimumKomi
	MaximumKomi                 = game.MaximumKomi
)

// AnalysisVisitsTypeError は探索数に整数以外を指定した誤りを返す。
func AnalysisVisitsTypeError() error {
	return errors.New("visitsには整数を指定してください")
}

func gameNameWithinLimit(name string) bool {
	return len([]rune(name)) <= maximumGameNameLength
}

// ValidateGameName は空を許容して対局名の文字数を検査する。
func ValidateGameName(name string) error {
	if !gameNameWithinLimit(name) {
		return newInputSizeLimitError("対局名は%d文字以下にしてください", maximumGameNameLength)
	}
	return nil
}

// ValidateRequiredGameName は空を許容せずに対局名の文字数を検査する。
func ValidateRequiredGameName(name string) error {
	if name == "" {
		return fmt.Errorf("対局名は1文字以上%d文字以下にしてください", maximumGameNameLength)
	}
	if !gameNameWithinLimit(name) {
		return newInputSizeLimitError("対局名は1文字以上%d文字以下にしてください", maximumGameNameLength)
	}
	return nil
}

func variationTitleWithinLimit(title string) bool {
	return len([]rune(title)) <= maximumVariationTitleLength
}

// ValidateVariationTitle は変化名の文字数を検査する。
func ValidateVariationTitle(title string) error {
	if !variationTitleWithinLimit(title) {
		return newInputSizeLimitError("変化名は%d文字以下にしてください", maximumVariationTitleLength)
	}
	return nil
}

func noteWithinLimit(note string) bool {
	return len([]rune(note)) <= maximumNoteLength
}

// ValidateNote はメモの文字数を検査する。
func ValidateNote(note string) error {
	if !noteWithinLimit(note) {
		return newInputSizeLimitError("メモは%d文字以下にしてください", maximumNoteLength)
	}
	return nil
}

func ValidateHumanColor(value string) error {
	if !game.IsColor(value) {
		return fmt.Errorf("あなたの石の色には黒または白を指定してください")
	}
	return nil
}

func ValidateAnalysisVisits(value int) error {
	if value < MinimumAnalysisVisits || value > MaximumAnalysisVisits {
		return fmt.Errorf("visitsは%d以上%d以下にしてください", MinimumAnalysisVisits, MaximumAnalysisVisits)
	}
	return nil
}

func ValidateAnalysisSequenceLength(length int) error {
	if length > MaximumAnalysisSequenceSize {
		return fmt.Errorf("解析手順は%d手以下にしてください", MaximumAnalysisSequenceSize)
	}
	return nil
}

func ValidateVariationSequenceLength(length int) error {
	if length < 1 || length > MaximumAnalysisSequenceSize {
		return fmt.Errorf("変化の手順は1手以上%d手以下にしてください", MaximumAnalysisSequenceSize)
	}
	return nil
}

func ValidateDeletionNodeIDs(nodeIDs []string) error {
	if len(nodeIDs) == 0 {
		return fmt.Errorf("確認した削除対象の局面を指定してください")
	}
	seen := make(map[string]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if _, exists := seen[nodeID]; exists {
			return fmt.Errorf("確認した削除対象の局面が重複しています")
		}
		seen[nodeID] = struct{}{}
	}
	return nil
}
