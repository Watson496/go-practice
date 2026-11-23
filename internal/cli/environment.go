package cli

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
)

// LogLevelOption は共通のログレベルオプションを追加する。
func LogLevelOption(flags *flag.FlagSet) *string {
	return flags.String("log-level", "info", logLevelHelp)
}

// ParseConfiguredLogLevelOption はコマンドラインと環境変数からログレベルを決める。
func ParseConfiguredLogLevelOption(flags *flag.FlagSet, value string) (slog.Level, error) {
	provided := false
	flags.Visit(func(option *flag.Flag) {
		provided = provided || option.Name == "log-level"
	})
	return parseConfiguredLogLevel(value, provided)
}

const (
	DefaultAddress   = "127.0.0.1:8080"
	DefaultServerURL = "http://" + DefaultAddress
)

func parseLogLevel(value string) (slog.Level, error) {
	levels := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	if level, ok := levels[value]; ok {
		return level, nil
	}
	return 0, fmt.Errorf("ログレベル%q：debug、info、warn、errorのいずれかを指定してください", value)
}

func parseConfiguredLogLevel(value string, argumentProvided bool) (slog.Level, error) {
	fromEnvironment := false
	if !argumentProvided {
		if configured := os.Getenv("LOG_LEVEL"); configured != "" {
			value = configured
			fromEnvironment = true
		}
	}
	level, err := parseLogLevel(value)
	if err == nil || !fromEnvironment {
		return level, err
	}
	return 0, fmt.Errorf("環境変数LOG_LEVELが不正です：%w", err)
}
