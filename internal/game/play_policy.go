package game

import (
	"errors"
	"fmt"
)

const noUndoableHumanMoveMessage = "待ったで戻せるあなたの操作がありません"

// ValidateHumanAction は、現在の局面で人の対局操作を受け付けられるかを返す。
func ValidateHumanAction(snapshot Snapshot, humanColor string) error {
	return validatePlayerAction(snapshot, humanColor, true)
}

// ValidateAIAction は、現在の局面でAIの対局操作を受け付けられるかを返す。
func ValidateAIAction(snapshot Snapshot, humanColor string) error {
	return validatePlayerAction(snapshot, humanColor, false)
}

func validatePlayerAction(snapshot Snapshot, humanColor string, human bool) error {
	if snapshot.Status != "playing" {
		return errGameFinished
	}
	isHumanTurn := snapshot.CurrentPlayer == humanColor
	if human != isHumanTurn {
		if human {
			return fmt.Errorf("あなたの手番ではありません")
		}
		return fmt.Errorf("AIの手番ではありません")
	}
	return nil
}

// UndoTargetNodeID は、開始局面から現在の局面までの手順のうち最も新しい人の操作を探し、
// 待ったの後に現在の局面とする、その操作の前の局面のIDを返す。
// 人の操作は、人の石の色の側の着手、パス、投了である。
func UndoTargetNodeID(snapshot Snapshot, humanColor string) (string, error) {
	nodes := make(map[string]TreeNode, len(snapshot.TreeNodes))
	for _, node := range snapshot.TreeNodes {
		nodes[node.ID] = node
	}
	for node, ok := nodes[snapshot.CurrentNodeID]; ok && node.ParentID != ""; node, ok = nodes[node.ParentID] {
		if isHumanOperation(node, humanColor) {
			return node.ParentID, nil
		}
	}
	return "", errors.New(noUndoableHumanMoveMessage)
}

func isHumanOperation(node TreeNode, humanColor string) bool {
	if node.Move != nil {
		return node.Move.Color == humanColor
	}
	return node.Result != nil && node.Result.Type == "resignation" && node.Result.Loser == humanColor
}
