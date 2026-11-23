package game

import "fmt"

// TacticalStone は戦術判定の開始局面に置く石を表す。
type TacticalStone struct {
	Color      string
	Coordinate string
}

// CandidateTactics は候補手を盤面へ適用した戦術上の結果を表す。
type CandidateTactics struct {
	Move                  string
	Legal                 bool
	CapturedStones        []string
	SavedOwnAtariGroups   [][]string
	OwnGroupsStillInAtari [][]string
}

type tacticalGroup struct {
	color     string
	stones    []string
	liberties []string
}

// AnalyzeCandidateTactics は対局と同じ規則で候補手の取りとアタリを調べる。
func AnalyzeCandidateTactics(boardSize int, setup []TacticalStone, moves []Move, candidates []string, player string) ([]CandidateTactics, error) {
	setupStones := make(map[string]string, len(setup))
	for index, stone := range setup {
		if !IsColor(stone.Color) {
			return nil, fmt.Errorf("初期配置の%d個目（色：%s、座標：%s）が不正です", index+1, stone.Color, stone.Coordinate)
		}
		if _, exists := setupStones[stone.Coordinate]; exists {
			return nil, fmt.Errorf("初期配置の%d個目が座標%sに重複しています", index+1, stone.Coordinate)
		}
		setupStones[stone.Coordinate] = stone.Color
	}
	if !IsColor(player) {
		return nil, fmt.Errorf("現在の手番%qが不正です", player)
	}
	initialPlayer := player
	if len(moves) > 0 {
		initialPlayer = moves[0].Color
	}
	session, err := NewSessionWithSetup("戦術判定", boardSize, DefaultKomi, 0, setupStones, initialPlayer)
	if err != nil {
		return nil, err
	}
	for index, move := range moves {
		if session.currentPlayer != move.Color {
			return nil, fmt.Errorf("%d手目（色：%s、座標：%s）を再現できません：手番が一致しません", index+1, move.Color, move.Coordinate)
		}
		if _, err := session.Play(move.Coordinate); err != nil {
			return nil, fmt.Errorf("%d手目（色：%s、座標：%s）を再現できません：%w", index+1, move.Color, move.Coordinate, err)
		}
		if index+1 < len(moves) && session.result != nil {
			session.ContinueAfterResult()
		}
	}
	if session.currentPlayer != player {
		return nil, fmt.Errorf("現在の手番%qが着手履歴と一致しません", player)
	}
	if session.result != nil {
		session.ContinueAfterResult()
	}
	beforeGroups := session.tacticalGroups()
	results := make([]CandidateTactics, 0, len(candidates))
	for _, coordinate := range candidates {
		copySession := session.Clone()
		move, err := copySession.Play(coordinate)
		result := CandidateTactics{Move: coordinate, Legal: err == nil, CapturedStones: move.Captured}
		if err != nil {
			results = append(results, result)
			continue
		}
		afterGroups := copySession.tacticalGroups()
		for _, before := range beforeGroups {
			if before.color != player || len(before.liberties) != 1 {
				continue
			}
			after, found := tacticalGroupContainingAny(afterGroups, player, before.stones)
			if found && len(after.liberties) > 1 {
				result.SavedOwnAtariGroups = append(result.SavedOwnAtariGroups, before.stones)
			}
		}
		for _, group := range afterGroups {
			if group.color == player && len(group.liberties) == 1 {
				result.OwnGroupsStillInAtari = append(result.OwnGroupsStillInAtari, group.stones)
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func (session *Session) tacticalGroups() []tacticalGroup {
	groups := make([]tacticalGroup, 0)
	for _, group := range boardGroups(session.size, session.grid) {
		groups = append(groups, tacticalGroup{color: group.color, stones: session.coordinates(group.stones), liberties: session.coordinates(group.liberties)})
	}
	return groups
}

func tacticalGroupContainingAny(groups []tacticalGroup, color string, stones []string) (tacticalGroup, bool) {
	wanted := make(map[string]bool, len(stones))
	for _, stone := range stones {
		wanted[stone] = true
	}
	for _, group := range groups {
		if group.color != color {
			continue
		}
		for _, stone := range group.stones {
			if wanted[stone] {
				return group, true
			}
		}
	}
	return tacticalGroup{}, false
}
