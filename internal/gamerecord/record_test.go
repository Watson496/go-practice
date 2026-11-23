package gamerecord

import (
	"testing"
	"time"

	"go-practice/internal/game"
)

func Test記録した設定を内部表現へ変換する(t *testing.T) {
	level, color := 4, "W"
	gotLevel, gotColor, err := Settings(&level, &color)
	if err != nil {
		t.Fatal(err)
	}
	if gotLevel != 4 || gotColor != "W" {
		t.Fatalf("変換結果が一致しません：level=%d color=%q", gotLevel, gotColor)
	}

	gotLevel, gotColor, err = Settings(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotLevel != 0 || gotColor != "" {
		t.Fatalf("未設定の変換結果が一致しません：level=%d color=%q", gotLevel, gotColor)
	}
}

func Test記録した設定の不正な値を拒否する(t *testing.T) {
	invalidLevel, invalidColor := 5, "X"
	if _, _, err := Settings(&invalidLevel, nil); err == nil {
		t.Fatal("範囲外のAIレベルを受け付けました")
	}
	if _, _, err := Settings(nil, &invalidColor); err == nil {
		t.Fatal("不正な石の色を受け付けました")
	}
}

func TestAIと人の石の色の整合を検証する(t *testing.T) {
	black, white := "B", "W"
	if err := validateAIColor(&white, black); err != nil {
		t.Fatal(err)
	}
	if err := validateAIColor(nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := validateAIColor(&black, black); err == nil {
		t.Fatal("同じ石の色を受け付けました")
	}
	if err := validateAIColor(&white, ""); err == nil {
		t.Fatal("人の石の色が未設定なのにAIの色を受け付けました")
	}
}

func Test対局名をSGFの空白規則で正規化する(t *testing.T) {
	name, err := CanonicalName("\t 対局名　\f")
	if err != nil {
		t.Fatal(err)
	}
	if name != " 対局名　" {
		t.Fatalf("正規化した対局名が一致しません：%q", name)
	}
}

func TestSGFの開始局面から対局名を取得する(t *testing.T) {
	snapshot := game.Snapshot{RootProperties: []game.SGFProperty{{Ident: "GN", Values: []string{" 対局名 "}}}}
	name, err := sgfName(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if name != "対局名" {
		t.Fatalf("対局名が一致しません：%q", name)
	}
}

func Test記録日時は正規形だけを受け付ける(t *testing.T) {
	startedAt, importedAt, err := ParseTimes("2026-11-17T01:00:00+09:00", "", "対局")
	if err != nil {
		t.Fatal(err)
	}
	if FormatTime(startedAt) != "2026-11-17T01:00:00+09:00" || !importedAt.IsZero() {
		t.Fatalf("日時が一致しません：startedAt=%s importedAt=%s", startedAt, importedAt)
	}
	if _, _, err := ParseTimes("2026-11-17T01:00:00.000+09:00", "", "対局"); err == nil {
		t.Fatal("正規形でない開始日時を受け付けました")
	}
}

func Test開始日時が不明な記録には読み込んだ時刻と対局名を求める(t *testing.T) {
	if _, _, err := ParseTimes("", "", "対局"); err == nil {
		t.Fatal("読み込んだ時刻のない記録を受け付けました")
	}
	if _, _, err := ParseTimes("", "2026-11-17T01:00:00+09:00", ""); err == nil {
		t.Fatal("対局名のない記録を受け付けました")
	}
}

func TestSGFの対局日から開始日時を取得する(t *testing.T) {
	snapshot := game.Snapshot{RootProperties: []game.SGFProperty{{Ident: "DT", Values: []string{"2026-11-17"}}}}
	startedAt, err := sgfStartedAt(snapshot, time.FixedZone("JST", 9*60*60))
	if err != nil {
		t.Fatal(err)
	}
	if startedAt.Format(time.RFC3339) != "2026-11-17T00:00:00+09:00" {
		t.Fatalf("開始日時が一致しません：%s", startedAt.Format(time.RFC3339))
	}
}

func TestJSONの対局記録を復元できる形へ準備する(t *testing.T) {
	session, err := game.NewSessionWithSetup("exported", 9, 7.5, 0, nil, "B")
	if err != nil {
		t.Fatal(err)
	}
	level, humanColor, aiColor := 1, "B", "W"
	prepared, err := PrepareJSON(JSONRecord{
		SchemaVersion: JSONSchemaVersion,
		RecordedAt:    "2026-11-17T01:00:00+09:00",
		HumanColor:    &humanColor,
		AIColor:       &aiColor,
		AILevel:       &level,
		Game:          session.Snapshot(),
	}, "imported")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Session.Snapshot().ID != "imported" || prepared.AILevel != 1 || prepared.HumanColor != "B" {
		t.Fatalf("準備した記録が一致しません：%+v", prepared)
	}
}

func TestJSONの対局名と開始局面の対局名の不一致を拒否する(t *testing.T) {
	session, err := game.NewSessionWithSetup("exported", 9, 7.5, 0, nil, "B")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	snapshot.RootProperties = append(snapshot.RootProperties, game.SGFProperty{Ident: "GN", Values: []string{"別名"}})
	level, humanColor, aiColor := 1, "B", "W"
	_, err = PrepareJSON(JSONRecord{
		SchemaVersion: JSONSchemaVersion,
		RecordedAt:    "2026-11-17T01:00:00+09:00",
		Name:          "対局名",
		HumanColor:    &humanColor,
		AIColor:       &aiColor,
		AILevel:       &level,
		Game:          snapshot,
	}, "imported")
	if err == nil {
		t.Fatal("対局名が一致しない記録を受け付けました")
	}
}

func Test名前と日付のないSGFに読み込んだ時刻から名前を付ける(t *testing.T) {
	session, err := game.NewSessionWithSetup("imported", 9, 7.5, 0, nil, "B")
	if err != nil {
		t.Fatal(err)
	}
	location := time.FixedZone("JST", 9*60*60)
	importedAt := time.Date(2026, 11, 17, 1, 23, 0, 0, location)
	prepared, err := PrepareSGF(session, importedAt, location)
	if err != nil {
		t.Fatal(err)
	}
	rootName, exists, err := game.SinglePropertyValue(prepared.Session.Snapshot().RootProperties, "GN")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Name != "261117-0123読込" || !exists || rootName != prepared.Name || !prepared.StartedAt.IsZero() {
		t.Fatalf("準備したSGFが一致しません：%+v", prepared)
	}
}
