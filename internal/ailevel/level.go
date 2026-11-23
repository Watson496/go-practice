// Package ailevel はアプリで扱うAIレベルの範囲を定義する。
package ailevel

import "fmt"

const (
	Minimum = 1
	Maximum = 4
)

// Valid はAIレベルが対応範囲にあるかを返す。
func Valid(level int) bool {
	return level >= Minimum && level <= Maximum
}

// Validate はAIレベルが対応範囲内かを検査する。
func Validate(level int) error {
	if !Valid(level) {
		return fmt.Errorf("AIレベルは%d以上%d以下にしてください", Minimum, Maximum)
	}
	return nil
}
