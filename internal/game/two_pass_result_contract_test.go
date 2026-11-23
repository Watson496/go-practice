package game

import "testing"

func Test復元は結果または取り消しなしで二連続パスの主分岐の末端を拒否する(t *testing.T) {
	session := mustNewTestSession(t, "missing-two-pass-result", 9, 7.5)
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("pass"); err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	resultID := snapshot.CurrentNodeID
	resultParentID := ""
	for _, node := range snapshot.TreeNodes {
		if node.ID == resultID {
			resultParentID = node.ParentID
		}
	}
	nodes := snapshot.TreeNodes[:0]
	for _, node := range snapshot.TreeNodes {
		if node.ID != resultID {
			nodes = append(nodes, node)
		}
	}
	snapshot.TreeNodes = nodes
	snapshot.CurrentNodeID, snapshot.MainLineNodeID = resultParentID, resultParentID
	snapshot.Status, snapshot.Result, snapshot.ConsecutivePasses = "playing", nil, 0
	if _, err := RestoreSession(snapshot); err == nil {
		t.Fatalf("結果のない二連続パス末端のエラー = %v", err)
	}
}
