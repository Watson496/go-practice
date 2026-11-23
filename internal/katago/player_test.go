package katago

import (
	"testing"

	"go-practice/internal/game"
)

func Test初心者向け着手の条件が局所的な交換に従う(t *testing.T) {
	t.Parallel()
	position := game.Snapshot{BoardSize: 19, CurrentPlayer: "W", Moves: []game.Move{
		{Color: "W", Coordinate: "D16"},
		{Color: "B", Coordinate: "C15"},
	}}
	moveContext := beginnerMoveContext(position)
	if len(moveContext.FocusMoves) != 2 || moveContext.FocusMoves[0] != "D16" || moveContext.FocusMoves[1] != "C15" {
		t.Fatalf("想定外の着手条件：%+v", moveContext)
	}
}
