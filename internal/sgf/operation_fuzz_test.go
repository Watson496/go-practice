package sgf

import (
	"reflect"
	"testing"

	"go-practice/internal/game"
)

func Fuzz対局操作後のSGFは安定して往復する(f *testing.F) {
	sources := []string{
		"(;FF[4]SZ[5])",
		"(;FF[4]CA[UTF-8]SZ[5];B[cc];W[bb](;B[bc])(;C[検討]))",
		"(;FF[4]SZ[5]RE[B+R];B[cc])",
		"(;FF[4]SZ[5]RE[Void];B[];W[])",
	}
	for sourceIndex := range sources {
		f.Add(byte(sourceIndex), []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13})
	}
	coordinates := []string{"A1", "B2", "C3", "D4", "E5", "pass", "Z99"}
	estimator := func(game.Snapshot) ([]string, error) { return []string{}, nil }
	f.Fuzz(func(t *testing.T, sourceIndex byte, operations []byte) {
		if len(operations) > 48 {
			operations = operations[:48]
		}
		session, err := DecodeGameWithDeadStoneEstimator(sources[int(sourceIndex)%len(sources)], "操作前", estimator)
		if err != nil {
			t.Fatal(err)
		}
		for offset := 0; offset < len(operations); offset += 3 {
			action := operations[offset]
			nodeSelector := action
			coordinateSelector := action
			if offset+1 < len(operations) {
				nodeSelector = operations[offset+1]
			}
			if offset+2 < len(operations) {
				coordinateSelector = operations[offset+2]
			}
			before := session.Snapshot()
			node := before.TreeNodes[int(nodeSelector)%len(before.TreeNodes)]
			coordinate := coordinates[int(coordinateSelector)%len(coordinates)]
			switch action % 14 {
			case 0:
				_, err = session.Play(coordinate)
			case 1:
				err = session.Navigate(node.ID)
			case 2:
				_, err = session.AddVariation(node.ID, []string{coordinate}, nil, game.AgentOrigin, "検討図")
			case 3:
				_, err = session.DeleteVariation(node.ID)
			case 4:
				err = session.PromoteVariation(node.ID)
			case 5:
				err = session.Resign()
			case 6:
				err = session.Undo()
			case 7:
				err = session.CommitCurrentNodeToMainLine()
			case 8:
				_, err = session.PlayVariation(coordinate)
			case 9:
				_, err = session.AppendSGFNode([]game.SGFProperty{{Ident: "C", Values: []string{"検討メモ"}}})
			case 10:
				err = session.UpdateNodeReview(node.ID, "更新したメモ", !node.Important)
			case 11:
				err = session.SetNodeImportant(node.ID, !node.Important)
			case 12:
				session.SetGameName("検証対局")
				err = nil
			case 13:
				err = session.SetSGFProperties(node.ID, []game.SGFProperty{{Ident: "XX", Values: []string{"保持値"}}})
			}
			if err != nil {
				if !reflect.DeepEqual(session.Snapshot(), before) {
					t.Fatalf("失敗した操作が対局を変更しました：操作=%d、エラー=%v", action%14, err)
				}
				continue
			}

			first, err := Encode(session.Snapshot())
			if err != nil {
				t.Fatalf("操作後の対局をSGFへ書き出せません：%v", err)
			}
			restored, err := DecodeGameWithDeadStoneEstimator(first, "操作後", estimator)
			if err != nil {
				t.Fatalf("操作後に書き出したSGFを読み込めません：%v\n%s", err, first)
			}
			second, err := Encode(restored.Snapshot())
			if err != nil {
				t.Fatalf("読み込んだSGFを再び書き出せません：%v", err)
			}
			if first != second {
				t.Fatalf("操作後のSGFが安定していません\n1回目：%s\n2回目：%s", first, second)
			}
		}
	})
}
