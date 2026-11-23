package apicontract

import (
	"strings"
	"testing"

	"go-practice/internal/game"
)

func Test文字数の境界を共有する(t *testing.T) {
	maximumGameName := string(make([]rune, 80))
	tooLongGameName := string(make([]rune, 81))
	if !gameNameWithinLimit(maximumGameName) || gameNameWithinLimit(tooLongGameName) || ValidateGameName(maximumGameName) != nil || ValidateGameName(tooLongGameName) == nil || ValidateRequiredGameName("") == nil {
		t.Fatal("対局名の文字数制限が不正です")
	}
	maximumVariationTitle := string(make([]rune, 80))
	tooLongVariationTitle := string(make([]rune, 81))
	if !variationTitleWithinLimit(maximumVariationTitle) || variationTitleWithinLimit(tooLongVariationTitle) || ValidateVariationTitle(maximumVariationTitle) != nil || ValidateVariationTitle(tooLongVariationTitle) == nil {
		t.Fatal("変化名の文字数制限が不正です")
	}
	maximumNote := string(make([]rune, 4000))
	tooLongNote := string(make([]rune, 4001))
	if !noteWithinLimit(maximumNote) || noteWithinLimit(tooLongNote) || ValidateNote(maximumNote) != nil || ValidateNote(tooLongNote) == nil {
		t.Fatal("メモの文字数制限が不正です")
	}
}

func Test読み込んだ棋譜の文章を正規化して上限を検査する(t *testing.T) {
	snapshot := game.Snapshot{
		RootProperties: []game.SGFProperty{{Ident: "C", Values: []string{"一\t二\n\r三"}}},
		TreeNodes: []game.TreeNode{{
			ID:             "root",
			VariationTitle: " 変化\v名 ",
		}},
	}
	normalized := NormalizeSnapshotUserText(snapshot)
	if got := normalized.RootProperties[0].Values[0]; got != "一 二\n三" {
		t.Fatalf("メモ = %q", got)
	}
	if got := normalized.TreeNodes[0].VariationTitle; got != "変化 名" {
		t.Fatalf("変化名 = %q", got)
	}
	if err := ValidateSnapshotUserText(normalized); err != nil {
		t.Fatalf("上限内の文章を拒否した：%v", err)
	}

	normalized.RootProperties[0].Values[0] = strings.Repeat("字", 4001)
	err := ValidateSnapshotUserText(normalized)
	field, maximum, ok := SnapshotTextLimit(err)
	if !ok || field != "メモ" || maximum != 4000 || !IsInputSizeLimit(err) {
		t.Fatalf("メモの上限超過を識別できない：%v", err)
	}
}

func Test解析手順の境界を共有する(t *testing.T) {
	if err := ValidateAnalysisSequenceLength(12); err != nil {
		t.Fatalf("上限の解析手順を拒否した：%v", err)
	}
	if err := ValidateAnalysisSequenceLength(13); err == nil {
		t.Fatal("上限を超えた解析手順を受け付けた")
	}
	if err := ValidateVariationSequenceLength(1); err != nil {
		t.Fatalf("1手の変化を拒否した：%v", err)
	}
	for _, length := range []int{0, 13} {
		if err := ValidateVariationSequenceLength(length); err == nil {
			t.Fatalf("%d手の変化を受け付けた", length)
		}
	}
}

func Test削除対象の局面一覧を検査する(t *testing.T) {
	for _, test := range []struct {
		values []string
		valid  bool
	}{
		{values: nil},
		{values: []string{"n1", "n1"}},
		{values: []string{"n1", "n2"}, valid: true},
	} {
		err := ValidateDeletionNodeIDs(test.values)
		if test.valid != (err == nil) {
			t.Fatalf("削除対象%vの検査結果 = %v", test.values, err)
		}
	}
}

func Test対局作成と解析の値域を共有する(t *testing.T) {
	tests := []struct {
		name    string
		valid   func() error
		invalid func() error
	}{
		{name: "あなたの石の色", valid: func() error { return ValidateHumanColor("B") }, invalid: func() error { return ValidateHumanColor("黒") }},
		{name: "探索数", valid: func() error { return ValidateAnalysisVisits(2000) }, invalid: func() error { return ValidateAnalysisVisits(2001) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.valid(); err != nil {
				t.Fatalf("対応値を拒否した：%v", err)
			}
			if err := test.invalid(); err == nil {
				t.Fatal("対応しない値を受け付けた")
			}
		})
	}
}
