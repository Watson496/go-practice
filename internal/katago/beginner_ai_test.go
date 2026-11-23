package katago

import (
	"math"
	"testing"

	"go-practice/internal/ailevel"
)

func Test全AIレベルに初心者向け設定がある(t *testing.T) {
	if len(beginnerAIProfiles) != ailevel.Maximum-ailevel.Minimum+1 {
		t.Fatalf("初心者向け設定の数 = %d", len(beginnerAIProfiles))
	}
	for level := ailevel.Minimum; level <= ailevel.Maximum; level++ {
		_, err := beginnerProfile(level)
		if err != nil {
			t.Fatalf("AIレベル%dの設定を取得できない：%v", level, err)
		}
	}
	for _, level := range []int{ailevel.Minimum - 1, ailevel.Maximum + 1} {
		if _, err := beginnerProfile(level); err == nil {
			t.Fatalf("AIレベル%dの設定を取得できた", level)
		}
	}
}

func Test初心者設定はレベルごとの選択条件と一致する(t *testing.T) {
	expected := map[int]beginnerAIProfile{
		1: {AnalysisVisits: 50, MaxScoreLoss: 15, Temperature: 4, CandidateCap: 20, MinVisits: 1, TacticalBias: 1, TargetLoss: 3, LossSpread: 1.5, LocalityBias: 4, LocalityMaxScoreLoss: 5},
		2: {AnalysisVisits: 150, MaxScoreLoss: 3, Temperature: 1.2, CandidateCap: 10, MinVisits: 3, TacticalBias: 2, LocalityBias: 2, LocalityMaxScoreLoss: 2},
		3: {AnalysisVisits: 300, MaxScoreLoss: 1.5, Temperature: 0.8, CandidateCap: 8, MinVisits: 5, TacticalBias: 1.3, LocalityBias: 1.25, LocalityMaxScoreLoss: 1},
		4: {AnalysisVisits: 500, MaxScoreLoss: 0.5, Temperature: 0.45, CandidateCap: 5, MinVisits: 8, TacticalBias: 1},
	}
	for level, expectedProfile := range expected {
		profile, err := beginnerProfile(level)
		if err != nil {
			t.Fatal(err)
		}
		if profile != expectedProfile {
			t.Fatalf("AIレベル%dの設定 = %+v、期待値 = %+v", level, profile, expectedProfile)
		}
	}
}

func Test近接判定は盤の大きさに応じた境界を使う(t *testing.T) {
	tests := []struct {
		boardSize int
		inside    string
		outside   string
	}{
		{boardSize: 5, inside: "C3", outside: "D4"},
		{boardSize: 9, inside: "D4", outside: "E5"},
		{boardSize: 13, inside: "E5", outside: "F6"},
		{boardSize: 19, inside: "F6", outside: "G7"},
	}
	for _, test := range tests {
		if !moveIsNearFocus(test.inside, []string{"A1"}, test.boardSize) {
			t.Errorf("%d路盤の境界内%sを近接手として扱っていません", test.boardSize, test.inside)
		}
		if moveIsNearFocus(test.outside, []string{"A1"}, test.boardSize) {
			t.Errorf("%d路盤の境界外%sを近接手として扱いました", test.boardSize, test.outside)
		}
	}
}

func Test初心者向け選択は直前の戦いに近い妥当な着手を優先する(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Candidates: []CandidateSummary{
		{Move: "Q4", Visits: 20, ScoreLead: 1, Prior: 0.5},
		{Move: "D16", Visits: 20, ScoreLead: 0, Prior: 0.5},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 5, Temperature: 1, CandidateCap: 5, MinVisits: 1, TacticalBias: 1, LocalityBias: 4, LocalityMaxScoreLoss: 2}
	selection, err := selectBeginnerMoveWithContext(summary, nil, profile, moveSelectionContext{BoardSize: 19, FocusMoves: []string{"D17"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if selection.options[0].LocalityMultiplier != 0 || selection.options[1].LocalityMultiplier != 4 || selection.options[1].Weight <= selection.options[0].Weight {
		t.Fatalf("近接候補が優先されていない：%+v", selection.options)
	}
}

func Test初心者向け着手では局所性よりアタリの防御を優先する(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Candidates: []CandidateSummary{
		{Move: "Q4", Visits: 20, ScoreLead: 1, Prior: 0.5},
		{Move: "D16", Visits: 20, ScoreLead: 0, Prior: 0.5},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 5, Temperature: 1, CandidateCap: 5, MinVisits: 1, TacticalBias: 2, LocalityBias: 4, LocalityMaxScoreLoss: 2}
	tactics := []candidateTactics{{Move: "Q4", Legal: true, SavedOwnAtariGroups: [][]string{{"Q3"}}}}
	selection, err := selectBeginnerMoveWithContext(summary, tactics, profile, moveSelectionContext{BoardSize: 19, FocusMoves: []string{"D17"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if selection.options[1].LocalityMultiplier != 0 || selection.options[0].Weight <= selection.options[1].Weight {
		t.Fatalf("局所性が緊急の守りより優先された：%+v", selection.options)
	}
}

func Test選択対象外のアタリ救出手は局所性の重みを変えない(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Candidates: []CandidateSummary{
		{Move: "Q4", Visits: 20, ScoreLead: 1, Prior: 0.5},
		{Move: "D16", Visits: 20, ScoreLead: 0, Prior: 0.5},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 5, Temperature: 1, CandidateCap: 1, MinVisits: 1, TacticalBias: 2, LocalityBias: 4, LocalityMaxScoreLoss: 2}
	tactics := []candidateTactics{{Move: "D16", Legal: true, SavedOwnAtariGroups: [][]string{{"D15"}}}}
	selection, err := selectBeginnerMoveWithContext(summary, tactics, profile, moveSelectionContext{BoardSize: 19, FocusMoves: []string{"Q5"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.options) != 1 || selection.options[0].Move != "Q4" || selection.options[0].LocalityMultiplier != 4 {
		t.Fatalf("選択対象外の着手が局所性へ影響した：%+v", selection.options)
	}
}

func Test注目局面の近くでもI列の座標を拒否する(t *testing.T) {
	t.Parallel()

	if moveIsNearFocus("I5", []string{"J5"}, 9) {
		t.Fatal("不正なI5がJ5と同じ点として扱われた")
	}
	if !moveIsNearFocus("J5", []string{"J5"}, 9) {
		t.Fatal("正しいJ5が注目点として扱われていない")
	}
}

func Test初心者向け選択はレベル別の損失目標を重みに反映する(t *testing.T) {
	t.Parallel()

	summary := Summary{CurrentPlayer: "B", Candidates: []CandidateSummary{
		{Move: "E5", Visits: 10, ScoreLead: 10, Prior: 0.3},
		{Move: "D4", Visits: 10, ScoreLead: 7, Prior: 0.3},
		{Move: "A1", Visits: 10, ScoreLead: 2, Prior: 0.3},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 15, Temperature: 4, CandidateCap: 20, MinVisits: 1, TacticalBias: 1, TargetLoss: 3, LossSpread: 1.5}
	selection, err := selectBeginnerMoveWithContext(summary, nil, profile, moveSelectionContext{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if selection.options[1].Weight <= selection.options[0].Weight || selection.options[1].Weight <= selection.options[2].Weight {
		t.Fatalf("対象を失う手が優先されていない：%+v", selection.options)
	}
}

func Test初心者の手を選択して大きな損失を除外する(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Candidates: []CandidateSummary{
		{Move: "E5", Visits: 10, ScoreLead: 1, Prior: 0.4},
		{Move: "D4", Visits: 10, ScoreLead: 0, Prior: 0.3},
		{Move: "A1", Visits: 10, ScoreLead: -10, Prior: 0.3},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 2, Temperature: 1, CandidateCap: 10, MinVisits: 1, TacticalBias: 1}
	selection, err := selectBeginnerMoveWithContext(summary, nil, profile, moveSelectionContext{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.options) != 2 {
		t.Fatalf("候補数 = %d、期待値 = 2", len(selection.options))
	}
	for _, option := range selection.options {
		if option.Move == "A1" {
			t.Fatal("大損する手が選択対象に残っている")
		}
	}
}

func Test初心者向け着手は不正な候補を除いて次の合法手を使う(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Visits: 30, Candidates: []CandidateSummary{
		{Move: "E5", Visits: 20, ScoreLead: 3, Prior: 0.6},
		{Move: "D4", Visits: 10, ScoreLead: 2, Prior: 0.4},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 2, Temperature: 1, CandidateCap: 10, MinVisits: 1, TacticalBias: 1}
	tactics := []candidateTactics{{Move: "E5", Legal: false}, {Move: "D4", Legal: true}}

	selection, err := selectBeginnerMoveWithContext(summary, tactics, profile, moveSelectionContext{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if selection.EngineMove != "E5" || selection.SelectedMove != "D4" || len(selection.options) != 1 || selection.options[0].Move != "D4" {
		t.Fatalf("不正な候補を除いた選択 = %+v", selection)
	}

	tactics[1].Legal = false
	if _, err := selectBeginnerMoveWithContext(summary, tactics, profile, moveSelectionContext{}, 1); err == nil {
		t.Fatal("すべての候補が不正でも着手を選んだ")
	}
}

func Test不正な最上位候補を除いた最善の合法手は探索数の下限を問わない(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Visits: 30, Candidates: []CandidateSummary{
		{Move: "E5", Visits: 29, ScoreLead: 3, Prior: 0.6},
		{Move: "D4", Visits: 1, ScoreLead: 2.9, Prior: 0.4},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 0.5, Temperature: 1, CandidateCap: 5, MinVisits: 8, TacticalBias: 1}
	tactics := []candidateTactics{{Move: "E5", Legal: false}, {Move: "D4", Legal: true}}

	selection, err := selectBeginnerMoveWithContext(summary, tactics, profile, moveSelectionContext{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if selection.EngineMove != "E5" || selection.SelectedMove != "D4" || len(selection.options) != 1 {
		t.Fatalf("不正な最上位候補を除いた選択 = %+v", selection)
	}
}

func Test候補の数の上限は打てる候補手だけで数える(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Visits: 30, Candidates: []CandidateSummary{
		{Move: "E5", Visits: 10, ScoreLead: 3, Prior: 0.4},
		{Move: "D4", Visits: 10, ScoreLead: 3, Prior: 0.3},
		{Move: "C3", Visits: 10, ScoreLead: 3, Prior: 0.3},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 2, Temperature: 1, CandidateCap: 2, MinVisits: 1, TacticalBias: 1}
	tactics := []candidateTactics{{Move: "E5", Legal: false}, {Move: "D4", Legal: true}, {Move: "C3", Legal: true}}

	selection, err := selectBeginnerMoveWithContext(summary, tactics, profile, moveSelectionContext{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.options) != 2 || selection.options[0].Move != "D4" || selection.options[1].Move != "C3" {
		t.Fatalf("上限までの候補 = %+v", selection.options)
	}
}

func Test打てない候補手を除いた先頭がパスならパスする(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Visits: 30, Candidates: []CandidateSummary{
		{Move: "E5", Visits: 10, ScoreLead: 3, Prior: 0.4},
		{Move: "pass", Visits: 10, ScoreLead: 1, Prior: 0.3},
		{Move: "C3", Visits: 10, ScoreLead: 0, Prior: 0.3},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 5, Temperature: 1, CandidateCap: 5, MinVisits: 1, TacticalBias: 1}
	tactics := []candidateTactics{{Move: "E5", Legal: false}, {Move: "C3", Legal: true}}

	selection, err := selectBeginnerMoveWithContext(summary, tactics, profile, moveSelectionContext{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if selection.SelectedMove != "pass" {
		t.Fatalf("選んだ手 = %q", selection.SelectedMove)
	}
}

func Test目差損の上限をわずかでも超える候補を除外する(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Visits: 20, Candidates: []CandidateSummary{
		{Move: "D4", Visits: 10, ScoreLead: 1, Prior: 0.5},
		{Move: "E4", Visits: 10, ScoreLead: 0.4999999999, Prior: 0.5},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 0.5, Temperature: 1, CandidateCap: 5, MinVisits: 1, TacticalBias: 1}
	selection, err := selectBeginnerMoveWithContext(summary, nil, profile, moveSelectionContext{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.options) != 1 || selection.options[0].Move != "D4" {
		t.Fatalf("上限を超えた候補を含む選択肢 = %+v", selection.options)
	}
}

func Test初心者向け着手の選択で白番の視点を使う(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "W", Candidates: []CandidateSummary{
		{Move: "D4", Visits: 10, ScoreLead: 3, Prior: 0.5},
		{Move: "E5", Visits: 10, ScoreLead: 1, Prior: 0.5},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 0.5, Temperature: 1, CandidateCap: 10, MinVisits: 1, TacticalBias: 1}
	selection, err := selectBeginnerMoveWithContext(summary, nil, profile, moveSelectionContext{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if selection.EngineMove != "D4" || len(selection.options) != 1 || selection.SelectedMove != "D4" {
		t.Fatalf("想定外の白の選択：%+v", selection)
	}
}

func Test初心者向け着手は再現可能な基本戦術を選ぶ(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Candidates: []CandidateSummary{
		{Move: "B3", Visits: 10, ScoreLead: 0, Prior: 0.2},
		{Move: "D4", Visits: 10, ScoreLead: 0, Prior: 0.2},
	}}
	tactics := []candidateTactics{{Move: "B3", Legal: true, SavedOwnAtariGroups: [][]string{{"B2"}}}}
	profile := beginnerAIProfile{MaxScoreLoss: 2, Temperature: 1, CandidateCap: 10, MinVisits: 1, TacticalBias: 2.5}
	first, err := selectBeginnerMoveWithContext(summary, tactics, profile, moveSelectionContext{}, 42)
	if err != nil {
		t.Fatal(err)
	}
	second, err := selectBeginnerMoveWithContext(summary, tactics, profile, moveSelectionContext{}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if first.SelectedMove != second.SelectedMove {
		t.Fatalf("同じ乱数初期値で%sと%sが選ばれた", first.SelectedMove, second.SelectedMove)
	}
	if first.options[0].Weight <= first.options[1].Weight {
		t.Fatal("救出する手に戦術的な重み加算がない")
	}
}

func Test初心者向け着手は重みに比例して確率で選ぶ(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Candidates: []CandidateSummary{
		{Move: "C4", Visits: 20, ScoreLead: 0, Prior: 0.25},
		{Move: "D4", Visits: 20, ScoreLead: 0, Prior: 0.75},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 1, Temperature: 1, CandidateCap: 5, MinVisits: 1, TacticalBias: 1}
	selectedCounts := map[string]int{}
	for seed := int64(1); seed <= 1000; seed++ {
		selection, err := selectBeginnerMoveWithContext(summary, nil, profile, moveSelectionContext{}, seed)
		if err != nil {
			t.Fatal(err)
		}
		selectedCounts[selection.SelectedMove]++
	}
	if selectedCounts["C4"] == 0 || selectedCounts["D4"] == 0 {
		t.Fatalf("乱数初期値を変えても選択に揺らぎがない：%v", selectedCounts)
	}
	if selectedCounts["D4"] < 700 || selectedCounts["D4"] > 800 {
		t.Fatalf("重みに比例した選択回数になっていない：%v", selectedCounts)
	}
}

func Test初心者向け着手の選択でKataGoの候補を基準にする(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Candidates: []CandidateSummary{
		{Move: "C4", Visits: 30, ScoreLead: -3, Prior: 0.4},
		{Move: "A5", Visits: 1, ScoreLead: 8, Prior: 0.001},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 0.5, Temperature: 1, CandidateCap: 5, MinVisits: 8, TacticalBias: 1}
	selection, err := selectBeginnerMoveWithContext(summary, nil, profile, moveSelectionContext{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.options) != 1 || selection.SelectedMove != "C4" {
		t.Fatalf("想定外の選択：%+v", selection)
	}
}

func Test事前確率は百万分の一を下限として重みを計算する(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Candidates: []CandidateSummary{
		{Move: "C4", Visits: 30, ScoreLead: 0, Prior: 0},
		{Move: "D4", Visits: 20, ScoreLead: 0, Prior: 1e-7},
		{Move: "E4", Visits: 20, ScoreLead: 0, Prior: 1e-5},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 1, Temperature: 4, CandidateCap: 5, MinVisits: 1, TacticalBias: 1}
	selection, err := selectBeginnerMoveWithContext(summary, nil, profile, moveSelectionContext{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	minimumWeight := math.Pow(1e-6, 1.0/4)
	originalWeight := math.Pow(1e-5, 1.0/4)
	if len(selection.options) != 3 || selection.options[0].Weight != minimumWeight || selection.options[1].Weight != minimumWeight || selection.options[2].Weight != originalWeight {
		t.Fatalf("事前確率から求めた重み = %+v", selection.options)
	}
}

func Test明らかに不利なパスを初心者向け着手から除外する(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Candidates: []CandidateSummary{
		{Move: "C4", Visits: 30, ScoreLead: 0, Prior: 0.4},
		{Move: "pass", Visits: 20, ScoreLead: -1, Prior: 0.4},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 5, Temperature: 1, CandidateCap: 5, MinVisits: 1, TacticalBias: 1}
	selection, err := selectBeginnerMoveWithContext(summary, nil, profile, moveSelectionContext{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.options) != 1 || selection.options[0].Move != "C4" {
		t.Fatalf("パスが選択対象に残っている：%+v", selection.options)
	}
}

func Test最善手がパスなら初心者向け選択もパスする(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "W", Visits: 500, Candidates: []CandidateSummary{
		{Move: "pass", Visits: 300, ScoreLead: -12, Prior: 0.4},
		{Move: "A1", Visits: 200, ScoreLead: -9, Prior: 0.6},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 15, Temperature: 4, CandidateCap: 20, MinVisits: 1, TacticalBias: 1, TargetLoss: 3, LossSpread: 1.5}
	for seed := int64(1); seed <= 20; seed++ {
		selection, err := selectBeginnerMoveWithContext(summary, nil, profile, moveSelectionContext{}, seed)
		if err != nil {
			t.Fatal(err)
		}
		if selection.SelectedMove != "pass" || len(selection.options) != 1 || selection.options[0].Move != "A1" {
			t.Fatalf("乱数初期値 %dでパスが最善になった後に着手を選んだ：%+v", seed, selection)
		}
	}
}

func Testパスが事実上互角の場合に初心者向け着手パスを選択する(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "W", Visits: 50, Candidates: []CandidateSummary{
		{Move: "R16", Visits: 20, ScoreLead: 7.40, Prior: 0.2},
		{Move: "pass", Visits: 5, ScoreLead: 7.46, Prior: 0.02},
		{Move: "K5", Visits: 5, ScoreLead: 7.50, Prior: 0.02},
	}}
	profile, err := beginnerProfile(1)
	if err != nil {
		t.Fatal(err)
	}
	for seed := int64(1); seed <= 20; seed++ {
		selection, err := selectBeginnerMoveWithContext(summary, nil, profile, moveSelectionContext{}, seed)
		if err != nil {
			t.Fatal(err)
		}
		if selection.SelectedMove != "pass" || len(selection.options) != 2 {
			t.Fatalf("乱数初期値 %dで実質的に終了した対局を終局していない：%+v", seed, selection)
		}
	}
}

func Testパスは最善手からの損が四分の一目以内のときだけ選ぶ(t *testing.T) {
	t.Parallel()
	profile := beginnerAIProfile{MaxScoreLoss: 5, Temperature: 1, CandidateCap: 5, MinVisits: 1, TacticalBias: 1}
	for _, test := range []struct {
		name     string
		passLead float64
		wantPass bool
	}{
		{name: "上限と同じ", passLead: 9.75, wantPass: true},
		{name: "上限を超える", passLead: 9.749999999, wantPass: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			summary := Summary{CurrentPlayer: "B", Candidates: []CandidateSummary{
				{Move: "C4", Visits: 20, ScoreLead: 10, Prior: 1},
				{Move: "pass", Visits: 10, ScoreLead: test.passLead, Prior: 0},
			}}
			selection, err := selectBeginnerMoveWithContext(summary, nil, profile, moveSelectionContext{}, 1)
			if err != nil {
				t.Fatal(err)
			}
			if got := selection.SelectedMove == "pass"; got != test.wantPass {
				t.Fatalf("パスの選択 = %t、期待値 = %t", got, test.wantPass)
			}
		})
	}
}

func Test石を取りアタリの石も助ける手に戦術倍率を一度だけ掛ける(t *testing.T) {
	t.Parallel()
	summary := Summary{CurrentPlayer: "B", Candidates: []CandidateSummary{
		{Move: "C4", Visits: 20, ScoreLead: 0, Prior: 0.5},
		{Move: "D4", Visits: 20, ScoreLead: 0, Prior: 0.5},
	}}
	tactics := []candidateTactics{{
		Move: "C4", Legal: true,
		CapturedStones:      []string{"C3"},
		SavedOwnAtariGroups: [][]string{{"C5"}},
	}}
	profile := beginnerAIProfile{MaxScoreLoss: 1, Temperature: 1, CandidateCap: 5, MinVisits: 1, TacticalBias: 2}
	selection, err := selectBeginnerMoveWithContext(summary, tactics, profile, moveSelectionContext{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.options) != 2 || selection.options[0].Weight != selection.options[1].Weight*2 {
		t.Fatalf("戦術倍率を反映した重み = %+v", selection.options)
	}
}
