package sgf

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test解析により属性と変化が保持される(t *testing.T) {
	source := "(;GM[1]FF[4]SZ[9]KM[7.5]AB[aa][cc]PB[Black]PW[White]XX[unknown];B[ee]C[line 1\\]\nline 2](;W[df]C[first])(;W[]C[second]))"
	collection, err := parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.games) != 1 {
		t.Fatalf("対局数 = %d", len(collection.games))
	}
	root := collection.games[0]
	if got := root.Values("AB"); len(got) != 2 || got[0] != "aa" || got[1] != "cc" {
		t.Fatalf("黒の初期配置 = %#v", got)
	}
	if got, _ := root.Value("XX"); got != "unknown" {
		t.Fatalf("未知属性の値 = %q", got)
	}
	move := root.Children[0]
	if got, _ := move.Value("C"); got != "line 1]\nline 2" {
		t.Fatalf("コメントの値 = %q", got)
	}
	if len(move.Children) != 2 {
		t.Fatalf("変化数 = %d", len(move.Children))
	}
	if got, _ := move.Children[0].Value("W"); got != "df" {
		t.Fatalf("最初の変化の着手 = %q", got)
	}
	if got, ok := move.Children[1].Value("W"); !ok || got != "" {
		t.Fatalf("2番目の変化の着手 = %q、存在 = %v", got, ok)
	}
}

func TestLFCRを1つの改行として読む(t *testing.T) {
	collection, err := parse("(;FF[4]C[x\n\ry]N[a\\\n\rb])")
	if err != nil {
		t.Fatal(err)
	}
	root := collection.games[0]
	if value, _ := root.Value("C"); value != "x\ny" {
		t.Fatalf("改行を正規化した値 = %q", value)
	}
	if value, _ := root.Value("N"); value != "ab" {
		t.Fatalf("soft linebreakを除いた値 = %q", value)
	}
}

func Test不正な構文を拒否する(t *testing.T) {
	for _, source := range []string{"(;FF[4]SZ[9])　", "(;FF[4]SZ[9]sz[9])"} {
		if _, err := parse(source); err == nil {
			t.Fatalf("不正なSGFを受け付けました：%q", source)
		}
	}
}

func Test解析により不正なSGFが拒否される(t *testing.T) {
	for _, source := range []string{"", "()", "(;SZ)", "(;C[unfinished)"} {
		if _, err := parse(source); err == nil {
			t.Errorf("不正なSGFを受け付けました：%q", source)
		}
	}
}

func Test深すぎる変化を理由付きで拒否する(t *testing.T) {
	withinLimit := strings.Repeat("(;", 4096) + strings.Repeat(")", 4096)
	if _, err := parse(withinLimit); err != nil {
		t.Fatalf("上限ちょうどの変化を拒否した：%v", err)
	}

	source := strings.Repeat("(;", 4097) + strings.Repeat(")", 4097)
	if _, err := parse(source); err == nil {
		t.Fatal("上限を超えた変化の深さを受け付けました")
	} else {
		var limit *inputSizeLimitError
		if !errors.As(err, &limit) {
			t.Fatalf("上限超過のエラー種類 = %T", err)
		}
	}
}

func Test局面数の上限を境界で検査する(t *testing.T) {
	withinLimit := "(" + strings.Repeat(";", 4096) + ")"
	if _, err := parse(withinLimit); err != nil {
		t.Fatalf("上限ちょうどの局面を拒否した：%v", err)
	}

	overLimit := "(" + strings.Repeat(";", 4097) + ")"
	if _, err := parse(overLimit); err == nil {
		t.Fatal("上限を超えた局面数を受け付けました")
	} else {
		var limit *inputSizeLimitError
		if !errors.As(err, &limit) {
			t.Fatalf("上限超過のエラー種類 = %T", err)
		}
	}
}

func Test実際のKaTrain棋譜を解析して未知のプロパティを保持する(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sgf", "katrain-1.14-japanese-anonymized.sgf"))
	if err != nil {
		t.Fatal(err)
	}
	collection, err := parse(string(source))
	if err != nil {
		t.Fatal(err)
	}
	root := collection.games[0]
	for property, want := range map[string]string{"AP": "KaTrain:1.14.0", "KTV": "1.0", "RU": "japanese"} {
		if got, ok := root.Value(property); !ok || got != want {
			t.Fatalf("%s = %q、存在 = %v、期待値 = %q", property, got, ok, want)
		}
	}
	if got := countMainLineMoves(root); got != 52 {
		t.Fatalf("手数 = %d、期待値 = 52", got)
	}
}

func countMainLineMoves(node *node) int {
	count := 0
	if _, black := node.Value("B"); black {
		count++
	}
	if _, white := node.Value("W"); white {
		count++
	}
	if len(node.Children) > 0 {
		count += countMainLineMoves(node.Children[0])
	}
	return count
}
