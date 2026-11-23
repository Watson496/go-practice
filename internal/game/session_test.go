package game

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func Test対局条件の選択肢を呼び出し元から変更できない(t *testing.T) {
	boardSizes := SupportedBoardSizes()
	handicaps := SupportedHandicaps()
	colors := SupportedColors()
	boardSizes[0] = 99
	handicaps[0] = 99
	colors[0] = "X"

	if got := SupportedBoardSizes(); !reflect.DeepEqual(got, []int{5, 6, 7, 9, 13, 19}) {
		t.Fatalf("対応盤サイズ = %v", got)
	}
	if got := SupportedHandicaps(); !reflect.DeepEqual(got, []int{0, 2, 3, 4, 5, 6, 7, 8, 9}) {
		t.Fatalf("対応する置き碁の石数 = %v", got)
	}
	if got := SupportedColors(); !reflect.DeepEqual(got, []string{"B", "W"}) {
		t.Fatalf("対応する石の色 = %v", got)
	}
	if err := validateBoardSize(9); err != nil {
		t.Fatalf("対応する盤サイズを拒否した：%v", err)
	}
	if err := validateBoardSize(8); err == nil {
		t.Fatal("対応していない盤サイズを受け付けた")
	}
	if err := validateKomi(7.5); err != nil {
		t.Fatalf("対応するコミを拒否した：%v", err)
	}
	if err := validateKomi(7.25); err == nil {
		t.Fatal("対応していないコミを受け付けた")
	}
	for _, handicap := range []int{0, 2, 9} {
		t.Run("置き碁を受け付ける_"+strconv.Itoa(handicap), func(t *testing.T) {
			if !isSupportedHandicap(handicap) || validateHandicap(handicap) != nil {
				t.Fatalf("対応する置き碁の石数%dを拒否した", handicap)
			}
		})
	}
	for _, handicap := range []int{-1, 1, 10} {
		t.Run("置き碁を拒否する_"+strconv.Itoa(handicap), func(t *testing.T) {
			if isSupportedHandicap(handicap) || validateHandicap(handicap) == nil {
				t.Fatalf("対応していない置き碁の石数%dを受け付けた", handicap)
			}
		})
	}
}

func Testコミの文字列表現を丸めずに検査する(t *testing.T) {
	for _, value := range []string{"-100", "-0.5", "0", "7.5", "1e1", "100.0"} {
		t.Run("受付_"+value, func(t *testing.T) {
			if !IsValidKomiLiteral(value) {
				t.Fatalf("正しいコミ%qを拒否した", value)
			}
		})
	}
	for _, value := range []string{"", "0.1", "0.49999999999999999", "7.50000000000000001", "101"} {
		t.Run("拒否_"+strconv.Quote(value), func(t *testing.T) {
			if IsValidKomiLiteral(value) {
				t.Fatalf("0.5目単位ではないコミ%qを受け付けた", value)
			}
		})
	}
}

func Test負のゼロのコミを正のゼロへ正規化する(t *testing.T) {
	session, err := NewConfiguredSession("zero-komi", 9, math.Copysign(0, -1), 0)
	if err != nil {
		t.Fatal(err)
	}
	if komi := session.Snapshot().Komi; komi != 0 || math.Signbit(komi) {
		t.Fatalf("正規化後のコミ = %v", komi)
	}
}

func Test結果のJSONは点数の内訳の有無を1つの形で表す(t *testing.T) {
	encoded, err := json.Marshal(Result{
		Type: "points", Cause: "two_passes", Winner: "W", Margin: 7.5,
		BlackScore: 0, WhiteScore: 7.5, ScoreKnown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"blackScore":0`) || !strings.Contains(string(encoded), `"whiteScore":7.5`) {
		t.Fatalf("0点を含む点数の内訳が両方書かれていない：%s", encoded)
	}
	for _, source := range []string{
		`{"type":"points","cause":"recorded","margin":0,"scoreKnown":true}`,
		`{"type":"points","cause":"recorded","margin":0,"blackScore":0,"scoreKnown":true}`,
		`{"type":"points","cause":"recorded","margin":0,"blackScore":0,"whiteScore":0,"scoreKnown":false}`,
		`{"type":"recorded","cause":"recorded","value":"Void"}`,
	} {
		t.Run(source, func(t *testing.T) {
			var result Result
			if err := json.Unmarshal([]byte(source), &result); err == nil {
				t.Fatalf("点数の内訳の表現が不完全なJSONを受け付けた：%s", source)
			}
		})
	}
	var restored Result
	if err := json.Unmarshal(encoded, &restored); err != nil || !sameResult(restored, Result{
		Type: "points", Cause: "two_passes", Winner: "W", Margin: 7.5,
		BlackScore: 0, WhiteScore: 7.5, ScoreKnown: true,
	}) {
		t.Fatalf("書き出した結果を復元できない：結果 = %+v、エラー = %v", restored, err)
	}
}

func Test未解釈の記録結果は空の値もJSONで往復する(t *testing.T) {
	for _, value := range []string{"W+T", "Void", "?", ""} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			result := Result{Type: "recorded", Cause: "recorded", Value: value}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), `"value":`+strconv.Quote(value)) {
				t.Fatalf("記録値%qがJSONにありません：%s", value, encoded)
			}
			var restored Result
			if err := json.Unmarshal(encoded, &restored); err != nil {
				t.Fatal(err)
			}
			if !sameResult(restored, result) {
				t.Fatalf("記録結果が一致しません：書き出し前=%+v、復元後=%+v", result, restored)
			}
		})
	}
}

func TestSGF開始局面の省略情報と対局条件の矛盾を拒否する(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Snapshot)
	}{
		{
			name: "盤サイズ",
			change: func(snapshot *Snapshot) {
				snapshot.BoardSize = 9
				snapshot.SGFRootStyle = &SGFRootStyle{}
			},
		},
		{
			name: "コミ",
			change: func(snapshot *Snapshot) {
				snapshot.Komi = 0.5
				snapshot.SGFRootStyle = &SGFRootStyle{BoardSizeExplicit: true}
			},
		},
		{
			name: "開始手番",
			change: func(snapshot *Snapshot) {
				snapshot.InitialPlayer = "W"
				snapshot.CurrentPlayer = "W"
				snapshot.SGFRootStyle = &SGFRootStyle{BoardSizeExplicit: true, KomiExplicit: true}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := mustNewTestSession(t, "省略情報", 19, DefaultKomi).Snapshot()
			test.change(&snapshot)
			if _, err := RestoreSession(snapshot); err == nil {
				t.Fatalf("矛盾した省略情報を理由付きで拒否しませんでした：%v", err)
			}
		})
	}
}

func Benchmark対局の棋譜ツリー履歴362局面の検証(b *testing.B) {
	snapshot := benchmarkSnapshot362Nodes()
	b.ResetTimer()
	for range b.N {
		if err := ValidateGameTreeHistory(snapshot); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark対局状態362局面の復元(b *testing.B) {
	snapshot := benchmarkSnapshot362Nodes()
	b.ResetTimer()
	for range b.N {
		if _, err := RestoreSession(snapshot); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkSnapshot362Nodes() Snapshot {
	nodes := make([]TreeNode, 1, 363)
	nodes[0] = TreeNode{ID: "root"}
	parentID := "root"
	for index := 1; index <= 362; index++ {
		id := "n" + strconv.Itoa(index)
		color := "B"
		if index%2 == 0 {
			color = "W"
		}
		nodes = append(nodes, TreeNode{ID: id, ParentID: parentID, Move: &Move{Color: color, Coordinate: "pass"}})
		parentID = id
	}
	return Snapshot{
		ID: "benchmark", BoardSize: 9, Rules: "chinese", Komi: 7.5, SetupStones: map[string]string{}, InitialPlayer: "B",
		CurrentPlayer: "B", Status: "playing", Moves: []Move{}, Stones: map[string]string{}, Captures: map[string]int{"B": 0, "W": 0},
		TreeNodes: nodes, CurrentNodeID: "root", MainLineNodeID: "root", NextNodeNumber: 363,
	}
}

func Test終局状態のJSON往復で末端の結果局面を使う(t *testing.T) {
	session := mustNewTestSession(t, "finished-round-trip", 9, 7.5)
	mustPlayTest(t, session, "pass")
	mustPlayTest(t, session, "pass")
	snapshot := session.Snapshot()
	resultNodes := 0
	for _, node := range snapshot.TreeNodes {
		if node.Result != nil {
			resultNodes++
			if node.Move != nil {
				t.Fatalf("結果局面に着手がある：%+v", node)
			}
			for _, candidate := range snapshot.TreeNodes {
				if candidate.ParentID == node.ID {
					t.Fatalf("結果局面に後続がある：%+v", node)
				}
			}
		}
	}
	if resultNodes != 1 {
		t.Fatalf("結果局面数 = %d", resultNodes)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreSession(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot(); !reflect.DeepEqual(got, snapshot) {
		t.Fatalf("JSON往復後の状態が異なる：\n実際：%+v\n期待値：%+v", got, snapshot)
	}
}

func Test状態の複製が変更可能な値を元の対局と共有しない(t *testing.T) {
	session := mustNewTestSession(t, "snapshot-copy", 9, 7.5)
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	snapshot.Result.Margin = 999
	snapshot.Moves[0].Captured = append(snapshot.Moves[0].Captured, "A1")

	actual := session.Snapshot()
	if actual.Result.Margin == 999 {
		t.Fatal("状態の結果を書き換えると対局状態の結果も変わった")
	}
	if slices.Contains(actual.Moves[0].Captured, "A1") {
		t.Fatal("状態のアゲハマを書き換えると対局状態の着手履歴も変わった")
	}
}

func Test対局状態の復元で必須項目のない状態を拒否する(t *testing.T) {
	session, err := NewConfiguredSession("strict-format", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	base := session.Snapshot()
	for _, test := range []struct {
		mutate func(*Snapshot)
	}{
		{mutate: func(snapshot *Snapshot) { snapshot.Rules = "" }},
		{mutate: func(snapshot *Snapshot) { snapshot.InitialPlayer = "" }},
		{mutate: func(snapshot *Snapshot) { snapshot.SetupStones = nil }},
	} {
		snapshot := base
		test.mutate(&snapshot)
		if _, err := RestoreSession(snapshot); err == nil {
			t.Fatalf("必須項目のない保存状態を受け付けました")
		}
	}
}

func Test対局状態の復元で目差ゼロの勝者と複数値の単一属性が拒否される(t *testing.T) {
	session := mustNewTestSession(t, "strict-result", 9, 7.5)
	snapshot := session.Snapshot()
	result := &Result{Type: "points", Cause: "recorded", Winner: "B", Margin: 0, ScoreKnown: false}
	snapshot.Result = result
	snapshot.Status = "finished"
	snapshot.TreeNodes = append(snapshot.TreeNodes, TreeNode{ID: "n1", ParentID: "root", Result: result})
	snapshot.CurrentNodeID, snapshot.MainLineNodeID = "n1", "n1"
	snapshot.NextNodeNumber = 2
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatalf("勝者があり目差0の結果のエラー = %v", err)
	}

	for _, ident := range []string{"C", "DT", "ST"} {
		snapshot = session.Snapshot()
		snapshot.RootProperties = []SGFProperty{{Ident: ident, Values: []string{"a", "b"}}}
		if _, err := RestoreSession(snapshot); err == nil {
			t.Fatalf("複数値の%sのエラー = %v", ident, err)
		}
	}
	snapshot = session.Snapshot()
	snapshot.RootProperties = []SGFProperty{{Ident: "ST", Values: []string{"4"}}}
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatalf("範囲外のSTのエラー = %v", err)
	}
	snapshot = session.Snapshot()
	snapshot.RootProperties = []SGFProperty{{Ident: "N", Values: []string{"a", "b"}}}
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatalf("単値プロパティの複数値のエラー = %v", err)
	}
	snapshot = session.Snapshot()
	snapshot.RootProperties = []SGFProperty{{Ident: "LB", Values: []string{"aa:a", "bb:b"}}}
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("一覧型属性の複数値を拒否した：%v", err)
	}
}

func Test対局状態の復元でSGFへ書き出せないプロパティ値を拒否する(t *testing.T) {
	session := mustNewTestSession(t, "invalid-sgf-property", 9, 7.5)
	for _, test := range []struct {
		property SGFProperty
	}{
		{property: SGFProperty{Ident: "LB", Values: []string{"zz:a"}}},
		{property: SGFProperty{Ident: "MN", Values: []string{"x"}}},
		{property: SGFProperty{Ident: "TR", Values: []string{"jj"}}},
		{property: SGFProperty{Ident: "BM", Values: []string{"3"}}},
		{property: SGFProperty{Ident: "KO", Values: []string{"x"}}},
		{property: SGFProperty{Ident: "AR", Values: []string{"aa"}}},
		{property: SGFProperty{Ident: "V", Values: []string{"abc"}}},
		{property: SGFProperty{Ident: "OB", Values: []string{"-1"}}},
	} {
		snapshot := session.Snapshot()
		snapshot.RootProperties = []SGFProperty{test.property}
		if _, err := RestoreSession(snapshot); err == nil {
			t.Fatalf("書き出せないプロパティを受け付けました：%+v", test.property)
		}
	}
}

func Test二連続パスの二手目へ移動しても終局結果を表示する(t *testing.T) {
	session := mustNewTestSession(t, "two-pass-position", 9, 7.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	resultNodeID := session.Snapshot().CurrentNodeID
	resultNode := treeNodeForTest(t, session.Snapshot(), resultNodeID)
	if err := session.Navigate(resultNode.ParentID); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.Status != "finished" || snapshot.Result == nil || snapshot.Result.Cause != "two_passes" {
		t.Fatalf("二連続パスの二手目の状態 = status:%s result:%+v", snapshot.Status, snapshot.Result)
	}
	if _, err := session.Play("E5"); !errors.Is(err, errGameFinished) {
		t.Fatalf("終局後の着手のエラー = %v", err)
	}
}

func mustNewTestSession(t *testing.T, id string, boardSize int, komi float64) *Session {
	t.Helper()
	session, err := NewConfiguredSession(id, boardSize, komi, 0)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func mustPlayTest(t *testing.T, session *Session, coordinate string) {
	t.Helper()
	if _, err := session.Play(coordinate); err != nil {
		t.Fatal(err)
	}
}

func mustCommitCurrentNodeTest(t *testing.T, session *Session) {
	t.Helper()
	if err := session.CommitCurrentNodeToMainLine(); err != nil {
		t.Fatal(err)
	}
}

func mustSnapshotNode(t *testing.T, snapshot Snapshot, nodeID string) TreeNode {
	t.Helper()
	for _, node := range snapshot.TreeNodes {
		if node.ID == nodeID {
			return node
		}
	}
	t.Fatalf("局面%sが棋譜にない", nodeID)
	return TreeNode{}
}

func Test対局状態を保持して手番を追跡する(t *testing.T) {
	session, err := NewConfiguredSession("game-1", 5, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, move := range []string{"D1", "D2", "C2", "A5", "E2", "A4"} {
		if _, err := session.Play(move); err != nil {
			t.Fatalf("%sへ着手できない：%v", move, err)
		}
	}
	move, err := session.Play("D3")
	if err != nil {
		t.Fatal(err)
	}
	if len(move.Captured) != 1 || move.Captured[0] != "D2" {
		t.Fatalf("想定外の取り：%+v", move)
	}
	snapshot := session.Snapshot()
	if snapshot.CurrentPlayer != "W" || snapshot.Captures["B"] != 1 || snapshot.Stones["D3"] != "B" {
		t.Fatalf("想定外の状態：%+v", snapshot)
	}
	for index := range snapshot.TreeNodes {
		if snapshot.TreeNodes[index].Move != nil && len(snapshot.TreeNodes[index].Move.Captured) > 0 {
			snapshot.TreeNodes[index].Move.Captured[0] = "d2"
		}
	}
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatalf("非正規な取りの座標の復元エラー = %v", err)
	}
}

func Test対局状態が自殺手と占有点への着手を拒否する(t *testing.T) {
	session := mustNewTestSession(t, "game-1", 5, 7.5)
	for _, move := range []string{"E5", "A2", "E4", "B1"} {
		if _, err := session.Play(move); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := session.Play("A1"); err == nil {
		t.Fatalf("自殺手のエラー = %v", err)
	}
	if _, err := session.Play("A2"); err == nil {
		t.Fatalf("占有点への着手のエラー = %v", err)
	}
}

func Test対局状態は2回のパス後に終了する(t *testing.T) {
	session := mustNewTestSession(t, "game-1", 9, 7.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("PASS"); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.Status != "finished" || snapshot.ConsecutivePasses != 2 {
		t.Fatalf("想定外の状態：%+v", snapshot)
	}
	if snapshot.Result == nil || snapshot.Result.Type != "points" || snapshot.Result.Winner != "W" || snapshot.Result.Margin != 7.5 || snapshot.Result.BlackScore != 0 || snapshot.Result.WhiteScore != 7.5 {
		t.Fatalf("想定外の得点結果：%+v", snapshot.Result)
	}
	if _, err := session.Play("E5"); err == nil {
		t.Fatalf("終局後の着手のエラー = %v", err)
	}
}

func Test領域点数は一方の色だけで囲まれた領域を数える(t *testing.T) {
	session, err := NewSessionWithSetup("game-1", 5, 0.5, 0, map[string]string{
		"C2": "B", "B3": "B", "D3": "B", "C4": "B", "A1": "W",
	}, "B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	result := session.Snapshot().Result
	if result == nil {
		t.Fatal("二連続パスの点数結果がない")
	}
	if result.Winner != "B" || result.Margin != 3.5 || result.BlackScore != 5 || result.WhiteScore != 1.5 {
		t.Fatalf("想定外の面積得点：%+v", result)
	}
}

func Test領域点数は白だけに囲まれた地を白の点数へ加える(t *testing.T) {
	stones := map[string]string{}
	for row := 1; row <= 5; row++ {
		stones[fmt.Sprintf("C%d", row)] = "B"
		stones[fmt.Sprintf("D%d", row)] = "W"
	}
	session, err := NewSessionWithSetup("白地を含む点数", 5, 0.5, 0, stones, "B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	result := session.Snapshot().Result
	if result == nil || result.Winner != "B" || result.Margin != 4.5 || result.BlackScore != 15 || result.WhiteScore != 10.5 {
		t.Fatalf("白地を含む面積得点 = %+v", result)
	}
}

func Test領域点数の目差を半目単位で算出する(t *testing.T) {
	session, err := NewSessionWithSetup("half-point-score", 5, -0.5, 0, map[string]string{
		"C2": "B", "B3": "B", "D3": "B", "C4": "B", "A1": "W",
	}, "B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	result := session.Snapshot().Result
	if result == nil || result.Winner != "B" || result.Margin != 4.5 || result.BlackScore != 5.5 || result.WhiteScore != 1 {
		t.Fatalf("小数コミの領域点数 = %+v", result)
	}
	if _, err := RestoreSession(session.Snapshot()); err != nil {
		t.Fatalf("小数コミの終局状態を復元できない：%v", err)
	}
}

func Test領域点数が同点を表せる(t *testing.T) {
	session := mustNewTestSession(t, "game-1", 5, 0)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	result := session.Snapshot().Result
	if result == nil {
		t.Fatal("二連続パスの点数結果がない")
	}
	if result.Winner != "" || result.Margin != 0 || result.BlackScore != 0 || result.WhiteScore != 0 {
		t.Fatalf("想定外の持碁結果：%+v", result)
	}
}

func Test負のコミでは両方の点数を負にならないよう補正する(t *testing.T) {
	session := mustNewTestSession(t, "game-1", 5, -0.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.Result == nil || snapshot.Result.Winner != "B" || snapshot.Result.Margin != 0.5 || snapshot.Result.BlackScore != 0.5 || snapshot.Result.WhiteScore != 0 {
		t.Fatalf("負のコミの点数結果 = %+v", snapshot.Result)
	}
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("負のコミの終局局面を復元できない：%v", err)
	}
}

func Test対局状態は投了により終了する(t *testing.T) {
	session := mustNewTestSession(t, "game-1", 9, 7.5)
	if err := session.Resign(); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.Status != "finished" || snapshot.Result == nil || snapshot.Result.Type != "resignation" || snapshot.Result.Winner != "W" || snapshot.Result.Loser != "B" {
		t.Fatalf("想定外の投了結果：%+v", snapshot)
	}
	if _, err := session.Play("E5"); err == nil {
		t.Fatalf("投了後の着手のエラー = %v", err)
	}
}

func Test棋譜ツリーの局面へ移動すると投了結果を復元する(t *testing.T) {
	session := mustNewTestSession(t, "resigned-tree", 9, 7.5)
	mustPlayTest(t, session, "E5")
	mustPlayTest(t, session, "D5")
	if err := session.Resign(); err != nil {
		t.Fatal(err)
	}
	resignedNodeID := session.Snapshot().CurrentNodeID
	if snapshot := session.Snapshot(); len(snapshot.Moves) != 2 || snapshot.MainLineNodeID != "root" {
		t.Fatalf("投了によって着手列または主分岐が変化した：%+v", snapshot)
	}
	if err := session.Navigate("root"); err != nil {
		t.Fatal(err)
	}
	if snapshot := session.Snapshot(); snapshot.Status != "playing" || snapshot.Result != nil {
		t.Fatalf("想定外の過去局面状態：%+v", snapshot)
	}
	if err := session.Navigate(resignedNodeID); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.Status != "finished" || snapshot.Result == nil || snapshot.Result.Type != "resignation" || snapshot.Result.Winner != "W" || snapshot.Result.Loser != "B" {
		t.Fatalf("投了が復元されていない：%+v", snapshot)
	}
	if _, err := session.Play("C5"); err == nil {
		t.Fatalf("復元した投了後の着手エラー = %v", err)
	}
	if err := session.Undo(); err != nil {
		t.Fatal(err)
	}
	if snapshot := session.Snapshot(); snapshot.CurrentNodeID == resignedNodeID || snapshot.Status != "playing" || len(snapshot.TreeNodes) != 4 {
		t.Fatalf("待ったで投了分岐が保持されていない：%+v", snapshot)
	}
}

func Test対局状態が盤面同型の反復を拒否する(t *testing.T) {
	session := mustNewTestSession(t, "game-1", 5, 7.5)
	for _, move := range []string{"B1", "C1", "A2", "C3", "B3", "D2", "pass", "B2", "C2"} {
		if _, err := session.Play(move); err != nil {
			t.Fatalf("%sへ着手できない：%v", move, err)
		}
	}
	if _, err := session.Play("B2"); err == nil {
		t.Fatalf("コウの取り返しのエラー = %v", err)
	}
}

func Test指定した盤サイズで対局状態を作る(t *testing.T) {
	for _, size := range []int{5, 6, 7, 9, 13, 19} {
		t.Run(strconv.Itoa(size)+"路", func(t *testing.T) {
			if _, err := NewConfiguredSession("game", size, 7.5, 0); err != nil {
				t.Errorf("盤サイズ%dを作成できない：%v", size, err)
			}
		})
	}
	if _, err := NewConfiguredSession("game", 8, 7.5, 0); err == nil {
		t.Fatalf("未対応の盤サイズのエラー = %v", err)
	}
}

func Test新しい対局状態が空の初期配置を使う(t *testing.T) {
	session, err := NewConfiguredSession("empty", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.Moves == nil {
		t.Fatal("着手列は空値ではなく空の配列である必要がある")
	}
	if snapshot.Stones == nil {
		t.Fatal("盤上の石は空値ではなく空の対応表である必要がある")
	}
}

func Test保存した対局状態を復元する(t *testing.T) {
	session := mustNewTestSession(t, "saved", 5, 7.5)
	for _, move := range []string{"C3", "D3", "pass", "C4"} {
		if _, err := session.Play(move); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := session.Snapshot()
	restored, err := RestoreSession(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot(); !reflect.DeepEqual(got, snapshot) {
		t.Fatalf("復元した状態が異なる：\n実際：%+v\n期待値：%+v", got, snapshot)
	}
}

func Test対局状態を復元すると投了が復元される(t *testing.T) {
	session := mustNewTestSession(t, "saved", 9, 7.5)
	if err := session.Resign(); err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreSession(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if result := restored.Snapshot().Result; result == nil || result.Type != "resignation" || result.Winner != "W" {
		t.Fatalf("想定外の結果：%+v", result)
	}
}

func Test対局状態の復元で無効な棋譜ツリー結果を拒否する(t *testing.T) {
	tests := []struct {
		name   string
		result Result
		move   *Move
	}{
		{name: "未対応の結果種別", result: Result{Type: "invalid"}},
		{name: "着手と結果を併記した局面", result: Result{Type: "resignation", Cause: "recorded", Winner: "W", Loser: "B"}, move: &Move{Color: "B", Coordinate: "E5"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := Snapshot{
				ID: "invalid-result", BoardSize: 9, Rules: "chinese", Komi: 7.5,
				InitialPlayer: "B", CurrentPlayer: "B", Status: "finished", SetupStones: map[string]string{},
				Moves: []Move{}, Stones: map[string]string{}, Captures: map[string]int{"B": 0, "W": 0},
				Result: &test.result, TreeNodes: []TreeNode{{ID: "root"}, {ID: "n1", ParentID: "root", Move: test.move, Result: &test.result}},
				CurrentNodeID: "n1", MainLineNodeID: "n1", NextNodeNumber: 2,
			}
			if _, err := RestoreSession(snapshot); err == nil {
				t.Fatalf("不正な棋譜ツリー結果を受け付けました")
			}
		})
	}
}

func Test対局状態の復元で局面番号の整数オーバーフローを拒否する(t *testing.T) {
	base := mustNewTestSession(t, "node-number-overflow", 9, 7.5).Snapshot()
	for _, mutate := range []func(*Snapshot){
		func(snapshot *Snapshot) { snapshot.NextNodeNumber = math.MaxInt },
		func(snapshot *Snapshot) { snapshot.NextNodeNumber = math.MaxInt - 1 },
		func(snapshot *Snapshot) { snapshot.NextNodeNumber = math.MaxInt - MaximumTreeNodes },
		func(snapshot *Snapshot) {
			id := "n" + strconv.Itoa(math.MaxInt-1)
			snapshot.TreeNodes = append(snapshot.TreeNodes, TreeNode{
				ID: id, ParentID: "root", Move: &Move{Color: "B", Coordinate: "E5"},
			})
			snapshot.CurrentNodeID = id
			snapshot.MainLineNodeID = id
			snapshot.Moves = []Move{{Color: "B", Coordinate: "E5"}}
			snapshot.Stones = map[string]string{"E5": "B"}
			snapshot.CurrentPlayer = "W"
		},
	} {
		snapshot := base
		snapshot.TreeNodes = append([]TreeNode(nil), base.TreeNodes...)
		mutate(&snapshot)
		if _, err := RestoreSession(snapshot); err == nil {
			t.Fatalf("局面番号の上限を超えた状態のエラー = %v", err)
		}
	}
}

func Test棋譜の局面数上限を復元と操作で共有する(t *testing.T) {
	snapshot := mustNewTestSession(t, "node-limit", 9, 7.5).Snapshot()
	for number := 1; number < 4096; number++ {
		snapshot.TreeNodes = append(snapshot.TreeNodes, TreeNode{ID: "n" + strconv.Itoa(number), ParentID: "root"})
	}
	snapshot.NextNodeNumber = 4096
	session, err := RestoreSession(snapshot)
	if err != nil {
		t.Fatalf("上限ちょうどの棋譜を復元できない：%v", err)
	}
	before := session.Snapshot()
	if _, err := session.AppendSGFNode([]SGFProperty{{Ident: "C", Values: []string{"上限後の局面"}}}); err == nil {
		t.Fatalf("上限後の局面追加のエラー = %v", err)
	}
	if after := session.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("上限後の局面追加が状態を変更した")
	}
	for _, coordinate := range []string{"E5", "pass"} {
		if _, err := session.Play(coordinate); err == nil {
			t.Fatalf("上限後の着手%sのエラー = %v", coordinate, err)
		}
		if after := session.Snapshot(); !reflect.DeepEqual(after, before) {
			t.Fatalf("上限後の着手%sが状態を変更した", coordinate)
		}
	}
	if err := session.Resign(); err == nil {
		t.Fatal("上限ちょうどの棋譜へ結果の局面を追加できた")
	}
	if after := session.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("上限後の投了が状態を変更した")
	}

	overLimit := snapshot
	overLimit.TreeNodes = append([]TreeNode(nil), snapshot.TreeNodes...)
	overLimit.TreeNodes = append(overLimit.TreeNodes, TreeNode{ID: "n" + strconv.Itoa(4096), ParentID: "root"})
	overLimit.NextNodeNumber++
	if _, err := RestoreSession(overLimit); err == nil {
		t.Fatalf("上限を超えた棋譜のエラー = %v", err)
	}
}

func Test対局状態の復元で完成形にない局面番号を拒否する(t *testing.T) {
	base := mustNewTestSession(t, "invalid-node-number", 9, 7.5).Snapshot()
	tests := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{name: "次の番号がない", mutate: func(snapshot *Snapshot) { snapshot.NextNodeNumber = 0 }},
		{name: "次の番号が既存番号以下", mutate: func(snapshot *Snapshot) {
			snapshot.TreeNodes = append(snapshot.TreeNodes, TreeNode{ID: "n1", ParentID: "root", Move: &Move{Color: "B", Coordinate: "E5"}})
			snapshot.NextNodeNumber = 1
			snapshot.CurrentNodeID = "n1"
			snapshot.MainLineNodeID = "n1"
			snapshot.Moves = []Move{{Color: "B", Coordinate: "E5"}}
			snapshot.Stones = map[string]string{"E5": "B"}
			snapshot.CurrentPlayer = "W"
		}},
		{name: "負の番号", mutate: func(snapshot *Snapshot) { snapshot.TreeNodes[0].ID = "n-1" }},
		{name: "先頭ゼロ", mutate: func(snapshot *Snapshot) { snapshot.TreeNodes[0].ID = "n01" }},
		{name: "任意の文字列", mutate: func(snapshot *Snapshot) { snapshot.TreeNodes[0].ID = "node" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := base
			snapshot.TreeNodes = append([]TreeNode(nil), base.TreeNodes...)
			test.mutate(&snapshot)
			if _, err := RestoreSession(snapshot); err == nil {
				t.Fatalf("完成形にない局面番号のエラー = %v", err)
			}
		})
	}
}

func Test結果が記録された後に復元対局状態が続行を拒否する(t *testing.T) {
	result := &Result{Type: "resignation", Cause: "recorded", Winner: "W", Loser: "B"}
	snapshot := Snapshot{
		ID: "continued-result", BoardSize: 9, Rules: "chinese", Komi: 7.5,
		InitialPlayer: "B", CurrentPlayer: "B", Status: "finished", SetupStones: map[string]string{},
		Moves: []Move{}, Stones: map[string]string{}, Captures: map[string]int{"B": 0, "W": 0}, Result: result,
		TreeNodes: []TreeNode{
			{ID: "root"},
			{ID: "n1", ParentID: "root", Result: result},
			{ID: "n2", ParentID: "n1", Move: &Move{Color: "B", Coordinate: "E5"}},
		},
		CurrentNodeID: "n1", MainLineNodeID: "n1",
		NextNodeNumber: 3,
	}
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatalf("結果局面の後続のエラー = %v", err)
	}
}

func Test着手後の属性局面に置いた派生結果を復元する(t *testing.T) {
	session, err := NewConfiguredSession("finished-comment", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendSGFNode([]SGFProperty{{Ident: "C", Values: []string{"after"}}}); err != nil {
		t.Fatal(err)
	}
	beforeDuplicate := session.Snapshot()
	if _, err := session.AppendSGFNode([]SGFProperty{{Ident: "C", Values: []string{"duplicate"}}}); err == nil {
		t.Fatalf("重複するコメントのエラー = %v", err)
	}
	if afterDuplicate := session.Snapshot(); !reflect.DeepEqual(afterDuplicate, beforeDuplicate) {
		t.Fatalf("重複するコメントの拒否後に状態が変わった：拒否前=%+v、拒否後=%+v", beforeDuplicate, afterDuplicate)
	}
	snapshot := session.Snapshot()

	restored, err := RestoreSession(snapshot)
	if err != nil {
		t.Fatalf("終局後のコメント局面を復元できない：%v", err)
	}
	if got := restored.Snapshot(); got.Status != "finished" || got.Result == nil || got.CurrentNodeID != snapshot.CurrentNodeID {
		t.Fatalf("復元結果が不正：%+v", got)
	}
}

func Test局面プロパティは変更前に検証する(t *testing.T) {
	session, err := NewConfiguredSession("局面プロパティの検証", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	nodeID := session.Snapshot().CurrentNodeID
	before := session.Snapshot()
	for _, properties := range [][]SGFProperty{
		{{Ident: "C"}},
		{{Ident: "GN", Values: []string{"途中の対局名"}}},
		{{Ident: "B", Values: []string{"ee"}}},
	} {
		if err := session.SetSGFProperties(nodeID, properties); err == nil {
			t.Fatalf("不正な局面プロパティを受け付けた：%+v", properties)
		}
		if !reflect.DeepEqual(session.Snapshot(), before) {
			t.Fatalf("不正な局面プロパティの拒否後に状態が変化した：%+v", properties)
		}
	}
	if err := session.SetSGFProperties(nodeID, []SGFProperty{{Ident: "C", Values: []string{"検討メモ"}}}); err != nil {
		t.Fatalf("正しい局面プロパティを拒否した：%v", err)
	}
	if _, err := RestoreSession(session.Snapshot()); err != nil {
		t.Fatalf("局面プロパティの変更後の状態を復元できない：%v", err)
	}
}

func Test変化の記録結果が主分岐の末端を変更しない(t *testing.T) {
	session, err := NewConfiguredSession("variation-result", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	mainLineEnd := session.Snapshot().MainLineNodeID
	if err := session.Navigate("root"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("D4"); err != nil {
		t.Fatal(err)
	}
	if err := session.SetSGFRecordedResult(&Result{Type: "resignation", Cause: "recorded", Winner: "B", Loser: "W"}); err != nil {
		t.Fatal(err)
	}
	if got := session.Snapshot().MainLineNodeID; got != mainLineEnd {
		t.Fatalf("変化の結果で主分岐の末端が移動した：実際 = %q、期待値 = %q", got, mainLineEnd)
	}
}

func Test対局状態の復元で開始局面外の開始局面のみの属性を拒否する(t *testing.T) {
	session, err := NewConfiguredSession("invalid-property", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	for index := range snapshot.TreeNodes {
		if snapshot.TreeNodes[index].ID == snapshot.CurrentNodeID {
			snapshot.TreeNodes[index].Properties = []SGFProperty{{Ident: "KM", Values: []string{"0.5"}}}
		}
	}
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatalf("途中局面の開始局面専用プロパティのエラー = %v", err)
	}
}

func Test対局状態の復元ですべての棋譜ツリー分岐を検証する(t *testing.T) {
	session := mustNewTestSession(t, "invalid-branch", 9, 7.5)
	mustPlayTest(t, session, "E5")
	snapshot := session.Snapshot()
	snapshot.TreeNodes = append(snapshot.TreeNodes, TreeNode{ID: "n2", ParentID: "root", Move: &Move{Color: "W", Coordinate: "D4"}})
	snapshot.NextNodeNumber = 3
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatalf("未選択分岐の不正な手番のエラー = %v", err)
	}

	snapshot = session.Snapshot()
	snapshot.TreeNodes = append(snapshot.TreeNodes,
		TreeNode{ID: "n2", ParentID: "n3"},
		TreeNode{ID: "n3", ParentID: "n2"},
	)
	snapshot.NextNodeNumber = 4
	restored := make(chan error, 1)
	go func() {
		_, err := RestoreSession(snapshot)
		restored <- err
	}()
	select {
	case err := <-restored:
		if err == nil {
			t.Fatalf("未選択分岐の循環のエラー = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("未選択分岐の循環検証が完了しませんでした")
	}
}

func Test対局状態の復元で兄弟分岐を個別に検証する(t *testing.T) {
	session := mustNewTestSession(t, "valid-branches", 9, 7.5)
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("D5"); err != nil {
		t.Fatal(err)
	}
	mustCommitCurrentNodeTest(t, session)
	if _, err := session.AddVariation("root", []string{"D4", "E4"}, nil, AgentOrigin, "別の進行"); err != nil {
		t.Fatal(err)
	}

	snapshot := session.Snapshot()
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("兄弟分岐を独立した局面として検証できない：%v", err)
	}
}

func Test対局状態の復元で同じ着手列の異なる兄弟結果を保つ(t *testing.T) {
	session := mustNewTestSession(t, "conflicting-results", 9, 7.5)
	snapshot := session.Snapshot()
	blackWin := Result{Type: "recorded", Cause: "recorded", Value: "B+T"}
	whiteWin := Result{Type: "recorded", Cause: "recorded", Value: "W+T"}
	snapshot.TreeNodes = append(snapshot.TreeNodes,
		TreeNode{ID: "n1", ParentID: "root", Result: &blackWin},
		TreeNode{ID: "n2", ParentID: "root", Result: &whiteWin},
	)
	snapshot.CurrentNodeID = "n1"
	snapshot.MainLineNodeID = "n1"
	snapshot.NextNodeNumber = 3
	snapshot.Status = "finished"
	snapshot.Result = &blackWin
	restored, err := RestoreSession(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Snapshot().TreeNodes) != 3 {
		t.Fatalf("復元した棋譜の局面数 = %d", len(restored.Snapshot().TreeNodes))
	}
}

func Test棋譜ツリーと食い違う付随情報を復元時に拒否する(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Snapshot)
	}{
		{name: "取られた石", change: func(snapshot *Snapshot) {
			for index := range snapshot.TreeNodes {
				if snapshot.TreeNodes[index].Move != nil {
					snapshot.TreeNodes[index].Move.Captured = []string{"A1"}
				}
			}
		}},
		{name: "SGFプロパティの識別子", change: func(snapshot *Snapshot) {
			snapshot.RootProperties = []SGFProperty{{Ident: "C)", Values: []string{"invalid"}}}
		}},
		{name: "AIの選択情報", change: func(snapshot *Snapshot) {
			for index := range snapshot.TreeNodes {
				if snapshot.TreeNodes[index].Move != nil {
					snapshot.TreeNodes[index].AIDecision = &AIDecision{Level: 9, Visits: -1, EngineMove: "missing", SelectedMove: "missing"}
				}
			}
		}},
		{name: "変化の生成元", change: func(snapshot *Snapshot) {
			for index := range snapshot.TreeNodes {
				if snapshot.TreeNodes[index].Move != nil {
					snapshot.TreeNodes[index].Origin = "unknown"
				}
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := mustNewTestSession(t, "invalid-metadata", 9, 7.5)
			mustPlayTest(t, session, "E5")
			snapshot := session.Snapshot()
			test.change(&snapshot)
			if _, err := RestoreSession(snapshot); err == nil {
				t.Fatalf("不正な棋譜情報を受け付けました")
			}
		})
	}
}

func Test対局状態の復元で無効な置き碁と点数結果が拒否される(t *testing.T) {
	session := mustNewTestSession(t, "invalid-settings", 9, 7.5)
	snapshot := session.Snapshot()
	snapshot.Handicap = -1
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatalf("負の置き碁の石数のエラー = %v", err)
	}

	session = mustNewTestSession(t, "invalid-points", 9, 7.5)
	mustPlayTest(t, session, "pass")
	mustPlayTest(t, session, "pass")
	snapshot = session.Snapshot()
	invalid := Result{Type: "points", Cause: "recorded", Winner: "B", Margin: 1, BlackScore: 10, WhiteScore: 20, ScoreKnown: true}
	snapshot.Result = &invalid
	for index := range snapshot.TreeNodes {
		if snapshot.TreeNodes[index].ID == snapshot.CurrentNodeID {
			snapshot.TreeNodes[index].Result = &invalid
		}
	}
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatalf("不整合な得点結果のエラー = %v", err)
	}
}

func Test記録された点数の内訳は目差と一致すれば保持する(t *testing.T) {
	session := mustNewTestSession(t, "impossible-breakdown", 9, 7.5)
	result := Result{Type: "points", Cause: "recorded", Winner: "B", Margin: 3, BlackScore: 100, WhiteScore: 97, ScoreKnown: true}
	if err := session.SetSGFRecordedResult(&result); err != nil {
		t.Fatalf("記録された点数の内訳を拒否した：%v", err)
	}
}

func Test対局状態の復元は非正規な着手座標を拒否する(t *testing.T) {
	for _, coordinate := range []string{"d4", " D4 ", "A01", "A+1", "PASS"} {
		t.Run(coordinate, func(t *testing.T) {
			session := mustNewTestSession(t, "noncanonical-coordinate", 9, 7.5)
			if _, err := session.Play("D4"); err != nil {
				t.Fatal(err)
			}
			snapshot := session.Snapshot()
			for index := range snapshot.TreeNodes {
				if snapshot.TreeNodes[index].Move != nil {
					snapshot.TreeNodes[index].Move.Coordinate = coordinate
				}
			}
			if _, err := RestoreSession(snapshot); err == nil {
				t.Fatalf("非正規な座標%qの復元エラー = %v", coordinate, err)
			}
		})
	}
}

func Test半目単位で一致する点数の内訳を受け付ける(t *testing.T) {
	session := mustNewTestSession(t, "半目単位の点数", 9, -0.5)
	result := Result{
		Type: "points", Cause: "recorded", Winner: "B", Margin: 2.5,
		BlackScore: 10, WhiteScore: 7.5, ScoreKnown: true,
	}
	if err := session.SetSGFRecordedResult(&result); err != nil {
		t.Fatalf("半目単位で一致する点数の内訳を拒否した：%v", err)
	}
}

func Test保存された点数の結果で内訳が明示的に省略される場合がある(t *testing.T) {
	session, err := NewConfiguredSession("imported", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}

	snapshot := session.Snapshot()
	imported := Result{Type: "points", Cause: "recorded", Winner: "B", Margin: 0.5}
	snapshot.Result = &imported
	for index := range snapshot.TreeNodes {
		if snapshot.TreeNodes[index].ID == snapshot.CurrentNodeID {
			snapshot.TreeNodes[index].Result = &imported
		}
	}
	restored, err := RestoreSession(snapshot)
	if err != nil {
		t.Fatalf("「RestoreSession」がエラーを返した：%v", err)
	}
	if result := restored.Snapshot().Result; result == nil || !reflect.DeepEqual(*result, imported) {
		t.Fatalf("結果 = %+v、期待値 = %+v", result, imported)
	}
}

func Test二連続パス結果の点数の内訳を省略した保存状態を拒否する(t *testing.T) {
	session := mustNewTestSession(t, "two-pass-without-breakdown", 9, 7.5)
	mustPlayTest(t, session, "pass")
	mustPlayTest(t, session, "pass")
	snapshot := session.Snapshot()
	omitted := Result{Type: "points", Cause: "two_passes", Winner: "W", Margin: 7.5}
	snapshot.Result = &omitted
	for index := range snapshot.TreeNodes {
		if snapshot.TreeNodes[index].Result != nil {
			snapshot.TreeNodes[index].Result = &omitted
		}
	}
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatalf("点数の内訳を省略した二連続パス結果のエラー = %v", err)
	}
}

func Test明示した記録結果が二連続パスの算出結果で置き換わらない(t *testing.T) {
	session := mustNewTestSession(t, "recorded-breakdown", 9, 7.5)
	mustPlayTest(t, session, "pass")
	mustPlayTest(t, session, "pass")
	recorded := Result{Type: "points", Cause: "recorded", Winner: "W", Margin: 7.5, BlackScore: 10, WhiteScore: 17.5, ScoreKnown: true, DeadStones: []string{}}
	if err := session.SetSGFRecordedResult(&recorded); err != nil {
		t.Fatalf("記録結果を設定できない：%v", err)
	}
	result := session.Snapshot().Result
	if result == nil || !reflect.DeepEqual(*result, recorded) {
		t.Fatalf("結果 = %+v、期待値 = %+v", result, recorded)
	}
}

func Test対局状態の復元で対局全体の結果から結果局面が作成されない(t *testing.T) {
	session := mustNewTestSession(t, "top-level-result", 9, 7.5)
	mustPlayTest(t, session, "E5")
	snapshot := session.Snapshot()
	snapshot.Status = "finished"
	snapshot.Result = &Result{Type: "resignation", Cause: "recorded", Winner: "W", Loser: "B"}
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatalf("棋譜の木にない対局全体の結果のエラー = %v", err)
	}
}

func Test待ったが主分岐と対局結果の両方を更新して往復できる(t *testing.T) {
	tests := []struct {
		name   string
		finish func(*Session) error
	}{
		{name: "投了", finish: func(session *Session) error { return session.Resign() }},
		{name: "二連続パス", finish: func(session *Session) error {
			if _, err := session.Play("pass"); err != nil {
				return err
			}
			_, err := session.Play("pass")
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := mustNewTestSession(t, "undo-round-trip", 9, 7.5)
			if err := test.finish(session); err != nil {
				t.Fatal(err)
			}
			if before := session.Snapshot(); before.Status != "finished" || before.Result == nil {
				t.Fatalf("待った前に終局していない：%+v", before)
			}
			mustCommitCurrentNodeTest(t, session)
			if err := session.Undo(); err != nil {
				t.Fatal(err)
			}
			snapshot := session.Snapshot()
			if snapshot.MainLineNodeID != snapshot.CurrentNodeID || snapshot.Result != nil || snapshot.Status != "playing" {
				t.Fatalf("待った後の主分岐状態 = %+v", snapshot)
			}
			data, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var decoded Snapshot
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if _, err := RestoreSession(decoded); err != nil {
				t.Fatalf("待った後のJSONを復元できない：%v", err)
			}
		})
	}
}

func Test記録点数を待った後の結果を二連続パスと扱わない(t *testing.T) {
	session := mustNewTestSession(t, "recorded-point-undo", 9, 7.5)
	mustPlayTest(t, session, "E5")
	mustPlayTest(t, session, "pass")
	beforeResult := session.Snapshot()
	result := &Result{Type: "points", Cause: "recorded", Winner: "B", Margin: 3.5}
	if err := session.SetSGFRecordedResult(result); err != nil {
		t.Fatal(err)
	}
	if err := session.Undo(); err != nil {
		t.Fatal(err)
	}
	afterUndo := session.Snapshot()
	if !reflect.DeepEqual(afterUndo.Moves, beforeResult.Moves) || afterUndo.CurrentNodeID != beforeResult.CurrentNodeID {
		t.Fatalf("記録結果の待ったで直前のパスまで取り消した：%+v", afterUndo)
	}
}

func Test記録結果を待った後にユーザーが投了した終局理由を記録する(t *testing.T) {
	session := mustNewTestSession(t, "reuse-recorded-result", 9, 7.5)
	recorded := &Result{Type: "resignation", Cause: "recorded", Winner: "W", Loser: "B"}
	if err := session.SetSGFRecordedResult(recorded); err != nil {
		t.Fatal(err)
	}
	if err := session.Undo(); err != nil {
		t.Fatal(err)
	}
	if err := session.Resign(); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	resultCount := 0
	for _, node := range snapshot.TreeNodes {
		if node.Result != nil {
			resultCount++
		}
	}
	if resultCount != 1 || snapshot.Result == nil || snapshot.Result.Cause != "resignation" {
		t.Fatalf("再到達後の結果 = %+v、結果node数 = %d", snapshot.Result, resultCount)
	}
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("再到達した結果を復元できない：%v", err)
	}
}

func Test自動結果と異なる同じ手順の記録結果を別の変化に保つ(t *testing.T) {
	session, err := NewConfiguredSession("既存の記録結果", 9, -0.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := session.Play("pass"); err != nil {
			t.Fatal(err)
		}
	}
	result := Result{Type: "recorded", Cause: "recorded", Value: "Void"}
	if err := session.SetSGFRecordedResult(&result); err != nil {
		t.Fatal(err)
	}
	before := session.Snapshot()
	secondPass := mustSnapshotNode(t, before, before.CurrentNodeID).ParentID
	firstPass := mustSnapshotNode(t, before, secondPass).ParentID
	if _, err := session.AddVariation(firstPass, []string{"pass"}, nil, AgentOrigin, "自動結果"); err != nil {
		t.Fatal(err)
	}
	results := 0
	for _, node := range session.Snapshot().TreeNodes {
		if node.Result != nil {
			results++
		}
	}
	if results < 2 {
		t.Fatalf("同じ着手列に残った結果 = %d", results)
	}
}

func Test記録結果がある着手列に投了結果の変化を追加できる(t *testing.T) {
	session, err := NewConfiguredSession("食い違う記録結果", 9, -0.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := session.Play("pass"); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := session.Snapshot()
	resultNode := mustSnapshotNode(t, snapshot, snapshot.CurrentNodeID)
	completionID := resultNode.ParentID
	if err := session.Navigate(completionID); err != nil {
		t.Fatal(err)
	}
	session.ContinueAfterResult()
	if err := session.Resign(); err != nil {
		t.Fatal(err)
	}
	results := 0
	for _, node := range session.Snapshot().TreeNodes {
		if node.Result != nil {
			results++
		}
	}
	if results < 2 {
		t.Fatalf("同じ着手列に残った結果 = %d", results)
	}
}

func Test二連続パスの終局理由には一致する着手履歴が必要になる(t *testing.T) {
	session := mustNewTestSession(t, "false-two-passes", 9, 7.5)
	mustPlayTest(t, session, "pass")
	result := &Result{Type: "points", Cause: "two_passes", Winner: "W", Margin: 7.5, BlackScore: 0, WhiteScore: 7.5, ScoreKnown: true}
	if err := session.SetSGFRecordedResult(result); err == nil {
		t.Fatalf("パスが1回だけの局面へ二連続パス結果を記録したエラー = %v", err)
	}
}

func Test開始局面に置かれた結果を復元時に拒否する(t *testing.T) {
	session := mustNewTestSession(t, "root-result", 9, 7.5)
	snapshot := session.Snapshot()
	result := Result{Type: "points", Cause: "recorded", Winner: "B", Margin: 100}
	snapshot.Status = "finished"
	snapshot.Result = &result
	snapshot.TreeNodes[0].Result = &result
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatalf("開始局面の結果のエラー = %v", err)
	}
}

func Test進行中の着手局面で復元対局状態が点数結果を拒否する(t *testing.T) {
	session := mustNewTestSession(t, "early-point-result", 9, 7.5)
	mustPlayTest(t, session, "E5")
	snapshot := session.Snapshot()
	result := Result{Type: "points", Cause: "recorded", Winner: "B", Margin: 5}
	snapshot.Status = "finished"
	snapshot.Result = &result
	for index := range snapshot.TreeNodes {
		if snapshot.TreeNodes[index].ID == snapshot.CurrentNodeID {
			snapshot.TreeNodes[index].Result = &result
		}
	}
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatalf("対局途中の着手局面にある点数結果のエラー = %v", err)
	}
}

func Test設定した置き碁の石を盤へ配置する(t *testing.T) {
	session, err := NewConfiguredSession("handicap", 9, 0.5, 5)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.CurrentPlayer != "W" || snapshot.Handicap != 5 || len(snapshot.SetupStones) != 5 || len(snapshot.Moves) != 0 {
		t.Fatalf("想定外の置き碁：%+v", snapshot)
	}
	for _, coordinate := range []string{"C3", "C7", "E5", "G3", "G7"} {
		if snapshot.Stones[coordinate] != "B" || snapshot.SetupStones[coordinate] != "B" {
			t.Errorf("%sに置き碁の石がない", coordinate)
		}
	}
	restored, err := RestoreSession(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot(); !reflect.DeepEqual(got, snapshot) {
		t.Fatalf("復元した状態が異なる：\n実際：%+v\n期待値：%+v", got, snapshot)
	}
}

func Test置き碁は対応していない石数を拒否する(t *testing.T) {
	for _, testCase := range []struct {
		boardSize int
		handicap  int
	}{
		{boardSize: 5, handicap: 6},
		{boardSize: 6, handicap: 5},
	} {
		t.Run(strconv.Itoa(testCase.boardSize)+"路_"+strconv.Itoa(testCase.handicap)+"子", func(t *testing.T) {
			if _, err := NewConfiguredSession("game", testCase.boardSize, 0.5, testCase.handicap); err == nil {
				t.Fatalf("%d路盤の%d子局のエラー = %v", testCase.boardSize, testCase.handicap, err)
			}
		})
	}
	if _, err := NewConfiguredSession("game", 9, 0.5, 1); err == nil {
		t.Fatalf("1子の置き碁のエラー = %v", err)
	}
}

func Test盤サイズごとの上限まで固定配置を使える(t *testing.T) {
	for _, boardSize := range SupportedBoardSizes() {
		t.Run(strconv.Itoa(boardSize)+"路", func(t *testing.T) {
			maximum := map[int]int{5: 5, 6: 4, 7: 9, 9: 9, 13: 9, 19: 9}[boardSize]
			session, err := NewConfiguredSession("handicap", boardSize, DefaultHandicapKomi, maximum)
			if err != nil {
				t.Fatalf("%d路盤の%d子局を作成できない：%v", boardSize, maximum, err)
			}
			snapshot := session.Snapshot()
			if snapshot.Handicap != maximum || len(snapshot.SetupStones) != maximum || snapshot.InitialPlayer != "W" {
				t.Fatalf("%d路盤の%d子局 = %+v", boardSize, maximum, snapshot)
			}
		})
	}
}

func Test置石を盤ごとの線と順に配置する(t *testing.T) {
	tests := []struct {
		boardSize int
		corners   []string
		center    string
		leftRight []string
		topBottom []string
		maximum   int
	}{
		{5, []string{"D4", "B2", "D2", "B4"}, "C3", nil, nil, 5},
		{6, []string{"E5", "B2", "E2", "B5"}, "", nil, nil, 4},
		{7, []string{"F6", "B2", "F2", "B6"}, "D4", []string{"B4", "F4"}, []string{"D6", "D2"}, 9},
		{9, []string{"G7", "C3", "G3", "C7"}, "E5", []string{"C5", "G5"}, []string{"E7", "E3"}, 9},
		{13, []string{"K10", "D4", "K4", "D10"}, "G7", []string{"D7", "K7"}, []string{"G10", "G4"}, 9},
		{19, []string{"Q16", "D4", "Q4", "D16"}, "K10", []string{"D10", "Q10"}, []string{"K16", "K4"}, 9},
	}
	for _, test := range tests {
		for handicap := 2; handicap <= test.maximum; handicap++ {
			t.Run(fmt.Sprintf("%d路_%d子", test.boardSize, handicap), func(t *testing.T) {
				session, err := NewConfiguredSession("置石", test.boardSize, DefaultHandicapKomi, handicap)
				if err != nil {
					t.Fatal(err)
				}
				stones := session.Snapshot().SetupStones
				if len(stones) != handicap {
					t.Fatalf("置石の数 = %d、期待値 = %d", len(stones), handicap)
				}
				expected := append([]string(nil), test.corners[:min(handicap, 4)]...)
				if handicap >= 5 && handicap%2 == 1 {
					expected = append(expected, test.center)
				}
				if handicap >= 6 {
					expected = append(expected, test.leftRight...)
				}
				if handicap >= 8 {
					expected = append(expected, test.topBottom...)
				}
				for _, coordinate := range expected {
					if stones[coordinate] != "B" {
						t.Fatalf("置石%sがありません：%v", coordinate, stones)
					}
				}
			})
		}
	}
}

func Test待ったが取り消した着手を変化として保持する(t *testing.T) {
	session := mustNewTestSession(t, "tree", 9, 7.5)
	for _, coordinate := range []string{"E5", "D5"} {
		if _, err := session.Play(coordinate); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.Undo(); err != nil {
		t.Fatal(err)
	}
	if err := session.Undo(); err != nil {
		t.Fatal(err)
	}
	for _, coordinate := range []string{"F5", "F4"} {
		if _, err := session.Play(coordinate); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := session.Snapshot()
	if len(snapshot.Moves) != 2 || snapshot.Moves[0].Coordinate != "F5" {
		t.Fatalf("想定外の現在手順：%#v", snapshot.Moves)
	}
	if len(snapshot.TreeNodes) != 5 {
		t.Fatalf("棋譜の局面が想定外：%#v", snapshot.TreeNodes)
	}
	rootChildren := 0
	for _, node := range snapshot.TreeNodes {
		if node.ParentID == "root" {
			rootChildren++
		}
	}
	if rootChildren != 2 {
		t.Fatalf("開始局面の子局面数 = %d、棋譜ツリー = %#v", rootChildren, snapshot.TreeNodes)
	}
	restored, err := RestoreSession(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot(); !reflect.DeepEqual(got, snapshot) {
		t.Fatalf("復元した棋譜ツリーが異なる：\n実際：%+v\n期待値：%+v", got, snapshot)
	}
}

func Test再起動後も削除した局面の識別情報を再利用しない(t *testing.T) {
	session, err := NewConfiguredSession("識別子", 5, DefaultKomi, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("D4"); err != nil {
		t.Fatal(err)
	}
	if err := session.Navigate("root"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("C3"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.DeleteVariation("n2"); err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreSession(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Navigate("root"); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Play("B2"); err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot().CurrentNodeID; got != "n3" {
		t.Fatalf("再起動後の局面の識別情報 = %q、期待値 = %q", got, "n3")
	}
}

func Test待ったで取り消した結果を変化に保存して状態を復元できる(t *testing.T) {
	session := mustNewTestSession(t, "undo-result", 9, 7.5)
	mustPlayTest(t, session, "pass")
	mustPlayTest(t, session, "pass")
	if err := session.Undo(); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.Status != "playing" || len(snapshot.Moves) != 1 {
		t.Fatalf("待った後の状態 = %+v", snapshot)
	}
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("待った後の状態を復元できない：%v", err)
	}
}

func Test既存の変化を選択して移動する(t *testing.T) {
	session := mustNewTestSession(t, "tree", 9, 7.5)
	mustPlayTest(t, session, "E5")
	originalNode := session.Snapshot().CurrentNodeID
	if err := session.Undo(); err != nil {
		t.Fatal(err)
	}
	mustPlayTest(t, session, "F5")
	if err := session.Navigate(originalNode); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if len(snapshot.Moves) != 1 || snapshot.Moves[0].Coordinate != "E5" || snapshot.Stones["E5"] != "B" {
		t.Fatalf("想定外の選択中の変化：%+v", snapshot)
	}
	if err := session.Navigate("missing"); err == nil {
		t.Fatalf("存在しない局面の選択エラー = %v", err)
	}
}

func Test変化を追加すると現在の位置を保ち既存の局面を再利用する(t *testing.T) {
	session := mustNewTestSession(t, "agent", 9, 7.5)
	mustPlayTest(t, session, "E5")
	displayed := session.Snapshot().CurrentNodeID
	nodes, err := session.AddVariation("root", []string{"D4", "F6"}, nil, "agent", "別の進行")
	if err != nil {
		t.Fatal(err)
	}
	if got := session.Snapshot().CurrentNodeID; got != displayed {
		t.Fatalf("現在局面が%sから%sへ移動した", displayed, got)
	}
	if len(nodes) != 2 {
		t.Fatalf("追加した局面が想定外：%v", nodes)
	}
	before := len(session.Snapshot().TreeNodes)
	duplicate, err := session.AddVariation("root", []string{"D4", "F6"}, nil, "agent", "重複")
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Snapshot().TreeNodes) != before || !reflect.DeepEqual(duplicate, nodes) {
		t.Fatalf("重複する変化が追加された：初回=%v、重複=%v", nodes, duplicate)
	}
	for _, node := range session.Snapshot().TreeNodes {
		if node.ID == nodes[0] && (node.Origin != "agent" || node.VariationTitle != "別の進行" || !node.Highlighted) {
			t.Fatalf("AI agent由来の情報がない：%+v", node)
		}
	}
}

func Test空白を含む座標で既存局面に印を付けない(t *testing.T) {
	session, err := NewConfiguredSession("variation-space", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("D4"); err != nil {
		t.Fatal(err)
	}
	if err := session.Navigate("root"); err != nil {
		t.Fatal(err)
	}
	ids, err := session.AddVariation("root", []string{"  D4  "}, nil, "agent", "検討図")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range session.Snapshot().TreeNodes {
		if node.ID == ids[0] && (node.Origin != "" || node.Highlighted || node.VariationTitle != "") {
			t.Fatalf("既存局面にAI agent由来の印が付いた：%+v", node)
		}
	}
}

func Test主分岐は表示された変化から独立している(t *testing.T) {
	session, err := NewConfiguredSession("main-line", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("D4"); err != nil {
		t.Fatal(err)
	}
	mustCommitCurrentNodeTest(t, session)
	mainLineNodeID := session.Snapshot().CurrentNodeID
	if _, err := session.AddVariation("root", []string{"F6"}, nil, "agent", "検討図"); err != nil {
		t.Fatal(err)
	}
	variationID := ""
	for _, node := range session.Snapshot().TreeNodes {
		if node.Move != nil && node.Move.Coordinate == "F6" {
			variationID = node.ID
		}
	}
	if err := session.Navigate(variationID); err != nil {
		t.Fatal(err)
	}
	if got := session.Snapshot().MainLineNodeID; got != mainLineNodeID {
		t.Fatalf("主分岐の末端 = %s、期待値 = %s", got, mainLineNodeID)
	}
}

func Test既存の変化を再利用して主分岐へ昇格する(t *testing.T) {
	session, err := NewConfiguredSession("promote", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := session.AddVariation("root", []string{"D4"}, nil, "agent", "検討図")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("D4"); err != nil {
		t.Fatal(err)
	}
	mustCommitCurrentNodeTest(t, session)
	snapshot := session.Snapshot()
	if snapshot.MainLineNodeID != nodes[0] {
		t.Fatalf("主分岐の末端 = %s、期待値 = %s", snapshot.MainLineNodeID, nodes[0])
	}
	for _, node := range snapshot.TreeNodes {
		if node.ID == nodes[0] && (node.Origin != "" || node.VariationTitle != "" || node.Highlighted) {
			t.Fatalf("主分岐へ確定した局面にAI agentの印が残っている：%+v", node)
		}
	}
}

func Test変化を削除すると部分木が削除され主分岐が保持される(t *testing.T) {
	session, err := NewConfiguredSession("delete-variation", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	mustCommitCurrentNodeTest(t, session)
	mainLineNodeID := session.Snapshot().MainLineNodeID
	variation, err := session.AddVariation("root", []string{"D4", "F6"}, nil, "agent", "削除する変化")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Navigate(variation[1]); err != nil {
		t.Fatal(err)
	}
	deleted, err := session.DeleteVariation(variation[0])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(deleted, variation) {
		t.Fatalf("削除した局面 = %v、期待値 = %v", deleted, variation)
	}
	snapshot := session.Snapshot()
	if snapshot.CurrentNodeID != "root" || snapshot.MainLineNodeID != mainLineNodeID {
		t.Fatalf("削除後の局面が想定外：%+v", snapshot)
	}
	if len(snapshot.TreeNodes) != 2 {
		t.Fatalf("棋譜ツリーの局面数 = %d、開始局面と主分岐局面を期待", len(snapshot.TreeNodes))
	}
	if _, err := session.DeleteVariation(mainLineNodeID); err != nil {
		t.Fatalf("主分岐局面の削除に失敗した：%v", err)
	}
	if snapshot := session.Snapshot(); snapshot.MainLineNodeID != "root" || len(snapshot.TreeNodes) != 1 {
		t.Fatalf("主分岐が開始局面まで短縮されていない：%+v", snapshot)
	}
}

func Test変化を削除すると作成順に局面が返される(t *testing.T) {
	session := mustNewTestSession(t, "delete-long-variation", 19, 7.5)
	variation, err := session.AddVariation("root", []string{
		"A1", "T19", "A2", "T18", "A3", "T17", "A4", "T16", "A5", "T15", "A6", "T14",
	}, nil, AgentOrigin, "長い変化")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.TreeNodes[0].ID != "root" || snapshot.TreeNodes[10].ID != "n10" {
		t.Fatalf("棋譜の局面順 = %v", snapshot.TreeNodes)
	}
	deleted, err := session.DeleteVariation(variation[0])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(deleted, variation) {
		t.Fatalf("削除した局面 = %v、作成順 = %v", deleted, variation)
	}
}

func Test二連続パスの結果を削除しても結果を再生成しない(t *testing.T) {
	session := mustNewTestSession(t, "delete-two-pass-result", 9, 7.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	resultNodeID := session.Snapshot().CurrentNodeID
	if _, err := session.DeleteVariation(resultNodeID); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.Status != "playing" || snapshot.Result != nil || snapshot.MainLineNodeID == resultNodeID {
		t.Fatalf("結果局面削除後の状態 = %+v", snapshot)
	}
	for _, node := range snapshot.TreeNodes {
		if node.ID == resultNodeID {
			t.Fatalf("削除した結果局面 %sが残っている", resultNodeID)
		}
	}
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("結果局面削除後の状態を復元できない：%v", err)
	}
}

func Test取り消した二連続パスを再び打つと結果に到達する(t *testing.T) {
	session := mustNewTestSession(t, "reach-canceled-result-again", 9, 7.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	firstPassID := session.Snapshot().CurrentNodeID
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	resultID := session.Snapshot().CurrentNodeID
	if _, err := session.DeleteVariation(resultID); err != nil {
		t.Fatal(err)
	}
	if err := session.Navigate(firstPassID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	mustCommitCurrentNodeTest(t, session)
	snapshot := session.Snapshot()
	if snapshot.Status != "finished" || snapshot.Result == nil || snapshot.Result.Cause != "two_passes" {
		t.Fatalf("二連続パスを再び打った後の状態 = %+v", snapshot)
	}
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("結果の取り消し局面へ再到達した状態を復元できない：%v", err)
	}
	for _, node := range snapshot.TreeNodes {
		if node.AutomaticResultCanceled {
			t.Fatalf("再び打った二連続パスに結果の取り消しが残った：%+v", node)
		}
	}
}

func Test取り消した二連続パスを変化で再び打つと結果を作る(t *testing.T) {
	session := mustNewTestSession(t, "variation-canceled-result", 9, 7.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	firstPassID := session.Snapshot().CurrentNodeID
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	resultID := session.Snapshot().CurrentNodeID
	if _, err := session.DeleteVariation(resultID); err != nil {
		t.Fatal(err)
	}
	if err := session.Navigate("root"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	mustCommitCurrentNodeTest(t, session)
	mainLineID := session.Snapshot().MainLineNodeID
	if _, err := session.AddVariation(firstPassID, []string{"pass"}, nil, AgentOrigin, "二連続パスの別図"); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.MainLineNodeID != mainLineID {
		t.Fatalf("変化の終局状態 = %+v", snapshot)
	}
	var hasCanceledResult, hasVariationResult bool
	for _, node := range snapshot.TreeNodes {
		hasCanceledResult = hasCanceledResult || node.AutomaticResultCanceled
		hasVariationResult = hasVariationResult || node.Result != nil && node.Result.Cause == "two_passes"
	}
	if hasCanceledResult {
		t.Fatal("再び打った二連続パスに結果の取り消しが残った")
	}
	if !hasVariationResult {
		t.Fatal("再び打った二連続パスの結果がありません")
	}
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("変化の終局を復元できない：%v", err)
	}
}

func Test取り消した二連続パス局面からの投了を復元できる(t *testing.T) {
	session := mustNewTestSession(t, "resign-canceled-result", 9, 7.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	resultID := session.Snapshot().CurrentNodeID
	if _, err := session.DeleteVariation(resultID); err != nil {
		t.Fatal(err)
	}
	if err := session.Resign(); err != nil {
		t.Fatal(err)
	}
	mustCommitCurrentNodeTest(t, session)
	snapshot := session.Snapshot()
	if snapshot.Result == nil || snapshot.Result.Cause != "resignation" {
		t.Fatalf("投了結果 = %+v", snapshot.Result)
	}
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("結果の取り消し局面からの投了を復元できない：%v", err)
	}
}

func Test復元時の結果比較が完全一致を求める(t *testing.T) {
	session := mustNewTestSession(t, "result-tolerance", 9, 7.5)
	if err := session.SetSGFRecordedResult(&Result{Type: "points", Cause: "recorded", Winner: "B", Margin: 3.5}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		delta   float64
		wantErr bool
	}{
		{name: "同じ値"},
		{name: "最小の異なる値", delta: math.Nextafter(3.5, 4) - 3.5, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := session.Snapshot()
			copy := *snapshot.Result
			copy.Margin += test.delta
			snapshot.Result = &copy
			_, err := RestoreSession(snapshot)
			if (err != nil) != test.wantErr {
				t.Fatalf("差分%gの復元エラー：%v", test.delta, err)
			}
		})
	}
}

func Test復元により非正規AI決定座標が拒否される(t *testing.T) {
	session := mustNewTestSession(t, "ai-coordinate", 9, 7.5)
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	for _, coordinate := range []string{" e5 ", "\u00a0e5", "e5\u2003", "\u3000e5\u3000"} {
		snapshot := session.Snapshot()
		for index := range snapshot.TreeNodes {
			if snapshot.TreeNodes[index].ID == snapshot.CurrentNodeID {
				snapshot.TreeNodes[index].AIDecision = &AIDecision{Level: 1, Visits: 1, EngineMove: coordinate, SelectedMove: "e5"}
			}
		}
		if _, err := RestoreSession(snapshot); err == nil {
			t.Fatalf("非正規のAI判断座標%qのエラー = %v", coordinate, err)
		}
	}
}

func Test複数のAI判断座標が不正でも同じ理由で拒否する(t *testing.T) {
	session := mustNewTestSession(t, "ai-coordinate-order", 9, 7.5)
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	for index := range snapshot.TreeNodes {
		if snapshot.TreeNodes[index].ID == snapshot.CurrentNodeID {
			snapshot.TreeNodes[index].AIDecision = &AIDecision{Level: 1, Visits: 1, EngineMove: " e5 ", SelectedMove: " d4 "}
		}
	}
	for range 100 {
		if _, err := RestoreSession(snapshot); err == nil {
			t.Fatalf("拒否理由 = %v", err)
		}
	}
}

func Test復元では文章型の正規化済みの値を受け付ける(t *testing.T) {
	session := mustNewTestSession(t, "property-whitespace", 9, 7.5)
	snapshot := session.Snapshot()
	for _, property := range []SGFProperty{
		{Ident: "GN", Values: []string{"name continued"}},
		{Ident: "C", Values: []string{"comment continued"}},
		{Ident: "PB", Values: []string{"井山　裕太"}},
	} {
		candidate := snapshot
		candidate.RootProperties = []SGFProperty{property}
		if _, err := RestoreSession(candidate); err != nil {
			t.Fatalf("属性%sの空白を拒否した：%v", property.Ident, err)
		}
	}
}

func TestGPで始まる未知のプロパティを復元時にも保持する(t *testing.T) {
	session := mustNewTestSession(t, "unknown-gp-property", 9, 7.5)
	snapshot := session.Snapshot()
	snapshot.RootProperties = []SGFProperty{{Ident: "GPME", Values: []string{"1"}}}
	restored, err := RestoreSession(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	properties := restored.Snapshot().RootProperties
	if len(properties) != 1 || properties[0].Ident != "GPME" || !slices.Equal(properties[0].Values, []string{"1"}) {
		t.Fatalf("復元した未知のプロパティ：%+v", properties)
	}
}

func Test着手のない変化にもAI_agentの印を保持できる(t *testing.T) {
	session := mustNewTestSession(t, "agent-metadata", 9, 7.5)
	snapshot := session.Snapshot()
	snapshot.TreeNodes = append(snapshot.TreeNodes, TreeNode{
		ID: "n1", ParentID: "root", Origin: AgentOrigin, VariationTitle: "説明した変化", Highlighted: true,
	})
	snapshot.NextNodeNumber = 2
	restored, err := RestoreSession(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	restoredSnapshot := restored.Snapshot()
	var found *TreeNode
	for index := range restoredSnapshot.TreeNodes {
		node := &restoredSnapshot.TreeNodes[index]
		if node.ID == "n1" {
			found = node
			break
		}
	}
	if found == nil || found.Origin != AgentOrigin || found.VariationTitle != "説明した変化" || !found.Highlighted {
		t.Fatalf("復元したAI agentの印：%+v", found)
	}
}

func Test主分岐を削除すると生き残っている最も古い分岐が昇格する(t *testing.T) {
	session, err := NewConfiguredSession("delete-main-line", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	deletedRoot := session.Snapshot().CurrentNodeID
	mustCommitCurrentNodeTest(t, session)
	if _, err := session.Play("F5"); err != nil {
		t.Fatal(err)
	}
	mustCommitCurrentNodeTest(t, session)
	variation, err := session.AddVariation("root", []string{"D4", "F6"}, nil, "agent", "残す変化")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AddVariation("root", []string{"C3", "G7"}, nil, "agent", "後から作った変化"); err != nil {
		t.Fatal(err)
	}
	for _, coordinate := range []string{"A1", "A2", "A3", "A4", "A5", "A6"} {
		if _, err := session.AddVariation("root", []string{coordinate}, nil, AgentOrigin, "並び順の検証"); err != nil {
			t.Fatal(err)
		}
	}
	snapshotBeforeDelete := session.Snapshot()
	slices.Reverse(snapshotBeforeDelete.TreeNodes)
	session, err = RestoreSession(snapshotBeforeDelete)
	if err != nil {
		t.Fatalf("局面の並びを変えた保存データを復元できない：%v", err)
	}

	deleted, err := session.DeleteVariation(deletedRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 {
		t.Fatalf("削除した局面 = %v、主分岐の部分木を期待", deleted)
	}
	snapshot := session.Snapshot()
	if snapshot.CurrentNodeID != "root" {
		t.Fatalf("現在局面 = %s、期待値 = 開始局面", snapshot.CurrentNodeID)
	}
	if snapshot.MainLineNodeID != variation[1] {
		t.Fatalf("主分岐の末端 = %s、期待値 = %s", snapshot.MainLineNodeID, variation[1])
	}
	for _, node := range snapshot.TreeNodes {
		if node.ID == variation[0] && (node.Origin != "" || node.VariationTitle != "" || node.Highlighted) {
			t.Fatalf("主分岐へ昇格した分岐にAI agentの印が残っている：%+v", node)
		}
	}
}

func Test変化を昇格すると主分岐が選択された分岐に変更される(t *testing.T) {
	session, err := NewConfiguredSession("promote-variation", 9, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	mustCommitCurrentNodeTest(t, session)
	oldMainLineNodeID := session.Snapshot().MainLineNodeID
	variation, err := session.AddVariation("root", []string{"D4"}, nil, "agent", "主分岐にする変化")
	if err != nil {
		t.Fatal(err)
	}
	for _, coordinate := range []string{"C3", "G7", "A1", "A2", "A3", "A4"} {
		if _, err := session.AddVariation("root", []string{coordinate}, nil, AgentOrigin, "番号を進める変化"); err != nil {
			t.Fatal(err)
		}
	}
	oldestContinuation, err := session.AddVariation(variation[0], []string{"F6"}, nil, AgentOrigin, "先に作った続き")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AddVariation(variation[0], []string{"H8"}, nil, AgentOrigin, "後から作った続き"); err != nil {
		t.Fatal(err)
	}
	snapshotBeforePromotion := session.Snapshot()
	slices.Reverse(snapshotBeforePromotion.TreeNodes)
	session, err = RestoreSession(snapshotBeforePromotion)
	if err != nil {
		t.Fatalf("局面の並びを変えた保存データを復元できない：%v", err)
	}
	if err := session.PromoteVariation(variation[0]); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.MainLineNodeID != oldestContinuation[0] {
		t.Fatalf("主分岐の末端 = %s、期待値 = %s", snapshot.MainLineNodeID, oldestContinuation[0])
	}
	for _, node := range snapshot.TreeNodes {
		if (slices.Contains(variation, node.ID) || slices.Contains(oldestContinuation, node.ID)) && (node.Origin != "" || node.VariationTitle != "" || node.Highlighted) {
			t.Fatalf("主分岐へ昇格した局面にAI agentの印が残っている：%+v", node)
		}
	}
	if _, err := session.DeleteVariation(oldMainLineNodeID); err != nil {
		t.Fatalf("以前の主分岐が変化になっていない：%v", err)
	}
}

func Test開始局面の削除と主分岐の昇格を拒否する(t *testing.T) {
	session := mustNewTestSession(t, "棋譜ツリーの拒否条件", 9, 7.5)
	if _, err := session.DeleteVariation("root"); err == nil {
		t.Fatal("開始局面を削除しました")
	}
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	mustCommitCurrentNodeTest(t, session)
	if err := session.PromoteVariation(session.Snapshot().CurrentNodeID); err == nil {
		t.Fatal("主分岐にある局面を昇格しました")
	}
}

func Test投了した側が局面の手番と違う保存状態を拒否する(t *testing.T) {
	session := mustNewTestSession(t, "投了の手番", 9, 7.5)
	if err := session.Resign(); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	for index := range snapshot.TreeNodes {
		if snapshot.TreeNodes[index].Result != nil {
			snapshot.TreeNodes[index].Result.Winner = "B"
			snapshot.TreeNodes[index].Result.Loser = "W"
		}
	}
	snapshot.Result.Winner = "B"
	snapshot.Result.Loser = "W"
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatal("手番でない側の投了を復元しました")
	}
}

func Test対局状態は固定の置石でないSGF初期配置をサポートする(t *testing.T) {
	session, err := NewSessionWithSetup("imported", 9, 6.5, 0, map[string]string{"A1": "B", "J9": "W"}, "W")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.InitialPlayer != "W" || snapshot.CurrentPlayer != "W" || snapshot.Stones["A1"] != "B" || snapshot.Stones["J9"] != "W" {
		t.Fatalf("想定外の初期配置：%+v", snapshot)
	}
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	if err := session.Navigate("root"); err != nil {
		t.Fatal(err)
	}
	if got := session.Snapshot(); got.CurrentPlayer != "W" || len(got.Stones) != 2 {
		t.Fatalf("開始局面で初期配置が復元されていない：%+v", got)
	}
}

func Test呼吸点のない石を含む初期配置を拒否する(t *testing.T) {
	tests := []struct {
		name  string
		setup map[string]string
	}{
		{name: "黒の一子", setup: map[string]string{"A1": "B", "B1": "W", "A2": "W"}},
		{name: "白の二子", setup: map[string]string{"A1": "W", "B1": "W", "A2": "B", "B2": "B", "C1": "B"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewSessionWithSetup("無呼吸の初期配置", 5, 0.5, 0, test.setup, "B")
			if !IsSetupGroupWithoutLiberties(err) {
				t.Fatalf("拒否理由 = %v", err)
			}
		})
	}
}

func Test呼吸点が一つ残る初期配置を受け付ける(t *testing.T) {
	session, err := NewSessionWithSetup("アタリの初期配置", 5, 0.5, 0, map[string]string{"A1": "B", "B1": "W"}, "B")
	if err != nil {
		t.Fatal(err)
	}
	if got := session.Snapshot().Stones; got["A1"] != "B" || got["B1"] != "W" {
		t.Fatalf("初期配置 = %+v", got)
	}
}

func Test盤の右端を越えた点を隣として扱わない(t *testing.T) {
	session, err := NewSessionWithSetup(
		"右端の取り",
		5,
		0.5,
		0,
		map[string]string{"E3": "B", "D3": "W", "E2": "W"},
		"W",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("E4"); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if _, remains := snapshot.Stones["E3"]; remains {
		t.Fatalf("取られる黒石が残っています：%+v", snapshot.Stones)
	}
	if snapshot.Captures["W"] != 1 {
		t.Fatalf("白の取った石 = %d、期待値 = 1", snapshot.Captures["W"])
	}
}

func Test局面メモを更新するとコメントと重要な印が保存される(t *testing.T) {
	session := mustNewTestSession(t, "review", 9, 7.5)
	mustPlayTest(t, session, "E5")
	nodeID := session.Snapshot().CurrentNodeID
	if err := session.UpdateNodeReview(nodeID, "中央を意識した手", true); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	var reviewed TreeNode
	for _, node := range snapshot.TreeNodes {
		if node.ID == nodeID {
			reviewed = node
		}
	}
	if !reviewed.Important || len(reviewed.Properties) != 1 || reviewed.Properties[0].Ident != "C" || reviewed.Properties[0].Values[0] != "中央を意識した手" {
		t.Fatalf("想定外の検討局面：%+v", reviewed)
	}
	if err := session.UpdateNodeReview(nodeID, "", false); err != nil {
		t.Fatal(err)
	}
	for _, node := range session.Snapshot().TreeNodes {
		if node.ID == nodeID && len(node.Properties) != 0 {
			t.Fatalf("消去したコメントが残っている：%+v", node)
		}
	}
}
