package game

import (
	"reflect"
	"strings"
	"testing"
)

func Test同じ着手列に別の結果があっても変化を作れる(t *testing.T) {
	session := mustNewTestSession(t, "failed-result-variation", 9, 7.5)
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if err := session.SetSGFRecordedResult(&Result{Type: "recorded", Cause: "recorded", Value: "?"}); err != nil {
		t.Fatal(err)
	}
	if err := session.Undo(); err != nil {
		t.Fatal(err)
	}
	if err := session.Undo(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.PlayVariation("pass"); err != nil {
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

func Test同じ着手列の結果は照合せず主分岐の結果を表示する(t *testing.T) {
	session := mustNewTestSession(t, "別々の結果", 5, 0.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	completionID := snapshot.TreeNodes[2].ID
	recorded := Result{Type: "recorded", Cause: "recorded", Value: "?"}
	snapshot.TreeNodes = append(snapshot.TreeNodes, TreeNode{ID: "n4", ParentID: completionID, Result: &recorded})
	snapshot.CurrentNodeID = completionID
	snapshot.MainLineNodeID = "n4"
	snapshot.NextNodeNumber = 5
	snapshot.Result = &recorded

	restored, err := RestoreSession(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	result := restored.Snapshot().Result
	if result == nil || result.Type != "recorded" || result.Value != "?" {
		t.Fatalf("表示した結果 = %+v", result)
	}
}

func Test同じ着手列の別分岐は同じ結果で終局できる(t *testing.T) {
	session := mustNewTestSession(t, "duplicate-semantic-path", 9, 7.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	firstPassID := session.Snapshot().CurrentNodeID
	if _, err := session.AppendSGFNode([]SGFProperty{{Ident: "C", Values: []string{"最初の分岐"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if err := session.Navigate(firstPassID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendSGFNode([]SGFProperty{{Ident: "C", Values: []string{"別の分岐"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatalf("別分岐の二連続パスを終局できない：%v", err)
	}
	resultCount := 0
	for _, node := range session.Snapshot().TreeNodes {
		if node.Result != nil {
			resultCount++
		}
	}
	if resultCount != 2 {
		t.Fatalf("別分岐の結果局面 = %d件、期待値 = 2件", resultCount)
	}
}

func canceledTwoPassSession(t *testing.T) (*Session, string) {
	t.Helper()
	session := mustNewTestSession(t, "result-cancellation", 9, 7.5)
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
	return session, session.Snapshot().CurrentNodeID
}

func Test取り消した結果局面へ移動した後に変化を追加できる(t *testing.T) {
	session, canceledID := canceledTwoPassSession(t)
	if _, err := session.Play("D4"); err != nil {
		t.Fatal(err)
	}
	if err := session.CommitCurrentNodeToMainLine(); err != nil {
		t.Fatal(err)
	}
	if err := session.Navigate(canceledID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AddVariation(canceledID, []string{"pass"}, nil, AgentOrigin, "別図"); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreSession(session.Snapshot()); err != nil {
		t.Fatalf("結果を取り消した途中局面から追加した変化を復元できない：%v", err)
	}
}

func Test取り消した主分岐の末端から変化の結果へ移動できる(t *testing.T) {
	session, canceledID := canceledTwoPassSession(t)
	variation, err := session.AddVariation(canceledID, []string{"D4"}, nil, AgentOrigin, "着手の別図")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Resign(); err != nil {
		t.Fatal(err)
	}
	resultID := session.Snapshot().CurrentNodeID
	for _, nodeID := range []string{variation[len(variation)-1], resultID} {
		if err := session.Navigate(nodeID); err != nil {
			t.Fatalf("変化局面 %sへ移動できない：%v", nodeID, err)
		}
	}
}

func Test変化の結果を昇格すると結果の取り消し印を消す(t *testing.T) {
	session, _ := canceledTwoPassSession(t)
	variation, err := session.AddVariation("root", []string{"pass", "pass"}, nil, AgentOrigin, "終局の別図")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PromoteVariation(variation[len(variation)-1]); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreSession(session.Snapshot()); err != nil {
		t.Fatalf("結果変化の主分岐昇格後を復元できない：%v", err)
	}
}

func Test取り消した自動結果の局面に投了結果を確定しても取り消し印を保つ(t *testing.T) {
	session, completionID := canceledTwoPassSession(t)
	if err := session.Resign(); err != nil {
		t.Fatal(err)
	}
	resultID := session.Snapshot().CurrentNodeID
	if !treeNodeForTest(t, session.Snapshot(), completionID).AutomaticResultCanceled {
		t.Fatal("投了変化の追加で主分岐の結果の取り消し印が消えました")
	}
	if err := session.CommitCurrentNodeToMainLine(); err != nil {
		t.Fatal(err)
	}
	if !treeNodeForTest(t, session.Snapshot(), completionID).AutomaticResultCanceled {
		t.Fatal("投了結果の主分岐確定で自動結果の取り消し印が消えました")
	}
	if session.Snapshot().MainLineNodeID != resultID {
		t.Fatalf("主分岐の末端 = %q、期待値 = %q", session.Snapshot().MainLineNodeID, resultID)
	}
	if _, err := RestoreSession(session.Snapshot()); err != nil {
		t.Fatalf("投了結果と取り消した自動結果を持つ状態を復元できない：%v", err)
	}
}

func Test結果局面の親を表示したまま結果を削除すると取り消し状態を再構築する(t *testing.T) {
	session := mustNewTestSession(t, "delete-viewed-result", 9, 7.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	resultID := session.Snapshot().CurrentNodeID
	completionID := treeNodeForTest(t, session.Snapshot(), resultID).ParentID
	if err := session.Navigate(completionID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.DeleteVariation(resultID); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.Status != "playing" || snapshot.Result != nil || snapshot.CurrentNodeID != completionID {
		t.Fatalf("結果削除後の現在の局面 = %+v", snapshot)
	}
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("結果削除後の状態を復元できない：%v", err)
	}
}

func Test二連続パスの二手目へ移動すると終局状態を復元する(t *testing.T) {
	session := mustNewTestSession(t, "navigate-before-result", 9, 7.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	resultID := session.Snapshot().CurrentNodeID
	completionID := treeNodeForTest(t, session.Snapshot(), resultID).ParentID
	if err := session.Navigate(completionID); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.Status != "finished" || snapshot.Result == nil || snapshot.CurrentNodeID != completionID {
		t.Fatalf("二連続パスの二手目へ移動した状態 = %+v", snapshot)
	}
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("二連続パスの二手目へ移動した状態を復元できない：%v", err)
	}
}

func Test二連続パス後の続きから待ったで戻ると結果の取り消しを記録する(t *testing.T) {
	session := mustNewTestSession(t, "undo-continuation", 9, 7.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	session.ContinueAfterResult()
	if _, err := session.Play("D4"); err != nil {
		t.Fatal(err)
	}
	if err := session.CommitCurrentNodeToMainLine(); err != nil {
		t.Fatal(err)
	}
	if err := session.Undo(); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	completionID := snapshot.MainLineNodeID
	if snapshot.Status != "playing" || snapshot.Result != nil || !treeNodeForTest(t, snapshot, completionID).AutomaticResultCanceled {
		t.Fatalf("二連続パスの局面へ戻った状態 = %+v", snapshot)
	}
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("二連続パスの局面へ戻った状態を復元できない：%v", err)
	}
}

func Test二連続パス後の主分岐を削除するとメモ局面に結果の取り消しを記録する(t *testing.T) {
	session := mustNewTestSession(t, "delete-continuation-after-comment", 9, 7.5)
	if _, err := session.Play("E5"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	resultID := session.Snapshot().CurrentNodeID
	completionID := treeNodeForTest(t, session.Snapshot(), resultID).ParentID
	if err := session.Navigate(completionID); err != nil {
		t.Fatal(err)
	}
	commentID, err := session.AppendSGFNode([]SGFProperty{{Ident: "C", Values: []string{"終局メモ"}}})
	if err != nil {
		t.Fatal(err)
	}
	session.ContinueAfterResult()
	if _, err := session.Play("B2"); err != nil {
		t.Fatal(err)
	}
	continuationID := session.Snapshot().CurrentNodeID
	if err := session.CommitCurrentNodeToMainLine(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.DeleteVariation(continuationID); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.MainLineNodeID != commentID || snapshot.Status != "playing" || snapshot.Result != nil {
		t.Fatalf("続きの削除後の状態 = %+v", snapshot)
	}
	if !treeNodeForTest(t, snapshot, completionID).AutomaticResultCanceled {
		t.Fatal("二連続パスの完了局面に結果の取り消しが記録されていません")
	}
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("続きの削除後の状態を復元できない：%v", err)
	}
}

func Test記録結果を削除すると二連続パスの取り消しを記録する(t *testing.T) {
	session := mustNewTestSession(t, "delete-recorded-result", 9, 7.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	recorded := &Result{Type: "recorded", Cause: "recorded", Value: "Void"}
	if err := session.SetSGFRecordedResult(recorded); err != nil {
		t.Fatal(err)
	}
	resultID := session.Snapshot().CurrentNodeID
	completionID := treeNodeForTest(t, session.Snapshot(), resultID).ParentID
	if err := session.Navigate(completionID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.DeleteVariation(resultID); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.Status != "playing" || snapshot.Result != nil || !treeNodeForTest(t, snapshot, completionID).AutomaticResultCanceled {
		t.Fatalf("記録結果削除後の状態 = %+v", snapshot)
	}
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("記録結果削除後の状態を復元できない：%v", err)
	}
}

func Test記録結果の変化を表示したまま主分岐へ昇格できる(t *testing.T) {
	session, _ := canceledTwoPassSession(t)
	if err := session.Navigate("root"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if err := session.SetSGFRecordedResult(&Result{Type: "recorded", Cause: "recorded", Value: "Void"}); err != nil {
		t.Fatal(err)
	}
	resultID := session.Snapshot().CurrentNodeID
	if err := session.PromoteVariation(resultID); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.Status != "finished" || snapshot.Result == nil || snapshot.Result.Type != "recorded" || snapshot.Result.Value != "Void" {
		t.Fatalf("記録結果の昇格後 = %+v", snapshot)
	}
	if _, err := RestoreSession(snapshot); err != nil {
		t.Fatalf("記録結果の昇格後を復元できない：%v", err)
	}
}

func Test一つの変化の途中で終局した後は着手を続けられない(t *testing.T) {
	session := mustNewTestSession(t, "agent-continuation", 9, 7.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	firstPassID := session.Snapshot().CurrentNodeID
	before := session.Snapshot()
	if _, err := session.AddVariation(firstPassID, []string{"pass", "D4"}, nil, AgentOrigin, "終局後の続き"); err == nil {
		t.Fatal("変化の終局後に着手を続けられました")
	}
	if after := session.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("失敗後に対局が変わった：前=%+v、後=%+v", before, after)
	}
}

func Test結果の局面から変化を追加できない(t *testing.T) {
	tests := []struct {
		name   string
		finish func(*testing.T, *Session)
	}{
		{name: "二連続パス", finish: func(t *testing.T, session *Session) {
			if _, err := session.Play("pass"); err != nil {
				t.Fatal(err)
			}
			if _, err := session.Play("pass"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "投了", finish: func(t *testing.T, session *Session) {
			if err := session.Resign(); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := mustNewTestSession(t, "finished-origin", 9, 7.5)
			test.finish(t, session)
			resultNodeID := session.Snapshot().CurrentNodeID
			before := session.Snapshot()
			if _, err := session.PlayVariation("D4"); err == nil {
				t.Fatal("結果の局面から変化へ着手できました")
			}
			if after := session.Snapshot(); !reflect.DeepEqual(after, before) {
				t.Fatalf("失敗後に対局が変わった：前=%+v、後=%+v", before, after)
			}
			if _, err := session.AddVariation(resultNodeID, []string{"D4"}, nil, AgentOrigin, "終局後の続き"); err == nil {
				t.Fatalf("結果局面からの変化のエラー = %v", err)
			}
			if after := session.Snapshot(); !reflect.DeepEqual(after, before) {
				t.Fatalf("失敗後に対局が変わった：前=%+v、後=%+v", before, after)
			}
		})
	}
}

func Testパス後の投了結果からも変化を追加できない(t *testing.T) {
	for _, coordinates := range [][]string{{"pass"}, {"pass", "pass"}} {
		t.Run(strings.Join(coordinates, "_"), func(t *testing.T) {
			session := mustNewTestSession(t, "resignation-after-pass", 9, 7.5)
			if _, err := session.Play("E5"); err != nil {
				t.Fatal(err)
			}
			if _, err := session.Play("pass"); err != nil {
				t.Fatal(err)
			}
			if err := session.Resign(); err != nil {
				t.Fatal(err)
			}
			resultNodeID := session.Snapshot().CurrentNodeID
			if _, err := session.AddVariation(resultNodeID, coordinates, nil, AgentOrigin, "投了後の変化"); err == nil {
				t.Fatalf("投了結果からの変化のエラー = %v", err)
			}
		})
	}
}

func Test取り消した二連続パスを再び打つと結果を再生成する(t *testing.T) {
	session, canceledID := canceledTwoPassSession(t)
	snapshot := session.Snapshot()
	firstPassID := ""
	for _, node := range snapshot.TreeNodes {
		if node.ID == canceledID {
			firstPassID = node.ParentID
		}
	}
	if firstPassID == "" {
		t.Fatal("結果の取り消し局面の親がありません")
	}
	if _, err := session.AddVariation(firstPassID, []string{"pass"}, nil, AgentOrigin, "終局の別図"); err != nil {
		t.Fatal(err)
	}
	snapshot = session.Snapshot()
	resultID := ""
	for _, node := range snapshot.TreeNodes {
		if node.ParentID == canceledID && node.Result != nil {
			resultID = node.ID
		}
		if node.AutomaticResultCanceled {
			t.Fatalf("再び打った二連続パスに結果の取り消しが残った：%+v", node)
		}
	}
	if resultID == "" {
		t.Fatal("再び打った二連続パスの結果がありません")
	}
	restored, err := RestoreSession(session.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Navigate(resultID); err != nil {
		t.Fatal(err)
	}
	if snapshot := restored.Snapshot(); snapshot.Status != "finished" || snapshot.Result == nil {
		t.Fatalf("復元後の再生成した結果 = %+v", snapshot)
	}
}

func treeNodeForTest(t *testing.T, snapshot Snapshot, nodeID string) TreeNode {
	t.Helper()
	for _, node := range snapshot.TreeNodes {
		if node.ID == nodeID {
			return node
		}
	}
	t.Fatalf("局面%sが棋譜の木にない", nodeID)
	return TreeNode{}
}
