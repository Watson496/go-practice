package katago

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-practice/internal/game"
)

func TestKataGo補助プロセス(t *testing.T) {
	if os.Getenv("GO_PRACTICE_KATAGO_HELPER") != "1" {
		return
	}
	var input query
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if input.OverrideSettings["reportAnalysisWinratesAs"] != "BLACK" {
		fmt.Fprintln(os.Stderr, "解析結果の視点が黒に固定されていません")
		os.Exit(2)
	}
	if input.Rules != applicationRules {
		fmt.Fprintf(os.Stderr, "解析規則が一致しません：%+v\n", input.Rules)
		os.Exit(2)
	}
	currentPlayer := input.InitialPlayer
	if currentPlayer == "" {
		currentPlayer = "B"
	}
	for range input.Moves {
		if currentPlayer == "B" {
			currentPlayer = "W"
		} else {
			currentPlayer = "B"
		}
	}
	ownership := make([]float64, input.BoardXSize*input.BoardYSize)
	selectedMove := "D4"
	for _, played := range input.Moves {
		if played[1] == selectedMove {
			selectedMove = "pass"
			break
		}
	}
	output := map[string]any{
		"id": input.ID, "turnNumber": len(input.Moves), "ownership": ownership,
		"rootInfo": map[string]any{
			"currentPlayer": currentPlayer, "scoreLead": 0, "winrate": 0.5, "visits": input.MaxVisits,
		},
		"moveInfos": []map[string]any{{"move": selectedMove, "visits": input.MaxVisits, "scoreLead": 0, "winrate": 0.5, "prior": 1, "pv": []string{selectedMove}}},
	}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func TestKataGoプロセスを介して局面を解析し着手を選ぶ(t *testing.T) {
	t.Setenv("GO_PRACTICE_KATAGO_HELPER", "1")
	helper := filepath.Join(t.TempDir(), "katago-helper")
	script := "#!/bin/sh\nexec \"" + os.Args[0] + "\" -test.run=TestKataGo補助プロセス -- \"$@\"\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	player := BeginnerPlayer{Options: Options{BinaryPath: helper, ModelPath: "model", ConfigPath: "config"}}
	position, err := game.NewConfiguredSession("解析境界", 5, 5.5, 0)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := player.AnalyzePosition(context.Background(), position.Snapshot(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Visits != 7 || summary.CurrentPlayer != "B" || len(summary.Ownership) != 25 || len(summary.Candidates) != 1 || summary.Candidates[0].Move != "D4" {
		t.Fatalf("解析結果 = %+v", summary)
	}
	selection, err := SelectMoveDetails(context.Background(), player, position.Snapshot(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if selection.EngineMove != "D4" || selection.SelectedMove != "D4" || selection.AnalysisVisits != 50 {
		t.Fatalf("着手選択 = %+v", selection)
	}

	for _, initialPlayer := range []string{"B", "W"} {
		endgame, err := game.NewSessionWithSetup("終盤の解析境界"+initialPlayer, 5, 5.5, 0, nil, initialPlayer)
		if err != nil {
			t.Fatal(err)
		}
		for _, coordinate := range []string{
			"A1", "E5", "C1", "C5", "E1", "A5", "B2", "D4", "D2",
			"B4", "B1", "D5", "D1", "B5", "A2", "E4", "E2",
		} {
			if _, err := endgame.Play(coordinate); err != nil {
				t.Fatalf("開始手番%sの終盤局面へ%sを追加できません：%v", initialPlayer, coordinate, err)
			}
		}
		selection, err = SelectMoveDetails(context.Background(), player, endgame.Snapshot(), 1)
		if err != nil {
			t.Fatal(err)
		}
		if selection.EngineMove != "pass" || selection.SelectedMove != "pass" || selection.AnalysisVisits != 50 {
			t.Fatalf("開始手番%sの終盤の着手選択 = %+v", initialPlayer, selection)
		}
	}
}

func TestKataGoの解析結果を読み取る(t *testing.T) {
	t.Parallel()

	result, err := readResult(strings.NewReader("\n{\"id\":\"test\",\"turnNumber\":3}\n"))
	if err != nil {
		t.Fatalf("解析結果の読み取りでエラーが発生した：%v", err)
	}
	if result["id"] != "test" {
		t.Fatalf("結果の識別子 = %#v、期待値 = test", result["id"])
	}
}

func Test解析結果の読取時にKataGoの診断行を除外する(t *testing.T) {
	t.Parallel()

	result, err := readResult(strings.NewReader("Engine ready\n{\"id\":\"test\",\"turnNumber\":3}\n"))
	if err != nil {
		t.Fatalf("解析結果の読み取りでエラーが発生した：%v", err)
	}
	if result["id"] != "test" {
		t.Fatalf("結果の識別子 = %#v、期待値 = test", result["id"])
	}
}

func Test解析結果に含まれるKataGoのエラーを報告する(t *testing.T) {
	t.Parallel()

	_, err := readResult(strings.NewReader("{\"id\":\"test\",\"error\":\"bad query\"}\n"))
	if err == nil {
		t.Fatal("KataGoが拒否した解析結果を受け付けました")
	}
}

func Test警告行を除外して解析結果を返す(t *testing.T) {
	t.Parallel()

	input := "{\"id\":\"test\",\"field\":\"focusMoves\",\"warning\":\"example warning\"}\n" +
		"{\"id\":\"test\",\"rootInfo\":{},\"moveInfos\":[],\"turnNumber\":0}\n"
	result, err := readResult(strings.NewReader(input))
	if err != nil {
		t.Fatalf("解析結果の読み取りでエラーが発生した：%v", err)
	}
	if _, exists := result["rootInfo"]; !exists {
		t.Fatalf("警告の次にある解析結果を返していない：%#v", result)
	}
	if _, exists := result["warning"]; exists {
		t.Fatalf("警告が解析結果に混入している：%#v", result)
	}
}

func Test標準エラー出力は末尾だけを保持する(t *testing.T) {
	t.Parallel()

	buffer := newTailBuffer(5)
	for _, value := range []string{"ab", "cdef", "ghijkl"} {
		written, err := buffer.Write([]byte(value))
		if err != nil || written != len(value) {
			t.Fatalf("書込結果 = %d、%v", written, err)
		}
	}
	if actual := buffer.String(); actual != "hijkl" {
		t.Fatalf("保持した末尾 = %q、期待値 = %q", actual, "hijkl")
	}
}

func Test解析に使うファイルが変わると解析版も変わる(t *testing.T) {
	directory := t.TempDir()
	binaryPath := filepath.Join(directory, "katago")
	modelPath := filepath.Join(directory, "model.bin")
	configPath := filepath.Join(directory, "analysis.cfg")
	for _, path := range []string{binaryPath, modelPath, configPath} {
		if err := os.WriteFile(path, []byte("初期内容"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	player := BeginnerPlayer{Options: Options{
		BinaryPath:     binaryPath,
		ModelPath:      modelPath,
		ConfigPath:     configPath,
		OverrideConfig: "maxVisits=100",
	}}
	before := player.AnalysisVersion()
	if !strings.Contains(before, "perspective=black;ko=POSITIONAL;scoring=AREA;suicide=false;whiteHandicapBonus=0") || !strings.Contains(before, "override:") || strings.Contains(before, "maxVisits=100") {
		t.Fatalf("解析版に解析条件が含まれていない：%q", before)
	}
	for _, path := range []string{binaryPath, modelPath, configPath} {
		if strings.Contains(before, path) {
			t.Fatalf("解析版にローカルパス%qが含まれている：%q", path, before)
		}
	}
	changedOverride := player
	changedOverride.Options.OverrideConfig = "maxVisits=101"
	if changedOverride.AnalysisVersion() == before {
		t.Fatal("KataGoの追加設定を変更しても解析版が変わらない")
	}
	if err := os.WriteFile(modelPath, []byte("変更後の長い内容"), 0o600); err != nil {
		t.Fatal(err)
	}
	after := player.AnalysisVersion()
	if after == before {
		t.Fatalf("解析ファイルを変更しても解析版が変わらない：%q", after)
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if missing := player.AnalysisVersion(); strings.Contains(missing, configPath) || !strings.Contains(missing, "config:") || !strings.Contains(missing, ":unavailable") {
		t.Fatalf("読めない解析ファイルが解析版に反映されていない：%q", missing)
	}
}
