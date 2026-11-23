// Package coordinate はアプリ内で共有するGTP座標の変換を扱う。
package coordinate

import "strconv"

func parseGTP(value string) (int, int, bool) {
	if len(value) < 2 || value[0] < 'A' || value[0] > 'Z' || value[0] == 'I' {
		return 0, 0, false
	}
	column := int(value[0] - 'A')
	if column > 8 {
		column--
	}
	row, err := strconv.Atoi(value[1:])
	if err != nil || row < 1 || FormatGTP(column, row) != value {
		return 0, 0, false
	}
	return column, row, true
}

// FormatGTP は0始まりの列と1始まりの行をGTP座標へ変換する。
func FormatGTP(column, row int) string {
	letter := column
	if letter >= 8 {
		letter++
	}
	return string(rune('A'+letter)) + strconv.Itoa(row)
}

// Point は盤の大きさに対するGTP座標を、上端から数える一次元の位置へ変換する。
func Point(value string, boardSize int) (int, bool) {
	column, row, ok := parseGTP(value)
	if !ok || column >= boardSize || row > boardSize {
		return 0, false
	}
	return (boardSize-row)*boardSize + column, true
}

// FromPoint は上端から数える一次元の位置をGTP座標へ変換する。
func FromPoint(point, boardSize int) string {
	return FormatGTP(point%boardSize, boardSize-point/boardSize)
}
