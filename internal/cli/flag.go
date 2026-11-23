// Package cli はコマンドライン引数とユーザー向けエラーを扱う。
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"strings"
	"syscall"
	"unicode"
)

const logLevelHelp = "ログレベル：debug、info、warn、error"

func ParseFlags(flags *flag.FlagSet, args []string, output io.Writer) error {
	flags.SetOutput(io.Discard)
	err := flags.Parse(args)
	flags.SetOutput(output)
	if errors.Is(err, flag.ErrHelp) {
		flags.Usage()
		return flag.ErrHelp
	}
	if err == nil {
		if flags.NArg() > 0 {
			return fmt.Errorf("オプションではない引数は指定できません：%s", strings.Join(flags.Args(), " "))
		}
		return nil
	}
	message := err.Error()
	switch {
	case strings.HasPrefix(message, "flag provided but not defined: "):
		name := strings.TrimPrefix(message, "flag provided but not defined: ")
		return fmt.Errorf("定義されていないオプションです：%s", optionDisplayName(flags, args, name))
	case strings.HasPrefix(message, "flag needs an argument: "):
		name := strings.TrimPrefix(message, "flag needs an argument: ")
		return fmt.Errorf("オプション「%s」に値が必要です", optionDisplayName(flags, args, name))
	case strings.Contains(message, "invalid value"), strings.Contains(message, "parse error"):
		name := optionNameFromError(message)
		if name != "" {
			return fmt.Errorf("オプション「%s」の値が不正です", optionDisplayName(flags, args, name))
		}
		return fmt.Errorf("オプションの値が不正です")
	default:
		return fmt.Errorf("オプションを解釈できません")
	}
}

func optionNameFromError(message string) string {
	for _, prefix := range []string{" for flag -", " for -"} {
		if start := strings.Index(message, prefix); start >= 0 {
			return strings.SplitN(message[start+len(prefix):], ":", 2)[0]
		}
	}
	return ""
}

func optionDisplayName(flags *flag.FlagSet, args []string, name string) string {
	plainName := strings.TrimLeft(name, "-")
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			break
		}
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			continue
		}
		option := strings.SplitN(argument, "=", 2)[0]
		if strings.TrimLeft(option, "-") == plainName {
			return option
		}
		registered := flags.Lookup(strings.TrimLeft(option, "-"))
		if registered == nil || strings.Contains(argument, "=") {
			continue
		}
		if boolean, ok := registered.Value.(interface{ IsBoolFlag() bool }); ok && boolean.IsBoolFlag() {
			continue
		}
		index++
	}
	return "--" + plainName
}

func PrintHelp(flags *flag.FlagSet, command string, output io.Writer) {
	fmt.Fprintf(output, "使い方：%s [オプション]\n", command)
	flags.VisitAll(func(option *flag.Flag) {
		fmt.Fprintf(output, "  --%s", option.Name)
		if boolean, ok := option.Value.(interface{ IsBoolFlag() bool }); !ok || !boolean.IsBoolFlag() {
			fmt.Fprint(output, " <値>")
		}
		fmt.Fprintf(output, "\n      %s", option.Usage)
		if option.DefValue != "" && option.DefValue != "false" {
			fmt.Fprintf(output, "（既定値：%q）", option.DefValue)
		}
		fmt.Fprintln(output)
	})
}

type localizedError struct {
	message string
	cause   error
}

func (err *localizedError) Error() string { return err.message }
func (err *localizedError) Unwrap() error { return err.cause }

// LocalizeError は原因を保ったまま、ユーザー向けの日本語で表示できるエラーを返す。
func LocalizeError(err error) error {
	return &localizedError{message: localizeErrorMessage(err), cause: err}
}

func localizeErrorMessage(err error) string {
	if pathError, ok := err.(*os.PathError); ok {
		return localizeSystemError(pathError.Err)
	}
	if networkError, ok := err.(*net.OpError); ok {
		if networkError.Op == "listen" && errors.Is(networkError, fs.ErrPermission) {
			return "待受アドレスを利用する権限がありません"
		}
		return localizeSystemError(networkError.Err)
	}
	cause := errors.Unwrap(err)
	if cause != nil {
		for _, separator := range []string{": ", "："} {
			prefix := strings.TrimSuffix(err.Error(), separator+cause.Error())
			if prefix != err.Error() {
				return prefix + "：" + localizeErrorMessage(cause)
			}
		}
	}
	if strings.IndexFunc(err.Error(), func(r rune) bool {
		return unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han)
	}) >= 0 {
		return err.Error()
	}
	return "処理に失敗しました"
}

func localizeSystemError(err error) string {
	var addressError *net.AddrError
	if errors.As(err, &addressError) {
		return "待受アドレスが不正です"
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "待受アドレスが不正です"
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "ファイルまたはディレクトリが見つかりません"
	case errors.Is(err, fs.ErrPermission):
		return "ファイルまたはディレクトリを利用する権限がありません"
	case errors.Is(err, syscall.EROFS):
		return "読み取り専用の場所には書き込めません"
	case errors.Is(err, syscall.ENOTDIR):
		return "指定したパスはディレクトリではありません"
	case errors.Is(err, syscall.EISDIR):
		return "指定したパスはディレクトリです"
	case errors.Is(err, syscall.EMFILE), errors.Is(err, syscall.ENFILE):
		return "開いているファイルが多すぎます"
	case errors.Is(err, syscall.EADDRINUSE):
		return "指定したアドレスはすでに使用されています"
	default:
		return "OSの処理に失敗しました"
	}
}
