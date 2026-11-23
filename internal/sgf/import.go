// Package sgf はSGFの読み込みと書き出しを扱う。
package sgf

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"go-practice/internal/coordinate"
	"go-practice/internal/game"
)

// DeadStoneEstimator はSGFだけでは死石を決められない局面の死石を返す。
type DeadStoneEstimator func(game.Snapshot) ([]string, error)

// DecodeGameWithDeadStoneEstimator は死石の推測手段を使ってSGFを対局へ変換する。
func DecodeGameWithDeadStoneEstimator(source, id string, estimateDeadStones DeadStoneEstimator) (*game.Session, error) {
	if !utf8.ValidString(source) {
		return nil, decodeErrorf(reasonEncoding, "SGFはUTF-8として正しくありません")
	}
	if strings.HasPrefix(source, "\uFEFF") {
		return nil, decodeErrorf(reasonBOM, "SGFの先頭にUTF-8のBOMを含めることはできません")
	}
	if strings.TrimSpace(source) == "" {
		return nil, decodeErrorf(reasonSyntax, "SGFが空です")
	}
	collection, err := parse(source)
	if err != nil {
		return nil, withDecodeReason(reasonSyntax, err)
	}
	if len(collection.games) != 1 {
		return nil, decodeErrorf(reasonCollection, "SGFの棋譜集合には対局を1つだけ指定してください")
	}
	root := collection.games[0]
	if err := validateDistinctSiblingMoves(root); err != nil {
		return nil, err
	}
	if err := validatePropertyPlacement(root, true); err != nil {
		return nil, withDecodeReason(reasonProperty, err)
	}
	gameType, gameTypeExplicit := root.Value("GM")
	if gameTypeExplicit && gameType != "1" {
		return nil, decodeErrorf(reasonGameType, "SGFの対局種別GM[%s]には対応していません。囲碁を表すGM[1]だけを読み込めます", gameType)
	}
	fileFormat, hasFileFormat := root.Value("FF")
	if !hasFileFormat {
		return nil, decodeErrorf(reasonFileFormat, "SGFのファイル形式FFがありません。FF[4]を指定してください")
	}
	if fileFormat != "4" {
		return nil, decodeErrorf(reasonFileFormat, "SGFのファイル形式FF[%s]には対応していません。FF[4]だけを読み込めます", fileFormat)
	}
	charset, hasCharset := root.Value("CA")
	if hasCharset && !strings.EqualFold(charset, "UTF-8") {
		return nil, decodeErrorf(reasonCharset, "SGFの文字コードCA[%s]には対応していません。UTF-8だけを読み込めます", charset)
	}
	if !hasCharset && !isASCII(source) {
		return nil, decodeErrorf(reasonCharset, "非ASCII文字を含むSGFには文字コードCA[UTF-8]を指定してください")
	}
	size, err := rootBoardSize(root)
	if err != nil {
		return nil, withDecodeReason(reasonBoardSize, err)
	}
	if !game.IsSupportedBoardSize(size) {
		return nil, decodeErrorf(reasonBoardSize, "SGFの盤サイズ%dには対応していません", size)
	}
	if err := validateAuxiliaryProperties(root, size); err != nil {
		return nil, withDecodeReason(reasonProperty, err)
	}
	komi := game.DefaultKomi
	_, komiExplicit := root.Value("KM")
	if value, ok := root.Value("KM"); ok {
		komi, err = parseSGFDecimal(value, true)
		if err != nil {
			return nil, decodeErrorf(reasonKomi, "SGFのコミ%qが不正です", value)
		}
		if !game.IsValidKomiLiteral(value) {
			return nil, decodeErrorf(reasonKomi, "SGFのコミ%qは%d以上%d以下の0.5目単位で指定してください", value, int(game.MinimumKomi), int(game.MaximumKomi))
		}
	}
	rules, rulesExplicit := root.Value("RU")
	if rulesExplicit && !strings.EqualFold(rules, game.ChineseRulesSGFValue) {
		return nil, decodeErrorf(reasonRules, "SGFのルール%qには対応していません。RUには中国式の面積計算を表すChineseを指定してください", rules)
	}
	handicap := 0
	_, handicapExplicit := root.Value("HA")
	if value, ok := root.Value("HA"); ok {
		if !unsignedIntegerPattern.MatchString(value) {
			return nil, decodeErrorf(reasonSetup, "SGFの置き碁の石数%qが不正です", value)
		}
		handicap, err = strconv.Atoi(value)
		if err != nil || handicap < 0 {
			return nil, decodeErrorf(reasonSetup, "SGFの置き碁の石数%qが不正です", value)
		}
	}
	if !komiExplicit && handicap > 0 {
		komi = game.DefaultHandicapKomi
	}
	setup := make(map[string]string)
	for _, entry := range []struct {
		ident string
		color string
	}{{"AB", "B"}, {"AW", "W"}} {
		for _, value := range root.Values(entry.ident) {
			points, err := expandPoints(value, size)
			if err != nil {
				return nil, withDecodeReason(reasonSetup, err)
			}
			for _, point := range points {
				if previous := setup[point]; previous != "" {
					return nil, decodeErrorf(reasonSetup, "初期配置の座標%sが重複しています", point)
				}
				setup[point] = entry.color
			}
		}
	}
	if handicapExplicit {
		blackStones := 0
		for _, color := range setup {
			if color == "B" {
				blackStones++
			}
		}
		if blackStones != handicap {
			return nil, decodeErrorf(reasonSetup, "SGFの置き碁の石数HA[%d]が黒の初期配置ABの石数%dと一致しません", handicap, blackStones)
		}
	}
	initialPlayer := "B"
	if handicap > 0 {
		initialPlayer = "W"
	}
	if value, ok := root.Value("PL"); ok {
		initialPlayer = value
	} else if color, ok := firstMoveColor(root); ok {
		initialPlayer = color
	}
	session, err := game.NewSessionWithSetup(id, size, komi, handicap, setup, initialPlayer)
	if err != nil {
		if game.IsSetupGroupWithoutLiberties(err) {
			return nil, withDecodeReason(reasonSetupGroupWithoutLiberties, err)
		}
		return nil, withDecodeReason(reasonSetup, err)
	}
	_, playerExplicit := root.Value("PL")
	_, boardSizeExplicit := root.Value("SZ")
	_, applicationExplicit := root.Value("AP")
	rootStyle := game.SGFRootStyle{
		GameTypeExplicit: gameTypeExplicit, BoardSizeExplicit: boardSizeExplicit, CharsetExplicit: hasCharset, CharsetValue: charset,
		RulesExplicit: rulesExplicit, RulesValue: rules, KomiExplicit: komiExplicit,
		HandicapExplicit: handicapExplicit, PlayerExplicit: playerExplicit, ApplicationExplicit: applicationExplicit,
	}
	if applicationExplicit {
		for _, property := range root.Properties {
			if property.Ident == "AP" && len(property.ComposeValues) == 1 && len(property.ComposeValues[0]) == 2 {
				rootStyle.ApplicationName = property.ComposeValues[0][0]
				rootStyle.ApplicationVersion = property.ComposeValues[0][1]
				break
			}
		}
	}
	session.SetSGFRootStyle(rootStyle)
	if err := session.SetSGFProperties("root", extraProperties(root.Properties)); err != nil {
		return nil, withDecodeReason(reasonProperty, err)
	}
	if _, important := root.Value("HO"); important {
		if err := session.SetNodeImportant("root", true); err != nil {
			return nil, err
		}
	}

	importer := gameImporter{session: session}
	mainEnd, err := importer.importNode(root, true)
	if err != nil {
		return nil, err
	}
	session.PrepareSGFTree(mainEnd)
	if err := session.Navigate(mainEnd); err != nil {
		return nil, withDecodeReason(reasonStructure, fmt.Errorf("SGFの主分岐の末端へ移動できません：%w", err))
	}
	if err := session.SetMainLineNode(mainEnd); err != nil {
		return nil, err
	}
	if value, ok := root.Value("RE"); ok {
		result := parseResult(value)
		if result.Type == "points" {
			if calculated, found, calculationErr := calculateTwoPassResult(root, session, mainEnd, estimateDeadStones); calculationErr != nil {
				return nil, withDecodeReason(reasonResult, calculationErr)
			} else if found {
				if !game.SameResultOutcome(*result, *calculated) {
					return nil, decodeErrorf(reasonResult, "SGFの二連続パスの結果が、死石を除いて数えた結果と一致しません")
				}
				result = calculated
			}
		}
		if err := session.SetSGFRecordedResult(result); err != nil {
			return nil, withDecodeReason(reasonResult, err)
		}
		mainEnd = session.Cursor().NodeID
		if err := session.SetMainLineNode(mainEnd); err != nil {
			return nil, err
		}
	} else if calculated, found, calculationErr := calculateTwoPassResult(root, session, mainEnd, estimateDeadStones); calculationErr != nil {
		return nil, withDecodeReason(reasonResult, calculationErr)
	} else if found {
		if err := session.SetSGFRecordedResult(calculated); err != nil {
			return nil, withDecodeReason(reasonResult, err)
		}
		mainEnd = session.Cursor().NodeID
		if err := session.SetMainLineNode(mainEnd); err != nil {
			return nil, err
		}
	}
	if err := game.ValidateGameTreeHistory(session.Snapshot()); err != nil {
		snapshot := session.Snapshot()
		return nil, withDecodeReason(reasonStructure, fmt.Errorf("SGFの棋譜構造が不正です（主分岐の末端%s、現在の局面%s）：%w", snapshot.MainLineNodeID, snapshot.CurrentNodeID, err))
	}
	return session, nil
}

func calculateTwoPassResult(root *node, session *game.Session, mainEnd string, estimateDeadStones DeadStoneEstimator) (*game.Result, bool, error) {
	_, hasRecordedResult := root.Value("RE")
	if mainLineEndsAtMarker(root) && !hasRecordedResult && root.Values("TB") == nil && root.Values("TW") == nil {
		return nil, false, nil
	}
	if err := session.Navigate(mainEnd); err != nil {
		return nil, false, err
	}
	position := session.Snapshot()
	var mainEndMove *game.Move
	for _, treeNode := range position.TreeNodes {
		if treeNode.ID == mainEnd {
			mainEndMove = treeNode.Move
			break
		}
	}
	if mainEndMove == nil {
		// プロパティを持つ空の主分岐終端を越えて、直前の二連続パスを数える。
		if root.Values("TB") == nil && root.Values("TW") == nil {
			return nil, false, nil
		}
	} else if mainEndMove.Coordinate != "pass" {
		return nil, false, nil
	}
	if !game.IsTwoPassCompletion(position, mainEnd) {
		return nil, false, nil
	}
	deadStones, territoryFound, err := deadStonesFromTerritory(root, position)
	if err != nil {
		return nil, false, err
	}
	if !territoryFound && estimateDeadStones != nil {
		deadStones, err = estimateDeadStones(position)
		if err != nil {
			return nil, false, &deadStoneEstimationError{cause: err}
		}
	}
	calculated, err := game.ScorePosition(position, mainEnd, deadStones)
	if err != nil {
		return nil, false, err
	}
	return calculated, true, nil
}

func mainLineEndsAtMarker(root *node) bool {
	current := root
	for {
		if current.MainLineEndsHere {
			return true
		}
		if len(current.Children) == 0 {
			return false
		}
		current = current.Children[0]
	}
}

func deadStonesFromTerritory(root *node, position game.Snapshot) ([]string, bool, error) {
	values := map[string][]string{"B": {}, "W": {}}
	found := false
	for current := root; current != nil; {
		for _, entry := range []struct{ identifier, owner string }{{"TB", "B"}, {"TW", "W"}} {
			points := current.Values(entry.identifier)
			if points != nil {
				found = true
				for _, point := range points {
					if point != "" {
						values[entry.owner] = append(values[entry.owner], point)
					}
				}
			}
		}
		if len(current.Children) == 0 {
			break
		}
		current = current.Children[0]
	}
	dead := make([]string, 0)
	seen := make(map[string]bool)
	for owner, points := range values {
		opponent := "B"
		if owner == "B" {
			opponent = "W"
		}
		for _, value := range points {
			coordinates, err := expandPoints(value, position.BoardSize)
			if err != nil {
				return nil, false, err
			}
			for _, coordinate := range coordinates {
				if position.Stones[coordinate] == opponent && !seen[coordinate] {
					seen[coordinate] = true
					dead = append(dead, coordinate)
				}
			}
		}
	}
	slices.Sort(dead)
	return dead, found, nil
}

func isASCII(value string) bool {
	for index := range len(value) {
		if value[index] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func validateDistinctSiblingMoves(root *node) error {
	stack := []*node{root}
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		moves := make(map[string]bool)
		for _, child := range current.Children {
			color, coordinate, hasMove := nodeMove(child)
			if hasMove {
				if coordinate == "tt" {
					coordinate = ""
				}
				key := color + "\x00" + coordinate
				if moves[key] {
					return decodeErrorf(reasonStructure, "SGFの同じ局面から同じ着手%s[%s]が重複しています", color, coordinate)
				}
				moves[key] = true
			}
			stack = append(stack, child)
		}
	}
	return nil
}

func validatePropertyPlacement(rootNode *node, root bool) error {
	type propertyVisit struct {
		node   *node
		isRoot bool
	}
	stack := []propertyVisit{{node: rootNode, isRoot: root}}
	for len(stack) > 0 {
		last := len(stack) - 1
		visit := stack[last]
		stack = stack[:last]
		node, isRoot := visit.node, visit.isRoot
		if err := validateNodePropertyPlacement(node, isRoot); err != nil {
			return err
		}
		for index := len(node.Children) - 1; index >= 0; index-- {
			stack = append(stack, propertyVisit{node: node.Children[index]})
		}
	}
	return nil
}

func validateAuxiliaryProperties(rootNode *node, boardSize int) error {
	stack := []*node{rootNode}
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		for _, property := range current.Properties {
			if err := game.ValidateAuxiliarySGFProperty(game.SGFProperty{Ident: property.Ident, Values: property.Values}, boardSize); err != nil {
				return fmt.Errorf("SGFの%w", err)
			}
			for valueIndex, value := range property.Values {
				switch {
				case auxiliaryPointListProperties[property.Ident]:
					expectedComponents := 1
					if strings.Contains(value, ":") {
						expectedComponents = 2
					}
					if len(property.ComposeValues) != len(property.Values) || len(property.ComposeValues[valueIndex]) != expectedComponents {
						return fmt.Errorf("SGFのプロパティ%sの圧縮座標の区切りが不正です", property.Ident)
					}
					if value == "" && (property.Ident == "DD" || property.Ident == "VW") {
						continue
					}
				case property.Ident == "LB":
					separator := strings.IndexByte(value, ':')
					if len(property.ComposeValues) != len(property.Values) || len(property.ComposeValues[valueIndex]) != 2 || separator <= 0 {
						return fmt.Errorf("SGFのプロパティLBの区切りが不正です：LB[%s]", value)
					}
				case property.Ident == "FG":
					if value == "" {
						continue
					}
					separator := strings.IndexByte(value, ':')
					if len(property.ComposeValues) != 1 || len(property.ComposeValues[0]) != 2 || separator <= 0 {
						return fmt.Errorf("SGFのプロパティFGには図の番号と名前を指定してください")
					}
				case property.Ident == "AR" || property.Ident == "LN":
					if len(property.ComposeValues) != len(property.Values) || len(property.ComposeValues[valueIndex]) != 2 {
						return fmt.Errorf("SGFのプロパティ%sの座標の組が不正です：%s[%s]", property.Ident, property.Ident, value)
					}
				}
			}
		}
		stack = append(stack, current.Children...)
	}
	return nil
}

var auxiliaryPointListProperties = map[string]bool{
	"AB": true, "AE": true, "AW": true, "CR": true, "DD": true, "MA": true, "SL": true, "SQ": true,
	"TR": true, "TB": true, "TW": true, "VW": true,
}

func validateNodePropertyPlacement(node *node, root bool) error {
	if err := game.ValidateSGFPropertyList(gameProperties(node.Properties), root); err != nil {
		var propertyError *game.SGFPropertyError
		if errors.As(err, &propertyError) {
			if propertyError.Kind == game.SGFPropertyPlacementError {
				return withDecodeReason(reasonPlacement, err)
			}
			if propertyError.Kind == game.SGFPropertyDuplicateError {
				return withDecodeReason(reasonProperty, err)
			}
			return withDecodeReason(propertyValueReason(propertyError.Identifier), err)
		}
		return withDecodeReason(reasonProperty, err)
	}
	_, _, hasMove := nodeMove(node)
	if root && hasMove {
		return decodeErrorf(reasonMove, "SGFの開始局面に着手を置くことはできません")
	}
	if err := game.ValidateSGFPropertyPlacement(gameProperties(node.Properties), hasMove); err != nil {
		return withDecodeReason(reasonPlacement, err)
	}
	for _, property := range node.Properties {
		if property.Ident == "AP" && (len(property.ComposeValues) != 1 || len(property.ComposeValues[0]) != 2) {
			return decodeErrorf(reasonProperty, "SGFの作成元APはアプリケーション名と版をコロンで区切って指定してください")
		}
		if property.Ident == "AE" {
			return decodeErrorf(reasonProperty, "初期配置の石を取り除く処理がないため、SGFのプロパティAEには対応していません")
		}
	}
	if hasMove {
		for _, setupIdent := range setupPropertyIdentifiers {
			if _, hasSetup := node.Value(setupIdent); hasSetup {
				return decodeErrorf(reasonPlacement, "SGFの同じ局面に着手と初期配置のプロパティ%sを指定することはできません", setupIdent)
			}
		}
	}
	if err := validateNodeMove(node); err != nil {
		return decodeErrorf(reasonMove, "%v", err)
	}
	if root {
		if err := validateRecordedDate(gameProperties(node.Properties)); err != nil {
			return decodeErrorf(reasonDate, "SGFの%v", err)
		}
	}
	return nil
}

var setupPropertyIdentifiers = [...]string{"AB", "AW", "AE", "PL"}

func propertyValueReason(ident string) decodeErrorReason {
	switch ident {
	case "GM":
		return reasonGameType
	case "FF":
		return reasonFileFormat
	case "CA":
		return reasonCharset
	case "SZ":
		return reasonBoardSize
	case "RU":
		return reasonRules
	case "KM":
		return reasonKomi
	case "GN":
		return reasonGameName
	case "DT":
		return reasonDate
	case "HA", "AB", "AW", "AE", "PL":
		return reasonSetup
	case "B", "W":
		return reasonMove
	case "RE":
		return reasonResult
	default:
		return reasonProperty
	}
}

type gameImporter struct {
	session *game.Session
}

func (importer gameImporter) importNode(node *node, root bool) (string, error) {
	session := importer.session
	currentID := session.Cursor().NodeID
	moveColor, moveValue, hasMove := nodeMove(node)
	properties := extraProperties(node.Properties)
	importantValue, important := node.Value("HO")
	if important && importantValue != "1" {
		return "", decodeErrorf(reasonMetadata, "SGFの重要な局面HOには1を指定してください")
	}
	if !root && hasMove {
		cursor := session.Cursor()
		if moveColor != cursor.CurrentPlayer {
			return "", decodeErrorf(reasonMoveOrder, "SGFの着手%s[%s]はその局面の手番と一致しません", moveColor, moveValue)
		}
		coordinate, err := sgfToGTP(moveValue, cursor.BoardSize)
		if err != nil {
			return "", withDecodeReason(reasonMove, err)
		}
		if _, currentID, err = session.PlaySGFMove(coordinate); err != nil {
			return "", withDecodeReason(reasonMove, fmt.Errorf("着手%s[%s]：%w", moveColor, moveValue, err))
		}
		if err := session.SetSGFProperties(currentID, properties); err != nil {
			return "", withDecodeReason(reasonProperty, err)
		}
	} else if !root {
		var err error
		currentID, err = session.AppendSGFNode(properties)
		if err != nil {
			return "", withDecodeReason(reasonProperty, err)
		}
	}
	if important && !root {
		if err := session.SetNodeImportant(currentID, true); err != nil {
			return "", withDecodeReason(reasonMetadata, err)
		}
	}

	mainEnd := currentID
	origin := session.CaptureNavigationCheckpoint()
	for index, child := range node.Children {
		if index > 0 {
			session.RestoreNavigationCheckpoint(origin)
		}
		end, err := importer.importNode(child, false)
		if err != nil {
			return "", err
		}
		if index == 0 && !node.MainLineEndsHere {
			mainEnd = end
		}
	}
	return mainEnd, nil
}

func firstMoveColor(root *node) (string, bool) {
	colors := make(map[string]bool, 2)
	stack := []*node{root}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if color, _, found := nodeMove(current); found {
			colors[color] = true
			continue
		}
		stack = append(stack, current.Children...)
	}
	if len(colors) != 1 {
		return "", false
	}
	for color := range colors {
		return color, true
	}
	return "", false
}

func validateNodeMove(node *node) error {
	_, hasBlack := node.Value("B")
	_, hasWhite := node.Value("W")
	if hasBlack && hasWhite {
		return fmt.Errorf("SGFの局面に黒と白の着手が同時にあります")
	}
	return nil
}

// nodeMove は局面から黒または白の着手を返す。
func nodeMove(node *node) (color, value string, found bool) {
	black, hasBlack := node.Value("B")
	white, hasWhite := node.Value("W")
	if hasBlack {
		return "B", black, true
	}
	if hasWhite {
		return "W", white, true
	}
	return "", "", false
}

func extraProperties(properties []property) []game.SGFProperty {
	result := make([]game.SGFProperty, 0, len(properties))
	for _, property := range properties {
		if game.IsStructuredSGFProperty(property.Ident) {
			continue
		}
		result = append(result, game.SGFProperty{Ident: property.Ident, Values: append([]string(nil), property.Values...)})
	}
	return result
}

func gameProperties(properties []property) []game.SGFProperty {
	result := make([]game.SGFProperty, len(properties))
	for index, property := range properties {
		result[index] = game.SGFProperty{Ident: property.Ident, Values: append([]string(nil), property.Values...)}
	}
	return result
}

func rootBoardSize(root *node) (int, error) {
	value, ok := root.Value("SZ")
	if !ok {
		return 19, nil
	}
	parts := strings.Split(value, ":")
	for _, property := range root.Properties {
		if property.Ident == "SZ" && (len(property.ComposeValues) != 1 || len(property.ComposeValues[0]) != len(parts)) {
			return 0, fmt.Errorf("SGFの盤サイズSZの区切りが不正です")
		}
	}
	if len(parts) > 2 || (len(parts) == 2 && parts[0] != parts[1]) {
		return 0, fmt.Errorf("SGFは正方形の盤だけに対応しています：SZ[%s]", value)
	}
	for _, part := range parts {
		if !unsignedIntegerPattern.MatchString(part) {
			return 0, fmt.Errorf("SGFの盤サイズ%qが不正です", value)
		}
	}
	size, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("SGFの盤サイズ%qが不正です", value)
	}
	return size, nil
}

func expandPoints(value string, size int) ([]string, error) {
	parts := strings.Split(value, ":")
	if len(parts) == 1 {
		point, err := sgfToGTP(parts[0], size)
		if err != nil || point == "pass" {
			return nil, fmt.Errorf("初期配置の座標%qが不正です", value)
		}
		return []string{point}, nil
	}
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return nil, fmt.Errorf("圧縮した座標範囲%qが不正です", value)
	}
	x1, y1 := int(parts[0][0]-'a'), int(parts[0][1]-'a')
	x2, y2 := int(parts[1][0]-'a'), int(parts[1][1]-'a')
	if x1 < 0 || y1 < 0 || x2 < x1 || y2 < y1 || x2 >= size || y2 >= size {
		return nil, fmt.Errorf("圧縮した座標範囲%qが不正です", value)
	}
	points := make([]string, 0, (x2-x1+1)*(y2-y1+1))
	for y := y1; y <= y2; y++ {
		for x := x1; x <= x2; x++ {
			point, _ := sgfToGTP(string([]byte{'a' + byte(x), 'a' + byte(y)}), size)
			points = append(points, point)
		}
	}
	return points, nil
}

func sgfToGTP(value string, size int) (string, error) {
	if value == "" || value == "tt" {
		return "pass", nil
	}
	if len(value) != 2 {
		return "", fmt.Errorf("SGFの座標%qが不正です", value)
	}
	if value[0] < 'a' || value[0] > 'z' || value[1] < 'a' || value[1] > 'z' {
		return "", fmt.Errorf("SGFの座標%qが不正です", value)
	}
	x, y := int(value[0]-'a'), int(value[1]-'a')
	if x >= size || y >= size {
		return "", fmt.Errorf("SGFの座標%qが盤外です", value)
	}
	return coordinate.FormatGTP(x, size-y), nil
}

func parseResult(value string) *game.Result {
	if value == "0" || value == "Draw" {
		return &game.Result{Type: "points", Cause: "recorded"}
	}
	parts := strings.SplitN(value, "+", 2)
	if len(parts) != 2 {
		return &game.Result{Type: "recorded", Cause: "recorded", Value: value}
	}
	winner := parts[0]
	if !game.IsColor(winner) {
		return &game.Result{Type: "recorded", Cause: "recorded", Value: value}
	}
	detail := parts[1]
	if detail == "R" || detail == "Resign" {
		return decisiveRecordedResult("resignation", winner)
	}
	if !unsignedDecimalPattern.MatchString(detail) {
		return &game.Result{Type: "recorded", Cause: "recorded", Value: value}
	}
	margin, err := strconv.ParseFloat(detail, 64)
	if err != nil || margin <= 0 {
		return &game.Result{Type: "recorded", Cause: "recorded", Value: value}
	}
	return &game.Result{Type: "points", Cause: "recorded", Winner: winner, Margin: margin}
}

func decisiveRecordedResult(resultType, winner string) *game.Result {
	loser := "B"
	if winner == "B" {
		loser = "W"
	}
	return &game.Result{Type: resultType, Cause: "recorded", Winner: winner, Loser: loser}
}

var unsignedDecimalPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]*)?$`)

var signedDecimalPattern = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]*)?$`)

var unsignedIntegerPattern = regexp.MustCompile(`^[0-9]+$`)

func parseSGFDecimal(value string, signed bool) (float64, error) {
	pattern := unsignedDecimalPattern
	if signed {
		pattern = signedDecimalPattern
	}
	if !pattern.MatchString(value) {
		return 0, fmt.Errorf("数値%qがSGFの10進表記ではありません", value)
	}
	return strconv.ParseFloat(value, 64)
}
