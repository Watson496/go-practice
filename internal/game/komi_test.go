package game

import (
	"fmt"
	"math"
	"testing"
)

func Test新しい対局状態がコミを検証する(t *testing.T) {
	for _, komi := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -100.5, 100.5, 7.25, 6.1, 0.3} {
		t.Run("拒否_"+fmt.Sprint(komi), func(t *testing.T) {
			if _, err := NewConfiguredSession("invalid", 9, komi, 0); err == nil {
				t.Fatalf("不正なコミ%vのエラー = %v", komi, err)
			}
		})
	}
	for _, komi := range []float64{-100, -0.5, 0, 7.5, 100} {
		t.Run("受付_"+fmt.Sprint(komi), func(t *testing.T) {
			if _, err := NewConfiguredSession("valid", 9, komi, 0); err != nil {
				t.Fatalf("コミ%vを拒否した：%v", komi, err)
			}
		})
	}
}
