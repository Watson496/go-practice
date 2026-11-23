package jsonstructure

import (
	"encoding/json"
	"errors"
	"testing"
)

func requireErrorKind(t *testing.T, err error, kind errorKind) {
	t.Helper()
	var structure *Error
	if !errors.As(err, &structure) || structure.kind != kind {
		t.Fatalf("JSON構造エラーの種類 = %T %+v、期待値 = %d", err, err, kind)
	}
}

func Fuzz任意のJSONを安全に検査する(f *testing.F) {
	for _, source := range []string{
		`{}`,
		`{"value":1}`,
		`{"value":{"nested":true}}`,
		`{"value":1,"value":2}`,
		`[1]`,
		`{"value":}`,
	} {
		f.Add(source)
	}
	f.Fuzz(func(_ *testing.T, source string) {
		raw := json.RawMessage(source)
		_ = ValidateEncoding(raw)
		_ = ValidateObject(raw, nil)
		_ = ValidateObjectFields(raw, nil)
	})
}

func Test入れ子の項目名の重複を拒否する(t *testing.T) {
	raw := json.RawMessage(`{"outer":{"x":1,"x":2}}`)
	err := ValidateObject(raw, map[string]struct{}{"outer": {}})
	requireErrorKind(t, err, errorKindDuplicate)
}

func Test入れ子の項目名に重複がなければ受け付ける(t *testing.T) {
	raw := json.RawMessage(`{"outer":{"x":1,"items":[{"x":2}]}}`)
	if err := ValidateObject(raw, map[string]struct{}{"outer": {}}); err != nil {
		t.Fatalf("重複のない入れ子を拒否しました：%v", err)
	}
}

func Test最上位の検査では入れ子の値を呼び出し元へ委ねる(t *testing.T) {
	raw := json.RawMessage(`{"outer":{"x":1,"x":2}}`)
	if err := ValidateObjectFields(raw, map[string]struct{}{"outer": {}}); err != nil {
		t.Fatalf("値の検査を呼び出し元へ委ねる入口が入れ子を検査しました：%v", err)
	}
	if err := ValidateObjectFields(json.RawMessage(`{"outer":1,"outer":2}`), map[string]struct{}{"outer": {}}); err == nil {
		t.Fatal("最上位の重複した項目を受け付けました")
	}
}

func Test未知の項目を拒否する(t *testing.T) {
	err := ValidateObject(json.RawMessage(`{"known":1,"unknown":2}`), map[string]struct{}{"known": {}})
	requireErrorKind(t, err, errorKindUnknown)
}

func Testオブジェクト以外の最上位の値を拒否する(t *testing.T) {
	err := ValidateObject(json.RawMessage(`[1]`), nil)
	requireErrorKind(t, err, errorKindObject)
}

func Test後続するJSONの値を拒否する(t *testing.T) {
	err := ValidateObject(json.RawMessage(`{} {}`), nil)
	requireErrorKind(t, err, errorKindTrailing)
}

func Test文法の誤りを日本語で報告する(t *testing.T) {
	err := ValidateObject(json.RawMessage(`{"value":}`), map[string]struct{}{"value": {}})
	requireErrorKind(t, err, errorKindSyntax)
}

func Test文字を黙って置き換えるJSONを拒否する(t *testing.T) {
	for _, source := range [][]byte{
		append([]byte(`{"value":"`), append([]byte{0xff}, []byte(`"}`)...)...),
		[]byte(`{"value":"\ud800"}`),
		[]byte(`{"value":"\udc00"}`),
		[]byte(`{"\ud800":1}`),
	} {
		t.Run("拒否_"+string(source), func(t *testing.T) {
			if err := ValidateEncoding(source); err == nil {
				t.Fatalf("文字を置き換える入力を受け付けました：%q", source)
			}
		})
	}
	for _, source := range [][]byte{
		[]byte(`{"value":"日本語"}`),
		[]byte(`{"value":"\ud83d\ude00"}`),
		[]byte(`{"value":"引用符：\""}`),
	} {
		t.Run("受付_"+string(source), func(t *testing.T) {
			if err := ValidateEncoding(source); err != nil {
				t.Fatalf("有効な文字を拒否しました：入力 = %q、エラー = %v", source, err)
			}
		})
	}
}

func TestJSONのBOMを理由付きで拒否する(t *testing.T) {
	source := append([]byte{0xef, 0xbb, 0xbf}, []byte(`{"value":1}`)...)
	requireErrorKind(t, ValidateEncoding(source), errorKindEncoding)
}
