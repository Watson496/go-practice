package api

import (
	"errors"

	"go-practice/internal/game"
)

// 対局の設定のうち、AIのレベルと人の石の色は、未設定にできる。
// 対局の内部では、未設定を0と空文字列で表し、JSONではnullで表す。
const unsetAILevel = 0

const unsetSettingsMessage = "AIのレベルと人の石の色を設定してください"

var errUnsetSettings = errors.New(unsetSettingsMessage)

func optionalAILevel(level int) *int {
	if level == unsetAILevel {
		return nil
	}
	return &level
}

func optionalColor(color string) *string {
	if color == "" {
		return nil
	}
	return &color
}

// validateConfiguredSettings は、着手、パス、AIの手、投了、待ったの前に、
// AIのレベルと人の石の色が設定されていることを確かめる。
func validateConfiguredSettings(level int, humanColor string) error {
	if level == unsetAILevel || humanColor == "" {
		return errUnsetSettings
	}
	return nil
}

// validateHumanAction は、設定と対局の状態から、人の着手、パス、投了を受け付けられるかを返す。
func validateHumanAction(snapshot game.Snapshot, stored *storedGame) error {
	if err := validateConfiguredSettings(stored.aiLevel, stored.humanColor); err != nil {
		return err
	}
	return game.ValidateHumanAction(snapshot, stored.humanColor)
}

// validateAIAction は、設定と対局の状態から、AIの手を受け付けられるかを返す。
func validateAIAction(snapshot game.Snapshot, stored *storedGame) error {
	if err := validateConfiguredSettings(stored.aiLevel, stored.humanColor); err != nil {
		return err
	}
	return game.ValidateAIAction(snapshot, stored.humanColor)
}

// undoTarget は、設定と対局の状態から、待ったの後に"現在の局面"とする局面を返す。
func undoTarget(snapshot game.Snapshot, stored *storedGame) (string, error) {
	if err := validateConfiguredSettings(stored.aiLevel, stored.humanColor); err != nil {
		return "", err
	}
	return game.UndoTargetNodeID(snapshot, stored.humanColor)
}
