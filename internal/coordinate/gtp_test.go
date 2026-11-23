package coordinate

import (
	"strconv"
	"testing"
)

func TestGTP座標を列と行の間で往復する(t *testing.T) {
	for _, test := range []struct {
		value       string
		column, row int
	}{
		{value: "A1", column: 0, row: 1},
		{value: "H19", column: 7, row: 19},
		{value: "J10", column: 8, row: 10},
		{value: "T19", column: 18, row: 19},
	} {
		t.Run(test.value, func(t *testing.T) {
			column, row, ok := parseGTP(test.value)
			if !ok || column != test.column || row != test.row {
				t.Fatalf("解析結果 = 列%d、行%d、有効%t", column, row, ok)
			}
			if actual := FormatGTP(column, row); actual != test.value {
				t.Fatalf("列%d、行%dの座標 = %s", column, row, actual)
			}
		})
	}
}

func Test不正なGTP座標を拒否する(t *testing.T) {
	for _, value := range []string{"", "A", "I1", "a1", "A-", "A-1", "A0", "A01", "A+1", "1A"} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			if _, _, ok := parseGTP(value); ok {
				t.Fatalf("不正な座標を受け付けた：%q", value)
			}
		})
	}
}

func Test盤上の位置とGTP座標を往復する(t *testing.T) {
	for point, value := range []string{"A3", "B3", "C3", "A2", "B2", "C2", "A1", "B1", "C1"} {
		t.Run(value, func(t *testing.T) {
			actual, ok := Point(value, 3)
			if !ok || actual != point {
				t.Fatalf("位置 = %d、有効%t", actual, ok)
			}
			if restored := FromPoint(point, 3); restored != value {
				t.Fatalf("位置%dの座標 = %s", point, restored)
			}
		})
	}
	for _, value := range []string{"A0", "A4", "D1", "I1"} {
		t.Run("範囲外_"+value, func(t *testing.T) {
			if _, ok := Point(value, 3); ok {
				t.Fatalf("3路盤の座標%sを受け付けた", value)
			}
		})
	}
}
