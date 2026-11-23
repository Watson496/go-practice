package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"go-practice/internal/apicontract"
	"go-practice/internal/game"
	"go-practice/internal/gamerecord"
	"go-practice/internal/katago"
	"go-practice/internal/sgf"
)

const (
	savedGameSchemaVersion = "1"
	gameOrderFile          = "game-order.txt"
)

type savedGame struct {
	SchemaVersion string                    `json:"schemaVersion" required:"true"`
	StartedAt     string                    `json:"startedAt" required:"true"`
	ImportedAt    string                    `json:"importedAt,omitempty"`
	Name          string                    `json:"name,omitempty"`
	Game          game.Snapshot             `json:"game" required:"true"`
	AILevel       *int                      `json:"aiLevel" required:"true"`
	HumanColor    *string                   `json:"humanColor" required:"true"`
	AnalysisCache map[string]cachedAnalysis `json:"analysisCache,omitempty"`
}

type storageLoadError struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

type categorizedStorageLoadError struct {
	public string
	cause  error
}

type gameRecordTooLargeError struct{}

func (*gameRecordTooLargeError) Error() string {
	return fmt.Sprintf("対局のJSONが%dMiBを超えるため保存できません", maximumRequestBodySize>>20)
}

type gameSGFTooLargeError struct{}

func (*gameSGFTooLargeError) Error() string {
	return fmt.Sprintf("対局のSGFが%dMiBを超えるため保存できません", maximumRequestBodySize>>20)
}

func (failure *categorizedStorageLoadError) Error() string { return failure.cause.Error() }
func (failure *categorizedStorageLoadError) Unwrap() error { return failure.cause }

func storageLoadFailure(public string, cause error) error {
	return &categorizedStorageLoadError{public: public, cause: cause}
}

func (server *Server) listStorageErrors(writer http.ResponseWriter, _ *http.Request) {
	failures := append(make([]storageLoadError, 0, len(server.storageLoadErrors)), server.storageLoadErrors...)
	writeJSON(writer, http.StatusOK, failures)
}

func NewPersistentServer(dataDir string, analyzer AIEngine) (*Server, error) {
	slog.Debug("保存済み対局の読み込みを開始します", "data_dir", dataDir)
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("保存先を指定してください")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("保存先を作成できません：%w", err)
	}
	server := newServer(dataDir, analyzer)
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, fmt.Errorf("保存先を読み込めません：%w", err)
	}
	for _, entry := range entries {
		if isAbandonedStorageTemporary(entry.Name()) {
			if err := os.Remove(filepath.Join(dataDir, entry.Name())); err != nil {
				server.reportStorageLoadError(entry.Name(), "保存処理の一時ファイルを削除できません", err)
			}
			continue
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		if filepath.Ext(entry.Name()) != ".json" {
			server.reportStorageLoadError(entry.Name(), "保存ファイルの拡張子は小文字の.jsonにしてください", fmt.Errorf("保存ファイルの拡張子が不正です"))
			continue
		}
		if entry.IsDir() {
			server.reportStorageLoadError(entry.Name(), "保存ファイルではなくディレクトリです", fmt.Errorf("保存ファイルの場所がディレクトリです"))
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			server.reportStorageLoadError(entry.Name(), "保存ファイルにシンボリックリンクは使えません", fmt.Errorf("保存ファイルの場所がシンボリックリンクです"))
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			server.reportStorageLoadError(entry.Name(), "保存ファイルの種類を確認できません", infoErr)
			continue
		}
		if !info.Mode().IsRegular() {
			server.reportStorageLoadError(entry.Name(), "保存ファイルが通常ファイルではありません", fmt.Errorf("保存ファイルの種類が不正です：%s", info.Mode().Type()))
			continue
		}
		if err := server.loadSavedGame(filepath.Join(dataDir, entry.Name())); err != nil {
			server.reportStorageLoadError(entry.Name(), publicStorageLoadError(err), err)
		}
	}
	order, err := loadGameOrder(dataDir)
	if err != nil {
		server.reportStorageLoadError(gameOrderFile, "保存した対局の並び順を読み込めません", err)
		order = nil
	}
	server.gameOrder = existingGameOrder(server.games, order)
	slog.Info("保存済み対局を読み込みました", "count", len(server.games), "errors", len(server.storageLoadErrors), "data_dir", dataDir)
	return server, nil
}

// existingGameOrder は、並び順のうち、読み込めた対局だけを残す。並び順にない対局は、
// まだ並べ替えていない対局として扱う。
func existingGameOrder(games map[string]*storedGame, order []string) []string {
	return slices.DeleteFunc(order, func(id string) bool {
		_, exists := games[id]
		return !exists
	})
}

func loadGameOrder(dataDir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, gameOrderFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("保存した対局の並び順を読み込めません：%w", err)
	}
	order := make([]string, 0)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(string(data), "\n") {
		id := strings.TrimSpace(line)
		if id == "" {
			continue
		}
		if !validGameID(id) {
			return nil, fmt.Errorf("保存した対局の並び順に不正な識別情報があります")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("保存した対局の並び順に同じ対局が重複しています")
		}
		seen[id] = struct{}{}
		order = append(order, id)
	}
	return order, nil
}

func (server *Server) persistGameOrder() error {
	data := strings.Join(server.gameOrder, "\n")
	if data != "" {
		data += "\n"
	}
	if err := atomicWrite(filepath.Join(server.dataDir, gameOrderFile), []byte(data)); err != nil {
		return fmt.Errorf("並び順を書き込めません：%w", err)
	}
	return nil
}

func isAbandonedStorageTemporary(name string) bool {
	return strings.HasPrefix(name, ".go-practice-") &&
		(strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, ".tmp.previous"))
}

func (server *Server) reportStorageLoadError(file, reason string, err error) {
	failure := storageLoadError{File: file, Reason: reason}
	server.storageLoadErrors = append(server.storageLoadErrors, failure)
	slog.Warn("保存済み対局を読み込めませんでした", "file", file, "reason", reason, "error", err)
}

func (server *Server) loadSavedGame(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return storageLoadFailure("保存ファイルを読み込めません", fmt.Errorf("ファイルを読み込めません：%w", err))
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return storageLoadFailure("保存ファイルを読み込めません", fmt.Errorf("ファイルを読み込めません：%w", err))
	}
	var saved savedGame
	if err := decodeStrictJSON(bytes.NewReader(data), &saved); err != nil {
		return storageLoadFailure("保存ファイルのJSONが不正です", fmt.Errorf("JSONを解釈できません：%w", err))
	}
	id := strings.TrimSuffix(filepath.Base(path), ".json")
	session, startedAt, err := validateSavedGame(id, saved)
	if err != nil {
		return err
	}
	_, importedAt, err := gamerecord.ParseTimes(saved.StartedAt, saved.ImportedAt, saved.Name)
	if err != nil {
		return storageLoadFailure("保存した開始日時か読み込んだ時刻が不正です", err)
	}
	aiLevel, humanColor, err := gamerecord.Settings(saved.AILevel, saved.HumanColor)
	if err != nil {
		return storageLoadFailure("保存したAIレベルか対局者の石の色が不正です", err)
	}
	server.games[id] = &storedGame{
		session: session, aiLevel: aiLevel, humanColor: humanColor,
		startedAt: startedAt, importedAt: importedAt, name: saved.Name,
	}
	loaded := server.games[id]
	loaded.replaceAnalysisCache(analysisCacheForVersion(saved.AnalysisCache, server.analysisVersion))
	response := loaded.response()
	loaded.publishedResponse.Store(&response)
	slog.Debug("保存済み対局を読み込みました", "game_id", id, "ai_level", aiLevel, "human_color", humanColor)
	return nil
}

func analysisCacheForVersion(entries map[string]cachedAnalysis, version string) map[string]cachedAnalysis {
	current := make(map[string]cachedAnalysis)
	for key, entry := range entries {
		if entry.Version == version {
			current[key] = entry
		}
	}
	if len(current) == 0 {
		return nil
	}
	return current
}

// orderingTime は、まだ並べ替えていない対局を並べる時刻を返す。開始日時が不明の対局は、読み込んだ時刻を使う。
func (stored *storedGame) orderingTime() time.Time {
	if stored.startedAt.IsZero() {
		return stored.importedAt
	}
	return stored.startedAt
}

func validateSavedGame(id string, saved savedGame) (*game.Session, time.Time, error) {
	if !validGameID(id) || filepath.Base(id) != id {
		return nil, time.Time{}, storageLoadFailure("保存ファイル名が不正です", fmt.Errorf("対局の識別情報が不正です"))
	}
	if saved.SchemaVersion != savedGameSchemaVersion {
		return nil, time.Time{}, storageLoadFailure("対応していない保存形式です", fmt.Errorf("未対応の保存形式です：%q", saved.SchemaVersion))
	}
	if saved.Game.ID != id {
		return nil, time.Time{}, storageLoadFailure("保存ファイル名と対局データが一致しません", fmt.Errorf("ファイル名と対局の識別情報が一致しません"))
	}
	if err := validateDerivedSnapshotShape(saved.Game); err != nil {
		return nil, time.Time{}, storageLoadFailure("保存した対局データに必要な項目がありません", err)
	}
	if _, _, err := gamerecord.Settings(saved.AILevel, saved.HumanColor); err != nil {
		return nil, time.Time{}, storageLoadFailure("保存したAIレベルかあなたの石の色が不正です", err)
	}
	name, err := gamerecord.CanonicalName(saved.Name)
	if err != nil || name != saved.Name {
		return nil, time.Time{}, storageLoadFailure("保存した対局名が不正です", fmt.Errorf("対局名が不正です"))
	}
	startedAt, _, err := gamerecord.ParseTimes(saved.StartedAt, saved.ImportedAt, saved.Name)
	if err != nil {
		return nil, time.Time{}, storageLoadFailure("保存した開始日時か読み込んだ時刻が不正です", err)
	}
	if err := apicontract.ValidateSnapshotUserText(saved.Game); err != nil {
		if field, maximum, ok := apicontract.SnapshotTextLimit(err); ok {
			return nil, time.Time{}, storageLoadFailure(fmt.Sprintf("保存した棋譜の%sが%d文字を超えています", field, maximum), err)
		}
		return nil, time.Time{}, storageLoadFailure("保存した棋譜の文章が不正です", err)
	}
	rootName, hasRootName, err := game.SinglePropertyValue(saved.Game.RootProperties, "GN")
	if err != nil {
		return nil, time.Time{}, storageLoadFailure("保存した棋譜の対局名GNが不正です", err)
	}
	if hasRootName != (saved.Name != "") || rootName != saved.Name {
		return nil, time.Time{}, storageLoadFailure("保存した対局名と棋譜の対局名が一致しません", fmt.Errorf("対局名と開始局面の対局名GNが一致しません"))
	}
	dateMatches, err := sgf.RecordedAtMatches(saved.Game, startedAt)
	if err != nil {
		return nil, time.Time{}, storageLoadFailure("保存した棋譜の対局日DTが不正です", err)
	}
	if !dateMatches {
		return nil, time.Time{}, storageLoadFailure("保存した開始日時と棋譜の対局日が一致しません", fmt.Errorf("開始日時と開始局面の対局日DTが一致しません"))
	}
	session, err := game.RestoreSession(saved.Game)
	if err != nil {
		if game.IsGameTreeHistoryError(err) {
			return nil, time.Time{}, storageLoadFailure("保存した棋譜の手順または結果が不正です", err)
		}
		return nil, time.Time{}, storageLoadFailure("保存した盤面状態が棋譜と一致しません", fmt.Errorf("対局を復元できません：%w", err))
	}
	if err := validateAnalysisCache(saved.Game, saved.AnalysisCache); err != nil {
		return nil, time.Time{}, storageLoadFailure("保存した解析情報が不正です", fmt.Errorf("局面解析の保存値が不正です：%w", err))
	}
	if err := validateExportSizes(saved); err != nil {
		var jsonTooLarge *gameRecordTooLargeError
		if errors.As(err, &jsonTooLarge) {
			return nil, time.Time{}, storageLoadFailure(fmt.Sprintf("保存した対局のJSONが%dMiBを超えています", maximumRequestBodySize>>20), err)
		}
		var sgfTooLarge *gameSGFTooLargeError
		if errors.As(err, &sgfTooLarge) {
			return nil, time.Time{}, storageLoadFailure(fmt.Sprintf("保存した対局のSGFが%dMiBを超えています", maximumRequestBodySize>>20), err)
		}
		return nil, time.Time{}, storageLoadFailure("保存した対局の書き出し内容が不正です", err)
	}
	return session, startedAt, nil
}

func validateExportSizes(saved savedGame) error {
	record := exportGameRecordValues(saved.Game, saved.AILevel, saved.HumanColor, saved.StartedAt, saved.ImportedAt, saved.Name)
	contents, err := marshalJSON(record, "")
	if err != nil {
		return fmt.Errorf("書き出すJSONを作成できません：%w", err)
	}
	if len(contents) > maximumRequestBodySize {
		return &gameRecordTooLargeError{}
	}
	sgfContents, err := sgf.Encode(saved.Game)
	if err != nil {
		return fmt.Errorf("書き出すSGFを作成できません：%w", err)
	}
	if len(sgfContents) > maximumRequestBodySize {
		return &gameSGFTooLargeError{}
	}
	return nil
}

func validateDerivedSnapshotShape(snapshot game.Snapshot) error {
	if snapshot.CurrentPlayer == "" || snapshot.Status == "" {
		return fmt.Errorf("保存した対局に必要な状態がありません")
	}
	if snapshot.Moves == nil || snapshot.Stones == nil || snapshot.Captures == nil {
		return fmt.Errorf("保存した対局に必要な棋譜データがありません")
	}
	if _, ok := snapshot.Captures["B"]; !ok {
		return fmt.Errorf("保存した対局に黒のアゲハマがありません")
	}
	if _, ok := snapshot.Captures["W"]; !ok {
		return fmt.Errorf("保存した対局に白のアゲハマがありません")
	}
	return nil
}

func publicStorageLoadError(err error) string {
	var pathError *fs.PathError
	if errors.As(err, &pathError) {
		return "保存ファイルを読み込めません"
	}
	var categorized *categorizedStorageLoadError
	if errors.As(err, &categorized) {
		return categorized.public
	}
	return "保存した対局データが不正です"
}

func validateAnalysisCache(snapshot game.Snapshot, cache map[string]cachedAnalysis) error {
	type position struct {
		turnNumber    int
		currentPlayer string
	}
	positions := map[string]position{"root": {currentPlayer: snapshot.InitialPlayer}}
	children := make(map[string][]game.TreeNode, len(snapshot.TreeNodes))
	for _, node := range snapshot.TreeNodes {
		if node.ID != "root" {
			children[node.ParentID] = append(children[node.ParentID], node)
		}
	}
	queue := []string{"root"}
	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]
		parent := positions[parentID]
		for _, node := range children[parentID] {
			child := parent
			if node.Move != nil {
				child.turnNumber++
				child.currentPlayer = "B"
				if node.Move.Color == "B" {
					child.currentPlayer = "W"
				}
			}
			positions[node.ID] = child
			queue = append(queue, node.ID)
		}
	}
	for cacheKey, entry := range cache {
		nodeID, visits, validKey := parseAnalysisCacheKey(cacheKey)
		position, exists := positions[nodeID]
		if !validKey || !exists {
			return fmt.Errorf("解析情報%qが示す局面は存在しません", cacheKey)
		}
		if visits <= 0 {
			return fmt.Errorf("解析情報%qのvisitsが不正です", cacheKey)
		}
		if strings.TrimSpace(entry.Version) == "" {
			return fmt.Errorf("解析情報%qに解析器の版がありません", cacheKey)
		}
		if entry.Summary.Candidates == nil {
			return fmt.Errorf("解析情報%qに候補手の一覧がありません", cacheKey)
		}
		if err := katago.ValidateSummaryForPosition(entry.Summary, snapshot.BoardSize, position.turnNumber, position.currentPlayer); err != nil {
			return fmt.Errorf("解析情報%qが不正です：%w", cacheKey, err)
		}
	}
	return nil
}

func (server *Server) persist(stored *storedGame) error {
	return server.persistWithAnalysisCache(stored, stored.analysisCacheEntries())
}

func (server *Server) persistSessionMutation(stored *storedGame, before *game.Session) error {
	if err := server.persist(stored); err != nil {
		stored.session = before
		return err
	}
	return nil
}

func (server *Server) persistWithAnalysisCache(stored *storedGame, analysisCache map[string]cachedAnalysis) error {
	stored.persistenceMu.Lock()
	defer stored.persistenceMu.Unlock()
	started := time.Now()
	saved := savedGame{
		SchemaVersion: savedGameSchemaVersion,
		StartedAt:     gamerecord.FormatTime(stored.startedAt),
		ImportedAt:    gamerecord.FormatTime(stored.importedAt),
		Name:          stored.name,
		Game:          stored.session.Snapshot(),
		AILevel:       optionalAILevel(stored.aiLevel),
		HumanColor:    optionalColor(stored.humanColor),
		AnalysisCache: analysisCache,
	}
	if _, _, err := validateSavedGame(saved.Game.ID, saved); err != nil {
		return fmt.Errorf("保存内容が不正です：%w", err)
	}
	data, err := marshalJSON(saved, "  ")
	if err != nil {
		return fmt.Errorf("保存内容をJSONへ変換できません：%w", err)
	}
	path := filepath.Join(server.dataDir, saved.Game.ID+".json")
	if err := atomicWrite(path, data); err != nil {
		return fmt.Errorf("対局を保存できません：%w", err)
	}
	response := stored.response()
	if published := stored.publishedResponse.Load(); published == nil || !reflect.DeepEqual(*published, response) {
		stored.publishedResponse.Store(&response)
	}
	slog.Debug("対局を保存しました", "game_id", saved.Game.ID, "duration", time.Since(started))
	return nil
}

func marshalJSON(value any, indent string) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if indent != "" {
		encoder.SetIndent("", indent)
	}
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (stored *storedGame) analysisCacheEntries() map[string]cachedAnalysis {
	stored.analysisCacheMu.RLock()
	defer stored.analysisCacheMu.RUnlock()
	if len(stored.analysisCache) == 0 {
		return nil
	}
	entries := make(map[string]cachedAnalysis, len(stored.analysisCache))
	for key, cached := range stored.analysisCache {
		entries[key] = cached
	}
	return entries
}

func (stored *storedGame) replaceAnalysisCache(entries map[string]cachedAnalysis) {
	stored.analysisCacheMu.Lock()
	defer stored.analysisCacheMu.Unlock()
	if len(entries) == 0 {
		stored.analysisCache = nil
		return
	}
	replacement := make(map[string]cachedAnalysis, len(entries))
	for key, cached := range entries {
		replacement[key] = cached
	}
	stored.analysisCache = replacement
}

func (stored *storedGame) cachedAnalysis(key, version string) (cachedAnalysis, bool) {
	stored.analysisCacheMu.RLock()
	defer stored.analysisCacheMu.RUnlock()
	cached, found := stored.analysisCache[key]
	return cached, found && cached.Version == version
}

func (server *Server) removePersistedGame(id string) error {
	path := filepath.Join(server.dataDir, id+".json")
	if err := atomicDelete(path); err != nil {
		return err
	}
	slog.Debug("保存済み対局を削除しました", "game_id", id)
	return nil
}

func atomicWrite(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".go-practice-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer removeStorageArtifact(temporaryPath, "保存用の一時ファイルを片付けられません")
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	backupPath := temporaryPath + ".previous"
	hadPrevious := true
	if err := os.Link(path, backupPath); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		hadPrevious = false
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		removeStorageArtifact(backupPath, "保存前の控えを片付けられません")
		return err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		if rollbackErr := rollbackReplace(path, backupPath, hadPrevious); rollbackErr != nil {
			return transactionRollbackError("保存の確定", err, rollbackErr)
		}
		return err
	}
	if hadPrevious {
		removeStorageArtifact(backupPath, "保存前の控えを片付けられません")
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			slog.Warn("保存後の後片付けを確定できません", "path", path, "error", err)
		}
	}
	return nil
}

func rollbackReplace(path, backupPath string, hadPrevious bool) error {
	if hadPrevious {
		if err := os.Rename(backupPath, path); err != nil {
			return err
		}
	} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func atomicDelete(path string) error {
	backup, err := os.CreateTemp(filepath.Dir(path), ".go-practice-delete-*.tmp")
	if err != nil {
		return err
	}
	backupPath := backup.Name()
	cleanupEmptyBackup := true
	defer func() {
		if cleanupEmptyBackup {
			removeStorageArtifact(backupPath, "削除用の一時ファイルを片付けられません")
		}
	}()
	if err := backup.Close(); err != nil {
		return err
	}
	if err := os.Remove(backupPath); err != nil {
		return err
	}
	if err := os.Rename(path, backupPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("%sを削除できません：%w", filepath.Base(path), err)
	}
	cleanupEmptyBackup = false
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		if rollbackErr := os.Rename(backupPath, path); rollbackErr != nil {
			return transactionRollbackError("削除の確定", err, rollbackErr)
		}
		if rollbackErr := syncDirectory(filepath.Dir(path)); rollbackErr != nil {
			return fmt.Errorf("削除の確定に失敗し、復元も確定できません：%v、%w", rollbackErr, err)
		}
		return fmt.Errorf("削除を確定できません：%w", err)
	}
	removeStorageArtifact(backupPath, "削除した対局の控えを片付けられません")
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		slog.Warn("削除後の後片付けを確定できません", "path", path, "error", err)
	}
	return nil
}

func removeStorageArtifact(path, message string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Warn(message, "path", path, "error", err)
	}
}

func transactionRollbackError(operation string, operationErr, rollbackErr error) error {
	if errors.Is(rollbackErr, operationErr) {
		return fmt.Errorf("%sに失敗し、元の内容も復元できません：%w", operation, operationErr)
	}
	return fmt.Errorf("%sに失敗し、元の内容も復元できません（復元時の原因：%v）：%w", operation, rollbackErr, operationErr)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
