package katago

import "go-practice/internal/game"

type candidateTactics = game.CandidateTactics

func analyzeTactics(boardSize int, setup, moves []move, candidateMoves []string, player string) ([]candidateTactics, error) {
	setupStones := make([]game.TacticalStone, 0, len(setup))
	for _, stone := range setup {
		setupStones = append(setupStones, game.TacticalStone{Color: stone[0], Coordinate: stone[1]})
	}
	gameMoves := make([]game.Move, 0, len(moves))
	for _, move := range moves {
		gameMoves = append(gameMoves, game.Move{Color: move[0], Coordinate: move[1]})
	}
	return game.AnalyzeCandidateTactics(boardSize, setupStones, gameMoves, candidateMoves, player)
}
