// Package game は囲碁対局の状態と棋譜ツリーを管理する。
package game

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"go-practice/internal/ailevel"
	coord "go-practice/internal/coordinate"
)

const (
	MinimumKomi                     = -100.0
	MaximumKomi                     = 100.0
	minimumFixedHandicap            = 2
	maximumFixedHandicap            = 9
	gameTreePositionNotFoundMessage = "棋譜の局面が見つかりません"
	gameTreeCycleMessage            = "棋譜の木が循環しています"
	twoPassResultMismatchMessage    = "二連続パス結果が着手履歴と一致しません"
	// MaximumTreeNodesは開始局面を含めて1対局に保持できる局面数である。
	MaximumTreeNodes = 4096
)

var errGameFinished = errors.New("対局はすでに終了しています")

type boardConfiguration struct {
	size            int
	maximumHandicap int
}

var supportedBoards = []boardConfiguration{{5, 5}, {6, 4}, {7, 9}, {9, 9}, {13, 9}, {19, 9}}
var supportedColors = []string{"B", "W"}

func IsSupportedBoardSize(size int) bool {
	return slices.ContainsFunc(supportedBoards, func(board boardConfiguration) bool {
		return board.size == size
	})
}

func validateBoardSize(size int) error {
	if !IsSupportedBoardSize(size) {
		return fmt.Errorf("盤サイズ%dには対応していません", size)
	}
	return nil
}

func SupportedBoardSizes() []int {
	result := make([]int, len(supportedBoards))
	for index, board := range supportedBoards {
		result[index] = board.size
	}
	return result
}

func SupportedHandicaps() []int {
	result := []int{0}
	for handicap := minimumFixedHandicap; handicap <= maximumFixedHandicap; handicap++ {
		result = append(result, handicap)
	}
	return result
}

func isSupportedHandicap(handicap int) bool {
	return handicap == 0 || handicap >= minimumFixedHandicap && handicap <= maximumFixedHandicap
}

func validateHandicap(handicap int) error {
	if !isSupportedHandicap(handicap) {
		return fmt.Errorf("置き碁の石数は0または%d以上%d以下にしてください", minimumFixedHandicap, maximumFixedHandicap)
	}
	return nil
}

// maximumFixedHandicapForBoardSize は盤サイズで配置できる置石の上限を返す。
func maximumFixedHandicapForBoardSize(boardSize int) (int, error) {
	for _, board := range supportedBoards {
		if board.size == boardSize {
			return board.maximumHandicap, nil
		}
	}
	return 0, fmt.Errorf("盤サイズ%dには対応していません", boardSize)
}

func SupportedColors() []string {
	return append([]string(nil), supportedColors...)
}

func isValidKomi(komi float64) bool {
	return !math.IsNaN(komi) && !math.IsInf(komi, 0) && komi >= MinimumKomi && komi <= MaximumKomi && komi*2 == math.Trunc(komi*2)
}

// IsValidKomiLiteral は数値の文字列表現を丸めずにコミとして検査する。
func IsValidKomiLiteral(value string) bool {
	komi, ok := new(big.Rat).SetString(value)
	if !ok {
		return false
	}
	minimum := new(big.Rat).SetFloat64(MinimumKomi)
	maximum := new(big.Rat).SetFloat64(MaximumKomi)
	if komi.Cmp(minimum) < 0 || komi.Cmp(maximum) > 0 {
		return false
	}
	doubled := new(big.Rat).Mul(komi, big.NewRat(2, 1))
	return doubled.IsInt()
}

func validateKomi(komi float64) error {
	if !isValidKomi(komi) {
		return fmt.Errorf("コミは%.0f以上%.0f以下の0.5目単位で指定してください", MinimumKomi, MaximumKomi)
	}
	return nil
}

// IsColor は値が黒または白を表すかを返す。
func IsColor(value string) bool { return slices.Contains(supportedColors, value) }

type Move struct {
	Color      string   `json:"color" required:"true"`
	Coordinate string   `json:"coordinate" required:"true"`
	Captured   []string `json:"captured,omitempty"`
}

type Snapshot struct {
	ID                string            `json:"id" required:"true"`
	BoardSize         int               `json:"boardSize" required:"true"`
	Rules             string            `json:"rules" required:"true"`
	Komi              float64           `json:"komi" required:"true" value:"komi"`
	Handicap          int               `json:"handicap" required:"true"`
	SetupStones       map[string]string `json:"setupStones" required:"true" null:"reject"`
	InitialPlayer     string            `json:"initialPlayer" required:"true"`
	CurrentPlayer     string            `json:"currentPlayer" required:"true"`
	Status            string            `json:"status" required:"true"`
	ConsecutivePasses int               `json:"consecutivePasses" required:"true"`
	Moves             []Move            `json:"moves" required:"true" null:"reject"`
	Stones            map[string]string `json:"stones" required:"true" null:"reject"`
	Captures          map[string]int    `json:"captures" required:"true" null:"reject"`
	Result            *Result           `json:"result" required:"true"`
	TreeNodes         []TreeNode        `json:"treeNodes" required:"true" null:"reject"`
	CurrentNodeID     string            `json:"currentNodeId" required:"true"`
	MainLineNodeID    string            `json:"mainLineNodeId" required:"true"`
	NextNodeNumber    int               `json:"nextNodeNumber,omitempty" required:"true"`
	RootProperties    []SGFProperty     `json:"rootProperties,omitempty"`
	SGFRootStyle      *SGFRootStyle     `json:"sgfRootStyle,omitempty"`
}

// SGFRootStyle はSGFの開始局面で明示されていた省略可能な項目を保持する。
type SGFRootStyle struct {
	GameTypeExplicit    bool   `json:"gameTypeExplicit" required:"true"`
	BoardSizeExplicit   bool   `json:"boardSizeExplicit" required:"true"`
	CharsetExplicit     bool   `json:"charsetExplicit" required:"true"`
	CharsetValue        string `json:"charsetValue" required:"true"`
	RulesExplicit       bool   `json:"rulesExplicit" required:"true"`
	RulesValue          string `json:"rulesValue" required:"true"`
	KomiExplicit        bool   `json:"komiExplicit" required:"true"`
	HandicapExplicit    bool   `json:"handicapExplicit" required:"true"`
	PlayerExplicit      bool   `json:"playerExplicit" required:"true"`
	ApplicationExplicit bool   `json:"applicationExplicit" required:"true"`
	ApplicationName     string `json:"applicationName,omitempty"`
	ApplicationVersion  string `json:"applicationVersion,omitempty"`
}

type SGFProperty struct {
	Ident  string   `json:"ident" required:"true"`
	Values []string `json:"values" required:"true" null:"reject"`
}

// SinglePropertyValue は指定したプロパティの単一の値を返す。
func SinglePropertyValue(properties []SGFProperty, ident string) (string, bool, error) {
	for _, property := range properties {
		if property.Ident != ident {
			continue
		}
		if len(property.Values) != 1 {
			return "", true, fmt.Errorf("%sには値を1つだけ指定してください", ident)
		}
		return property.Values[0], true, nil
	}
	return "", false, nil
}

const (
	chineseRules         = "chinese"
	ChineseRulesSGFValue = "Chinese"
	DefaultKomi          = 7.5
	DefaultHandicapKomi  = 0.5
)

// SGFPropertyError はSGFプロパティの形状検査で見つかった不備を表す。
type SGFPropertyError struct {
	Identifier string
	Kind       SGFPropertyErrorKind
	cause      error
}

// SGFPropertyErrorKind はプロパティの不備が値と配置のどちらにあるかを表す。
type SGFPropertyErrorKind string

const (
	SGFPropertyValueError     SGFPropertyErrorKind = "value"
	SGFPropertyPlacementError SGFPropertyErrorKind = "placement"
	SGFPropertyDuplicateError SGFPropertyErrorKind = "duplicate"
)

func (err *SGFPropertyError) Error() string { return err.cause.Error() }

type TreeNode struct {
	ID                      string        `json:"id" required:"true"`
	ParentID                string        `json:"parentId,omitempty"`
	Move                    *Move         `json:"move,omitempty"`
	Result                  *Result       `json:"result,omitempty"`
	Properties              []SGFProperty `json:"properties,omitempty"`
	Important               bool          `json:"important,omitempty"`
	AIDecision              *AIDecision   `json:"aiDecision,omitempty"`
	Origin                  string        `json:"origin,omitempty"`
	VariationTitle          string        `json:"variationTitle,omitempty"`
	Highlighted             bool          `json:"highlighted,omitempty"`
	AutomaticResultCanceled bool          `json:"automaticResultCanceled,omitempty"`
}

// Cursor は棋譜の木における現在位置と、その局面で必要な最小限の状態を表す。
type Cursor struct {
	NodeID        string
	ParentID      string
	CurrentPlayer string
	BoardSize     int
	HasResult     bool
}

const AgentOrigin = "agent"

func (session *Session) Cursor() Cursor {
	node := session.treeNodes[session.currentNodeID]
	return Cursor{
		NodeID:        session.currentNodeID,
		ParentID:      node.ParentID,
		CurrentPlayer: session.currentPlayer,
		BoardSize:     session.size,
		HasResult:     node.Result != nil,
	}
}

// AddVariation は、アプリが現在表示する局面を変えず、指定局面から続く手順を追加または再利用する。
func (session *Session) AddVariation(originNodeID string, coordinates []string, deadStonesByMove map[int][]string, origin, title string) ([]string, error) {
	if len(coordinates) == 0 {
		return nil, fmt.Errorf("変化には1手以上の着手が必要です")
	}
	backup := session.Clone()
	originalNodeID := session.currentNodeID
	if err := session.Navigate(originNodeID); err != nil {
		*session = *backup
		return nil, err
	}
	if session.treeNodes[originNodeID].Result != nil {
		*session = *backup
		return nil, fmt.Errorf("結果の局面から変化を続けることはできません")
	}
	created := make([]string, 0, len(coordinates))
	titleAssigned := false
	for index, coordinate := range coordinates {
		coordinate = normalizeCoordinate(coordinate)
		firstNewNodeNumber := session.nextNodeNumber
		if _, err := session.PlayVariationWithDeadStones(coordinate, deadStonesByMove[index]); err != nil {
			*session = *backup
			return nil, fmt.Errorf("変化の%d手目：%w", index+1, err)
		}
		node := session.treeNodes[session.currentNodeID]
		if session.isTwoPassResultNode(node.ID) {
			resultNode := node
			if nodeIDNumber(resultNode.ID) >= firstNewNodeNumber {
				resultNode.Origin = origin
				resultNode.Highlighted = true
				session.treeNodes[resultNode.ID] = resultNode
			}
			node = session.treeNodes[resultNode.ParentID]
		}
		if nodeIDNumber(node.ID) >= firstNewNodeNumber {
			node.Origin = origin
			node.Highlighted = true
			if !titleAssigned {
				node.VariationTitle = title
				titleAssigned = true
			}
			session.treeNodes[node.ID] = node
		}
		created = append(created, node.ID)
	}
	if err := session.Navigate(originalNodeID); err != nil {
		*session = *backup
		return nil, err
	}
	return created, nil
}

// SetNodeImportant は棋譜の局面に重要な局面の印を付け外しする。
func (session *Session) SetNodeImportant(nodeID string, important bool) error {
	node, ok := session.treeNodes[nodeID]
	if !ok {
		return newCategorizedError(errorCategoryNodeNotFound, gameTreePositionNotFoundMessage)
	}
	node.Important = important
	session.treeNodes[nodeID] = node
	return nil
}

type AIDecision struct {
	Level        int           `json:"level" required:"true"`
	Visits       int           `json:"visits" required:"true"`
	EngineMove   string        `json:"engineMove" required:"true"`
	SelectedMove string        `json:"selectedMove" required:"true"`
	Candidates   []AICandidate `json:"candidates" required:"true" null:"reject"`
}

// AICandidate はAIが着手を選ぶ対象にした候補と、その重みを表す。
type AICandidate struct {
	Coordinate string  `json:"coordinate" required:"true"`
	Weight     float64 `json:"weight" required:"true"`
}

type Result struct {
	Type       string   `json:"type"`
	Cause      string   `json:"cause"`
	Value      string   `json:"value,omitempty"`
	Winner     string   `json:"winner,omitempty"`
	Loser      string   `json:"loser,omitempty"`
	Margin     float64  `json:"margin,omitempty"`
	BlackScore float64  `json:"blackScore,omitempty"`
	WhiteScore float64  `json:"whiteScore,omitempty"`
	ScoreKnown bool     `json:"scoreKnown"`
	DeadStones []string `json:"deadStones" required:"true" null:"reject"`
}

type resultJSONError struct{ message string }

func (err *resultJSONError) Error() string              { return err.message }
func (err *resultJSONError) PublicInputMessage() string { return err.message }

func (result Result) MarshalJSON() ([]byte, error) {
	type encodedResult struct {
		Type       string   `json:"type"`
		Cause      string   `json:"cause"`
		Value      *string  `json:"value,omitempty"`
		Winner     string   `json:"winner,omitempty"`
		Loser      string   `json:"loser,omitempty"`
		Margin     float64  `json:"margin,omitempty"`
		BlackScore *float64 `json:"blackScore,omitempty"`
		WhiteScore *float64 `json:"whiteScore,omitempty"`
		ScoreKnown bool     `json:"scoreKnown"`
		DeadStones []string `json:"deadStones"`
	}
	encoded := encodedResult{
		Type: result.Type, Cause: result.Cause, Winner: result.Winner, Loser: result.Loser,
		Margin: result.Margin, ScoreKnown: result.ScoreKnown, DeadStones: result.DeadStones,
	}
	if encoded.DeadStones == nil {
		encoded.DeadStones = []string{}
	}
	if result.ScoreKnown {
		encoded.BlackScore = &result.BlackScore
		encoded.WhiteScore = &result.WhiteScore
	}
	if result.Type == "recorded" {
		encoded.Value = &result.Value
	}
	return json.Marshal(encoded)
}

func (result *Result) UnmarshalJSON(data []byte) error {
	var decoded struct {
		Type       string   `json:"type"`
		Cause      string   `json:"cause"`
		Value      *string  `json:"value"`
		Winner     string   `json:"winner"`
		Loser      string   `json:"loser"`
		Margin     float64  `json:"margin"`
		BlackScore *float64 `json:"blackScore"`
		WhiteScore *float64 `json:"whiteScore"`
		ScoreKnown *bool    `json:"scoreKnown"`
		DeadStones []string `json:"deadStones"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if decoded.Cause == "" {
		return &resultJSONError{message: "結果には終局理由を指定してください"}
	}
	if decoded.Type == "recorded" && decoded.Value == nil {
		return &resultJSONError{message: "未解釈の記録結果にはSGFの結果値を指定してください"}
	}
	if decoded.Type != "recorded" && decoded.Value != nil {
		return &resultJSONError{message: "SGFの結果値は未解釈の記録結果にだけ指定できます"}
	}
	if decoded.ScoreKnown == nil {
		if decoded.Type == "points" {
			return &resultJSONError{message: "点数結果には点数の内訳の有無を指定してください"}
		}
		return &resultJSONError{message: "結果には点数の内訳の有無を指定してください"}
	}
	if *decoded.ScoreKnown && (decoded.BlackScore == nil || decoded.WhiteScore == nil) {
		return &resultJSONError{message: "点数の内訳がある結果には黒と白の点数を両方指定してください"}
	}
	if !*decoded.ScoreKnown && (decoded.BlackScore != nil || decoded.WhiteScore != nil) {
		return &resultJSONError{message: "点数の内訳がない結果には黒と白の点数を指定できません"}
	}
	blackScore, whiteScore := 0.0, 0.0
	if decoded.BlackScore != nil {
		blackScore = *decoded.BlackScore
		whiteScore = *decoded.WhiteScore
	}
	value := ""
	if decoded.Value != nil {
		value = *decoded.Value
	}
	*result = Result{
		Type: decoded.Type, Cause: decoded.Cause, Value: value, Winner: decoded.Winner, Loser: decoded.Loser,
		Margin: decoded.Margin, BlackScore: blackScore, WhiteScore: whiteScore,
		ScoreKnown: *decoded.ScoreKnown, DeadStones: decoded.DeadStones,
	}
	return nil
}

type Session struct {
	id                string
	size              int
	komi              float64
	handicap          int
	setupStones       map[string]string
	initialPlayer     string
	grid              []string
	currentPlayer     string
	status            string
	consecutivePasses int
	moves             []Move
	movePositionKeys  []string
	captures          map[string]int
	seenPositions     map[string]bool
	result            *Result
	treeNodes         map[string]TreeNode
	childNodeIDs      map[string][]string
	currentNodeID     string
	mainLineNodeID    string
	nextNodeNumber    int
	rootProperties    []SGFProperty
	sgfRootStyle      *SGFRootStyle
	replayOnly        bool
}

func (session *Session) Clone() *Session {
	clone := *session
	clone.grid = append([]string(nil), session.grid...)
	clone.setupStones = make(map[string]string, len(session.setupStones))
	for coordinate, color := range session.setupStones {
		clone.setupStones[coordinate] = color
	}
	clone.moves = make([]Move, len(session.moves))
	for index, move := range session.moves {
		clone.moves[index] = move
		clone.moves[index].Captured = append([]string(nil), move.Captured...)
	}
	clone.movePositionKeys = append([]string(nil), session.movePositionKeys...)
	clone.captures = map[string]int{"B": session.captures["B"], "W": session.captures["W"]}
	clone.seenPositions = make(map[string]bool, len(session.seenPositions))
	for position := range session.seenPositions {
		clone.seenPositions[position] = true
	}
	clone.treeNodes = make(map[string]TreeNode, len(session.treeNodes))
	for id, node := range session.treeNodes {
		clonedNode := node
		clonedNode.Properties = cloneProperties(node.Properties)
		if node.Move != nil {
			move := *node.Move
			move.Captured = append([]string(nil), node.Move.Captured...)
			clonedNode.Move = &move
		}
		if node.AIDecision != nil {
			decision := *node.AIDecision
			decision.Candidates = slices.Clone(node.AIDecision.Candidates)
			clonedNode.AIDecision = &decision
		}
		if node.Result != nil {
			result := *node.Result
			result.DeadStones = slices.Clone(node.Result.DeadStones)
			clonedNode.Result = &result
		}
		clone.treeNodes[id] = clonedNode
	}
	clone.childNodeIDs = cloneChildNodeIDs(session.childNodeIDs)
	clone.rootProperties = cloneProperties(session.rootProperties)
	if session.sgfRootStyle != nil {
		style := *session.sgfRootStyle
		clone.sgfRootStyle = &style
	}
	if session.result != nil {
		result := *session.result
		result.DeadStones = slices.Clone(session.result.DeadStones)
		clone.result = &result
	}
	return &clone
}

func NewConfiguredSession(id string, boardSize int, komi float64, handicap int) (*Session, error) {
	coordinates, err := fixedHandicapCoordinates(boardSize, handicap)
	if err != nil {
		return nil, err
	}
	setup := make(map[string]string, len(coordinates))
	for _, coordinate := range coordinates {
		setup[coordinate] = "B"
	}
	initialPlayer := "B"
	if handicap > 0 {
		initialPlayer = "W"
	}
	return newSessionWithSetup(id, boardSize, komi, handicap, setup, initialPlayer)
}

// NewSessionWithSetup は、SGFや保存した対局に記録された初期配置を持つ局面を作成する。
func NewSessionWithSetup(id string, boardSize int, komi float64, handicap int, setup map[string]string, initialPlayer string) (*Session, error) {
	if _, err := fixedHandicapCoordinates(boardSize, handicap); err != nil {
		return nil, err
	}
	if handicap > 0 {
		blackStones := 0
		for _, color := range setup {
			if color == "B" {
				blackStones++
			}
		}
		if blackStones != handicap {
			return nil, fmt.Errorf("置き碁の石数%dと黒の初期石%d個が一致しません", handicap, blackStones)
		}
	}
	return newSessionWithSetup(id, boardSize, komi, handicap, setup, initialPlayer)
}

func newSessionWithSetup(id string, boardSize int, komi float64, handicap int, setup map[string]string, initialPlayer string) (*Session, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("対局の識別情報を空にはできません")
	}
	if err := validateBoardSize(boardSize); err != nil {
		return nil, err
	}
	if err := validateKomi(komi); err != nil {
		return nil, err
	}
	if komi == 0 {
		komi = 0
	}
	if !IsColor(initialPlayer) {
		return nil, fmt.Errorf("開始手番にはBまたはWを指定してください")
	}
	session := &Session{
		id: id, size: boardSize, komi: komi, grid: make([]string, boardSize*boardSize),
		currentPlayer: initialPlayer, initialPlayer: initialPlayer, status: "playing", captures: map[string]int{"B": 0, "W": 0},
		seenPositions: make(map[string]bool), setupStones: make(map[string]string), handicap: handicap,
		treeNodes: map[string]TreeNode{"root": {ID: "root"}}, childNodeIDs: make(map[string][]string), currentNodeID: "root", mainLineNodeID: "root", nextNodeNumber: 1,
	}
	setupCoordinates := make([]string, 0, len(setup))
	for coordinate := range setup {
		setupCoordinates = append(setupCoordinates, coordinate)
	}
	sort.Strings(setupCoordinates)
	for _, coordinate := range setupCoordinates {
		color := setup[coordinate]
		if !IsColor(color) {
			return nil, fmt.Errorf("初期配置%sの石の色%qが不正です", coordinate, color)
		}
		coordinate = normalizeCoordinate(coordinate)
		point, ok := session.point(coordinate)
		if !ok {
			return nil, fmt.Errorf("初期配置の座標%qが不正です", coordinate)
		}
		if session.grid[point] != "" {
			return nil, fmt.Errorf("初期配置の座標%sが重複しています", coordinate)
		}
		session.grid[point] = color
		session.setupStones[coordinate] = color
	}
	for _, group := range boardGroups(session.size, session.grid) {
		if len(group.liberties) == 0 {
			return nil, newCategorizedError(
				errorCategorySetupGroupWithoutLiberties,
				fmt.Sprintf("初期配置に呼吸点のない%sの石があります", session.coordinate(group.stones[0])),
			)
		}
	}
	session.seenPositions[session.positionKey()] = true
	return session, nil
}

// fixedHandicapCoordinates は一般的な置き碁の座標を返す。
func fixedHandicapCoordinates(boardSize, handicap int) ([]string, error) {
	if handicap == 0 {
		return nil, nil
	}
	if err := validateHandicap(handicap); err != nil {
		return nil, err
	}
	if err := validateBoardSize(boardSize); err != nil {
		return nil, err
	}
	maximum, err := maximumFixedHandicapForBoardSize(boardSize)
	if err != nil {
		return nil, err
	}
	if handicap > maximum {
		return nil, fmt.Errorf("%d路盤の置き碁は%d子までです", boardSize, maximum)
	}
	low := 2
	if boardSize >= 13 {
		low = 4
	} else if boardSize >= 9 {
		low = 3
	}
	high := boardSize + 1 - low
	middle := (boardSize + 1) / 2
	point := func(column, row int) string { return coord.FormatGTP(column-1, row) }
	corners := []string{point(high, high), point(low, low), point(high, low), point(low, high)}
	result := append([]string(nil), corners[:min(handicap, 4)]...)
	if handicap == 5 || handicap == 7 || handicap == 9 {
		result = append(result, point(middle, middle))
	}
	if handicap >= 6 {
		result = append(result, point(low, middle), point(high, middle))
	}
	if handicap >= 8 {
		result = append(result, point(middle, low), point(middle, high))
	}
	return result, nil
}

func RestoreSession(snapshot Snapshot) (*Session, error) {
	if err := ValidateGameTreeHistory(snapshot); err != nil {
		return nil, &gameTreeHistoryError{cause: err}
	}
	session, err := NewSessionWithSetup(snapshot.ID, snapshot.BoardSize, snapshot.Komi, snapshot.Handicap, snapshot.SetupStones, snapshot.InitialPlayer)
	if err != nil {
		return nil, err
	}
	session.restoreTree(snapshot.TreeNodes, snapshot.CurrentNodeID, snapshot.NextNodeNumber)
	if err := session.SetMainLineNode(snapshot.MainLineNodeID); err != nil {
		return nil, fmt.Errorf("主分岐を復元できません：%w", err)
	}
	if err := session.rebuildCurrentLine(); err != nil {
		return nil, err
	}
	session.rootProperties = cloneProperties(snapshot.RootProperties)
	if snapshot.SGFRootStyle != nil {
		style := *snapshot.SGFRootStyle
		session.sgfRootStyle = &style
	}
	restored := session.Snapshot()
	if !reflect.DeepEqual(restored.Moves, snapshot.Moves) {
		return nil, fmt.Errorf("対局の現在の経路が着手履歴と一致しません")
	}
	if !equalOptionalResult(restored.Result, snapshot.Result) {
		return nil, fmt.Errorf("対局全体の結果が現在の棋譜の局面と一致しません")
	}
	if restored.Status != snapshot.Status || restored.CurrentPlayer != snapshot.CurrentPlayer || restored.ConsecutivePasses != snapshot.ConsecutivePasses {
		return nil, fmt.Errorf("対局状態が着手履歴と一致しません")
	}
	setupMismatch := !reflect.DeepEqual(restored.SetupStones, snapshot.SetupStones)
	if !reflect.DeepEqual(restored.Stones, snapshot.Stones) || setupMismatch || !reflect.DeepEqual(restored.Captures, snapshot.Captures) {
		return nil, fmt.Errorf("盤面が着手履歴と一致しません")
	}
	return session, nil
}

func (session *Session) Play(coordinate string) (Move, error) {
	return session.PlayWithDeadStones(coordinate, nil)
}

// PlayWithDeadStones は着手を適用し、二連続パスなら指定した死石を除いて結果を作る。
func (session *Session) PlayWithDeadStones(coordinate string, deadStones []string) (Move, error) {
	move, _, err := session.playWithNode(coordinate, false, deadStones)
	return move, err
}

// PlaySGFMove はSGFの取り込みで着手を適用し、着手局面の識別情報を返す。
func (session *Session) PlaySGFMove(coordinate string) (Move, string, error) {
	return session.playWithNode(coordinate, true, nil)
}

// PlayVariation は、現在の局面から指定した着手を変化として追加する。
func (session *Session) PlayVariation(coordinate string) (Move, error) {
	return session.PlayVariationWithDeadStones(coordinate, nil)
}

// PlayVariationWithDeadStones は変化へ着手し、二連続パスなら指定した死石を除いて結果を作る。
func (session *Session) PlayVariationWithDeadStones(coordinate string, deadStones []string) (Move, error) {
	if session.treeNodes[session.currentNodeID].Result != nil {
		return Move{}, errGameFinished
	}
	checkpoint := session.CaptureNavigationCheckpoint()
	session.ContinueAfterResult()
	move, err := session.PlayWithDeadStones(coordinate, deadStones)
	if err != nil {
		session.RestoreNavigationCheckpoint(checkpoint)
	}
	return move, err
}

// PreviewMove は実際の対局を変更せずに、指定した着手後の局面を返す。
// variation が true のときは、変化への着手として終局前の状態から局面を進める。
func (session *Session) PreviewMove(coordinate string, variation bool) (Snapshot, error) {
	preview := session.Clone()
	if variation {
		if preview.treeNodes[preview.currentNodeID].Result != nil {
			return Snapshot{}, errGameFinished
		}
		preview.ContinueAfterResult()
	}
	if _, _, err := preview.playWithNode(coordinate, true, nil); err != nil {
		return Snapshot{}, err
	}
	return preview.Snapshot(), nil
}

// PreviewTwoPassMove は、着手が二連続パスを完成させる場合だけ適用後の局面を返す。
func (session *Session) PreviewTwoPassMove(coordinate string, variation bool) (Snapshot, bool, error) {
	if !strings.EqualFold(strings.TrimSpace(coordinate), "pass") || session.consecutivePasses != 1 {
		return Snapshot{}, false, nil
	}
	position, err := session.PreviewMove(coordinate, variation)
	return position, true, err
}

func (session *Session) playWithNode(coordinate string, importing bool, deadStones []string) (Move, string, error) {
	if session.status != "playing" {
		return Move{}, "", errGameFinished
	}
	coordinate = normalizeCoordinate(coordinate)
	if coordinate == "pass" {
		previousNodeID := session.currentNodeID
		previousPlayer := session.currentPlayer
		previousPasses := session.consecutivePasses
		previousMovesLength := len(session.moves)
		previousPositionKeysLength := len(session.movePositionKeys)
		previousNextNodeNumber := session.nextNodeNumber
		move := Move{Color: session.currentPlayer, Coordinate: "pass"}
		if err := session.appendTreeMove(move); err != nil {
			return Move{}, "", err
		}
		passNodeID := session.currentNodeID
		createdPassNode := session.nextNodeNumber != previousNextNodeNumber
		previousPassNode := session.treeNodes[passNodeID]
		rollbackPass := func() {
			if createdPassNode {
				session.removeChildNodeID(previousNodeID, passNodeID)
				delete(session.childNodeIDs, passNodeID)
				delete(session.treeNodes, passNodeID)
				session.nextNodeNumber = previousNextNodeNumber
			} else {
				session.treeNodes[passNodeID] = previousPassNode
			}
			session.currentNodeID = previousNodeID
			session.currentPlayer = previousPlayer
			session.consecutivePasses = previousPasses
			session.moves = session.moves[:previousMovesLength]
			session.movePositionKeys = session.movePositionKeys[:previousPositionKeysLength]
			session.status = "playing"
			session.result = nil
		}
		session.moves = append(session.moves, move)
		session.movePositionKeys = append(session.movePositionKeys, "")
		session.consecutivePasses = consecutivePassesAfterMove(session.consecutivePasses, true)
		session.togglePlayer()
		if node := session.treeNodes[session.currentNodeID]; node.AutomaticResultCanceled {
			if importing {
				session.consecutivePasses = 0
				return move, passNodeID, nil
			}
			node.AutomaticResultCanceled = false
			session.treeNodes[node.ID] = node
		}
		if session.consecutivePasses == 2 {
			if importing {
				return move, passNodeID, nil
			}
			if err := session.validateDeadStonesOnBoard(deadStones); err != nil {
				rollbackPass()
				return Move{}, "", err
			}
			if err := session.appendResultNode(*session.areaScoreWithDeadStones(deadStones)); err != nil {
				rollbackPass()
				return Move{}, "", err
			}
		}
		return move, passNodeID, nil
	}
	point, ok := session.point(coordinate)
	if !ok {
		return Move{}, "", newCategorizedError(errorCategoryInvalidMove, fmt.Sprintf("座標%qが不正です", coordinate))
	}
	if session.grid[point] != "" {
		return Move{}, "", fmt.Errorf("座標%sにはすでに石があります", coordinate)
	}

	before := append([]string(nil), session.grid...)
	session.grid[point] = session.currentPlayer
	opponent := OtherColor(session.currentPlayer)
	var capturedPoints []int
	checked := make(map[int]bool)
	for _, neighbor := range session.neighbors(point) {
		if session.grid[neighbor] != opponent || checked[neighbor] {
			continue
		}
		stones, liberties := session.groupAt(neighbor)
		for _, stone := range stones {
			checked[stone] = true
		}
		if len(liberties) == 0 {
			capturedPoints = append(capturedPoints, stones...)
			for _, stone := range stones {
				session.grid[stone] = ""
			}
		}
	}
	_, liberties := session.groupAt(point)
	if len(liberties) == 0 {
		session.grid = before
		return Move{}, "", fmt.Errorf("%sは自殺手です", coordinate)
	}
	key := session.positionKey()
	if session.seenPositions[key] {
		session.grid = before
		return Move{}, "", fmt.Errorf("%sは以前と同じ盤面を繰り返します", coordinate)
	}

	captured := session.coordinates(capturedPoints)
	move := Move{Color: session.currentPlayer, Coordinate: coordinate, Captured: captured}
	if err := session.appendTreeMove(move); err != nil {
		session.grid = before
		return Move{}, "", err
	}
	moveNodeID := session.currentNodeID
	session.moves = append(session.moves, move)
	session.movePositionKeys = append(session.movePositionKeys, key)
	session.captures[session.currentPlayer] += len(captured)
	session.consecutivePasses = 0
	session.seenPositions[key] = true
	session.togglePlayer()
	return move, moveNodeID, nil
}

func (session *Session) appendTreeMove(move Move) error {
	if session.replayOnly {
		return nil
	}
	for _, id := range session.childNodeIDs[session.currentNodeID] {
		node := session.treeNodes[id]
		if node.Move != nil && node.Move.Color == move.Color && node.Move.Coordinate == move.Coordinate {
			session.currentNodeID = node.ID
			return nil
		}
	}
	id, err := session.allocateNodeID()
	if err != nil {
		return err
	}
	moveCopy := move
	moveCopy.Captured = append([]string(nil), move.Captured...)
	session.treeNodes[id] = TreeNode{ID: id, ParentID: session.currentNodeID, Move: &moveCopy}
	session.childNodeIDs[session.currentNodeID] = append(session.childNodeIDs[session.currentNodeID], id)
	session.currentNodeID = id
	return nil
}

func (session *Session) allocateNodeID() (string, error) {
	if len(session.treeNodes) >= MaximumTreeNodes {
		return "", newCategorizedError(errorCategoryTreeLimit, fmt.Sprintf("棋譜の局面数は%d以下にしてください", MaximumTreeNodes))
	}
	if session.nextNodeNumber < 1 || session.nextNodeNumber > math.MaxInt-MaximumTreeNodes-1 {
		return "", fmt.Errorf("棋譜の次の局面番号が大きすぎます")
	}
	id := "n" + strconv.Itoa(session.nextNodeNumber)
	session.nextNodeNumber++
	return id, nil
}

// Undo は元の後続局面を削除せず、結果または現在の局面を取り消す。
// 通常は親局面へ戻り、二連続パスの結果では2手目のパスも待ったの対象にする。
func (session *Session) Undo() (err error) {
	if session.currentNodeID == "root" {
		return fmt.Errorf("待ったで戻せる着手がありません")
	}
	undoFromID := session.currentNodeID
	node, ok := session.treeNodes[session.currentNodeID]
	if !ok || node.ParentID == "" {
		return fmt.Errorf("現在の棋譜の局面が不正です")
	}
	backup := session.Clone()
	defer session.restoreWhenFailed(backup, &err)
	if node.Result != nil {
		// 記録された結果は結果だけを取り消す。二連続パスは終局を生じさせた
		// 2手目のパスも待ったの対象にする。
		if node.Result.Cause == "two_passes" && session.isTwoPassResultNode(node.ID) {
			if completionID, found := session.twoPassCompletionNodeID(node.ParentID); found {
				node = session.treeNodes[completionID]
			}
		}
	}
	session.currentNodeID = node.ParentID
	if session.mainLineNodeID == undoFromID {
		session.mainLineNodeID = session.currentNodeID
		session.markCanceledAutomaticResultAtMainLineEnd(session.mainLineNodeID)
	}
	return session.rebuildCurrentLine()
}

// Navigate は既存の棋譜の局面を選択し、その局面を再構築する。
func (session *Session) Navigate(nodeID string) error {
	if _, ok := session.treeNodes[nodeID]; !ok {
		return newCategorizedError(errorCategoryNodeNotFound, gameTreePositionNotFoundMessage)
	}
	previous := session.currentNodeID
	session.currentNodeID = nodeID
	if err := session.rebuildCurrentLine(); err != nil {
		session.currentNodeID = previous
		return err
	}
	return nil
}

// SetMainLineNode は"現在の局面"を変えずに主分岐の末端を設定する。
func (session *Session) SetMainLineNode(nodeID string) error {
	_, ok := session.treeNodes[nodeID]
	if !ok {
		return newCategorizedError(errorCategoryNodeNotFound, gameTreePositionNotFoundMessage)
	}
	session.mainLineNodeID = nodeID
	return nil
}

// CommitCurrentNodeToMainLine は現在の局面を主分岐の末端として確定し、AI agentの印を取り除く。
func (session *Session) CommitCurrentNodeToMainLine() (err error) {
	backup := session.Clone()
	defer session.restoreWhenFailed(backup, &err)
	session.mainLineNodeID = session.currentNodeID
	session.markCanceledAutomaticResultAtMainLineEnd(session.currentNodeID)
	session.clearCanceledMarkerForResult(session.currentNodeID)
	session.clearPromotedLineMetadata(session.currentNodeID)
	return session.rebuildCurrentLine()
}

func (session *Session) markCanceledAutomaticResultAtMainLineEnd(nodeID string) {
	if session.treeNodes[nodeID].Result != nil {
		return
	}
	completionID, found := session.twoPassCompletionNodeID(nodeID)
	if !found {
		return
	}
	completion := session.treeNodes[completionID]
	completion.AutomaticResultCanceled = true
	session.treeNodes[completion.ID] = completion
	if session.result != nil {
		session.ContinueAfterResult()
	}
}

// PrepareSGFTree はSGFに結果を記録できない変化の二連続パスを続行可能にし、
// 主分岐の結果を適用するまで主分岐の末端の自動結果も保留する。
func (session *Session) PrepareSGFTree(mainEndID string) {
	for id, node := range session.treeNodes {
		if node.Result != nil || len(session.childNodeIDs[id]) != 0 {
			continue
		}
		session.markCanceledAutomaticResultAtMainLineEnd(id)
	}
	session.markCanceledAutomaticResultAtMainLineEnd(mainEndID)
}

func (session *Session) clearCanceledMarkerForResult(nodeID string) {
	node := session.treeNodes[nodeID]
	if node.Result == nil || node.Result.Cause != "two_passes" || !session.isTwoPassResultNode(nodeID) {
		return
	}
	if completionID, ok := session.twoPassCompletionNodeID(node.ParentID); ok {
		completion := session.treeNodes[completionID]
		if completion.AutomaticResultCanceled {
			completion.AutomaticResultCanceled = false
			session.treeNodes[completion.ID] = completion
		}
	}
}

// twoPassCompletionNodeID は、着手のない局面を飛ばして二連続パスの2手目を返す。
func (session *Session) twoPassCompletionNodeID(nodeID string) (string, bool) {
	nearestMoves, consecutivePasses := indexTwoPassCompletions(session.treeNodes)
	return twoPassCompletionNodeIDInTree(session.treeNodes, nearestMoves, consecutivePasses, nodeID)
}

// isTwoPassResultNode は、二連続パスの2手目から着手のない局面だけを介して置かれた結果かを返す。
func (session *Session) isTwoPassResultNode(nodeID string) bool {
	node, ok := session.treeNodes[nodeID]
	if !ok || node.Result == nil {
		return false
	}
	_, found := session.twoPassCompletionNodeID(node.ParentID)
	return found
}

func (session *Session) nearestMoveNodeID(nodeID string) string {
	for nodeID != "" {
		node, ok := session.treeNodes[nodeID]
		if !ok {
			return ""
		}
		if node.Move != nil {
			return nodeID
		}
		nodeID = node.ParentID
	}
	return ""
}

// DeleteVariation は局面とその後続をすべて削除する。
// 主分岐の局面を削除した場合は親から続く並び順で先頭の分岐を主分岐へ昇格し、分岐がなければ親を末端とする。
// "現在の局面"が削除対象の部分木内にある場合は、削除した局面の親へ戻る。
func (session *Session) DeleteVariation(nodeID string) (deletedNodeIDs []string, err error) {
	if nodeID == "root" {
		return nil, newCategorizedError(errorCategoryInvalidTreeMutationInput, "開始局面は削除できません")
	}
	node, ok := session.treeNodes[nodeID]
	if !ok {
		return nil, newCategorizedError(errorCategoryNodeNotFound, gameTreePositionNotFoundMessage)
	}
	backup := session.Clone()
	defer session.restoreWhenFailed(backup, &err)
	wasMainLine := session.nodeIsOnMainLine(nodeID)

	deleted := make(map[string]bool)
	stack := []string{nodeID}
	for len(stack) > 0 {
		last := len(stack) - 1
		id := stack[last]
		stack = stack[:last]
		if deleted[id] {
			continue
		}
		deleted[id] = true
		stack = append(stack, session.childNodeIDs[id]...)
	}
	nearestMoves, consecutivePasses := indexTwoPassCompletions(session.treeNodes)
	deletedResultCompletions := make(map[string]bool)
	for id := range deleted {
		if node := session.treeNodes[id]; node.Result != nil {
			deletedResultCompletions[nearestMoves[node.ParentID]] = true
		}
	}
	affectedCompletions := make(map[string]bool)
	for id, node := range session.treeNodes {
		if isTwoPassCompletion(node, consecutivePasses[id]) && deletedResultCompletions[id] {
			affectedCompletions[id] = true
		}
	}
	if completionID, found := session.twoPassCompletionNodeID(node.ParentID); found {
		affectedCompletions[completionID] = true
	}
	ids := make([]string, 0, len(deleted))
	for id := range deleted {
		ids = append(ids, id)
		delete(session.treeNodes, id)
		delete(session.childNodeIDs, id)
	}
	remainingNearestMoves := indexNearestMoveNodeIDs(session.treeNodes)
	resultCompletions := make(map[string]bool)
	for _, remaining := range session.treeNodes {
		if remaining.Result != nil {
			resultCompletions[remainingNearestMoves[remaining.ParentID]] = true
		}
	}
	for completionID := range affectedCompletions {
		if deleted[completionID] {
			continue
		}
		if resultCompletions[completionID] {
			continue
		}
		completion := session.treeNodes[completionID]
		completion.AutomaticResultCanceled = true
		session.treeNodes[completion.ID] = completion
	}
	session.removeChildNodeID(node.ParentID, nodeID)
	sort.Slice(ids, func(i, j int) bool { return nodeIDLess(ids[i], ids[j]) })
	if deleted[session.currentNodeID] {
		session.currentNodeID = node.ParentID
	}
	if wasMainLine {
		children := session.childNodes(node.ParentID)
		sort.Slice(children, func(i, j int) bool { return nodeIDLess(children[i].ID, children[j].ID) })
		if len(children) == 0 {
			session.mainLineNodeID = node.ParentID
		} else if err := session.promoteVariation(children[0].ID); err != nil {
			return nil, err
		}
		session.markCanceledAutomaticResultAtMainLineEnd(session.mainLineNodeID)
	}
	if err := session.rebuildCurrentLine(); err != nil {
		return nil, err
	}
	return ids, nil
}

// PromoteVariation は選択した分岐を主分岐にし、以後は各局面の並び順で先頭の後続を末端までたどる。
func (session *Session) PromoteVariation(nodeID string) (err error) {
	backup := session.Clone()
	defer session.restoreWhenFailed(backup, &err)
	return session.promoteVariation(nodeID)
}

func (session *Session) promoteVariation(nodeID string) error {
	if nodeID == "root" {
		return newCategorizedError(errorCategoryInvalidTreeMutation, "開始局面はすでに主分岐にあります")
	}
	if _, ok := session.treeNodes[nodeID]; !ok {
		return newCategorizedError(errorCategoryNodeNotFound, gameTreePositionNotFoundMessage)
	}
	if session.nodeIsOnMainLine(nodeID) {
		return newCategorizedError(errorCategoryInvalidTreeMutation, "選択した局面はすでに主分岐にあります")
	}
	current := nodeID
	for {
		children := session.childNodes(current)
		if len(children) == 0 {
			break
		}
		sort.Slice(children, func(i, j int) bool { return nodeIDLess(children[i].ID, children[j].ID) })
		current = children[0].ID
	}
	session.mainLineNodeID = current
	session.markCanceledAutomaticResultAtMainLineEnd(current)
	session.clearCanceledMarkerForResult(current)
	session.clearPromotedLineMetadata(current)
	if err := session.rebuildCurrentLine(); err != nil {
		return err
	}
	if err := ValidateGameTreeHistory(session.Snapshot()); err != nil {
		return newCategorizedError(errorCategoryInvalidTreeMutation, err.Error())
	}
	return nil
}

func (session *Session) nodeIsOnMainLine(nodeID string) bool {
	for current := session.mainLineNodeID; current != ""; current = session.treeNodes[current].ParentID {
		if current == nodeID {
			return true
		}
	}
	return false
}

func (session *Session) childNodes(parentID string) []TreeNode {
	result := make([]TreeNode, 0, len(session.childNodeIDs[parentID]))
	for _, id := range session.childNodeIDs[parentID] {
		if node, ok := session.treeNodes[id]; ok {
			result = append(result, node)
		}
	}
	return result
}

func (session *Session) removeChildNodeID(parentID, nodeID string) {
	ids := session.childNodeIDs[parentID]
	for index, id := range ids {
		if id == nodeID {
			session.childNodeIDs[parentID] = append(ids[:index], ids[index+1:]...)
			return
		}
	}
}

func (session *Session) clearPromotedLineMetadata(nodeID string) {
	for currentID := nodeID; currentID != "root"; {
		node := session.treeNodes[currentID]
		node.Origin = ""
		node.VariationTitle = ""
		node.Highlighted = false
		session.treeNodes[currentID] = node
		currentID = node.ParentID
	}
}

func nodeIDNumber(id string) int {
	value, err := strconv.Atoi(strings.TrimPrefix(id, "n"))
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return value
}

func nodeIDLess(first, second string) bool {
	if first == "root" || second == "root" {
		return first == "root" && second != "root"
	}
	firstNumber, secondNumber := nodeIDNumber(first), nodeIDNumber(second)
	if firstNumber != secondNumber {
		return firstNumber < secondNumber
	}
	return first < second
}

func sortedTreeNodeIDs(nodes map[string]TreeNode) []string {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(first, second int) bool { return nodeIDLess(ids[first], ids[second]) })
	return ids
}

func (session *Session) SetSGFProperties(nodeID string, properties []SGFProperty) error {
	if err := validateSGFProperties(properties, nodeID == "root", session.size); err != nil {
		return err
	}
	if nodeID == "root" {
		session.rootProperties = cloneProperties(properties)
		return nil
	}
	node, ok := session.treeNodes[nodeID]
	if !ok {
		return newCategorizedError(errorCategoryNodeNotFound, gameTreePositionNotFoundMessage)
	}
	node.Properties = cloneProperties(properties)
	session.treeNodes[nodeID] = node
	return nil
}

func (session *Session) AppendSGFNode(properties []SGFProperty) (string, error) {
	if session.status == "finished" && session.treeNodes[session.currentNodeID].Result == nil && (session.result == nil || session.result.Cause != "two_passes") {
		return "", errGameFinished
	}
	if current := session.treeNodes[session.currentNodeID]; current.Result != nil {
		merged := append(cloneProperties(current.Properties), cloneProperties(properties)...)
		if err := validateSGFProperties(merged, false, session.size); err != nil {
			return "", err
		}
		current.Properties = merged
		session.treeNodes[current.ID] = current
		return current.ID, nil
	}
	if err := validateSGFProperties(properties, false, session.size); err != nil {
		return "", err
	}
	id, err := session.allocateNodeID()
	if err != nil {
		return "", err
	}
	session.treeNodes[id] = TreeNode{
		ID: id, ParentID: session.currentNodeID, Properties: cloneProperties(properties),
	}
	session.childNodeIDs[session.currentNodeID] = append(session.childNodeIDs[session.currentNodeID], id)
	session.currentNodeID = id
	return id, nil
}

// ContinueAfterResult は、結果が記録された局面から続く別分岐を開始する。
func (session *Session) ContinueAfterResult() {
	if session.status != "finished" || session.result == nil {
		return
	}
	if session.replayOnly {
		if session.result.Cause == "two_passes" {
			session.consecutivePasses = 0
		}
		session.result = nil
		session.status = "playing"
		return
	}
	node := session.treeNodes[session.currentNodeID]
	resetPasses := session.isTwoPassResultNode(node.ID)
	if node.Result == nil {
		_, resetPasses = session.twoPassCompletionNodeID(node.ID)
		if !resetPasses {
			return
		}
	}
	if node.Result != nil {
		session.currentNodeID = node.ParentID
	}
	session.result = nil
	session.status = "playing"
	if resetPasses {
		session.consecutivePasses = 0
	}
}

// SetSGFRecordedResult は、SGFの木を組み立てている途中の結果を設定する。
// 取り込み完了後の木全体はValidateGameTreeHistoryで検証する。
func (session *Session) SetSGFRecordedResult(result *Result) error {
	return session.setRecordedResult(result)
}

func (session *Session) setRecordedResult(result *Result) (err error) {
	if result == nil {
		return nil
	}
	clonedResult := *result
	if clonedResult.Type == "points" && clonedResult.Cause == "two_passes" && !clonedResult.ScoreKnown {
		areaScore := session.areaScoreWithDeadStones(clonedResult.DeadStones)
		if !SameResultOutcome(clonedResult, *areaScore) {
			return errors.New(twoPassResultMismatchMessage)
		}
		clonedResult = *areaScore
	}
	if err := validateResult(clonedResult); err != nil {
		return err
	}
	if err := validateResultForBoard(clonedResult, session.size); err != nil {
		return err
	}
	backup := session.Clone()
	defer session.restoreWhenFailed(backup, &err)
	current := session.treeNodes[session.currentNodeID]
	if clonedResult.Cause == "two_passes" {
		if err := session.validateDeadStonesOnBoard(clonedResult.DeadStones); err != nil {
			return err
		}
		_, followsTwoPasses := session.twoPassCompletionNodeID(current.ID)
		if current.Result == nil && !current.AutomaticResultCanceled && !followsTwoPasses {
			return fmt.Errorf("二連続パスによる結果は二連続パスの終局局面にだけ記録できます")
		}
		areaScore := session.areaScoreWithDeadStones(clonedResult.DeadStones)
		matchesAreaScore := sameResult(clonedResult, *areaScore)
		if !clonedResult.ScoreKnown {
			matchesAreaScore = SameResultOutcome(clonedResult, *areaScore)
		}
		if !matchesAreaScore {
			return errors.New(twoPassResultMismatchMessage)
		}
		if current.Result != nil && current.Result.Type == "points" && sameResult(*current.Result, *areaScore) {
			session.replaceCurrentResult(current, clonedResult)
			return nil
		}
		if current.Result == nil {
			for _, childID := range session.childNodeIDs[current.ID] {
				child := session.treeNodes[childID]
				if child.Result != nil && child.Result.Cause == "two_passes" && SameResultOutcome(*child.Result, clonedResult) {
					if session.mainLineNodeID == current.ID {
						session.mainLineNodeID = childID
					}
					session.currentNodeID = childID
					session.replaceCurrentResult(child, clonedResult)
					return session.rebuildCurrentLine()
				}
			}
		}
		// Playが作った一時的な自動結果を、SGFで明示された付随情報の手順へ移す。
		// 同じ着手列の別の手順を再利用すると、その手順にあるメモが失われるため、
		// 結果の局面だけを現在の手順へ付け替える。
		if current.Result == nil {
			if reused, reuseErr := session.reuseBareAutomaticResult(current.ID, clonedResult); reused || reuseErr != nil {
				return reuseErr
			}
		}
	}
	currentResultIsCanceled := false
	if current.Result != nil && current.Result.Cause == "two_passes" {
		if completionID, ok := session.twoPassCompletionNodeID(current.ParentID); ok {
			currentResultIsCanceled = session.treeNodes[completionID].AutomaticResultCanceled
		}
	}
	if clonedResult.Cause != "two_passes" {
		anchorID := current.ID
		if current.Result != nil {
			anchorID = current.ParentID
		}
		for _, childID := range session.childNodeIDs[anchorID] {
			child := session.treeNodes[childID]
			if child.Result == nil || child.Result.Cause == "two_passes" {
				continue
			}
			if sameResult(*child.Result, clonedResult) {
				if session.mainLineNodeID == current.ID || session.mainLineNodeID == anchorID {
					session.mainLineNodeID = child.ID
				}
				session.currentNodeID = child.ID
				return session.rebuildCurrentLine()
			}
		}
	}
	if clonedResult.Cause != "two_passes" && current.Result != nil && current.Result.Cause == "two_passes" && !currentResultIsCanceled {
		session.replaceCurrentResult(current, clonedResult)
		return nil
	}
	if clonedResult.Cause != "two_passes" && current.Result == nil {
		if reused, reuseErr := session.reuseBareAutomaticResult(current.ID, clonedResult); reused || reuseErr != nil {
			return reuseErr
		}
	}
	for currentID := session.currentNodeID; currentID != ""; currentID = session.treeNodes[currentID].ParentID {
		if existing := session.treeNodes[currentID].Result; existing != nil {
			if sameResult(*existing, clonedResult) {
				return nil
			}
			if currentID == session.currentNodeID {
				session.replaceCurrentResult(session.treeNodes[currentID], clonedResult)
				return nil
			}
			return fmt.Errorf("結果の後に別の結果を記録することはできません")
		}
	}
	resultBelongsToMainLine := session.currentNodeID == session.mainLineNodeID
	if err := session.appendResultNode(clonedResult); err != nil {
		return err
	}
	if resultBelongsToMainLine {
		session.mainLineNodeID = session.currentNodeID
	}
	if err := session.rebuildCurrentLine(); err != nil {
		return err
	}
	if resultBelongsToMainLine {
		session.clearCanceledMarkerForResult(session.currentNodeID)
	}
	return nil
}

// reuseBareAutomaticResult は、同じ着手列に自動生成された結果局面を現在の手順へ移して再利用する。
func (session *Session) reuseBareAutomaticResult(currentID string, result Result) (bool, error) {
	anchorID := session.nearestMoveNodeID(currentID)
	if anchorID == "" {
		anchorID = "root"
	}
	automaticID := session.findBareAutomaticResult(anchorID)
	if automaticID == "" {
		return false, nil
	}
	automatic := session.treeNodes[automaticID]
	currentWasMainLine := session.mainLineNodeID == currentID
	if automatic.ParentID != currentID {
		wasMainLine := session.mainLineNodeID == automaticID
		session.removeChildNodeID(automatic.ParentID, automaticID)
		automatic.ParentID = currentID
		session.treeNodes[automaticID] = automatic
		session.childNodeIDs[currentID] = append(session.childNodeIDs[currentID], automaticID)
		if wasMainLine {
			session.mainLineNodeID = currentID
		}
	}
	session.currentNodeID = automaticID
	if currentWasMainLine {
		session.mainLineNodeID = automaticID
	}
	session.replaceCurrentResult(automatic, result)
	return true, session.rebuildCurrentLine()
}

func (session *Session) restoreWhenFailed(backup *Session, err *error) {
	if *err != nil {
		*session = *backup
	}
}

func (session *Session) replaceCurrentResult(current TreeNode, result Result) {
	result.DeadStones = normalizedDeadStoneList(result.DeadStones)
	current.Result = &result
	session.treeNodes[current.ID] = current
	session.result = &result
	session.status = "finished"
}

func sameResult(first, second Result) bool {
	return first.Type == second.Type && first.Cause == second.Cause && first.Value == second.Value && first.Winner == second.Winner && first.Loser == second.Loser &&
		first.Margin == second.Margin && first.BlackScore == second.BlackScore && first.WhiteScore == second.WhiteScore && first.ScoreKnown == second.ScoreKnown && slices.Equal(first.DeadStones, second.DeadStones)
}

func normalizedDeadStoneList(deadStones []string) []string {
	result := slices.Clone(deadStones)
	if result == nil {
		result = []string{}
	}
	sort.Strings(result)
	return result
}

func equalOptionalResult(first, second *Result) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return sameResult(*first, *second)
}

func (session *Session) UpdateNodeReview(nodeID, note string, important bool) error {
	if nodeID == "root" {
		session.rootProperties = setProperty(session.rootProperties, "C", note)
		node := session.treeNodes["root"]
		node.Important = important
		session.treeNodes["root"] = node
		return nil
	}
	node, ok := session.treeNodes[nodeID]
	if !ok {
		return newCategorizedError(errorCategoryNodeNotFound, gameTreePositionNotFoundMessage)
	}
	node.Properties = setProperty(node.Properties, "C", note)
	node.Important = important
	session.treeNodes[nodeID] = node
	return nil
}

func (session *Session) SetCurrentNodeAIDecision(decision AIDecision) error {
	nodeID := session.currentNodeID
	node, ok := session.treeNodes[nodeID]
	for ok && node.Move == nil && node.ParentID != "" {
		nodeID = node.ParentID
		node, ok = session.treeNodes[nodeID]
	}
	if !ok || node.Move == nil {
		return fmt.Errorf("現在の局面に着手がありません")
	}
	return session.setNodeAIDecision(nodeID, decision)
}

func (session *Session) setNodeAIDecision(nodeID string, decision AIDecision) error {
	node, ok := session.treeNodes[nodeID]
	if !ok || node.Move == nil {
		return fmt.Errorf("棋譜の局面に着手がありません")
	}
	clonedDecision := decision
	clonedDecision.Candidates = slices.Clone(decision.Candidates)
	node.AIDecision = &clonedDecision
	if err := session.validateAIDecision(node); err != nil {
		return err
	}
	session.treeNodes[nodeID] = node
	return nil
}

// SetGameName は棋譜の開始局面にある対局名を更新する。
func (session *Session) SetGameName(name string) {
	session.rootProperties = setProperty(session.rootProperties, "GN", name)
}

func setProperty(properties []SGFProperty, ident, value string) []SGFProperty {
	result := make([]SGFProperty, 0, len(properties)+1)
	replaced := false
	for _, property := range properties {
		if property.Ident != ident {
			result = append(result, property)
			continue
		}
		if value != "" && !replaced {
			result = append(result, SGFProperty{Ident: ident, Values: []string{value}})
			replaced = true
		}
	}
	if value != "" && !replaced {
		result = append(result, SGFProperty{Ident: ident, Values: []string{value}})
	}
	return result
}

func (session *Session) rebuildCurrentLine() error {
	moves, err := treePath(session.treeNodes, session.currentNodeID)
	if err != nil {
		return err
	}
	rebuilt, err := NewSessionWithSetup(session.id, session.size, session.komi, session.handicap, session.setupStones, session.initialPlayer)
	if err != nil {
		return err
	}
	if err := rebuilt.replayMoves(moves); err != nil {
		return err
	}
	currentNode := session.treeNodes[session.currentNodeID]
	if rebuilt.result != nil && currentNode.Result == nil {
		if result := session.resultFollowingNode(session.currentNodeID); result != nil {
			cloned := *result
			cloned.DeadStones = slices.Clone(result.DeadStones)
			rebuilt.result = &cloned
			rebuilt.status = "finished"
		} else {
			rebuilt.ContinueAfterResult()
		}
	}
	var currentResult *Result
	if result := currentNode.Result; result != nil {
		if err := validateResult(*result); err != nil {
			return fmt.Errorf("棋譜の結果が不正です：%w", err)
		}
		clonedResult := *result
		currentResult = &clonedResult
	}
	session.grid = rebuilt.grid
	session.currentPlayer = rebuilt.currentPlayer
	session.status = rebuilt.status
	session.consecutivePasses = rebuilt.consecutivePasses
	session.moves = rebuilt.moves
	session.movePositionKeys = rebuilt.movePositionKeys
	session.captures = rebuilt.captures
	session.seenPositions = rebuilt.seenPositions
	session.result = rebuilt.result
	if currentResult != nil {
		session.result = currentResult
		session.status = "finished"
	}
	if session.status != "finished" {
		session.result = nil
	}
	return nil
}

// resultFollowingNode は、指定した局面から着手を挟まずに続く結果を返す。
// 複数の結果があるときは、現在の主分岐にある結果を優先する。
func (session *Session) resultFollowingNode(nodeID string) *Result {
	nearestMoves, consecutivePasses := indexTwoPassCompletions(session.treeNodes)
	searchRoot := nodeID
	if completionID, found := twoPassCompletionNodeIDInTree(session.treeNodes, nearestMoves, consecutivePasses, nodeID); found {
		if session.treeNodes[completionID].AutomaticResultCanceled {
			return nil
		}
		searchRoot = completionID
	}
	mainLinePath := make(map[string]bool)
	for currentID := session.mainLineNodeID; currentID != ""; currentID = session.treeNodes[currentID].ParentID {
		mainLinePath[currentID] = true
	}
	var automatic, fallback *Result
	var automaticID, fallbackID string
	pending := []string{searchRoot}
	seen := make(map[string]bool)
	for len(pending) > 0 {
		currentID := pending[0]
		pending = pending[1:]
		if seen[currentID] {
			continue
		}
		seen[currentID] = true
		current, ok := session.treeNodes[currentID]
		if !ok {
			continue
		}
		if current.Result != nil && !isCanceledTwoPassResultInTree(session.treeNodes, nearestMoves, consecutivePasses, current) {
			if mainLinePath[currentID] {
				return current.Result
			}
			if current.Result.Cause == "two_passes" && (automatic == nil || nodeIDLess(currentID, automaticID)) {
				automatic = current.Result
				automaticID = currentID
			}
			if fallback == nil || nodeIDLess(currentID, fallbackID) {
				fallback = current.Result
				fallbackID = currentID
			}
			continue
		}
		for _, childID := range session.childNodeIDs[currentID] {
			if child := session.treeNodes[childID]; child.Move == nil {
				pending = append(pending, childID)
			}
		}
	}
	if automatic != nil {
		return automatic
	}
	return fallback
}

// NavigationCheckpoint は棋譜ツリーを編集しながら元の局面へ戻るための状態を保持する。
// 復元しても、チェックポイント後に追加した棋譜ツリーの局面は残る。
type NavigationCheckpoint struct {
	state replayState
}

func (session *Session) CaptureNavigationCheckpoint() NavigationCheckpoint {
	return NavigationCheckpoint{state: session.captureReplayState(len(session.movePositionKeys))}
}

func (session *Session) RestoreNavigationCheckpoint(checkpoint NavigationCheckpoint) {
	for _, key := range session.movePositionKeys[checkpoint.state.addedPositions:] {
		if key != "" {
			delete(session.seenPositions, key)
		}
	}
	session.movePositionKeys = session.movePositionKeys[:checkpoint.state.addedPositions]
	session.grid = append(session.grid[:0], checkpoint.state.grid...)
	session.currentPlayer = checkpoint.state.currentPlayer
	session.status = checkpoint.state.status
	session.consecutivePasses = checkpoint.state.consecutivePasses
	session.moves = session.moves[:checkpoint.state.movesLength]
	session.captures["B"] = checkpoint.state.blackCaptures
	session.captures["W"] = checkpoint.state.whiteCaptures
	session.result = checkpoint.state.result
	session.currentNodeID = checkpoint.state.currentNodeID
}

func (session *Session) replayMoves(moves []Move) error {
	for index, move := range moves {
		session.ContinueAfterResult()
		if move.Color != session.currentPlayer {
			return fmt.Errorf("%d手目の色は%sですが、%sである必要があります", index+1, move.Color, session.currentPlayer)
		}
		replayed, err := session.Play(move.Coordinate)
		if err != nil {
			return fmt.Errorf("%d手目を再現できません：%w", index+1, err)
		}
		if !sameCoordinates(replayed.Captured, move.Captured) {
			return fmt.Errorf("%d手目の取った石が正しくありません", index+1)
		}
	}
	return nil
}

func (session *Session) Resign() error {
	if session.status != "playing" {
		return errGameFinished
	}
	loser := session.currentPlayer
	return session.appendResultNode(Result{Type: "resignation", Cause: "resignation", Winner: OtherColor(loser), Loser: loser})
}

func (session *Session) appendResultNode(result Result) (err error) {
	result.DeadStones = normalizedDeadStoneList(result.DeadStones)
	if session.replayOnly {
		clonedResult := result
		session.status = "finished"
		session.result = &clonedResult
		return nil
	}
	currentResultPathRoot := session.nearestMoveNodeID(session.currentNodeID)
	if currentResultPathRoot == "" {
		currentResultPathRoot = "root"
	}
	reusableID := session.findReusableResult(currentResultPathRoot, result)
	if reusableID != "" {
		reusable := session.treeNodes[reusableID]
		if result.Cause == "resignation" && reusable.Result.Type == "resignation" && reusable.Result.Cause == "recorded" {
			clonedResult := result
			reusable.Result = &clonedResult
			session.treeNodes[reusableID] = reusable
		}
		session.currentNodeID = reusableID
		session.status = "finished"
		clonedResult := *reusable.Result
		session.result = &clonedResult
		if session.isTwoPassResultNode(reusableID) {
			session.consecutivePasses = 2
		}
		return nil
	}
	id, err := session.allocateNodeID()
	if err != nil {
		return err
	}
	clonedResult := result
	session.treeNodes[id] = TreeNode{ID: id, ParentID: session.currentNodeID, Result: &clonedResult}
	session.childNodeIDs[session.currentNodeID] = append(session.childNodeIDs[session.currentNodeID], id)
	session.currentNodeID = id
	session.status = "finished"
	session.result = &clonedResult
	if session.isTwoPassResultNode(id) {
		session.consecutivePasses = 2
	}
	return nil
}

// findReusableResult は、現在の局面の直後または分岐のない付随情報の先にある同じ勝敗結果を返す。
func (session *Session) findReusableResult(nodeID string, result Result) string {
	passedMetadata := false
	for {
		children := session.childNodeIDs[nodeID]
		for _, childID := range children {
			child := session.treeNodes[childID]
			if child.Result != nil && SameResultOutcome(*child.Result, result) && (!passedMetadata || len(children) == 1 || isBareAutomaticResult(child)) {
				return childID
			}
		}
		if len(children) != 1 {
			return ""
		}
		child := session.treeNodes[children[0]]
		if child.Move != nil || child.Result != nil {
			return ""
		}
		nodeID = child.ID
		passedMetadata = true
	}
}

func (session *Session) findBareAutomaticResult(nodeID string) string {
	stack := []string{nodeID}
	found := ""
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, childID := range session.childNodeIDs[id] {
			child := session.treeNodes[childID]
			if child.Move != nil {
				continue
			}
			if child.Result != nil {
				if !isBareAutomaticResult(child) {
					continue
				}
				if found != "" {
					return ""
				}
				found = childID
				continue
			}
			stack = append(stack, childID)
		}
	}
	return found
}

func isBareAutomaticResult(node TreeNode) bool {
	return node.Result != nil && node.Result.Cause == "two_passes" && len(node.Properties) == 0 && !node.Important && node.Origin == ""
}

func (session *Session) validateDeadStonesOnBoard(deadStones []string) error {
	seen := make(map[string]bool, len(deadStones))
	for _, coordinate := range deadStones {
		point, ok := session.point(coordinate)
		if !ok || session.grid[point] == "" {
			return fmt.Errorf("死石の座標%qに石がありません", coordinate)
		}
		if seen[coordinate] {
			return fmt.Errorf("死石の座標%qが重複しています", coordinate)
		}
		seen[coordinate] = true
	}
	return nil
}

func (session *Session) areaScoreWithDeadStones(deadStones []string) *Result {
	grid := append([]string(nil), session.grid...)
	normalizedDead := make([]string, 0, len(deadStones))
	seen := make(map[string]bool, len(deadStones))
	for _, coordinate := range deadStones {
		coordinate = normalizeCoordinate(coordinate)
		point, ok := session.point(coordinate)
		if !ok || grid[point] == "" || seen[coordinate] {
			continue
		}
		seen[coordinate] = true
		normalizedDead = append(normalizedDead, coordinate)
		grid[point] = ""
	}
	sort.Strings(normalizedDead)
	blackScore, whiteScore := 0.0, session.komi
	if session.komi < 0 {
		blackScore = -session.komi
		whiteScore = 0
	}
	territory := territoryPointsOnBoard(session.size, grid)
	for _, color := range grid {
		switch color {
		case "B":
			blackScore++
		case "W":
			whiteScore++
		}
	}
	blackScore += float64(len(territory["B"]))
	whiteScore += float64(len(territory["W"]))
	winner := ""
	difference := new(big.Rat).Sub(decimalResultValue(blackScore), decimalResultValue(whiteScore))
	if difference.Sign() > 0 {
		winner = "B"
	} else if difference.Sign() < 0 {
		winner = "W"
		difference.Neg(difference)
	}
	margin, _ := difference.Float64()
	return &Result{Type: "points", Cause: "two_passes", Winner: winner, Margin: margin, BlackScore: blackScore, WhiteScore: whiteScore, ScoreKnown: true, DeadStones: normalizedDead}
}

// ScorePosition は指定した局面を、指定した死石を除いた中国式の面積で数える。
func ScorePosition(snapshot Snapshot, nodeID string, deadStones []string) (*Result, error) {
	session, err := RestoreSession(snapshot)
	if err != nil {
		return nil, err
	}
	if err := session.Navigate(nodeID); err != nil {
		return nil, err
	}
	return session.areaScoreWithDeadStones(deadStones), nil
}

// DeadStonesFromOwnership は石のまとまりごとのownership平均から死石を返す。
func DeadStonesFromOwnership(snapshot Snapshot, ownership []float64) ([]string, error) {
	area := snapshot.BoardSize * snapshot.BoardSize
	if len(ownership) != area {
		return nil, fmt.Errorf("ownershipの数が盤面と一致しません")
	}
	grid := make([]string, area)
	for coordinate, color := range snapshot.Stones {
		point, ok := coord.Point(coordinate, snapshot.BoardSize)
		if !ok || !IsColor(color) {
			return nil, fmt.Errorf("盤上の石が不正です")
		}
		grid[point] = color
	}
	dead := make([]string, 0)
	for _, group := range boardGroups(snapshot.BoardSize, grid) {
		sum := 0.0
		for _, point := range group.stones {
			sum += ownership[point]
		}
		average := sum / float64(len(group.stones))
		isDead := group.color == "B" && average <= -0.5 || group.color == "W" && average >= 0.5
		if isDead {
			for _, point := range group.stones {
				dead = append(dead, coord.FromPoint(point, snapshot.BoardSize))
			}
		}
	}
	sort.Strings(dead)
	return dead, nil
}

func (session *Session) Snapshot() Snapshot {
	stones := make(map[string]string)
	for point, color := range session.grid {
		if color != "" {
			stones[session.coordinate(point)] = color
		}
	}
	moves := make([]Move, len(session.moves))
	for index, move := range session.moves {
		moves[index] = move
		moves[index].Captured = append([]string(nil), move.Captured...)
	}
	captures := map[string]int{"B": session.captures["B"], "W": session.captures["W"]}
	setupStones := make(map[string]string, len(session.setupStones))
	for coordinate, color := range session.setupStones {
		setupStones[coordinate] = color
	}
	treeNodes := make([]TreeNode, 0, len(session.treeNodes))
	for _, node := range session.treeNodes {
		nodeCopy := node
		nodeCopy.Properties = cloneProperties(node.Properties)
		if node.Move != nil {
			move := *node.Move
			move.Captured = append([]string(nil), node.Move.Captured...)
			nodeCopy.Move = &move
		}
		if node.AIDecision != nil {
			decision := *node.AIDecision
			decision.Candidates = slices.Clone(node.AIDecision.Candidates)
			nodeCopy.AIDecision = &decision
		}
		if node.Result != nil {
			result := *node.Result
			result.DeadStones = slices.Clone(node.Result.DeadStones)
			nodeCopy.Result = &result
		}
		treeNodes = append(treeNodes, nodeCopy)
	}
	sort.Slice(treeNodes, func(i, j int) bool { return nodeIDLess(treeNodes[i].ID, treeNodes[j].ID) })
	var result *Result
	if session.result != nil {
		clonedResult := *session.result
		clonedResult.DeadStones = slices.Clone(session.result.DeadStones)
		result = &clonedResult
	}
	return Snapshot{
		ID: session.id, BoardSize: session.size, Rules: chineseRules, Komi: session.komi,
		Handicap: session.handicap, SetupStones: setupStones,
		InitialPlayer: session.initialPlayer,
		CurrentPlayer: session.currentPlayer, Status: session.status,
		ConsecutivePasses: session.consecutivePasses, Moves: moves, Stones: stones, Captures: captures,
		Result:    result,
		TreeNodes: treeNodes, CurrentNodeID: session.currentNodeID, MainLineNodeID: session.mainLineNodeID, NextNodeNumber: session.nextNodeNumber,
		RootProperties: cloneProperties(session.rootProperties),
		SGFRootStyle:   cloneSGFRootStyle(session.sgfRootStyle),
	}
}

func cloneSGFRootStyle(source *SGFRootStyle) *SGFRootStyle {
	if source == nil {
		return nil
	}
	style := *source
	return &style
}

// SetSGFRootStyle は読み込んだSGFで明示されていた開始局面の項目を記録する。
func (session *Session) SetSGFRootStyle(style SGFRootStyle) {
	session.sgfRootStyle = cloneSGFRootStyle(&style)
}

func cloneProperties(properties []SGFProperty) []SGFProperty {
	result := make([]SGFProperty, len(properties))
	for index, property := range properties {
		result[index] = SGFProperty{Ident: property.Ident, Values: append([]string(nil), property.Values...)}
	}
	return result
}

func cloneChildNodeIDs(source map[string][]string) map[string][]string {
	result := make(map[string][]string, len(source))
	for parentID, ids := range source {
		result[parentID] = append([]string(nil), ids...)
	}
	return result
}

func indexChildNodeIDs(nodes map[string]TreeNode) map[string][]string {
	result := make(map[string][]string)
	for id, node := range nodes {
		if id != "root" {
			result[node.ParentID] = append(result[node.ParentID], id)
		}
	}
	return result
}

func (session *Session) restoreTree(nodes []TreeNode, currentID string, storedNext int) {
	tree := make(map[string]TreeNode, len(nodes))
	for _, node := range nodes {
		tree[node.ID] = node
	}
	session.treeNodes = tree
	session.childNodeIDs = indexChildNodeIDs(tree)
	session.currentNodeID = currentID
	session.nextNodeNumber = storedNext
}

func (session *Session) validateTreeHistory(tree map[string]TreeNode) error {
	replayed, err := NewSessionWithSetup(session.id, session.size, session.komi, session.handicap, session.setupStones, session.initialPlayer)
	if err != nil {
		return err
	}
	replayed.replayOnly = true
	children := indexChildNodeIDs(tree)
	addedPositions := make([]string, 0)
	var validateChildren func(string) error
	validateChildren = func(parentID string) error {
		for _, id := range children[parentID] {
			node := tree[id]
			before := replayed.captureReplayState(len(addedPositions))
			if node.Move != nil {
				replayed.ContinueAfterResult()
				if node.Move.Color != replayed.currentPlayer {
					return fmt.Errorf("棋譜の着手色は%sですが、%sである必要があります", node.Move.Color, replayed.currentPlayer)
				}
				move, playErr := replayed.Play(node.Move.Coordinate)
				if playErr != nil {
					return fmt.Errorf("棋譜の着手を適用できません：%w", playErr)
				}
				if move.Coordinate != "pass" {
					addedPositions = append(addedPositions, replayed.positionKey())
				}
				if !sameCoordinates(move.Captured, node.Move.Captured) {
					return fmt.Errorf("棋譜の取った石が正しくありません")
				}
			}
			if node.Result != nil {
				if node.Result.Type == "resignation" && node.Result.Loser != replayed.currentPlayer {
					return fmt.Errorf("投了した%sに手番がありません", node.Result.Loser)
				}
				if node.Result.Cause == "two_passes" {
					if err := replayed.validateDeadStonesOnBoard(node.Result.DeadStones); err != nil {
						return fmt.Errorf("棋譜の死石が不正です：%w", err)
					}
					expected := replayed.areaScoreWithDeadStones(node.Result.DeadStones)
					matches := sameResult(*node.Result, *expected)
					if !node.Result.ScoreKnown {
						matches = SameResultOutcome(*node.Result, *expected)
					}
					if !matches {
						return fmt.Errorf("棋譜の%s", twoPassResultMismatchMessage)
					}
				}
			}
			if err := validateChildren(id); err != nil {
				return err
			}
			replayed.restoreReplayState(before, &addedPositions)
		}
		return nil
	}
	return validateChildren("root")
}

type replayState struct {
	grid              []string
	currentPlayer     string
	status            string
	consecutivePasses int
	movesLength       int
	blackCaptures     int
	whiteCaptures     int
	result            *Result
	currentNodeID     string
	addedPositions    int
}

func (session *Session) captureReplayState(addedPositions int) replayState {
	return replayState{
		grid: append([]string(nil), session.grid...), currentPlayer: session.currentPlayer, status: session.status,
		consecutivePasses: session.consecutivePasses, movesLength: len(session.moves),
		blackCaptures: session.captures["B"], whiteCaptures: session.captures["W"], result: session.result,
		currentNodeID: session.currentNodeID, addedPositions: addedPositions,
	}
}

func (session *Session) restoreReplayState(state replayState, addedPositions *[]string) {
	for _, key := range (*addedPositions)[state.addedPositions:] {
		delete(session.seenPositions, key)
	}
	*addedPositions = (*addedPositions)[:state.addedPositions]
	session.grid = state.grid
	session.currentPlayer = state.currentPlayer
	session.status = state.status
	session.consecutivePasses = state.consecutivePasses
	session.moves = session.moves[:state.movesLength]
	session.movePositionKeys = session.movePositionKeys[:state.movesLength]
	session.captures["B"], session.captures["W"] = state.blackCaptures, state.whiteCaptures
	session.result = state.result
	session.currentNodeID = state.currentNodeID
}

func (session *Session) validateAIDecision(node TreeNode) error {
	decision := node.AIDecision
	if node.Move == nil {
		return fmt.Errorf("AI判断は着手のある局面に記録してください")
	}
	if !ailevel.Valid(decision.Level) {
		return fmt.Errorf("AI判断のレベル%dが不正です", decision.Level)
	}
	if decision.Visits < 1 {
		return fmt.Errorf("AI判断のvisitsは正の値にしてください")
	}
	seenCandidates := make(map[string]bool, len(decision.Candidates))
	selectedFound := false
	for _, candidate := range decision.Candidates {
		coordinate := normalizeCoordinate(candidate.Coordinate)
		if coordinate != candidate.Coordinate || coordinate == "pass" {
			return fmt.Errorf("AI判断の候補手%qが不正です", candidate.Coordinate)
		}
		if _, ok := session.point(coordinate); !ok {
			return fmt.Errorf("AI判断の候補手%qが盤外です", coordinate)
		}
		if seenCandidates[coordinate] {
			return fmt.Errorf("AI判断の候補手%qが重複しています", coordinate)
		}
		if candidate.Weight <= 0 || math.IsNaN(candidate.Weight) || math.IsInf(candidate.Weight, 0) {
			return fmt.Errorf("AI判断の候補手%qの重みが不正です", coordinate)
		}
		seenCandidates[coordinate] = true
		selectedFound = selectedFound || coordinate == normalizeCoordinate(decision.SelectedMove)
	}
	for _, candidate := range []struct{ label, coordinate string }{
		{label: "エンジンの手", coordinate: decision.EngineMove},
		{label: "選択した手", coordinate: decision.SelectedMove},
	} {
		label, coordinate := candidate.label, candidate.coordinate
		normalized := normalizeCoordinate(coordinate)
		if coordinate != normalized {
			return fmt.Errorf("AI判断の%sの座標%qは正規の表記ではありません", label, coordinate)
		}
		if normalized != "pass" {
			if _, ok := session.point(normalized); !ok {
				return fmt.Errorf("AI判断の%s%qが不正です", label, coordinate)
			}
		}
	}
	if normalizeCoordinate(decision.SelectedMove) != normalizeCoordinate(node.Move.Coordinate) {
		return fmt.Errorf("AI判断で選んだ手が棋譜の着手と一致しません")
	}
	if len(decision.Candidates) == 0 && decision.SelectedMove != "pass" {
		return fmt.Errorf("AI判断には候補手を1つ以上記録してください")
	}
	if decision.SelectedMove != "pass" && !selectedFound {
		return fmt.Errorf("AI判断で選んだ手が候補手にありません")
	}
	return nil
}

// ValidateSGFPropertyList はプロパティ一覧の識別子、値、配置を検査する。
func ValidateSGFPropertyList(properties []SGFProperty, root bool) error {
	seen := make(map[string]bool, len(properties))
	for _, property := range properties {
		if property.Ident == "" {
			return &SGFPropertyError{Kind: SGFPropertyValueError, cause: fmt.Errorf("プロパティの識別子を空にはできません")}
		}
		for _, character := range property.Ident {
			if character < 'A' || character > 'Z' {
				return &SGFPropertyError{Identifier: property.Ident, Kind: SGFPropertyValueError, cause: fmt.Errorf("プロパティの識別子%qには大文字だけを使ってください", property.Ident)}
			}
		}
		if len(property.Values) == 0 {
			return &SGFPropertyError{Identifier: property.Ident, Kind: SGFPropertyValueError, cause: fmt.Errorf("プロパティ%sには値が必要です", property.Ident)}
		}
		for _, value := range property.Values {
			if normalized := NormalizeSGFPropertyValue(property.Ident, value); normalized != value {
				return &SGFPropertyError{Identifier: property.Ident, Kind: SGFPropertyValueError, cause: fmt.Errorf("プロパティ%sの空白はSGFの規則に従って正規化してください", property.Ident)}
			}
		}
		if isKnownSGFProperty(property.Ident) && !multipleValueSGFProperties[property.Ident] && len(property.Values) != 1 {
			return &SGFPropertyError{Identifier: property.Ident, Kind: SGFPropertyValueError, cause: fmt.Errorf("プロパティ%sには値を1つだけ指定してください", property.Ident)}
		}
		if property.Ident == "ST" && property.Values[0] != "0" && property.Values[0] != "1" && property.Values[0] != "2" && property.Values[0] != "3" {
			return &SGFPropertyError{Identifier: property.Ident, Kind: SGFPropertyValueError, cause: fmt.Errorf("プロパティSTには0から3までを指定してください")}
		}
		if seen[property.Ident] {
			return &SGFPropertyError{Identifier: property.Ident, Kind: SGFPropertyDuplicateError, cause: fmt.Errorf("プロパティ%sが複数あります", property.Ident)}
		}
		seen[property.Ident] = true
		if !root && rootOnlySGFProperties[property.Ident] {
			return &SGFPropertyError{Identifier: property.Ident, Kind: SGFPropertyPlacementError, cause: fmt.Errorf("プロパティ%sは開始局面だけで扱えます", property.Ident)}
		}
	}
	return nil
}

// ValidateSGFPropertyPlacement は着手への注釈を着手と同じ局面だけに認める。
func ValidateSGFPropertyPlacement(properties []SGFProperty, hasMove bool) error {
	if hasMove {
		return nil
	}
	for _, property := range properties {
		if moveAnnotationSGFProperties[property.Ident] {
			return &SGFPropertyError{Identifier: property.Ident, Kind: SGFPropertyPlacementError, cause: fmt.Errorf("プロパティ%sには同じ局面の着手が必要です", property.Ident)}
		}
	}
	return nil
}

func validateSGFProperties(properties []SGFProperty, root bool, boardSize int) error {
	if err := ValidateSGFPropertyList(properties, root); err != nil {
		return err
	}
	for _, property := range properties {
		if applicationStructuredSGFProperties[property.Ident] {
			return fmt.Errorf("プロパティ%sは棋譜の対応する項目へ指定してください", property.Ident)
		}
		if err := ValidateAuxiliarySGFProperty(property, boardSize); err != nil {
			return err
		}
	}
	return nil
}

// ValidateAuxiliarySGFProperty はSGFの付随プロパティの値を盤サイズに照らして検査する。
func ValidateAuxiliarySGFProperty(property SGFProperty, boardSize int) error {
	pointList := property.Ident == "CR" || property.Ident == "DD" || property.Ident == "MA" || property.Ident == "SL" || property.Ident == "SQ" || property.Ident == "TR" || property.Ident == "TB" || property.Ident == "TW" || property.Ident == "VW"
	for _, value := range property.Values {
		switch {
		case pointList:
			if value == "" && (property.Ident == "DD" || property.Ident == "VW" || property.Ident == "TB" || property.Ident == "TW") {
				continue
			}
			parts := strings.Split(value, ":")
			if len(parts) > 2 || !validSGFPoint(parts[0], boardSize) || (len(parts) == 2 && (!validSGFPoint(parts[1], boardSize) || parts[1][0] < parts[0][0] || parts[1][1] < parts[0][1])) {
				return fmt.Errorf("プロパティ%sの座標%qが不正です", property.Ident, value)
			}
		case property.Ident == "LB":
			point, _, found := strings.Cut(value, ":")
			if !found || !validSGFPoint(point, boardSize) {
				return fmt.Errorf("プロパティLBの座標%qが不正です", value)
			}
		case property.Ident == "AR" || property.Ident == "LN":
			first, second, found := strings.Cut(value, ":")
			if !found || strings.Contains(second, ":") || !validSGFPoint(first, boardSize) || !validSGFPoint(second, boardSize) {
				return fmt.Errorf("プロパティ%sの座標の組%qが不正です", property.Ident, value)
			}
		case property.Ident == "FG":
			if value == "" {
				continue
			}
			number, _, found := strings.Cut(value, ":")
			if !found || !validSignedInteger(number) {
				return fmt.Errorf("プロパティFGには図の番号と名前を指定してください")
			}
		case property.Ident == "MN" || property.Ident == "PM":
			if !validSignedInteger(value) {
				return fmt.Errorf("プロパティ%sには整数を指定してください", property.Ident)
			}
		case property.Ident == "OB" || property.Ident == "OW":
			if !validUnsignedInteger(value) {
				return fmt.Errorf("プロパティ%sには0以上の整数を指定してください", property.Ident)
			}
		case property.Ident == "BL" || property.Ident == "WL" || property.Ident == "TM" || property.Ident == "V":
			if !validSGFDecimal(value) {
				return fmt.Errorf("プロパティ%sには10進数を指定してください", property.Ident)
			}
		case property.Ident == "BM" || property.Ident == "DM" || property.Ident == "GB" || property.Ident == "GW" || property.Ident == "HO" || property.Ident == "TE" || property.Ident == "UC":
			if value != "1" && value != "2" {
				return fmt.Errorf("プロパティ%sには1または2を指定してください", property.Ident)
			}
		case property.Ident == "DO" || property.Ident == "IT" || property.Ident == "KO":
			if value != "" {
				return fmt.Errorf("プロパティ%sには空の値を指定してください", property.Ident)
			}
		}
	}
	return nil
}

func validSGFPoint(value string, boardSize int) bool {
	return len(value) == 2 && value[0] >= 'a' && value[0] < 'a'+byte(boardSize) && value[1] >= 'a' && value[1] < 'a'+byte(boardSize)
}

func validUnsignedInteger(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validSignedInteger(value string) bool {
	if strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		value = value[1:]
	}
	return validUnsignedInteger(value)
}

func validSGFDecimal(value string) bool {
	if strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		value = value[1:]
	}
	integer, fraction, found := strings.Cut(value, ".")
	return validUnsignedInteger(integer) && (!found || !strings.Contains(fraction, ".") && (fraction == "" || validUnsignedInteger(fraction)))
}

// NormalizeSGFPropertyValue は、FF4の文章型と単行文章型、および合成値の文章部分を正規化する。
// 対局日と対局名では、正規化した値の前後にあるSGFの空白も取り除く。
func NormalizeSGFPropertyValue(ident, value string) string {
	if ident == "LB" || ident == "FG" {
		if separator := strings.IndexByte(value, ':'); separator >= 0 {
			return value[:separator+1] + normalizeSGFText(value[separator+1:], false)
		}
	}
	text := ident == "C" || ident == "GC"
	simpleText := simpleTextSGFProperties[ident]
	if !text && !simpleText {
		return value
	}
	normalized := normalizeSGFText(value, text)
	if ident == "GN" {
		return TrimSGFWhitespace(normalized)
	}
	return normalized
}

// NormalizeVariationTitle は変化名をSGFの単行文章として正規化し、前後の空白を除く。
func NormalizeVariationTitle(value string) string {
	return TrimSGFWhitespace(NormalizeSGFPropertyValue("N", value))
}

var simpleTextSGFProperties = map[string]bool{
	"AN": true, "AP": true, "BR": true, "BT": true, "CA": true, "CP": true, "DT": true, "EV": true, "GN": true,
	"N": true, "ON": true, "OT": true, "PB": true, "PC": true, "PW": true, "RO": true,
	"RE": true, "RU": true, "SO": true, "US": true, "WR": true, "WT": true,
}

func normalizeSGFText(value string, preserveLineBreaks bool) string {
	var normalized strings.Builder
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '\r' || character == '\n' {
			if index+1 < len(value) && ((character == '\r' && value[index+1] == '\n') || (character == '\n' && value[index+1] == '\r')) {
				index++
			}
			if preserveLineBreaks {
				normalized.WriteByte('\n')
			} else {
				normalized.WriteByte(' ')
			}
			continue
		}
		if character == '\t' || character == '\v' || character == '\f' {
			normalized.WriteByte(' ')
			continue
		}
		normalized.WriteByte(character)
	}
	return normalized.String()
}

// TrimSGFWhitespace はSGFの文章型で空白として扱う文字を値の両端から除く。
func TrimSGFWhitespace(value string) string {
	return strings.Trim(value, " \t\r\n\v\f")
}

var rootOnlySGFProperties = map[string]bool{
	"GM": true, "FF": true, "CA": true, "AP": true, "ST": true, "SZ": true,
	"AN": true, "BR": true, "BT": true, "CP": true, "DT": true, "EV": true, "GC": true, "GN": true,
	"ON": true, "OT": true, "PB": true, "PC": true, "PW": true, "RE": true, "RO": true, "RU": true,
	"SO": true, "TM": true, "US": true, "WR": true, "WT": true, "KM": true, "HA": true,
	"AB": true, "AW": true, "PL": true,
}

var multipleValueSGFProperties = map[string]bool{
	"AB": true, "AW": true, "AE": true,
	"AR": true, "CR": true, "DD": true, "LB": true, "LN": true,
	"MA": true, "SL": true, "SQ": true, "TR": true, "TB": true, "TW": true, "VW": true,
}

var scalarSGFProperties = map[string]bool{
	"B": true, "W": true, "KO": true, "MN": true, "N": true, "C": true,
	"DM": true, "GB": true, "GW": true, "HO": true, "UC": true, "V": true,
	"BM": true, "DO": true, "IT": true, "TE": true, "BL": true, "OB": true, "OW": true, "WL": true,
	"FG": true, "PM": true,
}

var moveAnnotationSGFProperties = map[string]bool{"BM": true, "DO": true, "IT": true, "KO": true, "TE": true}

// isKnownSGFProperty は値の型を検証できるFF4のプロパティかを返す。
func isKnownSGFProperty(ident string) bool {
	return rootOnlySGFProperties[ident] || scalarSGFProperties[ident] || multipleValueSGFProperties[ident]
}

var applicationStructuredSGFProperties = map[string]bool{
	"GM": true, "FF": true, "CA": true, "AP": true, "SZ": true, "RU": true,
	"KM": true, "HA": true, "AB": true, "AW": true, "AE": true, "PL": true,
	"B": true, "W": true, "RE": true,
	"HO": true,
}

// IsStructuredSGFProperty はSessionの専用項目として保持するプロパティかを返す。
func IsStructuredSGFProperty(ident string) bool { return applicationStructuredSGFProperties[ident] }

func sameCoordinates(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	counts := make(map[string]int, len(first))
	for _, coordinate := range first {
		counts[coordinate]++
	}
	for _, coordinate := range second {
		if counts[coordinate] == 0 {
			return false
		}
		counts[coordinate]--
	}
	return true
}

func validateResult(result Result) error {
	if math.IsNaN(result.Margin) || math.IsInf(result.Margin, 0) || math.IsNaN(result.BlackScore) || math.IsInf(result.BlackScore, 0) || math.IsNaN(result.WhiteScore) || math.IsInf(result.WhiteScore, 0) {
		return fmt.Errorf("結果の点数には有限値を指定してください")
	}
	switch result.Type {
	case "resignation":
		if result.Value != "" {
			return fmt.Errorf("投了結果にSGFの記録値を含めることはできません")
		}
		if result.Cause != "resignation" && result.Cause != "recorded" {
			return fmt.Errorf("投了結果の終局理由%qが不正です", result.Cause)
		}
		if !IsColor(result.Loser) {
			return fmt.Errorf("投了結果の敗者%qが不正です", result.Loser)
		}
		if result.Winner != OtherColor(result.Loser) {
			return fmt.Errorf("投了結果の敗者%qに対する勝者%qが不正です", result.Loser, result.Winner)
		}
		if result.Margin != 0 || result.BlackScore != 0 || result.WhiteScore != 0 {
			return fmt.Errorf("投了結果に点数を含めることはできません")
		}
	case "recorded":
		if result.Cause != "recorded" {
			return fmt.Errorf("記録結果の終局理由%qが不正です", result.Cause)
		}
		if result.Winner != "" || result.Loser != "" || result.Margin != 0 || result.BlackScore != 0 || result.WhiteScore != 0 || result.ScoreKnown {
			return fmt.Errorf("未解釈の記録結果に勝者または点数を含めることはできません")
		}
	case "points":
		if result.Value != "" {
			return fmt.Errorf("点数結果にSGFの記録値を含めることはできません")
		}
		if result.Cause != "two_passes" && result.Cause != "recorded" {
			return fmt.Errorf("点数結果の終局理由%qが不正です", result.Cause)
		}
		if result.Winner != "" && !IsColor(result.Winner) {
			return fmt.Errorf("点数結果の勝者%qが不正です", result.Winner)
		}
		if result.Loser != "" {
			return fmt.Errorf("点数結果に敗者を含めることはできません")
		}
		if result.Margin < 0 {
			return fmt.Errorf("点数結果の目差に負の値を指定できません")
		}
		if result.Winner == "" && result.Margin != 0 {
			return fmt.Errorf("引き分けの目差は0である必要があります")
		}
		if result.Winner != "" && result.Margin <= 0 {
			return fmt.Errorf("勝敗のある点数結果の目差は正である必要があります")
		}
		if !result.ScoreKnown && (result.BlackScore != 0 || result.WhiteScore != 0) {
			return fmt.Errorf("内訳不明の点数結果に黒と白の点数を含めることはできません")
		}
		if result.Cause == "two_passes" && !result.ScoreKnown {
			return fmt.Errorf("二連続パスの点数結果には黒と白の点数が必要です")
		}
		if result.ScoreKnown {
			black := decimalResultValue(result.BlackScore)
			white := decimalResultValue(result.WhiteScore)
			difference := new(big.Rat).Sub(black, white)
			expectedWinner := ""
			if difference.Sign() > 0 {
				expectedWinner = "B"
			} else if difference.Sign() < 0 {
				expectedWinner = "W"
			}
			difference.Abs(difference)
			if result.Winner != expectedWinner || difference.Cmp(decimalResultValue(result.Margin)) != 0 {
				return fmt.Errorf("点数結果が黒と白の点数に一致しません")
			}
		}
	default:
		return fmt.Errorf("結果の種類%qには対応していません", result.Type)
	}
	return nil
}

func decimalResultValue(value float64) *big.Rat {
	result, _ := new(big.Rat).SetString(strconv.FormatFloat(value, 'f', -1, 64))
	return result
}

func validateResultForBoard(result Result, boardSize int) error {
	seenDeadStones := make(map[string]bool, len(result.DeadStones))
	for _, coordinate := range result.DeadStones {
		if normalized := normalizeCoordinate(coordinate); normalized != coordinate {
			return fmt.Errorf("死石の座標%qは正規の表記ではありません", coordinate)
		}
		if _, ok := coord.Point(coordinate, boardSize); !ok {
			return fmt.Errorf("死石の座標%qが盤外です", coordinate)
		}
		if seenDeadStones[coordinate] {
			return fmt.Errorf("死石の座標%qが重複しています", coordinate)
		}
		seenDeadStones[coordinate] = true
	}
	if result.Type != "points" && len(result.DeadStones) > 0 {
		return fmt.Errorf("点数以外の結果に死石を指定できません")
	}
	if result.Type != "points" {
		return nil
	}
	return nil
}

func treePath(nodes map[string]TreeNode, currentID string) ([]Move, error) {
	var reversed []Move
	seen := make(map[string]bool)
	for currentID != "root" {
		if seen[currentID] {
			return nil, errors.New(gameTreeCycleMessage)
		}
		seen[currentID] = true
		node, ok := nodes[currentID]
		if !ok || node.ParentID == "" {
			return nil, fmt.Errorf("棋譜の親子関係が不正です")
		}
		if node.Move != nil {
			reversed = append(reversed, *node.Move)
		}
		currentID = node.ParentID
	}
	moves := make([]Move, len(reversed))
	for index := range reversed {
		moves[len(reversed)-1-index] = reversed[index]
	}
	return moves, nil
}

func (session *Session) togglePlayer() { session.currentPlayer = OtherColor(session.currentPlayer) }

// OtherColor は反対側の石の色を返す。
func OtherColor(color string) string {
	if color == "B" {
		return "W"
	}
	return "B"
}

func normalizeCoordinate(coordinate string) string {
	coordinate = strings.TrimSpace(coordinate)
	if strings.EqualFold(coordinate, "pass") {
		return "pass"
	}
	return strings.ToUpper(coordinate)
}

func (session *Session) point(coordinate string) (int, bool) {
	return coord.Point(coordinate, session.size)
}

func (session *Session) coordinate(point int) string {
	return coord.FromPoint(point, session.size)
}

func (session *Session) coordinates(points []int) []string {
	result := make([]string, 0, len(points))
	for _, point := range points {
		result = append(result, session.coordinate(point))
	}
	sort.Strings(result)
	return result
}

func (session *Session) neighbors(point int) []int {
	return pointNeighbors(session.size, point)
}

func pointNeighbors(size, point int) []int {
	x, y := point%size, point/size
	result := make([]int, 0, 4)
	if x > 0 {
		result = append(result, point-1)
	}
	if x+1 < size {
		result = append(result, point+1)
	}
	if y > 0 {
		result = append(result, point-size)
	}
	if y+1 < size {
		result = append(result, point+size)
	}
	return result
}

// TerritoryCoordinates は、受け取った石の配置で黒と白の地になる空点の座標を返す。
func TerritoryCoordinates(size int, stones map[string]string) map[string][]string {
	grid := make([]string, size*size)
	for coordinate, color := range stones {
		if point, ok := coord.Point(coordinate, size); ok {
			grid[point] = color
		}
	}
	points := territoryPointsOnBoard(size, grid)
	territory := map[string][]string{"B": {}, "W": {}}
	for owner, ownedPoints := range points {
		for _, point := range ownedPoints {
			territory[owner] = append(territory[owner], coord.FromPoint(point, size))
		}
		sort.Strings(territory[owner])
	}
	return territory
}

func territoryPointsOnBoard(size int, grid []string) map[string][]int {
	territory := map[string][]int{"B": {}, "W": {}}
	visited := make([]bool, len(grid))
	for start, color := range grid {
		if color != "" || visited[start] {
			continue
		}
		queue, region, borders := []int{start}, []int{}, map[string]bool{}
		visited[start] = true
		for len(queue) > 0 {
			point := queue[0]
			queue = queue[1:]
			region = append(region, point)
			for _, neighbor := range pointNeighbors(size, point) {
				if grid[neighbor] != "" {
					borders[grid[neighbor]] = true
				} else if !visited[neighbor] {
					visited[neighbor] = true
					queue = append(queue, neighbor)
				}
			}
		}
		for _, owner := range []string{"B", "W"} {
			if len(borders) == 1 && borders[owner] {
				territory[owner] = append(territory[owner], region...)
			}
		}
	}
	return territory
}

func (session *Session) groupAt(start int) ([]int, []int) {
	return groupOnBoard(session.size, session.grid, start)
}

func groupOnBoard(size int, grid []string, start int) ([]int, []int) {
	color := grid[start]
	queue := []int{start}
	stones := map[int]bool{start: true}
	liberties := make(map[int]bool)
	for len(queue) > 0 {
		point := queue[0]
		queue = queue[1:]
		for _, neighbor := range pointNeighbors(size, point) {
			switch grid[neighbor] {
			case "":
				liberties[neighbor] = true
			case color:
				if !stones[neighbor] {
					stones[neighbor] = true
					queue = append(queue, neighbor)
				}
			}
		}
	}
	return sortedIntSetValues(stones), sortedIntSetValues(liberties)
}

type boardGroup struct {
	color     string
	stones    []int
	liberties []int
}

func boardGroups(size int, grid []string) []boardGroup {
	visited := make([]bool, len(grid))
	groups := make([]boardGroup, 0)
	for start, color := range grid {
		if color == "" || visited[start] {
			continue
		}
		stones, liberties := groupOnBoard(size, grid, start)
		for _, stone := range stones {
			visited[stone] = true
		}
		groups = append(groups, boardGroup{
			color: color, stones: stones, liberties: liberties,
		})
	}
	return groups
}

// validateSnapshotStructure は、書き出しと復元が共有する棋譜の木の構造を検証する。
func validateSnapshotStructure(snapshot Snapshot) error {
	if len(snapshot.TreeNodes) > MaximumTreeNodes {
		return newCategorizedError(errorCategoryTreeLimit, fmt.Sprintf("棋譜の局面数は%d以下にしてください", MaximumTreeNodes))
	}
	if snapshot.NextNodeNumber < 1 {
		return fmt.Errorf("棋譜の次の局面番号が不正です")
	}
	if snapshot.NextNodeNumber > math.MaxInt-MaximumTreeNodes-1 {
		return fmt.Errorf("棋譜の次の局面番号が大きすぎます")
	}
	if snapshot.Rules != chineseRules {
		return fmt.Errorf("対局のルール%qには対応していません", snapshot.Rules)
	}
	if !IsColor(snapshot.InitialPlayer) {
		return fmt.Errorf("対局の開始手番%qが不正です", snapshot.InitialPlayer)
	}
	if snapshot.SetupStones == nil {
		return fmt.Errorf("対局に初期配置がありません")
	}
	if len(snapshot.TreeNodes) == 0 {
		return fmt.Errorf("対局に棋譜の木がありません")
	}
	if err := validateSGFProperties(snapshot.RootProperties, true, snapshot.BoardSize); err != nil {
		return fmt.Errorf("開始局面のプロパティが不正です：%w", err)
	}
	if err := ValidateSGFPropertyPlacement(snapshot.RootProperties, false); err != nil {
		return fmt.Errorf("開始局面のプロパティの配置が不正です：%w", err)
	}
	if style := snapshot.SGFRootStyle; style != nil {
		if !style.BoardSizeExplicit && snapshot.BoardSize != 19 {
			return fmt.Errorf("SGFの盤サイズSZの有無と対局の盤サイズが一致しません")
		}
		defaultKomi := DefaultKomi
		if snapshot.Handicap > 0 {
			defaultKomi = DefaultHandicapKomi
		}
		if !style.KomiExplicit && snapshot.Komi != defaultKomi {
			return fmt.Errorf("SGFのコミKMの有無と対局のコミが一致しません")
		}
		if style.CharsetExplicit != (style.CharsetValue != "") || (style.CharsetExplicit && !strings.EqualFold(style.CharsetValue, "UTF-8")) {
			return fmt.Errorf("SGFの文字コードCAの有無と値が一致しません")
		}
		if style.RulesExplicit != (style.RulesValue != "") || (style.RulesExplicit && !strings.EqualFold(style.RulesValue, ChineseRulesSGFValue)) {
			return fmt.Errorf("SGFのルールRUの有無と値が一致しません")
		}
		if !style.ApplicationExplicit && (style.ApplicationName != "" || style.ApplicationVersion != "") {
			return fmt.Errorf("SGFの作成元APの有無と値が一致しません")
		}
		if strings.IndexFunc(style.ApplicationName+style.ApplicationVersion, func(character rune) bool {
			return unicode.IsSpace(character) && character != ' '
		}) >= 0 {
			return fmt.Errorf("SGFの作成元APが正規化されていません")
		}
	}
	nodes := append([]TreeNode(nil), snapshot.TreeNodes...)
	sort.Slice(nodes, func(first, second int) bool { return nodeIDLess(nodes[first].ID, nodes[second].ID) })
	byID := make(map[string]TreeNode, len(nodes))
	children := make(map[string]int, len(snapshot.TreeNodes))
	maximumNodeNumber := 0
	for _, node := range nodes {
		if node.ID == "" {
			return fmt.Errorf("棋譜の木に空の局面の識別情報があります")
		}
		if node.ID != "root" {
			numberText := strings.TrimPrefix(node.ID, "n")
			number, err := strconv.Atoi(numberText)
			if err != nil || number < 1 || "n"+strconv.Itoa(number) != node.ID {
				return fmt.Errorf("棋譜の局面の識別情報が不正です")
			}
			if number >= math.MaxInt-1 {
				return fmt.Errorf("棋譜の局面の識別情報の番号が大きすぎます")
			}
			maximumNodeNumber = max(maximumNodeNumber, number)
		}
		if _, exists := byID[node.ID]; exists {
			return fmt.Errorf("棋譜の木で局面の識別情報が重複しています")
		}
		if node.Move != nil {
			if node.Move.Coordinate != "pass" {
				if _, ok := coord.Point(node.Move.Coordinate, snapshot.BoardSize); !ok {
					return fmt.Errorf("棋譜の着手座標%qが不正です", node.Move.Coordinate)
				}
			}
			for _, captured := range node.Move.Captured {
				if _, ok := coord.Point(captured, snapshot.BoardSize); !ok {
					return fmt.Errorf("棋譜の取った石の座標%qが不正です", captured)
				}
			}
		}
		byID[node.ID] = node
		children[node.ParentID]++
	}
	if snapshot.NextNodeNumber <= maximumNodeNumber {
		return fmt.Errorf("棋譜の次の局面番号が既存の局面番号以下です")
	}
	root, ok := byID["root"]
	if !ok || root.ParentID != "" || root.Move != nil || root.Result != nil {
		return fmt.Errorf("棋譜の木に正しい開始局面がありません")
	}
	if len(root.Properties) > 0 {
		return fmt.Errorf("棋譜の木の開始局面プロパティは開始局面用の項目へ格納してください")
	}
	orderedIDs := sortedTreeNodeIDs(byID)
	for _, id := range orderedIDs {
		node := byID[id]
		if id != "root" {
			if _, ok := byID[node.ParentID]; !ok {
				return fmt.Errorf("棋譜の局面に親局面がありません")
			}
		}
	}
	visitState := make(map[string]uint8, len(byID))
	visitState["root"] = 2
	for _, id := range orderedIDs {
		path := make([]string, 0)
		for current := id; visitState[current] != 2; current = byID[current].ParentID {
			if visitState[current] == 1 {
				return errors.New(gameTreeCycleMessage)
			}
			visitState[current] = 1
			path = append(path, current)
		}
		for _, current := range path {
			visitState[current] = 2
		}
	}
	if style := snapshot.SGFRootStyle; style != nil && !style.PlayerExplicit {
		defaultPlayer := "B"
		if snapshot.Handicap > 0 {
			defaultPlayer = "W"
		}
		firstMoveColor := ""
		for _, node := range nodes {
			if node.Move != nil {
				firstMoveColor = node.Move.Color
				break
			}
		}
		if snapshot.InitialPlayer != defaultPlayer && firstMoveColor == "" {
			return fmt.Errorf("SGFの開始手番PLの有無と対局の開始手番が一致しません")
		}
	}
	nearestMoves, consecutivePasses := indexTwoPassCompletions(byID)
	followingMoves := indexFollowingMoves(byID)
	hasResult := false
	resultsByCompletion := make(map[string][]Result)
	for _, id := range orderedIDs {
		node := byID[id]
		if node.Result == nil {
			continue
		}
		hasResult = true
		if isCanceledTwoPassResultInTree(byID, nearestMoves, consecutivePasses, node) {
			continue
		}
		completionNodeID := nearestMoves[node.ParentID]
		resultsByCompletion[completionNodeID] = append(resultsByCompletion[completionNodeID], *node.Result)
	}
	mainLinePath := make(map[string]bool)
	for id := snapshot.MainLineNodeID; id != ""; id = byID[id].ParentID {
		mainLinePath[id] = true
		node := byID[id]
		if node.Origin != "" || node.VariationTitle != "" || node.Highlighted {
			return fmt.Errorf("主分岐の局面にAI agentの印を残すことはできません")
		}
	}
	if completionID, found := twoPassCompletionNodeIDInTree(byID, nearestMoves, consecutivePasses, snapshot.MainLineNodeID); found && byID[snapshot.MainLineNodeID].Result == nil && len(resultsByCompletion[completionID]) == 0 && !byID[completionID].AutomaticResultCanceled {
		return fmt.Errorf("主分岐の末端にある二連続パスには結果または結果の取り消しが必要です")
	}
	mainLineTwoPassResultParents := make(map[string]bool)
	for _, id := range orderedIDs {
		node := byID[id]
		if node.Result != nil && node.Result.Cause == "two_passes" && mainLinePath[node.ID] {
			if completionID, found := twoPassCompletionNodeIDInTree(byID, nearestMoves, consecutivePasses, node.ParentID); found {
				mainLineTwoPassResultParents[completionID] = true
			}
		}
	}
	for _, id := range orderedIDs {
		node := byID[id]
		if err := validateSGFProperties(node.Properties, false, snapshot.BoardSize); err != nil {
			return fmt.Errorf("棋譜の局面プロパティが不正です：%w", err)
		}
		if err := ValidateSGFPropertyPlacement(node.Properties, node.Move != nil); err != nil {
			return fmt.Errorf("棋譜の局面プロパティの配置が不正です：%w", err)
		}
		if node.AutomaticResultCanceled {
			completionID, isCompletion := twoPassCompletionNodeIDInTree(byID, nearestMoves, consecutivePasses, id)
			if !isCompletion || completionID != id {
				return fmt.Errorf("棋譜の結果の取り消しは二連続パスの2手目に指定してください")
			}
			if mainLineTwoPassResultParents[id] {
				return fmt.Errorf("主分岐に結果の取り消しと二連続パス結果が併存しています")
			}
		}
		if isTwoPassCompletion(node, consecutivePasses[id]) {
			if len(resultsByCompletion[id]) == 0 && !node.AutomaticResultCanceled && !followingMoves[id] {
				return fmt.Errorf("棋譜の二連続パスには結果または結果の取り消しが必要です")
			}
		}
		if node.Origin != "" && node.Origin != AgentOrigin {
			return fmt.Errorf("棋譜の局面の由来%qには対応していません", node.Origin)
		}
		if (node.VariationTitle != "" || node.Highlighted) && node.Origin == "" {
			return fmt.Errorf("棋譜の局面に由来のない変化の情報があります")
		}
		if node.AIDecision != nil && node.Move == nil {
			return fmt.Errorf("棋譜の局面に着手のないAI判断があります")
		}
		if node.Result != nil {
			if node.Move != nil || children[id] != 0 {
				return fmt.Errorf("結果を持つ棋譜の局面は着手のない末端にしてください")
			}
			if err := validateResult(*node.Result); err != nil {
				return fmt.Errorf("棋譜の結果が不正です：%w", err)
			}
			if err := validateResultForBoard(*node.Result, snapshot.BoardSize); err != nil {
				return fmt.Errorf("棋譜の結果が盤サイズまたはコミと一致しません：%w", err)
			}
		}
	}
	if _, ok := byID[snapshot.CurrentNodeID]; !ok {
		return fmt.Errorf("棋譜に現在の局面がありません")
	}
	if _, ok := byID[snapshot.MainLineNodeID]; !ok {
		return fmt.Errorf("主分岐の末端の局面がありません")
	}
	if snapshot.Status == "finished" {
		matchesCurrentPosition := false
		if currentResult := byID[snapshot.CurrentNodeID].Result; currentResult != nil && snapshot.Result != nil {
			matchesCurrentPosition = sameResult(*currentResult, *snapshot.Result)
		}
		completionID := nearestMoves[snapshot.CurrentNodeID]
		for _, result := range resultsByCompletion[completionID] {
			if snapshot.Result != nil && sameResult(result, *snapshot.Result) {
				matchesCurrentPosition = true
				break
			}
		}
		if hasResult && !matchesCurrentPosition {
			return fmt.Errorf("終局した対局の現在の局面に対応する結果がありません")
		}
	}
	return nil
}

func indexFollowingMoves(nodes map[string]TreeNode) map[string]bool {
	children := make(map[string][]string, len(nodes))
	for id, node := range nodes {
		children[node.ParentID] = append(children[node.ParentID], id)
	}
	result := make(map[string]bool, len(nodes))
	known := make(map[string]bool, len(nodes))
	var visit func(string) bool
	visit = func(id string) bool {
		if known[id] {
			return result[id]
		}
		known[id] = true
		for _, childID := range children[id] {
			if nodes[childID].Move != nil || visit(childID) {
				result[id] = true
				break
			}
		}
		return result[id]
	}
	for id := range nodes {
		visit(id)
	}
	return result
}

func isCanceledTwoPassResultInTree(nodes map[string]TreeNode, nearestMoves map[string]string, consecutivePasses map[string]int, node TreeNode) bool {
	if node.Result == nil || node.Result.Cause != "two_passes" {
		return false
	}
	completionID, found := twoPassCompletionNodeIDInTree(nodes, nearestMoves, consecutivePasses, node.ParentID)
	return found && nodes[completionID].AutomaticResultCanceled
}

// SameResultOutcome は、結果の種類、勝敗、目差が等しいかを返す。
func SameResultOutcome(first, second Result) bool {
	return first.Type == second.Type && first.Value == second.Value && first.Winner == second.Winner && first.Loser == second.Loser && first.Margin == second.Margin
}

func indexNearestMoveNodeIDs(nodes map[string]TreeNode) map[string]string {
	nearest := make(map[string]string, len(nodes))
	for id := range nodes {
		path := make([]string, 0)
		current := id
		for current != "" {
			if known, found := nearest[current]; found {
				current = known
				break
			}
			node := nodes[current]
			if node.Move != nil {
				break
			}
			path = append(path, current)
			current = node.ParentID
		}
		for _, pathID := range path {
			nearest[pathID] = current
		}
		if current != "" {
			nearest[current] = current
		}
	}
	return nearest
}

func indexConsecutivePassCounts(nodes map[string]TreeNode, nearestMoves map[string]string) map[string]int {
	counts := make(map[string]int, len(nodes))
	for id, node := range nodes {
		if node.Move == nil {
			continue
		}
		path := make([]string, 0)
		current := id
		for current != "" {
			if _, found := counts[current]; found {
				break
			}
			path = append(path, current)
			current = nearestMoves[nodes[current].ParentID]
		}
		for index := len(path) - 1; index >= 0; index-- {
			moveID := path[index]
			move := nodes[moveID]
			parentMoveID := nearestMoves[move.ParentID]
			counts[moveID] = consecutivePassesAfterMove(counts[parentMoveID], move.Move.Coordinate == "pass")
		}
	}
	return counts
}

// consecutivePassesAfterMove は、着手後の二連続パス判定用の回数を返す。
// 二連続パスの後に対局が続く棋譜では、次のパスから新しい組として数える。
func consecutivePassesAfterMove(current int, pass bool) int {
	if !pass {
		return 0
	}
	if current >= 2 {
		return 1
	}
	return current + 1
}

func isTwoPassCompletion(node TreeNode, consecutivePasses int) bool {
	return node.Move != nil && node.Move.Coordinate == "pass" && consecutivePasses == 2
}

func indexTwoPassCompletions(nodes map[string]TreeNode) (map[string]string, map[string]int) {
	nearestMoves := indexNearestMoveNodeIDs(nodes)
	return nearestMoves, indexConsecutivePassCounts(nodes, nearestMoves)
}

func twoPassCompletionNodeIDInTree(nodes map[string]TreeNode, nearestMoves map[string]string, consecutivePasses map[string]int, nodeID string) (string, bool) {
	secondID := nearestMoves[nodeID]
	if secondID == "" || !isTwoPassCompletion(nodes[secondID], consecutivePasses[secondID]) {
		return "", false
	}
	return secondID, true
}

// IsTwoPassCompletion は、指定した局面が、着手のない局面を挟む場合も含めて、
// 新しい組の二連続パスを完了した局面かを返す。
func IsTwoPassCompletion(snapshot Snapshot, nodeID string) bool {
	nodes := make(map[string]TreeNode, len(snapshot.TreeNodes))
	for _, node := range snapshot.TreeNodes {
		nodes[node.ID] = node
	}
	nearestMoves, consecutivePasses := indexTwoPassCompletions(nodes)
	_, found := twoPassCompletionNodeIDInTree(nodes, nearestMoves, consecutivePasses, nodeID)
	return found
}

// ValidateGameTreeHistory は棋譜の木の全経路について着手と結果が規則に従うことを確かめる。
func ValidateGameTreeHistory(snapshot Snapshot) error {
	if err := validateSnapshotStructure(snapshot); err != nil {
		return err
	}
	session, err := NewSessionWithSetup("snapshot-validation", snapshot.BoardSize, snapshot.Komi, snapshot.Handicap, snapshot.SetupStones, snapshot.InitialPlayer)
	if err != nil {
		return err
	}
	tree := make(map[string]TreeNode, len(snapshot.TreeNodes))
	for _, node := range snapshot.TreeNodes {
		tree[node.ID] = node
		if node.AIDecision != nil {
			if err := session.validateAIDecision(node); err != nil {
				return fmt.Errorf("棋譜の局面にあるAI判断が不正です：%w", err)
			}
		}
	}
	return session.validateTreeHistory(tree)
}

type gameTreeHistoryError struct{ cause error }

func (err *gameTreeHistoryError) Error() string { return err.cause.Error() }
func (err *gameTreeHistoryError) Unwrap() error { return err.cause }

// IsGameTreeHistoryError は復元失敗が棋譜の木の手順または結果によるものかを返す。
func IsGameTreeHistoryError(err error) bool {
	var historyError *gameTreeHistoryError
	return errors.As(err, &historyError)
}

func (session *Session) positionKey() string { return strings.Join(session.grid, ",") }

func sortedIntSetValues(set map[int]bool) []int {
	values := make([]int, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Ints(values)
	return values
}
