// Package gamerecord は保存・入出力する対局記録の規則を定義する。
package gamerecord

import (
	"errors"
	"fmt"
	"time"

	"go-practice/internal/ailevel"
	"go-practice/internal/apicontract"
	"go-practice/internal/game"
	"go-practice/internal/sgf"
)

const (
	unsetAILevel = 0
	// JSONSchemaVersion はユーザーが読み書きする対局記録の形式を表す。
	JSONSchemaVersion = "1"
)

// JSONRecord はユーザーが読み書きするJSONの対局記録である。
type JSONRecord struct {
	SchemaVersion string        `json:"schemaVersion" required:"true"`
	RecordedAt    string        `json:"recordedAt" required:"true"`
	ImportedAt    string        `json:"importedAt,omitempty"`
	Name          string        `json:"name,omitempty"`
	HumanColor    *string       `json:"humanColor" required:"true"`
	AIColor       *string       `json:"aiColor" required:"true"`
	AILevel       *int          `json:"aiLevel" required:"true"`
	Game          game.Snapshot `json:"game" required:"true"`
}

// ImportedJSON は検証を終えたJSONの対局記録である。
type ImportedJSON struct {
	Session    *game.Session
	AILevel    int
	HumanColor string
	StartedAt  time.Time
	ImportedAt time.Time
	Name       string
}

// ImportedSGF は検証を終えたSGFの対局記録である。
type ImportedSGF struct {
	Session    *game.Session
	StartedAt  time.Time
	ImportedAt time.Time
	Name       string
}

// PrepareJSON はJSONの対局記録を正規化して、復元できることまで検証する。
func PrepareJSON(record JSONRecord, id string) (ImportedJSON, error) {
	if record.SchemaVersion != JSONSchemaVersion {
		return ImportedJSON{}, fmt.Errorf("対応していないJSON保存形式です")
	}
	aiLevel, humanColor, err := Settings(record.AILevel, record.HumanColor)
	if err != nil {
		return ImportedJSON{}, err
	}
	if err := validateAIColor(record.AIColor, humanColor); err != nil {
		return ImportedJSON{}, err
	}
	name, err := CanonicalName(record.Name)
	if err != nil {
		return ImportedJSON{}, err
	}
	startedAt, importedAt, err := ParseTimes(record.RecordedAt, record.ImportedAt, name)
	if err != nil {
		return ImportedJSON{}, err
	}
	dateMatches, err := sgf.RecordedAtMatches(record.Game, startedAt)
	if err != nil {
		return ImportedJSON{}, fmt.Errorf("JSONの%w", err)
	}
	if !dateMatches {
		return ImportedJSON{}, fmt.Errorf("JSONの開始日時と開始局面の日時情報が一致しません")
	}
	record.Game.ID = id
	record.Game = apicontract.NormalizeSnapshotUserText(record.Game)
	rootName, hasRootName, err := game.SinglePropertyValue(record.Game.RootProperties, "GN")
	if err != nil {
		return ImportedJSON{}, fmt.Errorf("JSONの開始局面の対局名GNが不正です")
	}
	if (hasRootName && rootName != name) || (!hasRootName && name != "") {
		return ImportedJSON{}, fmt.Errorf("JSONの対局名と開始局面の対局名GNが一致しません")
	}
	if err := apicontract.ValidateSnapshotUserText(record.Game); err != nil {
		return ImportedJSON{}, fmt.Errorf("JSONの%w", err)
	}
	session, err := game.RestoreSession(record.Game)
	if err != nil {
		return ImportedJSON{}, fmt.Errorf("JSONの対局データが不正です：%w", err)
	}
	session.SetGameName(name)
	return ImportedJSON{Session: session, AILevel: aiLevel, HumanColor: humanColor, StartedAt: startedAt, ImportedAt: importedAt, Name: name}, nil
}

// PrepareSGF は復元済みのSGFを検証し、対局名と日時を決める。
func PrepareSGF(session *game.Session, importedAt time.Time, location *time.Location) (ImportedSGF, error) {
	snapshot := session.Snapshot()
	if err := apicontract.ValidateSnapshotUserText(snapshot); err != nil {
		return ImportedSGF{}, fmt.Errorf("SGFの%w", err)
	}
	name, err := sgfName(snapshot)
	if err != nil {
		return ImportedSGF{}, err
	}
	startedAt, err := sgfStartedAt(snapshot, location)
	if err != nil {
		return ImportedSGF{}, err
	}
	if name == "" && startedAt.IsZero() {
		name = importedAt.In(location).Format("060102-1504") + "読込"
	}
	session.SetGameName(name)
	return ImportedSGF{Session: session, StartedAt: startedAt, ImportedAt: importedAt, Name: name}, nil
}

// Settings はJSONに記録された任意の対局設定を内部表現へ変換する。
func Settings(level *int, humanColor *string) (int, string, error) {
	resolvedLevel, resolvedColor := unsetAILevel, ""
	if level != nil {
		if err := ailevel.Validate(*level); err != nil {
			return 0, "", err
		}
		resolvedLevel = *level
	}
	if humanColor != nil {
		if err := apicontract.ValidateHumanColor(*humanColor); err != nil {
			return 0, "", err
		}
		resolvedColor = *humanColor
	}
	return resolvedLevel, resolvedColor, nil
}

func validateAIColor(aiColor *string, humanColor string) error {
	if humanColor == "" {
		if aiColor != nil {
			return fmt.Errorf("人の石の色が未設定のときは、AIの色にnullを指定してください")
		}
		return nil
	}
	if aiColor == nil || !game.IsColor(*aiColor) {
		return fmt.Errorf("AIの色には黒または白を指定してください")
	}
	if *aiColor == humanColor {
		return fmt.Errorf("AIとあなたには異なる石の色を指定してください")
	}
	return nil
}

// CanonicalName は対局名を正規化して上限を検証する。
func CanonicalName(value string) (string, error) {
	name := game.NormalizeSGFPropertyValue("GN", value)
	if err := apicontract.ValidateGameName(name); err != nil {
		return "", err
	}
	return name, nil
}

func sgfName(snapshot game.Snapshot) (string, error) {
	raw, exists, err := game.SinglePropertyValue(snapshot.RootProperties, "GN")
	if err != nil {
		return "", fmt.Errorf("SGFの対局名GNには値を1つだけ指定してください")
	}
	if !exists {
		return "", nil
	}
	name, err := CanonicalName(raw)
	if err != nil {
		return "", fmt.Errorf("SGFの%w", err)
	}
	return name, nil
}

func sgfStartedAt(snapshot game.Snapshot, location *time.Location) (time.Time, error) {
	startedAt, err := sgf.RecordedAt(snapshot, time.Time{}, location)
	if err != nil {
		return time.Time{}, fmt.Errorf("SGFの%w", err)
	}
	return startedAt, nil
}

// ParseTimes は正規形の開始日時と読み込んだ時刻を検証して返す。
func ParseTimes(startedAtText, importedAtText, name string) (time.Time, time.Time, error) {
	startedAt, err := parseCanonicalTime(startedAtText, "開始日時にはRFC 3339の正規形で日時を指定してください")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	importedAt, err := parseCanonicalTime(importedAtText, "読み込んだ時刻にはRFC 3339の正規形で日時を指定してください")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if startedAt.IsZero() && importedAt.IsZero() {
		return time.Time{}, time.Time{}, errors.New("開始日時が不明の対局には、読み込んだ時刻を指定してください")
	}
	if startedAt.IsZero() && name == "" {
		return time.Time{}, time.Time{}, errors.New("開始日時が不明の対局には、対局名を指定してください")
	}
	return startedAt, importedAt, nil
}

func parseCanonicalTime(value, errorMessage string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, errors.New(errorMessage)
	}
	return parsed, nil
}

// FormatTime は日時をRFC 3339の正規形で返す。零値は空文字列にする。
func FormatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}
