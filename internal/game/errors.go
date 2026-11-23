package game

import (
	"errors"
)

type errorCategory uint8

const (
	errorCategoryInvalidMove errorCategory = iota + 1
	errorCategoryNodeNotFound
	errorCategoryInvalidTreeMutationInput
	errorCategoryInvalidTreeMutation
	errorCategoryTreeLimit
	errorCategorySetupGroupWithoutLiberties
)

type categorizedError struct {
	category errorCategory
	message  string
}

func (err *categorizedError) Error() string {
	return err.message
}

func newCategorizedError(category errorCategory, message string) error {
	return &categorizedError{category: category, message: message}
}

// IsInvalidMoveInput は、着手を試す前に拒否できる入力形式のエラーかを返す。
func IsInvalidMoveInput(err error) bool {
	return hasErrorCategory(err, errorCategoryInvalidMove)
}

// IsNodeNotFound は、指定した棋譜の局面が存在しないエラーかを返す。
func IsNodeNotFound(err error) bool {
	return hasErrorCategory(err, errorCategoryNodeNotFound)
}

// IsInvalidTreeMutationInput は、棋譜ツリーを変更する対象の指定が不正なエラーかを返す。
func IsInvalidTreeMutationInput(err error) bool {
	return hasErrorCategory(err, errorCategoryInvalidTreeMutationInput)
}

// IsInvalidTreeMutation は、現在の棋譜ツリーでは実行できない変更のエラーかを返す。
func IsInvalidTreeMutation(err error) bool {
	return hasErrorCategory(err, errorCategoryInvalidTreeMutation)
}

// IsTreeLimit は、棋譜へ局面を追加できる上限に達したエラーかを返す。
func IsTreeLimit(err error) bool {
	return hasErrorCategory(err, errorCategoryTreeLimit)
}

// IsSetupGroupWithoutLiberties は初期配置に呼吸点のない石のまとまりがあるエラーかを返す。
func IsSetupGroupWithoutLiberties(err error) bool {
	return hasErrorCategory(err, errorCategorySetupGroupWithoutLiberties)
}

func hasErrorCategory(err error, category errorCategory) bool {
	var categorized *categorizedError
	return errors.As(err, &categorized) && categorized.category == category
}
