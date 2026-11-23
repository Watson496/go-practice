package sgf

import (
	"errors"
	"fmt"
	"strings"

	"go-practice/internal/game"
)

type decodeErrorReason string

const (
	reasonEncoding                   decodeErrorReason = "encoding"
	reasonBOM                        decodeErrorReason = "bom"
	reasonSyntax                     decodeErrorReason = "syntax"
	reasonCollection                 decodeErrorReason = "collection"
	reasonGameType                   decodeErrorReason = "game_type"
	reasonFileFormat                 decodeErrorReason = "file_format"
	reasonCharset                    decodeErrorReason = "charset"
	reasonBoardSize                  decodeErrorReason = "board_size"
	reasonRules                      decodeErrorReason = "rules"
	reasonKomi                       decodeErrorReason = "komi"
	reasonGameName                   decodeErrorReason = "game_name"
	reasonDate                       decodeErrorReason = "date"
	reasonSetup                      decodeErrorReason = "setup"
	reasonSetupGroupWithoutLiberties decodeErrorReason = "setup_group_without_liberties"
	reasonProperty                   decodeErrorReason = "property"
	reasonPlacement                  decodeErrorReason = "placement"
	reasonMoveOrder                  decodeErrorReason = "move_order"
	reasonMove                       decodeErrorReason = "move"
	reasonResult                     decodeErrorReason = "result"
	reasonMetadata                   decodeErrorReason = "metadata"
	reasonStructure                  decodeErrorReason = "structure"
)

type decodeError struct {
	reason decodeErrorReason
	cause  error
}

type inputSizeLimitError struct {
	cause error
}

type deadStoneEstimationError struct {
	cause error
}

func (err *inputSizeLimitError) Error() string      { return err.cause.Error() }
func (err *inputSizeLimitError) Unwrap() error      { return err.cause }
func (err *deadStoneEstimationError) Error() string { return err.cause.Error() }
func (err *deadStoneEstimationError) Unwrap() error { return err.cause }

// IsInputSizeLimit はSGFの局面数または変化の深さが上限を超えた誤りかを返す。
func IsInputSizeLimit(err error) bool {
	var limit *inputSizeLimitError
	return errors.As(err, &limit) || game.IsTreeLimit(err)
}

// IsDeadStoneEstimationError はSGFの結果確認に使う死石の推測が失敗した誤りかを返す。
func IsDeadStoneEstimationError(err error) bool {
	var estimation *deadStoneEstimationError
	return errors.As(err, &estimation)
}

func (err *decodeError) Error() string { return err.cause.Error() }
func (err *decodeError) Unwrap() error { return err.cause }

func withDecodeReason(reason decodeErrorReason, err error) error {
	if err == nil {
		return nil
	}
	var existing *decodeError
	if errors.As(err, &existing) {
		return err
	}
	return &decodeError{reason: reason, cause: err}
}

func decodeErrorf(reason decodeErrorReason, format string, values ...any) error {
	return &decodeError{reason: reason, cause: fmt.Errorf(format, values...)}
}

// PublicDecodeError はSGF読み込みエラーを、内部IDや実装の詳細を含まないユーザー向けの理由へ変換する。
func PublicDecodeError(err error) string {
	var decoded *decodeError
	if !errors.As(err, &decoded) {
		return "SGFの棋譜構造が不正です"
	}
	switch decoded.reason {
	case reasonEncoding:
		return "SGFをUTF-8として読み込めません"
	case reasonBOM:
		return decoded.cause.Error()
	case reasonSyntax:
		detail := strings.TrimPrefix(decoded.cause.Error(), "SGFの")
		return "SGFの文法が不正です：" + detail
	case reasonCollection:
		return "SGFには対局を1つだけ指定してください"
	case reasonGameType:
		return "SGFの対局種別GMには囲碁を表す1を指定してください"
	case reasonFileFormat:
		return "SGFのファイル形式FFには4を指定してください"
	case reasonCharset:
		return "SGFの文字コードCAにはUTF-8を指定してください"
	case reasonBoardSize:
		return publicDecodeCause("SGFの盤サイズSZが不正です", decoded.cause)
	case reasonRules:
		return "SGFのルールRUには中国式の面積計算を表すChineseを指定してください"
	case reasonKomi:
		return publicDecodeCause("SGFのコミKMが不正です", decoded.cause)
	case reasonGameName:
		return "SGFの対局名GNには値を1つだけ指定してください"
	case reasonDate:
		return decoded.cause.Error()
	case reasonSetup:
		return publicDecodeCause("SGFの初期配置・置き碁・開始手番が不正です", decoded.cause)
	case reasonSetupGroupWithoutLiberties:
		return publicDecodeCause("SGFの初期配置に呼吸点のない石のまとまりがあります", decoded.cause)
	case reasonProperty:
		return publicDecodeCause("SGFのプロパティの配置または値が不正です", decoded.cause)
	case reasonPlacement:
		return publicDecodeCause("SGFのプロパティの配置が不正です", decoded.cause)
	case reasonMoveOrder:
		return publicDecodeCause("SGFの着手順と手番が一致しません", decoded.cause)
	case reasonMove:
		return publicDecodeCause("SGFの着手を盤面へ適用できません", decoded.cause)
	case reasonResult:
		return publicDecodeCause("SGFの結果REまたは点数の内訳が不正です", decoded.cause)
	case reasonMetadata:
		return publicDecodeCause("SGFのAI判断、重要な局面、または変化の情報が不正です", decoded.cause)
	case reasonStructure:
		return publicDecodeCause("SGFの棋譜構造が不正です", decoded.cause)
	}
	return "SGFの棋譜構造が不正です"
}

func publicDecodeCause(summary string, cause error) string {
	if strings.HasPrefix(cause.Error(), summary+"：") {
		return cause.Error()
	}
	detail := strings.TrimPrefix(cause.Error(), "SGFの")
	if detail == "" || detail == strings.TrimPrefix(summary, "SGFの") {
		return summary
	}
	return summary + "：" + detail
}
