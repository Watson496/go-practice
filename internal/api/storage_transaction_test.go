package api

import (
	"os"
	"path/filepath"
	"testing"
)

func Test原子的削除で保存ファイルを取り除く(t *testing.T) {
	path := filepath.Join(t.TempDir(), "game.json")
	if err := os.WriteFile(path, []byte("保存内容\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicDelete(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("削除後も保存ファイルが残っている：%v", err)
	}
}
