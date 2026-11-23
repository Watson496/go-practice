package game

import (
	"reflect"
	"testing"

	"go-practice/internal/coordinate"
)

func TestOwnershipは石のまとまりの平均で死石を決める(t *testing.T) {
	position := Snapshot{
		BoardSize: 5,
		Stones: map[string]string{
			"A1": "B",
			"A2": "B",
			"C3": "W",
			"E5": "B",
		},
	}
	ownership := make([]float64, 25)
	setOwnership := func(pointCoordinate string, value float64) {
		point, ok := coordinate.Point(pointCoordinate, position.BoardSize)
		if !ok {
			t.Fatalf("座標%sをownershipへ設定できません", pointCoordinate)
		}
		ownership[point] = value
	}
	setOwnership("A1", -0.6)
	setOwnership("A2", -0.4)
	setOwnership("C3", 0.5)
	setOwnership("E5", -0.49)

	dead, err := DeadStonesFromOwnership(position, ownership)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dead, []string{"A1", "A2", "C3"}) {
		t.Fatalf("死石 = %v", dead)
	}
}

func Test死石を除いた盤で中国式の面積を数える(t *testing.T) {
	session, err := NewSessionWithSetup(
		"死石を除いた面積",
		5,
		0.5,
		0,
		map[string]string{"A1": "B", "E5": "W"},
		"B",
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ScorePosition(session.Snapshot(), "root", []string{"E5"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Winner != "B" || result.Margin != 24.5 || result.BlackScore != 25 || result.WhiteScore != 0.5 || !reflect.DeepEqual(result.DeadStones, []string{"E5"}) {
		t.Fatalf("死石を除いた点数 = %+v", result)
	}
}

func Test二連続パスの結果を死石を除いて一度だけ作る(t *testing.T) {
	session, err := NewSessionWithSetup(
		"二連続パス",
		5,
		0.5,
		0,
		map[string]string{"A1": "B", "E5": "W"},
		"B",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.PlayWithDeadStones("pass", []string{"E5"}); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.Result == nil || snapshot.Result.Margin != 24.5 || !reflect.DeepEqual(snapshot.Result.DeadStones, []string{"E5"}) {
		t.Fatalf("二連続パスの結果 = %+v", snapshot.Result)
	}
	resultNodes := 0
	for _, node := range snapshot.TreeNodes {
		if node.Result != nil {
			resultNodes++
		}
	}
	if resultNodes != 1 {
		t.Fatalf("結果を持つ局面 = %d件", resultNodes)
	}
}

func Test置き碁の点数に白の置石補正を加えない(t *testing.T) {
	session, err := NewConfiguredSession("置き碁の点数", 5, DefaultHandicapKomi, 5)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ScorePosition(session.Snapshot(), "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.BlackScore != 25 || result.WhiteScore != DefaultHandicapKomi {
		t.Fatalf("置き碁の点数 = 黒%v、白%v", result.BlackScore, result.WhiteScore)
	}
}
