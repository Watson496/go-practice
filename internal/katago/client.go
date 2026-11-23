// Package katago はKataGoとの通信と解析結果の検証を扱う。
package katago

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"time"
)

type Options struct {
	BinaryPath     string
	ModelPath      string
	ConfigPath     string
	OverrideConfig string
}

type move [2]string

type rules struct {
	Ko                 string `json:"ko"`
	Scoring            string `json:"scoring"`
	Suicide            bool   `json:"suicide"`
	WhiteHandicapBonus string `json:"whiteHandicapBonus"`
}

var applicationRules = rules{Ko: "POSITIONAL", Scoring: "AREA", Suicide: false, WhiteHandicapBonus: "0"}

type query struct {
	ID               string         `json:"id"`
	InitialStones    []move         `json:"initialStones,omitempty"`
	InitialPlayer    string         `json:"initialPlayer,omitempty"`
	Moves            []move         `json:"moves"`
	Rules            rules          `json:"rules"`
	Komi             float64        `json:"komi"`
	BoardXSize       int            `json:"boardXSize"`
	BoardYSize       int            `json:"boardYSize"`
	AnalyzeTurns     []int          `json:"analyzeTurns"`
	MaxVisits        int            `json:"maxVisits"`
	IncludeOwnership bool           `json:"includeOwnership"`
	OverrideSettings map[string]any `json:"overrideSettings,omitempty"`
}

type result map[string]any

const maximumDiagnosticOutputLength = 4000

func analyze(ctx context.Context, options Options, query query) (result, error) {
	started := time.Now()
	slog.Debug("KataGo解析を開始します", "query_id", query.ID, "board_size", query.BoardXSize, "moves", len(query.Moves), "analyze_turns", len(query.AnalyzeTurns), "max_visits", query.MaxVisits)
	commandArgs := []string{
		"analysis",
		"-model", options.ModelPath,
		"-config", options.ConfigPath,
		"-quit-without-waiting",
	}
	if options.OverrideConfig != "" {
		commandArgs = append(commandArgs, "-override-config", options.OverrideConfig)
	}
	command := exec.CommandContext(ctx, options.BinaryPath, commandArgs...)
	configureProcessTermination(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("KataGoの標準入力を開けません：%w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("KataGoの標準出力を開けません：%w", err)
	}
	stderr := newTailBuffer(maximumDiagnosticOutputLength)
	command.Stderr = &stderr

	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("KataGoを起動できません：%w", err)
	}
	slog.Debug("KataGoプロセスを起動しました", "query_id", query.ID, "pid", command.Process.Pid)

	query.OverrideSettings = map[string]any{"reportAnalysisWinratesAs": "BLACK"}
	queryBytes, err := json.Marshal(query)
	if err != nil {
		_ = stdin.Close()
		_ = command.Wait()
		return nil, fmt.Errorf("KataGoの解析要求をJSONに変換できません：%w", err)
	}
	if _, err := stdin.Write(append(queryBytes, '\n')); err != nil {
		_ = stdin.Close()
		_ = command.Wait()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("KataGoへ解析要求を送信できません：%w。標準エラー出力：%s", err, stderr.String())
	}

	result, readErr := readResult(stdout)
	_ = stdin.Close()
	waitErr := command.Wait()
	if ctx.Err() != nil {
		slog.Debug("KataGo解析を中止しました", "query_id", query.ID, "duration", time.Since(started), "error", ctx.Err())
		return nil, ctx.Err()
	}
	if readErr != nil {
		return nil, fmt.Errorf("KataGoの解析結果を読み込めません：%w。標準エラー出力：%s", readErr, stderr.String())
	}
	if waitErr != nil {
		return nil, fmt.Errorf("KataGoが異常終了しました：%w。標準エラー出力：%s", waitErr, stderr.String())
	}
	slog.Debug("KataGo解析が完了しました", "query_id", query.ID, "duration", time.Since(started))
	return result, nil
}

func readResult(reader io.Reader) (result, error) {
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if !bytes.HasPrefix(bytes.TrimSpace(line), []byte("{")) {
			continue
		}
		var result result
		if err := json.Unmarshal(line, &result); err != nil {
			return nil, fmt.Errorf("KataGoのJSONを解釈できません：%w", err)
		}
		if errorMessage, ok := result["error"].(string); ok && errorMessage != "" {
			return nil, fmt.Errorf("KataGoが解析要求を拒否しました：%s", errorMessage)
		}
		if warning, ok := result["warning"].(string); ok && warning != "" {
			continue
		}
		return result, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("KataGoの解析結果がありません：%w", io.ErrUnexpectedEOF)
}

type tailBuffer struct {
	data  []byte
	limit int
}

func newTailBuffer(limit int) tailBuffer {
	return tailBuffer{data: make([]byte, 0, limit), limit: limit}
}

func (buffer *tailBuffer) Write(data []byte) (int, error) {
	written := len(data)
	if buffer.limit <= 0 {
		return written, nil
	}
	if len(data) >= buffer.limit {
		buffer.data = append(buffer.data[:0], data[len(data)-buffer.limit:]...)
		return written, nil
	}
	overflow := len(buffer.data) + len(data) - buffer.limit
	if overflow > 0 {
		copy(buffer.data, buffer.data[overflow:])
		buffer.data = buffer.data[:len(buffer.data)-overflow]
	}
	buffer.data = append(buffer.data, data...)
	return written, nil
}

func (buffer *tailBuffer) String() string { return string(buffer.data) }
