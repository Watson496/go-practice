package katago

import (
	"encoding/json"
	"math"
	"testing"

	"go-practice/internal/game"
)

func Fuzz解析結果の任意のJSONを安全に検証する(f *testing.F) {
	f.Add([]byte(`{"turnNumber":0,"rootInfo":{"currentPlayer":"B","scoreLead":0,"winrate":0.5,"visits":1},"ownership":[0],"moveInfos":[{"move":"pass","visits":1,"scoreLead":0,"winrate":0.5,"prior":0.5,"pv":["pass"]}]}`))
	f.Add([]byte(`{"rootInfo":null,"moveInfos":"不正"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		var decoded result
		if json.Unmarshal(data, &decoded) != nil {
			return
		}
		summary, err := summarize(decoded, 20)
		if err == nil {
			if err := validateSummary(summary, 0); err != nil {
				t.Fatalf("検証済みの解析結果が再検証に失敗した：%v", err)
			}
		}
	})
}

func Test解析候補を初心者向けの説明へ要約する(t *testing.T) {
	t.Parallel()

	result := result{
		"id":         "test",
		"turnNumber": float64(3),
		"rootInfo": map[string]any{
			"currentPlayer": "W",
			"scoreLead":     float64(1.25),
			"winrate":       float64(0.6),
			"visits":        float64(100),
		},
		"ownership": []any{float64(-0.5), float64(0.5)},
		"moveInfos": []any{
			map[string]any{
				"move":      "D4",
				"visits":    float64(80),
				"scoreLead": float64(1.4),
				"winrate":   float64(0.62),
				"prior":     float64(0.3),
				"pv":        []any{"D4", "F6"},
			},
		},
	}

	summary, err := summarize(result, 1)
	if err != nil {
		t.Fatalf("解析結果の要約でエラーが発生した：%v", err)
	}
	if summary.CurrentPlayer != "W" || len(summary.Candidates) != 1 {
		t.Fatalf("想定外の解析要約：%#v", summary)
	}
	if got := summary.Candidates[0].PV; len(got) != 2 || got[1] != "F6" {
		t.Fatalf("候補手の読み筋 = %#v", got)
	}
	if candidate := summary.Candidates[0]; candidate.ScoreLead != -1.4 || math.Abs(candidate.Winrate-0.38) > 1e-12 {
		t.Fatalf("白番から見た候補手の評価 = %+v", candidate)
	}
}

func Test解析結果に必要な情報がない場合は拒否する(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result result
	}{
		{name: "全体情報がない", result: result{}},
		{name: "候補手情報がない", result: result{"rootInfo": map[string]any{}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := summarize(test.result, 1)
			if err == nil {
				t.Fatal("必要な情報がない解析結果を受け付けました")
			}
		})
	}
}

func Test解析結果の型と値域が不正なら拒否する(t *testing.T) {
	valid := func() result {
		return result{
			"turnNumber": float64(0),
			"rootInfo":   map[string]any{"currentPlayer": "B", "scoreLead": float64(0), "winrate": float64(0.5), "visits": float64(1)},
			"ownership":  []any{float64(0)},
			"moveInfos": []any{map[string]any{
				"move": "pass", "visits": float64(0), "scoreLead": float64(0), "winrate": float64(0.5), "prior": float64(0.5), "pv": []any{"pass"},
			}},
		}
	}
	tests := []struct {
		name   string
		change func(result)
	}{
		{name: "手番", change: func(result result) { result["rootInfo"].(map[string]any)["currentPlayer"] = "X" }},
		{name: "勝率", change: func(result result) { result["rootInfo"].(map[string]any)["winrate"] = 1.1 }},
		{name: "探索数", change: func(result result) { result["rootInfo"].(map[string]any)["visits"] = 0.5 }},
		{name: "領域予測", change: func(result result) { result["ownership"] = []any{math.Inf(1)} }},
		{name: "候補手", change: func(result result) { result["moveInfos"].([]any)[0].(map[string]any)["move"] = "" }},
		{name: "読み筋", change: func(result result) { result["moveInfos"].([]any)[0].(map[string]any)["pv"] = []any{1} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := valid()
			test.change(result)
			_, err := summarize(result, 1)
			if err == nil {
				t.Fatal("不正な解析結果を受け付けました")
			}
		})
	}
}

func Test解析結果の候補手と読み筋を盤の範囲で検証する(t *testing.T) {
	base := Summary{
		CurrentPlayer: "B", Winrate: 0.5, Visits: 1,
		Ownership:  make([]float64, 81),
		Candidates: []CandidateSummary{{Move: "D4", Winrate: 0.5, Prior: 0.5, PV: []string{"pass", "E5"}}},
	}
	if err := validateSummary(base, 81); err != nil {
		t.Fatalf("正しい解析結果を拒否した：%v", err)
	}
	for _, test := range []struct {
		name   string
		change func(*Summary)
	}{
		{name: "候補手が盤外", change: func(summary *Summary) { summary.Candidates[0].Move = "T19" }},
		{name: "読み筋が不正", change: func(summary *Summary) { summary.Candidates[0].PV[1] = "I5" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			summary := base
			summary.Candidates = append([]CandidateSummary(nil), base.Candidates...)
			summary.Candidates[0].PV = append([]string(nil), base.Candidates[0].PV...)
			test.change(&summary)
			if err := validateSummary(summary, 81); err == nil {
				t.Fatal("盤の範囲外にある解析結果を受け付けた")
			}
		})
	}
}

func Test解析結果を要求した局面と照合する(t *testing.T) {
	session, err := game.NewSessionWithSetup("解析対象", 9, 7.5, 0, nil, "B")
	if err != nil {
		t.Fatalf("解析対象を作成できない：%v", err)
	}
	position := session.Snapshot()
	summary := Summary{TurnNumber: 0, CurrentPlayer: "B", Winrate: 0.5, Visits: 1, Ownership: make([]float64, 81)}
	if err := ValidateSummaryForPosition(summary, position.BoardSize, len(position.Moves), position.CurrentPlayer); err != nil {
		t.Fatalf("対象局面と一致する解析結果を拒否した：%v", err)
	}

	wrongTurn := summary
	wrongTurn.TurnNumber = 1
	if err := ValidateSummaryForPosition(wrongTurn, position.BoardSize, len(position.Moves), position.CurrentPlayer); err == nil {
		t.Fatal("対象局面と手数が違う解析結果を受け付けた")
	}
	wrongPlayer := summary
	wrongPlayer.CurrentPlayer = "W"
	if err := ValidateSummaryForPosition(wrongPlayer, position.BoardSize, len(position.Moves), position.CurrentPlayer); err == nil {
		t.Fatal("対象局面と手番が違う解析結果を受け付けた")
	}
}
