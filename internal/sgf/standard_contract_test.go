package sgf

import (
	"errors"
	"strings"
	"testing"

	"go-practice/internal/game"
)

func Test書き出すSGFは規格のプロパティだけを使う(t *testing.T) {
	session, err := game.NewConfiguredSession("standard-output", 5, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("C3"); err != nil {
		t.Fatal(err)
	}
	if err := session.SetNodeImportant(session.Snapshot().CurrentNodeID, true); err != nil {
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
	encoded, err := Encode(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "GP") {
		t.Fatalf("SGFにアプリ固有プロパティが含まれています：%s", encoded)
	}
	if !strings.Contains(encoded, "HO[1]") || !strings.Contains(encoded, "RE[") {
		t.Fatalf("SGFに重要な局面または結果がありません：%s", encoded)
	}
}

func Test結果の局面にあるメモと重要な局面を書き出す(t *testing.T) {
	session, err := game.NewConfiguredSession("結果の付随情報", 5, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, coordinate := range []string{"C3", "pass", "pass"} {
		if _, err := session.Play(coordinate); err != nil {
			t.Fatal(err)
		}
	}
	resultNodeID := session.Snapshot().CurrentNodeID
	if err := session.UpdateNodeReview(resultNodeID, "結果のメモ", true); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	for _, node := range snapshot.TreeNodes {
		if node.ID == resultNodeID {
			snapshot.MainLineNodeID = node.ParentID
			break
		}
	}
	encoded, err := Encode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, "(;)(;C[結果のメモ]HO[1])") {
		t.Fatalf("結果の局面の付随情報がありません：%s", encoded)
	}
}

func Test投了結果の付随情報と主分岐の末端を書き出す(t *testing.T) {
	session, err := game.NewConfiguredSession("投了結果の付随情報", 5, 7.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Resign(); err != nil {
		t.Fatal(err)
	}
	resultNodeID := session.Snapshot().CurrentNodeID
	if err := session.UpdateNodeReview(resultNodeID, "投了のメモ", true); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	for _, node := range snapshot.TreeNodes {
		if node.ID == resultNodeID {
			snapshot.MainLineNodeID = node.ParentID
			break
		}
	}
	encoded, err := Encode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, "(;)(;C[投了のメモ]HO[1])") {
		t.Fatalf("主分岐の末端または投了結果の付随情報がありません：%s", encoded)
	}
}

func Test地の範囲から死石を決めて二連続パスの結果を照合する(t *testing.T) {
	called := false
	session, err := DecodeGameWithDeadStoneEstimator("(;FF[4]CA[UTF-8]SZ[5]KM[7.5]RE[B+17.5];B[aa];W[cc];B[];W[]TB[cc])", "territory", func(game.Snapshot) ([]string, error) {
		called = true
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("地の範囲があるのにKataGoによる死石推測を呼びました")
	}
	result := session.Snapshot().Result
	if result == nil || len(result.DeadStones) != 1 || result.DeadStones[0] != "C3" || result.Winner != "B" || result.Margin != 17.5 {
		t.Fatalf("死石を反映した結果 = %+v", result)
	}
}

func Test地の範囲がなければKataGoで死石を決める(t *testing.T) {
	called := false
	session, err := DecodeGameWithDeadStoneEstimator("(;FF[4]CA[UTF-8]SZ[5]KM[7.5]RE[W+7.5];B[];W[])", "estimated", func(position game.Snapshot) ([]string, error) {
		called = true
		if len(position.Moves) != 2 {
			t.Fatalf("推測対象の手数 = %d", len(position.Moves))
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called || session.Snapshot().Result == nil {
		t.Fatal("KataGoで死石を決めた結果がありません")
	}
}

func Test死石を除いた結果と食い違うSGFを拒否する(t *testing.T) {
	_, err := DecodeGameWithDeadStoneEstimator("(;FF[4]CA[UTF-8]SZ[5]KM[7.5]RE[W+1.5];B[];W[])", "mismatch", func(game.Snapshot) ([]string, error) {
		return nil, nil
	})
	var decoded *decodeError
	if !errors.As(err, &decoded) || decoded.reason != reasonResult {
		t.Fatalf("食い違う結果の拒否理由 = %v", err)
	}
}

func TestGPで始まる未知のプロパティを保持する(t *testing.T) {
	const source = `(;FF[4]SZ[5]GPUNKNOWN[alpha\:beta][plain:colon];B[cc]GPAI[{value:1}])`
	session, err := DecodeGameWithDeadStoneEstimator(source, "unknown-properties", nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, "GPUNKNOWN[alpha:beta][plain:colon]") || !strings.Contains(encoded, "GPAI[{value:1}]") {
		t.Fatalf("GPで始まる未知のプロパティが保持されていません：%s", encoded)
	}
	restored, err := DecodeGameWithDeadStoneEstimator(encoded, "unknown-properties-restored", nil)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := Encode(restored.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if reencoded != encoded {
		t.Fatalf("未知のプロパティを含むSGFが安定して往復しません\n1回目：%s\n2回目：%s", encoded, reencoded)
	}
}

func Test目数と投了以外の結果を値のまま保持する(t *testing.T) {
	for _, value := range []string{"W+T", "W+F", "W+", "Void", "?", "X+custom", ""} {
		t.Run(value, func(t *testing.T) {
			source := "(;FF[4]SZ[5]RE[" + value + "])"
			session, err := DecodeGameWithDeadStoneEstimator(source, "recorded-result", nil)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := session.Snapshot()
			if snapshot.Status != "finished" || snapshot.Result == nil {
				t.Fatalf("終局状態になっていません：%+v", snapshot)
			}
			result := snapshot.Result
			if result.Type != "recorded" || result.Cause != "recorded" || result.Value != value || result.Winner != "" || result.Loser != "" {
				t.Fatalf("記録した結果：%+v", result)
			}
			encoded, err := Encode(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(encoded, "RE["+value+"]") {
				t.Fatalf("記録した結果がそのまま書き出されていません：%s", encoded)
			}
			restored, err := DecodeGameWithDeadStoneEstimator(encoded, "recorded-result-restored", nil)
			if err != nil {
				t.Fatal(err)
			}
			reencoded, err := Encode(restored.Snapshot())
			if err != nil {
				t.Fatal(err)
			}
			if reencoded != encoded {
				t.Fatalf("記録した結果を含むSGFが安定して往復しません\n1回目：%s\n2回目：%s", encoded, reencoded)
			}
		})
	}
}

func Test二連続パスの後でも目数と投了以外の結果を値のまま保持する(t *testing.T) {
	for _, value := range []string{"W+T", "W+F", "W+", "Void", "?"} {
		t.Run(value, func(t *testing.T) {
			source := "(;FF[4]SZ[5]RE[" + value + "];B[];W[])"
			session, err := DecodeGameWithDeadStoneEstimator(source, "recorded-result-after-passes", func(game.Snapshot) ([]string, error) {
				t.Fatal("値をそのまま記録する結果で死石の推測を呼びました")
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			result := session.Snapshot().Result
			if result == nil || result.Type != "recorded" || result.Value != value {
				t.Fatalf("記録した結果：%+v", result)
			}
			encoded, err := Encode(session.Snapshot())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(encoded, "RE["+value+"]") {
				t.Fatalf("記録した結果がそのまま書き出されていません：%s", encoded)
			}
		})
	}
}

func Test読み込んだ目数結果から地の範囲を合成しない(t *testing.T) {
	session, err := DecodeGameWithDeadStoneEstimator("(;FF[4]SZ[5]RE[B+3.5];B[aa])", "記録された目数結果", nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "TB[") || strings.Contains(encoded, "TW[") {
		t.Fatalf("記録された目数結果から地の範囲を合成しました：%s", encoded)
	}
}

func Test重要な局面の値を検査して意味を保持する(t *testing.T) {
	session, err := DecodeGameWithDeadStoneEstimator("(;FF[4]SZ[5];B[aa]HO[1])", "重要な局面", nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, ";B[aa]HO[1]") {
		t.Fatalf("重要な局面を保持していません：%s", encoded)
	}
	if _, err := DecodeGameWithDeadStoneEstimator("(;FF[4]SZ[5];B[aa]HO[x])", "不正な重要な局面", nil); err == nil {
		t.Fatal("不正なHOの値を受け付けました")
	}
	if _, err := DecodeGameWithDeadStoneEstimator("(;FF[4]SZ[5];B[aa]HO[2])", "度合いのある重要な局面", nil); err == nil {
		t.Fatal("重要さの度合いを受け付けました")
	}
}
