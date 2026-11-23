package sgf

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go-practice/internal/game"
)

func TestSGFの設定と棋譜ツリーを読み書きする(t *testing.T) {
	const source = "(;GM[1]FF[4]CA[UTF-8]SZ[7]RU[Chinese]KM[0.5]HA[2]AB[cc][ee]PL[W]GN[対局]DT[2026-11-14]XX[保持]C[開始]HO[1];W[dd]C[主分岐](;B[cd]HO[1])(;B[de]YY[変化]))"
	session, err := DecodeGameWithDeadStoneEstimator(source, "設定と棋譜ツリー", nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.BoardSize != 7 || snapshot.Komi != 0.5 || snapshot.Handicap != 2 || snapshot.InitialPlayer != "W" {
		t.Fatalf("対局設定 = 盤%d コミ%v 置石%d 開始手番%s", snapshot.BoardSize, snapshot.Komi, snapshot.Handicap, snapshot.InitialPlayer)
	}
	if snapshot.SetupStones["C5"] != "B" || snapshot.SetupStones["E3"] != "B" {
		t.Fatalf("初期配置 = %#v", snapshot.SetupStones)
	}
	encoded, err := Encode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, property := range []string{"GN[対局]", "DT[2026-11-14]", "XX[保持]", "C[開始]", "HO[1]", "YY[変化]"} {
		if !strings.Contains(encoded, property) {
			t.Fatalf("書き出したSGFに%sがありません：%s", property, encoded)
		}
	}
	assertStableSGF(t, encoded, nil)
}

func Test投了を待った対局は主分岐の末端を変えずに往復する(t *testing.T) {
	session, err := game.NewSessionWithSetup("投了を待った対局", 9, 7.5, 0, map[string]string{}, "B")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Resign(); err != nil {
		t.Fatal(err)
	}
	if err := session.Undo(); err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeGameWithDeadStoneEstimator(encoded, "投了を待った対局の再読み込み", nil)
	if err != nil {
		t.Fatalf("書き出したSGFを読み込めません：%v\n%s", err, encoded)
	}
	snapshot := restored.Snapshot()
	if snapshot.MainLineNodeID != "root" {
		t.Fatalf("再読み込み後の主分岐の末端 = %s、期待値 = root", snapshot.MainLineNodeID)
	}
}

func Test置石なしを明示した初期配置を拒否する(t *testing.T) {
	_, err := DecodeGameWithDeadStoneEstimator("(;FF[4]SZ[9]HA[0]AB[aa])", "置石なし", nil)
	var decoded *decodeError
	if !errors.As(err, &decoded) || decoded.reason != reasonSetup {
		t.Fatalf("拒否理由 = %v", err)
	}
}

func Test呼吸点のない石を含む初期配置を理由付きで拒否する(t *testing.T) {
	_, err := DecodeGameWithDeadStoneEstimator("(;FF[4]SZ[5]AB[ab][bb][ca]AW[aa][ba])", "無呼吸の初期配置", nil)
	var decoded *decodeError
	if !errors.As(err, &decoded) || decoded.reason != reasonSetupGroupWithoutLiberties {
		t.Fatalf("拒否理由 = %v", err)
	}
}

func Test初期配置の一般的な誤りを呼吸点の理由に分類しない(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
	}{
		{"黒白の初期配置が同じ座標", "(;FF[4]SZ[9]AB[aa]AW[aa])"},
		{"置石の上限超過", "(;FF[4]SZ[5]HA[6]AB[aa][ba][ca][da][ea][ab])"},
		{"開始手番が不正", "(;FF[4]SZ[5]PL[X]AB[cc])"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeGameWithDeadStoneEstimator(test.source, test.name, nil)
			var decoded *decodeError
			if !errors.As(err, &decoded) || decoded.reason != reasonSetup {
				t.Fatalf("拒否理由 = %v", err)
			}
		})
	}
}

func Test開始局面以外のAEも未対応のプロパティとして拒否する(t *testing.T) {
	_, err := DecodeGameWithDeadStoneEstimator("(;FF[4]SZ[9];B[aa]AE[bb])", "AEを含む棋譜", nil)
	var decoded *decodeError
	if !errors.As(err, &decoded) || decoded.reason != reasonProperty {
		t.Fatalf("拒否理由 = %v", err)
	}
}

func TestSGFの文字コードと規則を検査する(t *testing.T) {
	tests := []struct {
		name   string
		source string
		reason decodeErrorReason
	}{
		{"FFがない", "(;SZ[9])", reasonFileFormat},
		{"FFが違う", "(;FF[3]SZ[9])", reasonFileFormat},
		{"囲碁でない", "(;FF[4]GM[2]SZ[9])", reasonGameType},
		{"非ASCIIでCAがない", "(;FF[4]SZ[9]C[碁])", reasonCharset},
		{"文字コードが違う", "(;FF[4]CA[Shift_JIS]SZ[9])", reasonCharset},
		{"規則が違う", "(;FF[4]SZ[9]RU[Japanese])", reasonRules},
		{"盤が非対応", "(;FF[4]SZ[8])", reasonBoardSize},
		{"盤が正方形でない", "(;FF[4]SZ[9:7])", reasonBoardSize},
		{"コミの刻みが違う", "(;FF[4]SZ[9]KM[7.25])", reasonKomi},
		{"複数対局", "(;FF[4]SZ[9])(;FF[4]SZ[9])", reasonCollection},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeGameWithDeadStoneEstimator(test.source, test.name, nil)
			var decoded *decodeError
			if !errors.As(err, &decoded) || decoded.reason != test.reason {
				t.Fatalf("拒否理由 = %v", err)
			}
		})
	}
}

func TestSGFの拒否理由を種類で返す(t *testing.T) {
	tests := []struct {
		name   string
		source string
		reason decodeErrorReason
	}{
		{"UTF-8でない", string([]byte{0xff}), reasonEncoding},
		{"BOMを含む", "\ufeff(;FF[4]SZ[9])", reasonBOM},
		{"文法が不正", "(", reasonSyntax},
		{"対局名の値が複数", "(;FF[4]SZ[9]GN[a][b])", reasonGameName},
		{"日付が不正", "(;FF[4]SZ[9]DT[x])", reasonDate},
		{"置き碁の石数と配置が違う", "(;FF[4]SZ[9]HA[2])", reasonSetup},
		{"非対応のプロパティ", "(;FF[4]SZ[9]AE[aa])", reasonProperty},
		{"プロパティが重複", "(;FF[4]FF[4]SZ[9])", reasonProperty},
		{"途中に対局名がある", "(;FF[4]SZ[9];B[aa]GN[x])", reasonPlacement},
		{"途中に結果がある", "(;FF[4]SZ[9];B[aa]RE[B+R])", reasonPlacement},
		{"途中に日付がある", "(;FF[4]SZ[9];B[aa]DT[2026-11-22])", reasonPlacement},
		{"途中にコミがある", "(;FF[4]SZ[9];B[aa]KM[7.5])", reasonPlacement},
		{"途中に置石数がある", "(;FF[4]SZ[9];B[aa]HA[2])", reasonPlacement},
		{"途中に黒の初期配置がある", "(;FF[4]SZ[9];B[aa]AB[bb])", reasonPlacement},
		{"途中に白の初期配置がある", "(;FF[4]SZ[9];B[aa]AW[bb])", reasonPlacement},
		{"途中に開始手番がある", "(;FF[4]SZ[9];B[aa]PL[W])", reasonPlacement},
		{"手番と着手が違う", "(;FF[4]SZ[9];B[aa];B[bb])", reasonMoveOrder},
		{"石のある点へ打つ", "(;FF[4]SZ[9];B[aa];W[aa])", reasonMove},
		{"算出した結果と違う", "(;FF[4]SZ[9]KM[7.5]RE[B+1];B[];W[])", reasonResult},
		{"重要な局面の値が不正", "(;FF[4]SZ[9];B[aa]HO[2])", reasonMetadata},
		{"同じ着手の分岐が重複", "(;FF[4]SZ[9];B[aa](;W[bb])(;W[bb]))", reasonStructure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeGameWithDeadStoneEstimator(test.source, test.name, func(game.Snapshot) ([]string, error) {
				return []string{}, nil
			})
			var decoded *decodeError
			if !errors.As(err, &decoded) || decoded.reason != test.reason {
				t.Fatalf("拒否理由 = %v", err)
			}
		})
	}
}

func Test二連続パス後の続行と分岐を保持する(t *testing.T) {
	const source = "(;FF[4]CA[UTF-8]SZ[5];B[];W[](;B[cc];W[dd])(;B[cd]C[別の続き]))"
	session, err := DecodeGameWithDeadStoneEstimator(source, "二連続パス後の続行", nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, "(;B[cc];W[dd])") || !strings.Contains(encoded, "(;B[cd]C[別の続き])") {
		t.Fatalf("二連続パス後の分岐 = %s", encoded)
	}
	assertStableSGF(t, encoded, nil)
}

func Test二連続パスの結果と地を安定して往復する(t *testing.T) {
	const source = "(;FF[4]CA[UTF-8]SZ[5]KM[7.5]RE[B+17.5];B[aa];W[cc];B[];W[]TB[cc])"
	assertStableSGF(t, source, func(game.Snapshot) ([]string, error) {
		t.Fatal("TBから死石を判断できるため推測を呼んではいけません")
		return nil, nil
	})
}

func Test結果を削除した二連続パスを結果なしで安定して往復する(t *testing.T) {
	estimator := func(game.Snapshot) ([]string, error) { return []string{}, nil }
	session, err := DecodeGameWithDeadStoneEstimator("(;FF[4]SZ[5]RE[Void];B[];W[])", "結果の削除", estimator)
	if err != nil {
		t.Fatal(err)
	}
	resultNodeID := session.Snapshot().MainLineNodeID
	if _, err := session.DeleteVariation(resultNodeID); err != nil {
		t.Fatal(err)
	}
	first, err := Encode(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first, "RE[") {
		t.Fatalf("削除した結果を書き出しました：%s", first)
	}
	restored, err := DecodeGameWithDeadStoneEstimator(first, "結果の削除後", estimator)
	if err != nil {
		t.Fatalf("結果を削除したSGFを読み込めません：%v\n%s", err, first)
	}
	if restored.Snapshot().Result != nil {
		t.Fatalf("削除した結果が再生成されました：%+v", restored.Snapshot().Result)
	}
	second, err := Encode(restored.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("結果を削除したSGFが安定していません\n1回目：%s\n2回目：%s", first, second)
	}
}

func Test主分岐の末端に変化があるSGFは末端を保って安定する(t *testing.T) {
	session, err := game.NewConfiguredSession("主分岐の末端", 5, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AddVariation("root", []string{"B2"}, nil, game.AgentOrigin, "検討図"); err != nil {
		t.Fatal(err)
	}
	if session.Snapshot().MainLineNodeID != "root" {
		t.Fatalf("変化の追加で主分岐の末端が変わりました：%s", session.Snapshot().MainLineNodeID)
	}
	first, err := Encode(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeGameWithDeadStoneEstimator(first, "主分岐の末端", nil)
	if err != nil {
		t.Fatalf("主分岐の末端を保ったSGFを読み込めません：%v\n%s", err, first)
	}
	second, err := Encode(restored.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("主分岐の末端を保ったSGFが安定していません\n1回目：%s\n2回目：%s", first, second)
	}
}

func Test空の先頭分岐を主分岐の終端として読む(t *testing.T) {
	session, err := DecodeGameWithDeadStoneEstimator("(;FF[4]SZ[9];B[ee](;)(;W[dd]))", "主分岐の終端", nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if len(snapshot.TreeNodes) != 3 {
		t.Fatalf("対局の局面数 = %d、期待値 = 3", len(snapshot.TreeNodes))
	}
	mainEnd := snapshotNode(snapshot.TreeNodes, snapshot.MainLineNodeID)
	if mainEnd == nil || mainEnd.Move == nil || mainEnd.Move.Coordinate != "E5" {
		t.Fatalf("主分岐の末端 = %#v", mainEnd)
	}
}

func Test局面数上限でも書き出し用の終端は局面に数えない(t *testing.T) {
	session, err := game.NewConfiguredSession("局面数上限", 5, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	for number := 1; number < 4096; number++ {
		snapshot.TreeNodes = append(snapshot.TreeNodes, game.TreeNode{
			ID:       fmt.Sprintf("n%d", number),
			ParentID: "root",
			Properties: []game.SGFProperty{{
				Ident: "C", Values: []string{"変化"},
			}},
		})
	}
	snapshot.NextNodeNumber = 4096
	encoded, err := Encode(snapshot)
	if err != nil {
		t.Fatalf("局面数を増やさない終端を書き出せません：%v", err)
	}
	if _, err := DecodeGameWithDeadStoneEstimator(encoded, "局面数上限の往復", nil); err != nil {
		t.Fatalf("書き出した終端を局面に数えています：%v", err)
	}
}

func Test結果に使わない地の範囲を保持する(t *testing.T) {
	const source = "(;FF[4]SZ[9]TB[aa]TW[bb];B[ee]TB[cc])"
	session, err := DecodeGameWithDeadStoneEstimator(source, "地の範囲の保持", nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, property := range []string{"TB[aa]", "TW[bb]", ";B[ee]TB[cc]"} {
		if !strings.Contains(encoded, property) {
			t.Fatalf("地の範囲%sを保持していません：%s", property, encoded)
		}
	}
}

func Test地の範囲を主分岐の結果から根へ一度だけ書く(t *testing.T) {
	const source = "(;FF[4]CA[UTF-8]SZ[5]KM[7.5]RE[B+17.5];B[aa]TB[cc](;W[cc];B[];W[])(;W[bb]TW[dd]))"
	session, err := DecodeGameWithDeadStoneEstimator(source, "地の範囲の保持", func(game.Snapshot) ([]string, error) {
		t.Fatal("主分岐のTBから死石を判断できるため推測を呼んではいけません")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, "RE[B+17.5]TB[") || strings.Contains(encoded, ";B[aa]TB[") || strings.Contains(encoded, ";W[bb]TW[") {
		t.Fatalf("地の範囲が根の結果へまとまっていません：%s", encoded)
	}
	assertStableSGF(t, encoded, nil)
}

func Test地の範囲を死石を除いた盤面から黒白それぞれ書く(t *testing.T) {
	t.Run("黒地と白地", func(t *testing.T) {
		stones := map[string]string{}
		for row := 1; row <= 5; row++ {
			stones[fmt.Sprintf("C%d", row)] = "B"
			stones[fmt.Sprintf("D%d", row)] = "W"
		}
		session, err := game.NewSessionWithSetup("黒地と白地", 5, 0.5, 0, stones, "B")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.Play("pass"); err != nil {
			t.Fatal(err)
		}
		if _, err := session.Play("pass"); err != nil {
			t.Fatal(err)
		}
		if err := session.CommitCurrentNodeToMainLine(); err != nil {
			t.Fatal(err)
		}
		assertWrittenTerritory(t, session.Snapshot(),
			[]string{"aa", "ab", "ac", "ad", "ae", "ba", "bb", "bc", "bd", "be"},
			[]string{"ea", "eb", "ec", "ed", "ee"},
		)
	})

	t.Run("複数の死石", func(t *testing.T) {
		session, err := game.NewSessionWithSetup("複数の死石", 5, 0.5, 0, map[string]string{
			"A1": "B", "D5": "W", "E5": "W",
		}, "B")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.Play("pass"); err != nil {
			t.Fatal(err)
		}
		if _, err := session.PlayWithDeadStones("pass", []string{"D5", "E5"}); err != nil {
			t.Fatal(err)
		}
		if err := session.CommitCurrentNodeToMainLine(); err != nil {
			t.Fatal(err)
		}
		assertWrittenTerritory(t, session.Snapshot(), []string{
			"aa", "ab", "ac", "ad", "ba", "bb", "bc", "bd", "be", "ca", "cb", "cc", "cd", "ce",
			"da", "db", "dc", "dd", "de", "ea", "eb", "ec", "ed", "ee",
		}, []string{""})
	})

	t.Run("相手の地にも含まれる死石", func(t *testing.T) {
		const source = "(;FF[4]SZ[5]AW[ca][cb][cc][cd][ce][ec]TB[ec];B[];W[])"
		session, err := DecodeGameWithDeadStoneEstimator(source, "相手の地にも含まれる死石", func(game.Snapshot) ([]string, error) {
			t.Fatal("地の範囲があるのに死石を推測しました")
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		assertWrittenTerritory(t, session.Snapshot(), []string{"ec"}, []string{
			"aa", "ab", "ac", "ad", "ae", "ba", "bb", "bc", "bd", "be",
			"da", "db", "dc", "dd", "de", "ea", "eb", "ed", "ee",
		})
	})
}

func assertWrittenTerritory(t *testing.T, snapshot game.Snapshot, black, white []string) {
	t.Helper()
	encoded, err := Encode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	collection, err := parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.games) != 1 {
		t.Fatalf("書き出した対局数 = %d", len(collection.games))
	}
	gotBlack := append([]string{}, collection.games[0].Values("TB")...)
	gotWhite := append([]string{}, collection.games[0].Values("TW")...)
	black = append([]string{}, black...)
	white = append([]string{}, white...)
	slices.Sort(gotBlack)
	slices.Sort(gotWhite)
	slices.Sort(black)
	slices.Sort(white)
	if !slices.Equal(gotBlack, black) {
		t.Fatalf("黒地 = %v、期待値 = %v\n%s", gotBlack, black, encoded)
	}
	if !slices.Equal(gotWhite, white) {
		t.Fatalf("白地 = %v、期待値 = %v\n%s", gotWhite, white, encoded)
	}
}

func Test地がなければ死石推測を一度使う(t *testing.T) {
	calls := 0
	estimator := func(position game.Snapshot) ([]string, error) {
		calls++
		if len(position.Moves) != 4 {
			t.Fatalf("推測対象の手数 = %d", len(position.Moves))
		}
		return []string{"C3"}, nil
	}
	session, err := DecodeGameWithDeadStoneEstimator("(;FF[4]SZ[5]KM[7.5]RE[B+17.5];B[aa];W[cc];B[];W[])", "死石推測", estimator)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("死石推測の回数 = %d", calls)
	}
	result := session.Snapshot().Result
	if result == nil || len(result.DeadStones) != 1 || result.DeadStones[0] != "C3" {
		t.Fatalf("死石を含む結果 = %+v", result)
	}
}

func Test二連続パスの末端に変化があっても結果を数え直す(t *testing.T) {
	estimator := func(game.Snapshot) ([]string, error) { return []string{"A5"}, nil }
	const source = "(;FF[4]SZ[5]KM[7.5]RE[B+17.5];B[cc];W[aa];B[];W[](;)(;B[ea]))"
	session, err := DecodeGameWithDeadStoneEstimator(source, "末端からの変化", estimator)
	if err != nil {
		t.Fatal(err)
	}
	result := session.Snapshot().Result
	if result == nil || result.Cause != "two_passes" || !result.ScoreKnown || len(result.DeadStones) != 1 || result.DeadStones[0] != "A5" {
		t.Fatalf("数え直した結果 = %+v", result)
	}
	encoded, err := Encode(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeGameWithDeadStoneEstimator(encoded, "末端からの変化の往復", estimator)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot().Result; got == nil || !got.ScoreKnown || len(got.DeadStones) != 1 || got.DeadStones[0] != "A5" {
		t.Fatalf("往復後の結果 = %+v", got)
	}
}

func Test結果が死石を除いた計算と違えば拒否する(t *testing.T) {
	_, err := DecodeGameWithDeadStoneEstimator("(;FF[4]SZ[5]KM[7.5]RE[W+1.5];B[];W[])", "結果不一致", func(game.Snapshot) ([]string, error) {
		return nil, nil
	})
	var decoded *decodeError
	if !errors.As(err, &decoded) || decoded.reason != reasonResult {
		t.Fatalf("結果不一致の拒否理由 = %v", err)
	}
}

func Test局面数と括弧の深さの境界を検査する(t *testing.T) {
	var nodes strings.Builder
	nodes.WriteString("(;FF[4]SZ[5]")
	for index := 1; index < 4096; index++ {
		if index%2 == 1 {
			nodes.WriteString(";B[aa]")
		} else {
			nodes.WriteString(";W[bb]")
		}
	}
	nodes.WriteByte(')')
	if _, err := parse(nodes.String()); err != nil {
		t.Fatalf("局面数の上限ちょうどを解析できません：%v", err)
	}
	tooManyNodes := strings.TrimSuffix(nodes.String(), ")") + ";B[cc])"
	if _, err := parse(tooManyNodes); err == nil || !IsInputSizeLimit(err) {
		t.Fatalf("局面数超過のエラー = %v", err)
	}

	deep := strings.Repeat("(;C[x]", maximumGameTreeDepth) + strings.Repeat(")", maximumGameTreeDepth)
	if _, err := parse(deep); err != nil {
		t.Fatalf("括弧の深さの上限ちょうどを解析できません：%v", err)
	}
	tooDeep := "(;C[x]" + deep + ")"
	if _, err := parse(tooDeep); err == nil || !IsInputSizeLimit(err) {
		t.Fatalf("括弧の深さ超過のエラー = %v", err)
	}
}

func Fuzz受け付けたSGFは安定して往復する(f *testing.F) {
	for _, source := range []string{
		"(;FF[4]SZ[5])",
		"(;FF[4]SZ[9];B[ee];W[dd](;B[de])(;B[fe]C[変化]))",
		"(;FF[4]SZ[5]RE[W+R];B[cc])",
		"(;FF[4]SZ[5];B[];W[];B[cc])",
	} {
		f.Add(source)
	}
	f.Fuzz(func(t *testing.T, source string) {
		session, err := DecodeGameWithDeadStoneEstimator(source, "fuzz", nil)
		if err != nil {
			return
		}
		first, err := Encode(session.Snapshot())
		if err != nil {
			t.Fatal(err)
		}
		restored, err := DecodeGameWithDeadStoneEstimator(first, "fuzz-restored", nil)
		if err != nil {
			t.Fatalf("書き出したSGFを読み込めません：%v\n%s", err, first)
		}
		second, err := Encode(restored.Snapshot())
		if err != nil {
			t.Fatal(err)
		}
		if first != second {
			t.Fatalf("SGFが安定していません\n1回目：%s\n2回目：%s", first, second)
		}
	})
}

func assertStableSGF(t *testing.T, source string, estimator DeadStoneEstimator) {
	t.Helper()
	firstSession, err := DecodeGameWithDeadStoneEstimator(source, "1回目", estimator)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Encode(firstSession.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	secondSession, err := DecodeGameWithDeadStoneEstimator(first, "2回目", estimator)
	if err != nil {
		t.Fatalf("書き出したSGFを読み込めません：%v\n%s", err, first)
	}
	second, err := Encode(secondSession.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("SGFが安定していません\n1回目：%s\n2回目：%s", first, second)
	}
}
