package sgf

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go-practice/internal/coordinate"
	"go-practice/internal/game"
	"go-practice/internal/version"
)

// Encode は対局の状態を1つの棋譜を含むSGFとして書き出す。
func Encode(snapshot game.Snapshot) (string, error) {
	if err := game.ValidateGameTreeHistory(snapshot); err != nil {
		return "", fmt.Errorf("棋譜をSGFへ書き出せません：%w", err)
	}
	var output strings.Builder
	output.WriteString("(;")
	if snapshot.SGFRootStyle == nil || snapshot.SGFRootStyle.GameTypeExplicit {
		writeProperty(&output, "GM", "1")
	}
	writeProperty(&output, "FF", "4")
	if snapshot.SGFRootStyle == nil || snapshot.SGFRootStyle.CharsetExplicit {
		charset := "UTF-8"
		if snapshot.SGFRootStyle != nil && snapshot.SGFRootStyle.CharsetExplicit {
			charset = snapshot.SGFRootStyle.CharsetValue
		}
		writeProperty(&output, "CA", charset)
	}
	if snapshot.SGFRootStyle == nil {
		writeProperty(&output, "AP", version.Name+":"+version.Current)
	} else if snapshot.SGFRootStyle.ApplicationExplicit {
		writeComposeProperty(&output, "AP", snapshot.SGFRootStyle.ApplicationName, snapshot.SGFRootStyle.ApplicationVersion)
	}
	if snapshot.SGFRootStyle == nil || snapshot.SGFRootStyle.BoardSizeExplicit || snapshot.BoardSize != 19 {
		writeProperty(&output, "SZ", strconv.Itoa(snapshot.BoardSize))
	}
	if snapshot.SGFRootStyle == nil || snapshot.SGFRootStyle.RulesExplicit {
		rules := game.ChineseRulesSGFValue
		if snapshot.SGFRootStyle != nil {
			rules = snapshot.SGFRootStyle.RulesValue
		}
		writeProperty(&output, "RU", rules)
	}
	defaultKomi := game.DefaultKomi
	if snapshot.Handicap > 0 {
		defaultKomi = game.DefaultHandicapKomi
	}
	if snapshot.SGFRootStyle == nil || snapshot.SGFRootStyle.KomiExplicit || snapshot.Komi != defaultKomi {
		writeProperty(&output, "KM", formatNumber(snapshot.Komi))
	}
	if snapshot.Handicap > 0 || snapshot.SGFRootStyle != nil && snapshot.SGFRootStyle.HandicapExplicit {
		writeProperty(&output, "HA", strconv.Itoa(snapshot.Handicap))
	}
	for _, color := range []string{"B", "W"} {
		stones := make([]string, 0, len(snapshot.SetupStones))
		for point, stoneColor := range snapshot.SetupStones {
			if stoneColor == color {
				stones = append(stones, point)
			}
		}
		slices.Sort(stones)
		if len(stones) == 0 {
			continue
		}
		output.WriteString("A" + color)
		for _, point := range stones {
			converted, err := gtpToSGF(point, snapshot.BoardSize)
			if err != nil {
				return "", fmt.Errorf("初期配置の石を書き出せません：%w", err)
			}
			output.WriteByte('[')
			output.WriteString(converted)
			output.WriteByte(']')
		}
	}
	defaultPlayerDiffers := (snapshot.InitialPlayer == "W" && snapshot.Handicap == 0) || (snapshot.InitialPlayer == "B" && snapshot.Handicap > 0)
	if defaultPlayerDiffers && firstMainLineMoveColor(snapshot) == "" || snapshot.SGFRootStyle != nil && snapshot.SGFRootStyle.PlayerExplicit {
		writeProperty(&output, "PL", snapshot.InitialPlayer)
	}
	mainLineResult := snapshotMainLineResult(snapshot)
	if mainLineResult != nil {
		writeProperty(&output, "RE", encodeResult(mainLineResult))
		if mainLineResult.Cause == "two_passes" {
			if err := writeResultTerritory(&output, snapshot, mainLineResult); err != nil {
				return "", err
			}
		}
	}
	suppressTerritory := mainLineResult != nil && mainLineResult.Cause == "two_passes"
	writeExtraProperties(&output, snapshot.RootProperties, suppressTerritory)
	if root := snapshotNode(snapshot.TreeNodes, "root"); root != nil {
		if err := writeNodeMetadata(&output, *root); err != nil {
			return "", err
		}
	}

	mainLineChildren := snapshotMainLineChildren(snapshot.TreeNodes, snapshot.MainLineNodeID)
	childrenByParent := make(map[string][]game.TreeNode, len(snapshot.TreeNodes))
	for _, node := range snapshot.TreeNodes {
		childrenByParent[node.ParentID] = append(childrenByParent[node.ParentID], node)
	}
	emptyMainLineParent := emptyMainLineContinuationParent(snapshot, childrenByParent)
	if err := writeSnapshotContinuations(&output, childrenByParent, "root", snapshot.BoardSize, mainLineChildren, emptyMainLineParent, suppressTerritory); err != nil {
		return "", err
	}
	output.WriteString(")\n")
	encoded := output.String()
	if snapshot.SGFRootStyle != nil && !snapshot.SGFRootStyle.CharsetExplicit && !isASCII(encoded) {
		encoded = strings.Replace(encoded, "FF[4]", "FF[4]CA[UTF-8]", 1)
	}
	return encoded, nil
}

func firstMainLineMoveColor(snapshot game.Snapshot) string {
	byID := make(map[string]game.TreeNode, len(snapshot.TreeNodes))
	for _, node := range snapshot.TreeNodes {
		byID[node.ID] = node
	}
	path := make([]game.TreeNode, 0)
	for id := snapshot.MainLineNodeID; id != "" && id != "root"; id = byID[id].ParentID {
		path = append(path, byID[id])
	}
	for index := len(path) - 1; index >= 0; index-- {
		if path[index].Move != nil {
			return path[index].Move.Color
		}
	}
	return ""
}

func writeSnapshotContinuations(output *strings.Builder, childrenByParent map[string][]game.TreeNode, parentID string, boardSize int, mainLineChildren map[string]string, emptyMainLineParent string, suppressTerritory bool) error {
	children := childrenByParent[parentID]
	slices.SortStableFunc(children, func(first, second game.TreeNode) int {
		if first.ID == second.ID {
			return 0
		}
		mainLineChild := mainLineChildren[parentID]
		if first.ID == mainLineChild {
			return -1
		}
		if second.ID == mainLineChild {
			return 1
		}
		return 0
	})
	writtenChildren := slices.DeleteFunc(slices.Clone(children), omitResultNode)
	omittedMainLineResult := false
	for _, child := range children {
		if omitResultNode(child) {
			omittedMainLineResult = omittedMainLineResult || mainLineChildren[parentID] == child.ID
		}
	}
	keepParentAsMainLineEnd := parentID == emptyMainLineParent
	markerCount := 0
	if keepParentAsMainLineEnd {
		markerCount++
	}
	if omittedMainLineResult && len(writtenChildren) > 0 {
		markerCount++
	}
	continuationCount := markerCount + len(writtenChildren)
	if continuationCount == 0 {
		return nil
	}
	variation := continuationCount > 1
	for range markerCount {
		if variation {
			output.WriteString("(;)")
		} else {
			output.WriteByte(';')
		}
	}
	for _, child := range writtenChildren {
		if child.Move == nil && child.Result != nil {
			if variation {
				output.WriteByte('(')
			}
			output.WriteByte(';')
			writeExtraProperties(output, child.Properties, suppressTerritory)
			if err := writeNodeMetadata(output, child); err != nil {
				return err
			}
			if variation {
				output.WriteByte(')')
			}
			continue
		}
		if variation {
			output.WriteByte('(')
		}
		if child.Move == nil {
			output.WriteByte(';')
		} else if err := writeMove(output, *child.Move, boardSize); err != nil {
			return fmt.Errorf("棋譜の木にある局面を書き出せません：%w", err)
		}
		writeExtraProperties(output, child.Properties, suppressTerritory)
		if err := writeNodeMetadata(output, child); err != nil {
			return err
		}
		if err := writeSnapshotContinuations(output, childrenByParent, child.ID, boardSize, mainLineChildren, emptyMainLineParent, suppressTerritory); err != nil {
			return err
		}
		if variation {
			output.WriteByte(')')
		}
	}
	return nil
}

// emptyMainLineContinuationParent は、空の標準SGF局面を主分岐の末尾へ足す必要がある親を返す。
// 主分岐の末端に変化がある場合は、空の先頭分岐で主分岐の終端位置を保つ。結果を削除した
// 二連続パスの場合は、空局面で読み込み時の結果の再生成を防ぐ。
func emptyMainLineContinuationParent(snapshot game.Snapshot, childrenByParent map[string][]game.TreeNode) string {
	mainLineEnd := snapshotNode(snapshot.TreeNodes, snapshot.MainLineNodeID)
	if mainLineEnd == nil || mainLineEnd.Result != nil {
		return ""
	}
	hasWrittenChild := slices.ContainsFunc(childrenByParent[mainLineEnd.ID], func(child game.TreeNode) bool { return !omitResultNode(child) })
	if hasWrittenChild || mainLineEnd.Move != nil && game.IsTwoPassCompletion(snapshot, mainLineEnd.ID) {
		return mainLineEnd.ID
	}
	return ""
}

func omitResultNode(node game.TreeNode) bool {
	return node.Result != nil && !hasStandardNodeContent(node)
}

func hasStandardNodeContent(node game.TreeNode) bool {
	return node.Important || len(node.Properties) > 0
}

func writeNodeMetadata(output *strings.Builder, node game.TreeNode) error {
	if node.Important {
		writeProperty(output, "HO", "1")
	}
	return nil
}

func snapshotNode(nodes []game.TreeNode, id string) *game.TreeNode {
	for index := range nodes {
		if nodes[index].ID == id {
			return &nodes[index]
		}
	}
	return nil
}

func snapshotMainLineChildren(nodes []game.TreeNode, mainLineNodeID string) map[string]string {
	result := make(map[string]string)
	byID := make(map[string]game.TreeNode, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
	}
	for currentID := mainLineNodeID; currentID != "root"; {
		current := byID[currentID]
		result[current.ParentID] = currentID
		currentID = current.ParentID
	}
	return result
}

func snapshotMainLineResult(snapshot game.Snapshot) *game.Result {
	for _, node := range snapshot.TreeNodes {
		if node.ID == snapshot.MainLineNodeID && node.Result != nil {
			return node.Result
		}
	}
	return nil
}

func writeResultTerritory(output *strings.Builder, snapshot game.Snapshot, result *game.Result) error {
	if result == nil || result.Type != "points" {
		return nil
	}
	session, err := game.RestoreSession(snapshot)
	if err != nil {
		return fmt.Errorf("地の範囲を計算できません：%w", err)
	}
	if err := session.Navigate(snapshot.MainLineNodeID); err != nil {
		return fmt.Errorf("主分岐の末端へ移動できません：%w", err)
	}
	position := session.Snapshot()
	stonesWithoutDead := maps.Clone(position.Stones)
	for _, pointCoordinate := range result.DeadStones {
		delete(stonesWithoutDead, pointCoordinate)
	}
	territory := game.TerritoryCoordinates(snapshot.BoardSize, stonesWithoutDead)
	// 標準SGFには死石を直接表すプロパティがないため、死石の交点を相手の地へ含める。
	// 死石を取り除いて広がった地が反対側へ数えられていても、同じ交点をTBとTWの両方へは書かない。
	territorySets := map[string]map[string]bool{"B": {}, "W": {}}
	for owner, points := range territory {
		for _, point := range points {
			territorySets[owner][point] = true
		}
	}
	for _, pointCoordinate := range result.DeadStones {
		owner, other := "B", "W"
		if position.Stones[pointCoordinate] == "B" {
			owner, other = "W", "B"
		}
		if territorySets[other][pointCoordinate] {
			delete(territorySets[other], pointCoordinate)
			territory[other] = slices.DeleteFunc(territory[other], func(point string) bool { return point == pointCoordinate })
		}
		if !territorySets[owner][pointCoordinate] {
			territory[owner] = append(territory[owner], pointCoordinate)
			territorySets[owner][pointCoordinate] = true
		}
	}
	for _, owner := range []string{"B", "W"} {
		slices.Sort(territory[owner])
		output.WriteString("T" + owner)
		if len(territory[owner]) == 0 {
			output.WriteString("[]")
			continue
		}
		for _, coordinate := range territory[owner] {
			value, conversionErr := gtpToSGF(coordinate, snapshot.BoardSize)
			if conversionErr != nil {
				return fmt.Errorf("地の座標を書き出せません：%w", conversionErr)
			}
			output.WriteString("[" + value + "]")
		}
	}
	return nil
}

func writeExtraProperties(output *strings.Builder, properties []game.SGFProperty, suppressTerritory bool) {
	for _, property := range properties {
		if property.Ident == "HO" || suppressTerritory && (property.Ident == "TB" || property.Ident == "TW") {
			continue
		}
		output.WriteString(property.Ident)
		for _, value := range property.Values {
			output.WriteByte('[')
			output.WriteString(escapePropertyValue(property.Ident, value))
			output.WriteByte(']')
		}
	}
}

func escapePropertyValue(ident, value string) string {
	escaped := escape(value)
	if ident != "LB" && ident != "FG" {
		return escaped
	}
	separator := strings.IndexByte(escaped, ':')
	if separator < 0 {
		return escaped
	}
	return escaped[:separator+1] + strings.ReplaceAll(escaped[separator+1:], ":", `\:`)
}

func writeMove(output *strings.Builder, move game.Move, boardSize int) error {
	if !game.IsColor(move.Color) {
		return fmt.Errorf("石の色%qが不正です", move.Color)
	}
	converted, err := gtpToSGF(move.Coordinate, boardSize)
	if err != nil {
		return err
	}
	output.WriteByte(';')
	writeProperty(output, move.Color, converted)
	return nil
}

func encodeResult(result *game.Result) string {
	if result == nil {
		return ""
	}
	if result.Type == "resignation" && game.IsColor(result.Winner) {
		return result.Winner + "+R"
	}
	if result.Type == "recorded" {
		return result.Value
	}
	if result.Type != "points" {
		return ""
	}
	if result.Winner == "" {
		return "0"
	}
	if game.IsColor(result.Winner) {
		return result.Winner + "+" + formatNumber(result.Margin)
	}
	return ""
}

func gtpToSGF(value string, boardSize int) (string, error) {
	if strings.EqualFold(strings.TrimSpace(value), "pass") {
		return "", nil
	}
	value = strings.ToUpper(strings.TrimSpace(value))
	point, ok := coordinate.Point(value, boardSize)
	if !ok {
		return "", fmt.Errorf("座標%qが不正です", value)
	}
	return string([]byte{'a' + byte(point%boardSize), 'a' + byte(point/boardSize)}), nil
}

func writeProperty(output *strings.Builder, name, value string) {
	output.WriteString(name)
	output.WriteByte('[')
	output.WriteString(escape(value))
	output.WriteByte(']')
}

func writeComposeProperty(output *strings.Builder, name string, components ...string) {
	output.WriteString(name)
	output.WriteByte('[')
	for index, component := range components {
		if index > 0 {
			output.WriteByte(':')
		}
		output.WriteString(strings.ReplaceAll(escape(component), ":", `\:`))
	}
	output.WriteByte(']')
}

func escape(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "]", "\\]")
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
