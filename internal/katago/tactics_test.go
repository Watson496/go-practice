package katago

import "testing"

func analyzeTacticsAfterMoves(boardSize int, moves []move, candidateMoves []string) ([]candidateTactics, error) {
	player := "B"
	if len(moves) > 0 && moves[len(moves)-1][0] == "B" {
		player = "W"
	}
	return analyzeTactics(boardSize, nil, moves, candidateMoves, player)
}

func Test戦術を解析してアタリと救出着手を見つける(t *testing.T) {
	t.Parallel()

	moves := []move{
		{"B", "B2"}, {"W", "A2"}, {"B", "E5"},
		{"W", "B1"}, {"B", "E4"}, {"W", "C2"},
	}
	candidates, err := analyzeTacticsAfterMoves(5, moves, []string{"B3", "D4"})
	if err != nil {
		t.Fatalf("戦術情報の解析でエラーが発生した：%v", err)
	}
	if len(candidates) != 2 || len(candidates[0].SavedOwnAtariGroups) != 1 {
		t.Fatalf("候補B3がアタリの石の一団を救出していない：%#v", candidates)
	}
	if len(candidates[1].OwnGroupsStillInAtari) != 1 {
		t.Fatalf("候補D4の後にB2がアタリで残っていない：%#v", candidates[1])
	}
}

func Test初期配置を反映して戦術を解析する(t *testing.T) {
	t.Parallel()

	candidates, err := analyzeTactics(9, []move{{"B", "C3"}, {"B", "G7"}}, nil, []string{"E5", "C3"}, "W")
	if err != nil {
		t.Fatal(err)
	}
	if !candidates[0].Legal || candidates[1].Legal {
		t.Fatalf("候補手の合法性が想定外：%#v", candidates)
	}
}

func Test戦術解析がI列を含む不正な座標を拒否する(t *testing.T) {
	t.Parallel()

	candidates, err := analyzeTacticsAfterMoves(9, nil, []string{"I5", "J5"})
	if err != nil {
		t.Fatal(err)
	}
	if candidates[0].Legal || !candidates[1].Legal {
		t.Fatalf("候補手の合法性が想定外：%#v", candidates)
	}
}

func Test戦術解析が盤面同型の反復を拒否する(t *testing.T) {
	t.Parallel()

	moves := []move{
		{"B", "B1"}, {"W", "C1"}, {"B", "A2"},
		{"W", "C3"}, {"B", "B3"}, {"W", "D2"},
		{"B", "pass"}, {"W", "B2"}, {"B", "C2"},
	}
	candidates, err := analyzeTacticsAfterMoves(5, moves, []string{"B2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Legal {
		t.Fatalf("コウの取り返し候補が想定外：%#v", candidates)
	}
}

func Test二連続パス後の着手を含む局面を対局規則で解析する(t *testing.T) {
	t.Parallel()

	moves := []move{{"B", "pass"}, {"W", "pass"}, {"B", "C3"}}
	candidates, err := analyzeTacticsAfterMoves(5, moves, []string{"C3", "D3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Legal || !candidates[1].Legal {
		t.Fatalf("二連続パス後の候補手の合法性が想定外：%#v", candidates)
	}
}
