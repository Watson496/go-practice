package cli

import (
	"bytes"
	"errors"
	"flag"
	"log/slog"
	"net"
	"os"
	"syscall"
	"testing"
)

func Test引数解析が未知のオプションを拒否する(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.String("name", "", "名前")
	var output bytes.Buffer
	err := ParseFlags(flags, []string{"--unknown"}, &output)
	if err == nil {
		t.Fatalf("エラー = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("標準のフラグの英語出力が残っている：%s", output.String())
	}
}

func Test真偽値のオプションに空白区切りの値を指定すると拒否する(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.Bool("api-only", false, "APIだけを起動する")
	var output bytes.Buffer
	err := ParseFlags(flags, []string{"--api-only", "false", "--unknown"}, &output)
	if err == nil {
		t.Fatalf("エラー = %v", err)
	}
}

func Test不正な真偽値を拒否する(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.Bool("api-only", false, "APIだけを起動する")
	var output bytes.Buffer
	err := ParseFlags(flags, []string{"--api-only=maybe"}, &output)
	if err == nil {
		t.Fatalf("エラー = %v", err)
	}
}

func Test不正な整数値を拒否する(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.Int("level", 0, "水準")
	err := ParseFlags(flags, []string{"-level", "bad"}, &bytes.Buffer{})
	if err == nil {
		t.Fatalf("エラー = %v", err)
	}
}

func Test値に似たオプション名があっても不正な整数値を拒否する(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.String("name", "", "名前")
	flags.Int("level", 0, "水準")
	err := ParseFlags(flags, []string{"--name", "-level", "--level=bad"}, &bytes.Buffer{})
	if err == nil {
		t.Fatalf("エラー = %v", err)
	}
}

func Test未知のオプションと値の欠落を拒否する(t *testing.T) {
	for _, args := range [][]string{{"-unknown"}, {"--name"}} {
		flags := flag.NewFlagSet("test", flag.ContinueOnError)
		flags.String("name", "", "名前")
		if err := ParseFlags(flags, args, &bytes.Buffer{}); err == nil {
			t.Fatalf("不正な引数を受け付けました：%v", args)
		}
	}
}

func Test環境変数の不正なログレベルを引数で上書きできる(t *testing.T) {
	t.Setenv("LOG_LEVEL", "verbose")
	if _, err := parseConfiguredLogLevel("info", false); err == nil {
		t.Fatalf("環境変数のエラー = %v", err)
	}
	if level, err := parseConfiguredLogLevel("info", true); err != nil || level != slog.LevelInfo {
		t.Fatalf("引数で上書きした結果 = %v、エラー = %v", level, err)
	}
}

func Testヘルプに定義したオプションを出す(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.String("name", "既定", "名前")
	flags.Bool("enabled", false, "有効にする")
	var output bytes.Buffer
	PrintHelp(flags, "test", &output)
	text := output.String()
	for _, option := range []string{"--name", "--enabled"} {
		if !bytes.Contains([]byte(text), []byte(option)) {
			t.Fatalf("ヘルプにオプション%sがありません", option)
		}
	}
}

func Testシステム誤りを原因の型に基づいて日本語化する(t *testing.T) {
	tests := []error{
		&os.PathError{Op: "open", Path: "/missing", Err: os.ErrNotExist},
		&os.PathError{Op: "open", Path: "/readonly", Err: syscall.EROFS},
		&os.PathError{Op: "open", Path: "/file/child", Err: syscall.ENOTDIR},
		&os.PathError{Op: "open", Path: "/directory", Err: syscall.EISDIR},
		&os.PathError{Op: "open", Path: "/many", Err: syscall.EMFILE},
		&os.PathError{Op: "open", Path: "/many", Err: syscall.ENFILE},
		&net.OpError{Op: "listen", Err: syscall.EADDRINUSE},
		&net.OpError{Op: "listen", Err: syscall.EACCES},
		&net.OpError{Op: "listen", Err: syscall.EPERM},
		&net.OpError{Op: "listen", Err: &net.AddrError{Err: "missing port in address", Addr: "abc"}},
		&net.OpError{Op: "listen", Err: &net.DNSError{Err: "no such host", Name: "abc"}},
		errors.New("保存先を指定してください"),
		errors.New("unexpected failure: detail"),
	}
	for _, test := range tests {
		actual := LocalizeError(test)
		if !errors.Is(actual, test) {
			t.Fatalf("日本語化したエラーが原因を保持していません：%v", actual)
		}
	}
}
