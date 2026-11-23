package sgf

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"go-practice/internal/game"
)

const (
	maximumGameTreeDepth = 4096
	maximumGameTreeNodes = game.MaximumTreeNodes
	// 主分岐の終端位置だけを表す空のnodeは対局の局面に数えないため、
	// 解析中はその1個分を追加で受け付ける。
	maximumParsedGameTreeNodes = maximumGameTreeNodes + 1
)

// collection はSGFの棋譜集合であり、gamesは対局ごとの開始局面を持つ。
type collection struct {
	games []*node
}

// node はプロパティの出現順を保持し、手順の続きと変化を子局面として表す。
type node struct {
	Properties       []property
	Children         []*node
	MainLineEndsHere bool
}

// property はSGFプロパティの識別子、値、解析中だけ必要な複合値の構成要素を保持する。
type property struct {
	Ident         string
	Values        []string
	ComposeValues [][]string
}

// Values は内部の可変な記憶領域を公開せず、識別子に対応するすべての値を返す。
func (node *node) Values(ident string) []string {
	for _, property := range node.Properties {
		if property.Ident == ident {
			return append([]string(nil), property.Values...)
		}
	}
	return nil
}

// Value は識別子に対応する最初の値を返す。
func (node *node) Value(ident string) (string, bool) {
	for _, property := range node.Properties {
		if property.Ident == ident && len(property.Values) > 0 {
			return property.Values[0], true
		}
	}
	return "", false
}

// parse は未知のプロパティと変化を保持してSGFの棋譜集合を読み込む。
func parse(source string) (*collection, error) {
	parser := treeParser{source: source}
	collection := &collection{}
	parser.space()
	for parser.peek() == '(' {
		root, err := parser.gameTree()
		if err != nil {
			return nil, err
		}
		collection.games = append(collection.games, root)
		parser.space()
	}
	if len(collection.games) == 0 {
		return nil, fmt.Errorf("SGFの棋譜集合に対局がありません")
	}
	if !parser.done() {
		character, _ := utf8.DecodeRuneInString(parser.source[parser.index:])
		return nil, parser.errorf("予期しない文字「%c」があります", character)
	}
	for _, root := range collection.games {
		collapseMainLineEndMarker(root)
	}
	if countNodes(collection.games) > maximumGameTreeNodes {
		return nil, &inputSizeLimitError{cause: parser.errorf("棋譜の局面数が多すぎます")}
	}
	return collection, nil
}

func collapseMainLineEndMarker(root *node) {
	current := root
	for len(current.Children) > 0 {
		first := current.Children[0]
		if len(current.Children) > 1 && len(first.Properties) == 0 && len(first.Children) == 0 {
			current.MainLineEndsHere = true
			current.Children = current.Children[1:]
			return
		}
		current = first
	}
}

func countNodes(roots []*node) int {
	count := 0
	stack := append([]*node(nil), roots...)
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		count++
		stack = append(stack, current.Children...)
	}
	return count
}

type treeParser struct {
	source string
	index  int
	depth  int
	nodes  int
}

func (parser *treeParser) gameTree() (*node, error) {
	if parser.depth >= maximumGameTreeDepth {
		return nil, &inputSizeLimitError{cause: parser.errorf("変化の入れ子が深すぎます")}
	}
	parser.depth++
	defer func() { parser.depth-- }()
	if err := parser.take('('); err != nil {
		return nil, err
	}
	parser.space()
	var root, last *node
	for parser.peek() == ';' {
		node, err := parser.node()
		if err != nil {
			return nil, err
		}
		if root == nil {
			root = node
		} else {
			last.Children = append(last.Children, node)
		}
		last = node
		parser.space()
	}
	if root == nil {
		return nil, parser.errorf("対局に局面がありません")
	}
	for parser.peek() == '(' {
		variation, err := parser.gameTree()
		if err != nil {
			return nil, err
		}
		last.Children = append(last.Children, variation)
		parser.space()
	}
	if err := parser.take(')'); err != nil {
		return nil, err
	}
	return root, nil
}

func (parser *treeParser) node() (*node, error) {
	if parser.nodes >= maximumParsedGameTreeNodes {
		return nil, &inputSizeLimitError{cause: parser.errorf("棋譜の局面数が多すぎます")}
	}
	if err := parser.take(';'); err != nil {
		return nil, err
	}
	parser.nodes++
	node := &node{}
	parser.space()
	for isIdent(parser.peek()) {
		property, err := parser.property()
		if err != nil {
			return nil, err
		}
		node.Properties = append(node.Properties, property)
		parser.space()
	}
	return node, nil
}

func (parser *treeParser) property() (property, error) {
	start := parser.index
	for isIdent(parser.peek()) {
		parser.index++
	}
	parsedProperty := property{Ident: parser.source[start:parser.index]}
	for _, character := range parsedProperty.Ident {
		if character < 'A' || character > 'Z' {
			return property{}, parser.errorAtf(start, "プロパティ識別子%qには大文字だけを使用してください", parsedProperty.Ident)
		}
	}
	parser.space()
	for parser.peek() == '[' {
		composeIdent := ""
		if composeSGFProperties[parsedProperty.Ident] {
			composeIdent = parsedProperty.Ident
		}
		value, composeValue, err := parser.value(composeIdent)
		if err != nil {
			return property{}, err
		}
		parsedProperty.Values = append(parsedProperty.Values, game.NormalizeSGFPropertyValue(parsedProperty.Ident, value))
		parsedProperty.ComposeValues = append(parsedProperty.ComposeValues, composeValue)
		parser.space()
	}
	if len(parsedProperty.Values) == 0 {
		return property{}, parser.errorf("プロパティ%sに値がありません", parsedProperty.Ident)
	}
	return parsedProperty, nil
}

func (parser *treeParser) value(composeIdent string) (string, []string, error) {
	if err := parser.take('['); err != nil {
		return "", nil, err
	}
	var value strings.Builder
	components := []strings.Builder{{}}
	compose := composeIdent != ""
	for !parser.done() {
		character := parser.peek()
		parser.index++
		switch character {
		case ']':
			if !compose {
				return value.String(), nil, nil
			}
			result := make([]string, len(components))
			for index := range components {
				result[index] = components[index].String()
				if composeIdent == "AP" || ((composeIdent == "FG" || composeIdent == "LB") && index > 0) {
					result[index] = game.NormalizeSGFPropertyValue("AP", result[index])
				}
			}
			return value.String(), result, nil
		case '\\':
			if parser.done() {
				return "", nil, parser.errorf("プロパティの値がエスケープで終わっています")
			}
			escaped := parser.peek()
			parser.index++
			if escaped == '\r' {
				if parser.peek() == '\n' {
					parser.index++
				}
				continue
			}
			if escaped == '\n' {
				if parser.peek() == '\r' {
					parser.index++
				}
				continue
			}
			value.WriteByte(escaped)
			if compose {
				components[len(components)-1].WriteByte(escaped)
			}
		case '\r':
			if parser.peek() == '\n' {
				parser.index++
			}
			value.WriteByte('\n')
			if compose {
				components[len(components)-1].WriteByte('\n')
			}
		case '\n':
			if parser.peek() == '\r' {
				parser.index++
			}
			value.WriteByte('\n')
			if compose {
				components[len(components)-1].WriteByte('\n')
			}
		default:
			value.WriteByte(character)
			if compose {
				if character == ':' {
					components = append(components, strings.Builder{})
				} else {
					components[len(components)-1].WriteByte(character)
				}
			}
		}
	}
	return "", nil, parser.errorf("プロパティの値が閉じられていません")
}

var composeSGFProperties = map[string]bool{
	"AB": true, "AE": true, "AP": true, "AR": true, "AW": true,
	"CR": true, "DD": true, "FG": true, "LB": true, "LN": true,
	"MA": true, "SL": true, "SQ": true, "SZ": true, "TB": true,
	"TR": true, "TW": true, "VW": true,
}

func (parser *treeParser) take(want byte) error {
	if parser.peek() != want {
		return parser.errorf("%qが必要です", want)
	}
	parser.index++
	return nil
}

func (parser *treeParser) space() {
	for !parser.done() && unicode.IsSpace(rune(parser.peek())) {
		parser.index++
	}
}

func (parser *treeParser) peek() byte {
	if parser.done() {
		return 0
	}
	return parser.source[parser.index]
}

func (parser *treeParser) done() bool { return parser.index >= len(parser.source) }

func (parser *treeParser) errorf(format string, values ...any) error {
	return parser.errorAtf(parser.index, format, values...)
}

func (parser *treeParser) errorAtf(index int, format string, values ...any) error {
	return fmt.Errorf("SGFの%dバイト目を解析できません：%s", index+1, fmt.Sprintf(format, values...))
}

func isIdent(character byte) bool {
	return (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z')
}
