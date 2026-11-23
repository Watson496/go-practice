// Package jsonstructure はJSON入力の構造検査を入口間で共有する。
package jsonstructure

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

type errorKind uint8

const (
	errorKindEncoding errorKind = iota + 1
	errorKindSyntax
	errorKindObject
	errorKindDuplicate
	errorKindUnknown
	errorKindTrailing
)

// Error はJSONの構造不備を表す。
type Error struct {
	kind    errorKind
	message string
}

func (err *Error) Error() string { return err.message }

// ValidateEncoding はJSONを黙って別の文字列へ置き換えずに解析できることを確かめる。
func ValidateEncoding(source []byte) error {
	if !utf8.Valid(source) {
		return &Error{kind: errorKindEncoding, message: "JSONはUTF-8として正しくありません"}
	}
	if bytes.HasPrefix(source, []byte{0xef, 0xbb, 0xbf}) {
		return &Error{kind: errorKindEncoding, message: "JSONの先頭にUTF-8のBOMを含めることはできません"}
	}
	for index := 0; index < len(source); index++ {
		if source[index] != '"' {
			continue
		}
		index++
		for index < len(source) && source[index] != '"' {
			if source[index] != '\\' {
				index++
				continue
			}
			if index+1 >= len(source) {
				break
			}
			if source[index+1] != 'u' || index+6 > len(source) {
				index += 2
				continue
			}
			first, ok := unicodeEscapeValue(source[index+2 : index+6])
			if !ok {
				index += 6
				continue
			}
			if 0xdc00 <= first && first <= 0xdfff {
				return &Error{kind: errorKindEncoding, message: "JSONのUnicodeエスケープに単独の下位サロゲートがあります"}
			}
			if first < 0xd800 || first > 0xdbff {
				index += 6
				continue
			}
			if index+12 > len(source) || source[index+6] != '\\' || source[index+7] != 'u' {
				return &Error{kind: errorKindEncoding, message: "JSONのUnicodeエスケープに上位サロゲートと対応する下位サロゲートがありません"}
			}
			second, ok := unicodeEscapeValue(source[index+8 : index+12])
			if !ok || second < 0xdc00 || second > 0xdfff {
				return &Error{kind: errorKindEncoding, message: "JSONのUnicodeエスケープに上位サロゲートと対応する下位サロゲートがありません"}
			}
			index += 12
		}
	}
	return nil
}

func unicodeEscapeValue(digits []byte) (uint16, bool) {
	var value uint16
	for _, digit := range digits {
		value <<= 4
		switch {
		case '0' <= digit && digit <= '9':
			value += uint16(digit - '0')
		case 'a' <= digit && digit <= 'f':
			value += uint16(digit-'a') + 10
		case 'A' <= digit && digit <= 'F':
			value += uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

// ValidateValue はJSONの値を1つ読み、項目名の重複を拒否する。
func ValidateValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return publicStructureError(err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delimiter == '{' {
		return visitObjectFields(decoder, func(_ string) error {
			if err := ValidateValue(decoder); err != nil {
				return err
			}
			return nil
		})
	}
	for decoder.More() {
		if err := ValidateValue(decoder); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return publicStructureError(err)
}

// ValidateObject はJSONオブジェクトを1つ読み、項目名の重複と未知の項目を拒否する。
func ValidateObject(raw json.RawMessage, allowed map[string]struct{}) error {
	return validateObject(raw, allowed, true)
}

// ValidateObjectFields は最上位の項目名だけを検査し、値の構造は呼び出し元に委ねる。
func ValidateObjectFields(raw json.RawMessage, allowed map[string]struct{}) error {
	return validateObject(raw, allowed, false)
}

func validateObject(raw json.RawMessage, allowed map[string]struct{}, validateValues bool) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return &Error{kind: errorKindObject, message: "オブジェクトを指定してください"}
	}
	if err := visitObjectFields(decoder, func(name string) error {
		if allowed != nil {
			if _, exists := allowed[name]; !exists {
				return &Error{kind: errorKindUnknown, message: fmt.Sprintf("項目%qは指定できません", name)}
			}
		}
		if validateValues {
			if err := ValidateValue(decoder); err != nil {
				return wrapStructureError(fmt.Sprintf("項目%qの値が不正です", name), err)
			}
			return nil
		}
		var value json.RawMessage
		return publicStructureError(decoder.Decode(&value))
	}); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return &Error{kind: errorKindTrailing, message: "オブジェクトの後に値があります"}
	}
	return nil
}

func visitObjectFields(decoder *json.Decoder, visit func(string) error) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return publicStructureError(err)
		}
		name := token.(string)
		if _, exists := seen[name]; exists {
			return &Error{kind: errorKindDuplicate, message: fmt.Sprintf("JSONの項目%qが重複しています", name)}
		}
		seen[name] = struct{}{}
		if err := visit(name); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return publicStructureError(err)
	}
	return nil
}

func publicStructureError(err error) error {
	if err == nil {
		return nil
	}
	var structure *Error
	if errors.As(err, &structure) {
		return structure
	}
	return &Error{kind: errorKindSyntax, message: "JSONの文法が不正です"}
}

func wrapStructureError(prefix string, err error) error {
	var structure *Error
	if errors.As(err, &structure) {
		return &Error{kind: structure.kind, message: prefix + "：" + structure.message}
	}
	return &Error{kind: errorKindSyntax, message: prefix + "：JSONの文法が不正です"}
}
