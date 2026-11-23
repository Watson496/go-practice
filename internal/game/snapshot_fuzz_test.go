package game

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func Fuzz受理した保存状態は安定して復元できる(f *testing.F) {
	for _, seed := range fuzzSnapshotSeeds() {
		data, err := json.Marshal(seed)
		if err != nil {
			panic(err)
		}
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		var input Snapshot
		if err := json.Unmarshal(data, &input); err != nil {
			return
		}
		firstSession, err := RestoreSession(input)
		if err != nil {
			return
		}
		first := firstSession.Snapshot()
		encoded, err := json.Marshal(first)
		if err != nil {
			t.Fatalf("受理した保存状態を書き出せない：%v", err)
		}
		var decoded Snapshot
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("書き出した保存状態を読み取れない：%v", err)
		}
		secondSession, err := RestoreSession(decoded)
		if err != nil {
			t.Fatalf("書き出した保存状態を復元できない：%v", err)
		}
		second := secondSession.Snapshot()
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("保存状態が固定点に達していない：\n1回目：%+v\n2回目：%+v", first, second)
		}
	})
}

func Fuzz保存状態への操作は復元可能な状態を保つ(f *testing.F) {
	const operationCount = 14
	seeds := fuzzSnapshotSeeds()
	for index := range seeds {
		actions := []byte{0, 10, 11, 12, 13, 14, 15, 9, 8, 1, 7, 2, 6, 3, 5, 4}
		operations := make([]byte, 0, len(actions)*3)
		for actionIndex, action := range actions {
			operations = append(operations, action, byte(index+actionIndex), byte(actionIndex*2+1))
		}
		f.Add(byte(index), operations)
	}
	coordinates := []string{"A1", "B2", "C3", "D4", "E5", "pass", "Z99"}
	f.Fuzz(func(t *testing.T, seedIndex byte, operations []byte) {
		if len(operations) > 64 {
			operations = operations[:64]
		}
		session, err := RestoreSession(seeds[int(seedIndex)%len(seeds)])
		if err != nil {
			t.Fatal(err)
		}
		executed := make([]byte, 0, len(operations))
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
			executed = append(executed, action, nodeSelector, coordinateSelector)
			before := session.Snapshot()
			nodes := before.TreeNodes
			node := nodes[int(nodeSelector)%len(nodes)]
			coordinate := coordinates[int(coordinateSelector)%len(coordinates)]
			operation := action % operationCount
			switch operation {
			case 0:
				_, err = session.Play(coordinate)
			case 1:
				err = session.Navigate(node.ID)
			case 2:
				_, err = session.AddVariation(node.ID, []string{coordinate}, nil, AgentOrigin, "検討図")
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
				_, err = session.AppendSGFNode([]SGFProperty{{Ident: "C", Values: []string{"検討メモ"}}})
			case 9:
				err = session.setNodeAIDecision(node.ID, AIDecision{Level: 1, Visits: 1, EngineMove: coordinate, SelectedMove: coordinate})
			case 10:
				_, err = session.PlayVariation(coordinate)
			case 11:
				session.SetGameName(fmt.Sprintf("検証対局%d", nodeSelector))
				err = nil
			case 12:
				err = session.SetNodeImportant(node.ID, !node.Important)
			case 13:
				err = session.UpdateNodeReview(node.ID, "更新したメモ", !node.Important)
			}
			if err != nil {
				if !reflect.DeepEqual(session.Snapshot(), before) {
					beforeJSON, _ := json.Marshal(before)
					afterJSON, _ := json.Marshal(session.Snapshot())
					t.Fatalf("失敗した操作が状態を変更した：操作=%d、エラー=%v、操作前=%s、操作後=%s", operation, err, beforeJSON, afterJSON)
				}
				continue
			}
			if _, err := RestoreSession(session.Snapshot()); err != nil {
				beforeJSON, _ := json.Marshal(before)
				afterJSON, _ := json.Marshal(session.Snapshot())
				t.Fatalf("操作後の保存状態を復元できない：初期状態=%d、操作列=%v、最後の操作=%d、エラー=%v、操作前=%s、操作後=%s", int(seedIndex)%len(seeds), executed, operation, err, beforeJSON, afterJSON)
			}
		}
	})
}

func fuzzSnapshotSeeds() []Snapshot {
	playing, err := NewConfiguredSession("入力変異中", 9, 7.5, 0)
	if err != nil {
		panic(err)
	}
	if _, err := playing.Play("E5"); err != nil {
		panic(err)
	}
	finished := playing.Clone()
	if _, err := finished.Play("pass"); err != nil {
		panic(err)
	}
	if _, err := finished.Play("pass"); err != nil {
		panic(err)
	}
	canceled := finished.Clone()
	if _, err := canceled.DeleteVariation(canceled.Snapshot().CurrentNodeID); err != nil {
		panic(err)
	}
	recorded, err := NewConfiguredSession("記録結果", 9, 7.5, 0)
	if err != nil {
		panic(err)
	}
	if _, err := recorded.Play("E5"); err != nil {
		panic(err)
	}
	if err := recorded.SetSGFRecordedResult(&Result{Type: "resignation", Cause: "recorded", Winner: "B", Loser: "W"}); err != nil {
		panic(err)
	}
	recordedAfterPasses := finished.Clone()
	if err := recordedAfterPasses.SetSGFRecordedResult(&Result{Type: "recorded", Cause: "recorded", Value: "Void"}); err != nil {
		panic(err)
	}
	recordedLoser := finished.Snapshot().CurrentPlayer
	recordedWinner := OtherColor(recordedLoser)
	recordedResults := []Result{
		{Type: "resignation", Cause: "recorded", Winner: recordedWinner, Loser: recordedLoser},
		{Type: "recorded", Cause: "recorded", Value: recordedWinner + "+T"},
		{Type: "recorded", Cause: "recorded", Value: recordedWinner + "+"},
		{Type: "points", Cause: "recorded", Winner: "W", Margin: 7.5},
		{Type: "recorded", Cause: "recorded", Value: "?"},
	}
	recordedResultSnapshots := make([]Snapshot, 0, len(recordedResults))
	for _, result := range recordedResults {
		withResult := finished.Clone()
		if err := withResult.SetSGFRecordedResult(&result); err != nil {
			panic(err)
		}
		recordedResultSnapshots = append(recordedResultSnapshots, withResult.Snapshot())
	}
	negativeKomi, err := NewConfiguredSession("負のコミ", 9, -0.5, 0)
	if err != nil {
		panic(err)
	}
	if _, err := negativeKomi.Play("pass"); err != nil {
		panic(err)
	}
	if _, err := negativeKomi.Play("pass"); err != nil {
		panic(err)
	}
	continued := recorded.Clone()
	continued.ContinueAfterResult()
	if _, err := continued.Play("D4"); err != nil {
		panic(err)
	}
	if err := continued.CommitCurrentNodeToMainLine(); err != nil {
		panic(err)
	}
	branched := canceled.Clone()
	if _, err := branched.AddVariation(branched.Snapshot().CurrentNodeID, []string{"D4", "pass"}, nil, AgentOrigin, "検討図"); err != nil {
		panic(err)
	}
	result := []Snapshot{
		playing.Snapshot(),
		finished.Snapshot(),
		canceled.Snapshot(),
		recorded.Snapshot(),
		recordedAfterPasses.Snapshot(),
		continued.Snapshot(),
		branched.Snapshot(),
		negativeKomi.Snapshot(),
	}
	return append(result, recordedResultSnapshots...)
}
