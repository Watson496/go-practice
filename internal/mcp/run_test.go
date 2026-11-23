package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// runMCPTranscript は、標準入力を応答前に閉じない実際のstdio接続として要求列を実行する。
func runMCPTranscript(t *testing.T, serverURL, input, logLevel string) (string, string) {
	t.Helper()
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	var logs bytes.Buffer
	args := []string(nil)
	if serverURL != "" {
		args = append(args, "--server-url", serverURL)
	}
	if logLevel != "" {
		args = append(args, "--log-level", logLevel)
	}
	finished := make(chan error, 1)
	go func() {
		finished <- Run(inputReader, outputWriter, &logs, args)
		_ = outputWriter.Close()
	}()
	t.Cleanup(func() {
		_ = inputWriter.Close()
		_ = outputReader.Close()
	})

	decoder := json.NewDecoder(bufio.NewReader(outputReader))
	var output strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(input, "\n"), "\n") {
		if _, err := io.WriteString(inputWriter, line+"\n"); err != nil {
			t.Fatal(err)
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var fields map[string]json.RawMessage
		expectsResponse := json.Unmarshal([]byte(line), &fields) != nil
		if !expectsResponse {
			_, hasMethod := fields["method"]
			_, hasID := fields["id"]
			expectsResponse = hasID || (!hasMethod && !isJSONRPCResponse([]byte(line)))
		}
		if !expectsResponse {
			continue
		}
		var response json.RawMessage
		if err := decoder.Decode(&response); err != nil {
			t.Fatalf("MCP応答を読み取れません：%v", err)
		}
		output.Write(response)
		output.WriteByte('\n')
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("MCPサーバーが終了しない")
	}
	return output.String(), logs.String()
}
