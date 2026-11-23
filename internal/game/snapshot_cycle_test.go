package game

import (
	"testing"
	"time"
)

func Test主分岐の循環を停止せずに拒否する(t *testing.T) {
	snapshot := Snapshot{
		BoardSize: 9, Komi: 7.5, Rules: "chinese", InitialPlayer: "B", SetupStones: map[string]string{},
		TreeNodes:     []TreeNode{{ID: "root"}, {ID: "n1", ParentID: "n2"}, {ID: "n2", ParentID: "n1"}},
		CurrentNodeID: "n1", MainLineNodeID: "n1", NextNodeNumber: 3,
	}
	done := make(chan error, 1)
	go func() {
		_, err := RestoreSession(snapshot)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("主分岐の循環に対するエラー = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("主分岐が循環する状態の検証が停止しませんでした")
	}
}
