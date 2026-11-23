package cli

import (
	"flag"
	"log/slog"
	"testing"
)

func Testログレベルオプションの表示上の既定値は固定する(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	value := LogLevelOption(flags)
	if *value != "info" {
		t.Fatalf("既定値 = %q", *value)
	}
}

func Testログレベルの文字列を解析する(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{input: "debug", expected: slog.LevelDebug},
		{input: "info", expected: slog.LevelInfo},
		{input: "warn", expected: slog.LevelWarn},
		{input: "error", expected: slog.LevelError},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			actual, err := parseLogLevel(test.input)
			if err != nil {
				t.Fatalf("ログレベル%qの解析でエラーが発生した：%v", test.input, err)
			}
			if actual != test.expected {
				t.Fatalf("ログレベル%qの解析結果 = %v、期待値 = %v", test.input, actual, test.expected)
			}
		})
	}
	for _, input := range []string{"verbose", "DEBUG+2", "DEBUG"} {
		if _, err := parseLogLevel(input); err == nil {
			t.Fatalf("不正なログレベル%qを受け付けた", input)
		}
	}
}
