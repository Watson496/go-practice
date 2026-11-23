package sgf

import (
	"fmt"
	"strings"
	"time"

	"go-practice/internal/game"
)

// RecordedAt は棋譜の対局日をlocationで解釈して返す。対局日がなければfallbackを返す。
func RecordedAt(snapshot game.Snapshot, fallback time.Time, location *time.Location) (time.Time, error) {
	date, layout, hasDate, err := parseDate(snapshot.RootProperties)
	if err != nil {
		return time.Time{}, err
	}
	if hasDate {
		return time.ParseInLocation(layout, date, location)
	}
	return fallback, nil
}

// RecordedAtMatches は棋譜の日付情報が開始日時と一致するかを返す。
func RecordedAtMatches(snapshot game.Snapshot, recordedAt time.Time) (bool, error) {
	date, layout, hasDate, err := parseDate(snapshot.RootProperties)
	if err != nil {
		return false, err
	}
	return !hasDate || recordedAt.Format(layout) == date, nil
}

func validateRecordedDate(properties []game.SGFProperty) error {
	_, _, _, err := parseDate(properties)
	if err != nil {
		return err
	}
	return nil
}

func parseDate(properties []game.SGFProperty) (string, string, bool, error) {
	raw, exists, err := game.SinglePropertyValue(properties, "DT")
	if err != nil {
		return "", "", false, fmt.Errorf("対局日DTには値を1つだけ指定してください")
	}
	if !exists {
		return "", "", false, nil
	}
	value := strings.SplitN(game.TrimSGFWhitespace(raw), ",", 2)[0]
	for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
		if _, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return value, layout, true, nil
		}
	}
	return "", "", false, fmt.Errorf("対局日DTにはYYYY、YYYY-MM、YYYY-MM-DDのいずれかを指定してください")
}
