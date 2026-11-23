package game

import "testing"

func Test対局操作は終局と手番に従う(t *testing.T) {
	session := mustNewTestSession(t, "手番", 5, 0.5)
	start := session.Snapshot()
	if err := ValidateHumanAction(start, "B"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAIAction(start, "B"); err == nil {
		t.Fatalf("黒番でのAI操作のエラー = %v", err)
	}
	if _, err := session.Play("A1"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAIAction(session.Snapshot(), "B"); err != nil {
		t.Fatal(err)
	}
	if err := session.Resign(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHumanAction(session.Snapshot(), "B"); err != errGameFinished {
		t.Fatalf("終局後の操作のエラー = %v", err)
	}
}

func Test待ったの対象を対局の状態から決める(t *testing.T) {
	session := mustNewTestSession(t, "待った", 5, 0.5)
	if _, err := UndoTargetNodeID(session.Snapshot(), "B"); err == nil {
		t.Fatal("人の着手がない局面で待ったを受け付けました")
	}
	if _, err := session.Play("A1"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Play("B1"); err != nil {
		t.Fatal(err)
	}
	if target, err := UndoTargetNodeID(session.Snapshot(), "B"); err != nil || target != "root" {
		t.Fatalf("通常の待ったの対象 = %q、エラー = %v", target, err)
	}
	beforeResignation := session.Snapshot().CurrentNodeID
	if err := session.Resign(); err != nil {
		t.Fatal(err)
	}
	if target, err := UndoTargetNodeID(session.Snapshot(), "B"); err != nil || target != beforeResignation {
		t.Fatalf("人の投了の待ったの対象 = %q、エラー = %v", target, err)
	}
}

func Test待ったはAIの投了の前の人の着手から取り消す(t *testing.T) {
	session := mustNewTestSession(t, "AIの投了", 5, 0.5)
	if _, err := session.Play("A1"); err != nil {
		t.Fatal(err)
	}
	if err := session.Resign(); err != nil {
		t.Fatal(err)
	}
	if target, err := UndoTargetNodeID(session.Snapshot(), "B"); err != nil || target != "root" {
		t.Fatalf("AIの投了の待ったの対象 = %q、エラー = %v", target, err)
	}
}
