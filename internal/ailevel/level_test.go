package ailevel

import "testing"

func Test対応するAIレベルの範囲を判定する(t *testing.T) {
	for level := Minimum; level <= Maximum; level++ {
		if !Valid(level) || Validate(level) != nil {
			t.Fatalf("AIレベル%dを拒否した", level)
		}
	}
	for _, level := range []int{Minimum - 1, Maximum + 1} {
		if Valid(level) || Validate(level) == nil {
			t.Fatalf("AIレベル%dを受け付けた", level)
		}
	}
}
