import {
  Button,
  Checkbox,
  FileButton,
  Group,
  HoverCard,
  Loader,
  Menu,
  Modal,
  NumberInput,
  Paper,
  Popover,
  ScrollArea,
  SegmentedControl,
  Select,
  Slider,
  Stack,
  Switch,
  Table,
  Text,
  Textarea,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
  IconArrowBackUp,
  IconChevronLeft,
  IconChevronRight,
  IconDownload,
  IconFileImport,
  IconFlag,
  IconPlayerPlay,
  IconPlayerSkipForward,
  IconRefresh,
  IconTrash,
} from "@tabler/icons-react";
import {
  type ReactNode,
  type SetStateAction,
  useCallback,
  useEffect,
  useEffectEvent,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  candidateColor,
  candidateScoreLead,
  candidateScoreLoss,
  candidateWinrate,
  formatScoreLead,
  formatVisits,
  formatWinrate,
} from "./analysisDisplay";
import {
  APIError,
  beginUIInteraction,
  cancelAIMoves,
  createGame,
  deleteGame,
  deleteGameTreeVariation,
  endUIInteraction,
  type GameResponse,
  type GameTreeNode,
  getGame,
  getGameAnalysis,
  getGameJSON,
  getGameSGF,
  importGameJSON,
  importGameSGF,
  listGames,
  listStorageErrors,
  type Move,
  navigateGameTree,
  type PositionAnalysis,
  playAIMove,
  playMove,
  playVariation,
  promoteGameTreeVariation,
  renameGame,
  reorderGames,
  resignGame,
  setAgentSelection,
  type UIInteraction,
  undoMove,
  updateGameSettings,
  updateGameTreeNode,
} from "./api";
import { Board } from "./Board";
import {
  loadParsedStoredValue,
  parseEvaluationHistory,
  parseStoredGameID,
  parseStoredWorkspaceMode,
  pollingFailureKind,
  removeStoredValue,
  serializedEvaluations,
  serializedGameID,
  serializedWorkspaceMode,
  storedValueErrorMessage,
  writeStoredValue,
} from "./browserState";
import {
  applyAIMoveResponse,
  applyOptimisticMoveResponse,
  applyPolledGameResponse,
  applyReviewConflict,
  changedByNavigation,
  mergeReviewResponse,
  type ReviewTarget,
  reviewTargetFor,
  rollbackOptimisticMove,
} from "./frontendState";
import { GameSettingsModal } from "./GameSettingsModal";
import { gameControls } from "./gameControls";
import { gameName, pausedWhenOpened } from "./gamePresentation";
import {
  aiLevelDescriptions,
  aiLevels,
  type ConfiguredSettings,
  humanColors,
  playerLabel,
  settingsConfigured,
} from "./gameSettings";
import {
  adjacentBranchNodeId,
  extendSelectedVariation,
  layoutGameTree,
  nextTreeNodeId,
  treePathToNode,
  variationSubtreeNodeIds,
  walkTree,
} from "./gameTree";
import { maximumKomi, minimumKomi, parseKomiInput } from "./komi";
import { defaultKomi, handicapOptions } from "./newGame";
import { optimisticStones } from "./optimisticBoard";
import { resultDescription } from "./resultDescription";
import { SavedGameList } from "./SavedGameList";
import { createSerialRequests } from "./serialRequests";

const boardSizes = [5, 6, 7, 9, 13, 19].map((size) => ({
  value: String(size),
  label: `${size}路`,
}));
const layoutWidth = 1720;
const layoutHeight = 1080;

function ScaleToFit({ children }: { children: ReactNode }) {
  const [scale, setScale] = useState(() =>
    Math.min(
      window.innerWidth / layoutWidth,
      window.innerHeight / layoutHeight,
      1.25,
    ),
  );

  useEffect(() => {
    const resize = () =>
      setScale(
        Math.min(
          window.innerWidth / layoutWidth,
          window.innerHeight / layoutHeight,
          1.25,
        ),
      );
    window.addEventListener("resize", resize);
    resize();
    return () => window.removeEventListener("resize", resize);
  }, []);

  return (
    <div className="app-stage">
      <div
        className="app-scale-box"
        style={{ width: layoutWidth * scale, height: layoutHeight * scale }}
      >
        <div className="app-screen" style={{ transform: `scale(${scale})` }}>
          {children}
        </div>
      </div>
    </div>
  );
}
const latestRecordKey = "go-practice.latest-game-record";
const workspaceModeKey = "go-practice.workspace-mode";

export function App() {
  const [storedWorkspaceMode] = useState(() =>
    loadParsedStoredValue(
      localStorage,
      workspaceModeKey,
      parseStoredWorkspaceMode,
    ),
  );
  const initialWorkspaceMode = storedWorkspaceMode.ok
    ? (storedWorkspaceMode.value ?? "play")
    : "play";
  const [boardSize, setBoardSize] = useState("9");
  const [aiLevel, setAILevel] = useState("1");
  const [handicap, setHandicap] = useState("0");
  const [komi, setKomi] = useState<number | string>(7.5);
  const [humanColor, setHumanColor] = useState<"B" | "W">("B");
  const [state, setState] = useState<GameResponse | null>(null);
  const [savedGames, setSavedGames] = useState<GameResponse[]>([]);
  const savedGamesRevision = useRef(0);
  const updateSavedGames = useCallback(
    (update: SetStateAction<GameResponse[]>) => {
      savedGamesRevision.current++;
      setSavedGames(update);
    },
    [],
  );
  const [busy, setBusy] = useState(false);
  const [aiThinking, setAIThinking] = useState(false);
  const [aiPaused, setAIPaused] = useState(false);
  const aiRequest = useRef<AbortController | null>(null);
  const aiRequestSettled = useRef<Promise<void> | null>(null);
  const [restoring, setRestoring] = useState(true);
  const [ownershipVisible, setOwnershipVisible] = useState(true);
  const [ownershipIntensity, setOwnershipIntensity] = useState(40);
  const [candidatesVisible, setCandidatesVisible] = useState(
    initialWorkspaceMode === "analysis",
  );
  const [candidatePanelVisible, setCandidatePanelVisible] = useState(
    initialWorkspaceMode === "analysis",
  );
  const [ownership, setOwnership] = useState<number[]>([]);
  const [analysis, setAnalysis] = useState<PositionAnalysis | null>(null);
  const [analyzing, setAnalyzing] = useState(false);
  const [note, setNote] = useState("");
  const [important, setImportant] = useState(false);
  const [reviewTarget, setReviewTarget] = useState<ReviewTarget | null>(null);
  const [savingReview, setSavingReview] = useState(false);
  // 応答が届いたときに表示している局面を知るため、最も新しい状態を持つ
  const latestState = useRef(state);
  useEffect(() => {
    latestState.current = state;
  }, [state]);
  // メモの保存の応答が届いたとき、まだその局面を表示しているかを返す。
  // 別の局面へ移っていたら、その局面の書きかけと編集中の通知には触れない。
  function stillShowing(target: ReviewTarget) {
    const current = latestState.current;
    return (
      current?.game.id === target.gameId &&
      current.game.currentNodeId === target.nodeId
    );
  }
  const [shortcutHelpOpened, setShortcutHelpOpened] = useState(false);
  const [newGameOpened, setNewGameOpened] = useState(false);
  // 対局の設定のModal。playModeAfterSaveは、対局モードへの切り替えを待っているときにtrue
  const [settingsModal, setSettingsModal] = useState<{
    playModeAfterSave: boolean;
  } | null>(null);
  const [workspaceMode, setWorkspaceMode] = useState<"play" | "analysis">(
    initialWorkspaceMode,
  );
  const controls = gameControls(state, workspaceMode);
  const showError = (message: string) =>
    notifications.show({ color: "red", message, withBorder: true });
  const showEffectError = useEffectEvent(showError);
  const [evaluationHistory, setEvaluationHistory] = useState<
    Record<string, Pick<PositionAnalysis, "scoreLead" | "winrate" | "visits">>
  >({});
  const evaluationHistoryRef = useRef(evaluationHistory);
  const replaceEvaluationHistory = useCallback(
    (
      next: Record<
        string,
        Pick<PositionAnalysis, "scoreLead" | "winrate" | "visits">
      >,
    ) => {
      evaluationHistoryRef.current = next;
      setEvaluationHistory(next);
    },
    [],
  );
  const uiInteractions = useRef(new Map<string, UIInteraction>());
  const uiInteractionStarts = useRef(new Map<string, Promise<boolean>>());
  const uiInteractionActive = useRef(true);

  useEffect(() => {
    const message = storedValueErrorMessage(
      storedWorkspaceMode,
      "ブラウザに保存されたモードを読み取れませんでした。",
      "ブラウザに保存されたモードを読み取れず、削除もできませんでした。",
    );
    if (message) showEffectError(message);
  }, [storedWorkspaceMode]);

  async function beginScreenInteraction(
    key: string,
    value: Omit<UIInteraction, "token">,
  ) {
    if (uiInteractions.current.has(key)) return true;
    const starting = uiInteractionStarts.current.get(key);
    if (starting) return starting;
    const interaction = { ...value, token: crypto.randomUUID() };
    const request = beginUIInteraction(interaction)
      .then(() => {
        if (!uiInteractionActive.current) {
          void endUIInteraction(interaction.token).catch(() => undefined);
          return false;
        }
        uiInteractions.current.set(key, interaction);
        return true;
      })
      .catch((cause) => {
        showError(
          cause instanceof Error
            ? cause.message
            : "画面の操作状態を共有できませんでした",
        );
        return false;
      })
      .finally(() => uiInteractionStarts.current.delete(key));
    uiInteractionStarts.current.set(key, request);
    return request;
  }

  function endScreenInteraction(key: string) {
    const interaction = uiInteractions.current.get(key);
    if (!interaction) {
      const starting = uiInteractionStarts.current.get(key);
      if (starting)
        void starting.then((started) => {
          if (started) endScreenInteraction(key);
        });
      return;
    }
    uiInteractions.current.delete(key);
    void endUIInteraction(interaction.token).catch(() => undefined);
  }
  const endEffectInteraction = useEffectEvent(endScreenInteraction);

  // 開いた対局の設定を、新しい対局の条件の初期値にする
  function adoptGameSettings(response: GameResponse) {
    setBoardSize(String(response.game.boardSize));
    if (response.aiLevel !== null) setAILevel(String(response.aiLevel));
    setHandicap(String(response.game.handicap));
    setKomi(response.game.komi);
    if (response.humanColor !== null) setHumanColor(response.humanColor);
  }
  const adoptEffectGameSettings = useEffectEvent(adoptGameSettings);

  useEffect(() => {
    uiInteractionActive.current = true;
    const heartbeat = window.setInterval(() => {
      for (const interaction of uiInteractions.current.values()) {
        void beginUIInteraction(interaction).catch(() => undefined);
      }
    }, 30_000);
    return () => {
      uiInteractionActive.current = false;
      window.clearInterval(heartbeat);
      for (const interaction of uiInteractions.current.values()) {
        void endUIInteraction(interaction.token).catch(() => undefined);
      }
      uiInteractions.current.clear();
      uiInteractionStarts.current.clear();
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    void listStorageErrors()
      .then((errors) => {
        if (cancelled) return;
        for (const error of errors) {
          showEffectError(`${error.file}：${error.reason}`);
        }
      })
      .catch((cause) => {
        if (!cancelled) {
          showEffectError(
            cause instanceof Error
              ? cause.message
              : "保存ファイルの読み込み結果を確認できませんでした。",
          );
        }
      });
    const loadCurrentGames = async () => {
      for (;;) {
        const revision = savedGamesRevision.current;
        const games = await listGames();
        if (cancelled) return;
        if (revision === savedGamesRevision.current) {
          updateSavedGames(games);
          return;
        }
      }
    };
    void loadCurrentGames().catch((cause) => {
      if (!cancelled)
        showEffectError(
          cause instanceof Error
            ? `保存した対局の一覧を取得できませんでした。${cause.message}`
            : "保存した対局の一覧を取得できませんでした。",
        );
    });
    const storedGame = loadParsedStoredValue(
      localStorage,
      latestRecordKey,
      parseStoredGameID,
    );
    if (!storedGame.ok) {
      showEffectError(
        storedValueErrorMessage(
          storedGame,
          "ブラウザに保存された前回の対局を読み取れませんでした。",
          "ブラウザに保存された前回の対局を読み取れず、削除もできませんでした。",
        ),
      );
      setRestoring(false);
      return () => {
        cancelled = true;
      };
    }
    if (storedGame.value === null) {
      setRestoring(false);
      return () => {
        cancelled = true;
      };
    }
    void getGame(storedGame.value)
      .then((response) => {
        if (cancelled) return;
        updateSavedGames((games) =>
          games.some((entry) => entry.game.id === response.game.id)
            ? games
            : [response, ...games],
        );
        setState(response);
        adoptEffectGameSettings(response);
        setAIPaused(pausedWhenOpened(response));
      })
      .catch(() => {
        if (!cancelled)
          showEffectError(
            "前回の対局をサーバーから復元できませんでした。保存ファイルが削除されたか、Goサーバーが異なる保存先で起動している可能性があります。",
          );
      })
      .finally(() => {
        if (!cancelled) setRestoring(false);
      });
    return () => {
      cancelled = true;
    };
  }, [updateSavedGames]);

  useEffect(() => {
    if (!state) return;
    if (
      !writeStoredValue(
        localStorage,
        latestRecordKey,
        serializedGameID(state.game.id),
      )
    )
      showEffectError("前回の対局をブラウザに保存できませんでした。");
    setSavedGames((games) =>
      games.some((entry) => entry.game.id === state.game.id)
        ? games.map((entry) =>
            entry.game.id === state.game.id ? state : entry,
          )
        : [state, ...games],
    );
  }, [state]);

  const analysisGameID = state?.game.id;
  const analysisNodeID = state?.game.currentNodeId;
  const analysisMoveCount = state?.game.moves.length;
  const startAnalysis = useEffectEvent((controller: AbortController) => {
    if (!state) return;
    setAnalysis(null);
    setOwnership([]);
    setAnalyzing(true);
    const currentNode = state.game.treeNodes.find(
      (node) => node.id === state.game.currentNodeId,
    );
    const previousRequest = currentNode?.parentId
      ? getGameAnalysis(state.game.id, currentNode.parentId, controller.signal)
      : Promise.resolve(null);
    void Promise.all([
      getGameAnalysis(
        state.game.id,
        state.game.currentNodeId,
        controller.signal,
      ),
      previousRequest,
    ])
      .then(([result, previous]) => {
        setAnalysis(result);
        setOwnership(result.ownership ?? []);
        const next = {
          ...evaluationHistoryRef.current,
          [state.game.currentNodeId]: pickEvaluation(result),
          ...(currentNode?.parentId && previous
            ? { [currentNode.parentId]: pickEvaluation(previous) }
            : {}),
        };
        replaceEvaluationHistory(next);
        if (
          !writeStoredValue(
            localStorage,
            `go-practice.evaluations.${state.game.id}`,
            serializedEvaluations(next),
          )
        )
          showEffectError("評価履歴をブラウザに保存できませんでした。");
      })
      .catch((cause) => {
        if (cause instanceof DOMException && cause.name === "AbortError")
          return;
        showEffectError(
          cause instanceof Error
            ? cause.message
            : "ownershipを解析できませんでした",
        );
      })
      .finally(() => {
        if (!controller.signal.aborted) setAnalyzing(false);
      });
  });
  // 解析は、対局、現在の局面、手数のどれかが変わったときだけやり直す
  useEffect(() => {
    if (
      !analysisGameID ||
      !analysisNodeID ||
      analysisMoveCount === undefined ||
      busy ||
      aiThinking
    )
      return;
    const controller = new AbortController();
    startAnalysis(controller);
    return () => controller.abort();
  }, [aiThinking, busy, analysisGameID, analysisNodeID, analysisMoveCount]);

  const evaluationGameID = state?.game.id;
  useEffect(() => {
    if (!evaluationGameID) return;
    const storedEvaluations = loadParsedStoredValue(
      localStorage,
      `go-practice.evaluations.${evaluationGameID}`,
      parseEvaluationHistory,
    );
    if (!storedEvaluations.ok) {
      replaceEvaluationHistory({});
      showEffectError(
        storedValueErrorMessage(
          storedEvaluations,
          "ブラウザに保存された評価履歴を読み取れませんでした。",
          "ブラウザに保存された評価履歴を読み取れず、削除もできませんでした。",
        ),
      );
    } else {
      replaceEvaluationHistory(storedEvaluations.value ?? {});
    }
  }, [evaluationGameID, replaceEvaluationHistory]);

  useEffect(() => {
    if (!state) return;
    if (reviewTarget) {
      // 編集中の局面にいる間は書きかけを残し、別の局面へ移ったら書きかけを捨てる
      if (
        reviewTarget.gameId === state.game.id &&
        reviewTarget.nodeId === state.game.currentNodeId
      )
        return;
      setReviewTarget(null);
    }
    const node = state.game.treeNodes.find(
      (candidate) => candidate.id === state.game.currentNodeId,
    );
    const properties =
      state.game.currentNodeId === "root"
        ? state.game.rootProperties
        : node?.properties;
    setNote(
      properties?.find((property) => property.ident === "C")?.values[0] ?? "",
    );
    setImportant(node?.important ?? false);
  }, [state, reviewTarget]);

  const reviewGameID = state?.game.id;
  const reviewNodeID = state?.game.currentNodeId;
  useEffect(() => {
    if (!reviewGameID || !reviewNodeID) return;
    return () => endEffectInteraction("review");
  }, [reviewGameID, reviewNodeID]);

  // 画面とAI agentのどちらが局面を移動したときも、"現在の局面"が変わったら分析モードに切り替える
  const previousState = useRef<GameResponse | null>(null);
  const changedByPositionNavigation =
    previousState.current !== null && state !== null
      ? changedByNavigation(previousState.current, state)
      : false;
  const followNavigation = useEffectEvent((current: GameResponse | null) => {
    previousState.current = current;
    if (current && workspaceMode === "play" && changedByPositionNavigation) {
      changeWorkspaceMode("analysis");
    }
  });
  useEffect(() => followNavigation(state), [state]);

  const requestEffectAIMove = useEffectEvent(requestAIMove);
  useEffect(() => {
    if (!state) return;
    void setAgentSelection(state.game.id, state.game.currentNodeId).catch(
      (cause) => {
        showEffectError(
          cause instanceof Error
            ? cause.message
            : "AI agentと表示中の局面を共有できませんでした",
        );
      },
    );
  }, [state]);

  useEffect(() => {
    if (!state || busy || aiThinking) return;
    const gameId = state.game.id;
    const timer = window.setInterval(() => {
      const requested = state;
      void getGame(gameId)
        .then((latest) => {
          setState((current) =>
            applyPolledGameResponse(current, requested, latest),
          );
        })
        .catch((cause) => {
          if (pollingFailureKind(cause) !== "deleted") return;
          setState((current) => (current?.game.id === gameId ? null : current));
          updateSavedGames((games) =>
            games.filter((entry) => entry.game.id !== gameId),
          );
          aiRequest.current?.abort();
          setAIPaused(false);
          setAnalysis(null);
          setOwnership([]);
          replaceEvaluationHistory({});
          if (reviewTarget?.gameId === gameId) {
            setReviewTarget(null);
            endEffectInteraction("review");
          }
          const latestRemoved = removeStoredValue(
            localStorage,
            latestRecordKey,
          );
          const evaluationsRemoved = removeStoredValue(
            localStorage,
            `go-practice.evaluations.${gameId}`,
          );
          if (!latestRemoved || !evaluationsRemoved)
            showEffectError(
              "削除された対局の情報をブラウザから削除できませんでした。",
            );
          showEffectError("表示していた対局は削除されました。");
        });
    }, 1500);
    return () => window.clearInterval(timer);
  }, [
    state,
    busy,
    aiThinking,
    reviewTarget?.gameId,
    replaceEvaluationHistory,
    updateSavedGames,
  ]);

  useEffect(() => {
    if (
      !state ||
      workspaceMode !== "play" ||
      busy ||
      aiThinking ||
      aiPaused ||
      changedByPositionNavigation
    )
      return;
    if (controls.aiTurn) {
      void requestEffectAIMove(state);
    }
  }, [
    workspaceMode,
    busy,
    aiThinking,
    aiPaused,
    state,
    changedByPositionNavigation,
    controls.aiTurn,
  ]);

  // 対局モードで、AIのレベルか人の石の色が未設定の対局を開いたときは、設定のModalを出す
  const openedGameID = state?.game.id;
  const openedGameUnset = state ? !settingsConfigured(state) : false;
  const openSettingsForUnsetGame = useEffectEvent(() => {
    if (workspaceMode === "play")
      setSettingsModal({ playModeAfterSave: false });
  });
  useEffect(() => {
    if (openedGameID && openedGameUnset) openSettingsForUnsetGame();
  }, [openedGameID, openedGameUnset]);

  async function startGame() {
    const parsedKomi = parseKomiInput(String(komi));
    if (parsedKomi.error !== null || parsedKomi.value === null) {
      showError(parsedKomi.error ?? "コミが不正です。");
      return;
    }
    setBusy(true);
    try {
      await stopAIForScreenChange();
      const created = await createGame(
        Number(boardSize),
        Number(aiLevel),
        Number(handicap),
        humanColor,
        parsedKomi.value,
      );
      updateSavedGames((games) => [created, ...games]);
      setAIPaused(false);
      setState(created);
      changeWorkspaceMode("play");
      setNewGameOpened(false);
    } catch (cause) {
      showError(
        cause instanceof Error ? cause.message : "対局を開始できませんでした",
      );
    } finally {
      setBusy(false);
    }
  }

  async function requestAIMove(baseState: GameResponse) {
    const controller = new AbortController();
    aiRequest.current?.abort();
    aiRequest.current = controller;
    let settle = () => {};
    aiRequestSettled.current = new Promise((resolve) => {
      settle = resolve;
    });
    setAIPaused(false);
    setAIThinking(true);
    try {
      const response = await playAIMove(baseState.game.id, controller.signal);
      setState((current) =>
        applyAIMoveResponse(current, baseState, response.game),
      );
    } catch (cause) {
      if (!(cause instanceof DOMException && cause.name === "AbortError")) {
        setAIPaused(true);
        showError(
          cause instanceof Error ? cause.message : "AIが着手できませんでした",
        );
      }
    } finally {
      if (aiRequest.current === controller) {
        aiRequest.current = null;
        setAIThinking(false);
      }
      settle();
    }
  }

  function pauseAI() {
    if (!aiRequest.current) return;
    setAIPaused(true);
    aiRequest.current.abort();
  }

  // 画面が始めたAIの思考だけを中止し、実際に中止した場合だけ中止状態にする。
  // 本体のサーバーは、対局を変える操作を受け付けると、ほかの入口の要求も中止する。
  async function stopAIThinking() {
    const wasThinking = aiRequest.current !== null;
    if (!wasThinking) return false;
    const settled = aiRequestSettled.current;
    setAIPaused(true);
    aiRequest.current?.abort();
    await settled;
    return true;
  }

  async function stopAIForScreenChange() {
    await stopAIThinking();
    const gameID = state?.game.id;
    if (!gameID) return;
    try {
      await cancelAIMoves(gameID);
    } catch (cause) {
      // 対局がすでに削除されている場合なども、続く画面操作自体は行う。
      // 実際に残っているAIの処理は、対局を変える本体の操作でも中止される。
      if (!(cause instanceof APIError && cause.status === 404))
        showError(
          cause instanceof Error
            ? cause.message
            : "AIの思考を中止できませんでした。",
        );
    }
  }

  function changeBoardSize(value: string | null) {
    if (!value) return;
    setBoardSize(value);
    const available = handicapOptions(Number(value));
    if (!available.some((option) => option.value === handicap)) {
      setHandicap("0");
      setKomi(defaultKomi(0));
    }
  }

  function changeHandicap(value: string | null) {
    if (!value) return;
    setHandicap(value);
    setKomi(defaultKomi(Number(value)));
  }

  async function importSGF(file: File | null) {
    if (!file || busy) return;
    setBusy(true);
    try {
      await stopAIForScreenChange();
      const response = await importGameSGF(await file.arrayBuffer());
      updateSavedGames((games) => [response, ...games]);
      setAIPaused(pausedWhenOpened(response));
      setState(response);
      adoptGameSettings(response);
    } catch (cause) {
      showError(
        cause instanceof Error ? cause.message : "SGFを読み込めませんでした",
      );
    } finally {
      setBusy(false);
    }
  }

  async function importJSON(file: File | null) {
    if (!file || busy) return;
    setBusy(true);
    try {
      await stopAIForScreenChange();
      const response = await importGameJSON(await file.arrayBuffer());
      updateSavedGames((games) => [response, ...games]);
      setAIPaused(pausedWhenOpened(response));
      setState(response);
      adoptGameSettings(response);
    } catch (cause) {
      showError(
        cause instanceof Error ? cause.message : "JSONを読み込めませんでした",
      );
    } finally {
      setBusy(false);
    }
  }

  async function openSavedGame(id: string | null) {
    if (!id || busy || id === state?.game.id) return;
    setBusy(true);
    try {
      await stopAIForScreenChange();
      const response = await getGame(id);
      setState(response);
      adoptGameSettings(response);
      setAIPaused(pausedWhenOpened(response));
    } catch (cause) {
      showError(
        cause instanceof Error
          ? cause.message
          : "保存した対局を開けませんでした",
      );
    } finally {
      setBusy(false);
    }
  }

  async function removeSavedGame(id: string) {
    if (busy) return;
    setBusy(true);
    try {
      await deleteGame(id);
      updateSavedGames((games) =>
        games.filter((candidate) => candidate.game.id !== id),
      );
      if (state?.game.id === id) {
        aiRequest.current?.abort();
        setState(null);
        setAIPaused(false);
        setAnalysis(null);
        setOwnership([]);
        replaceEvaluationHistory({});
        const latestRemoved = removeStoredValue(localStorage, latestRecordKey);
        const evaluationsRemoved = removeStoredValue(
          localStorage,
          `go-practice.evaluations.${id}`,
        );
        if (!latestRemoved || !evaluationsRemoved)
          showError("削除した対局の情報をブラウザから削除できませんでした。");
      }
    } catch (cause) {
      showError(
        cause instanceof Error
          ? cause.message
          : "保存した対局を削除できませんでした",
      );
    } finally {
      setBusy(false);
    }
  }

  async function changeSavedGameName(id: string, name: string) {
    if (busy) return false;
    setBusy(true);
    try {
      const previous = savedGames.find((candidate) => candidate.game.id === id);
      const response = await renameGame(id, name, previous?.name ?? "");
      updateSavedGames((games) =>
        games.map((candidate) =>
          candidate.game.id === id ? response : candidate,
        ),
      );
      if (state?.game.id === id) setState(response);
      return true;
    } catch (cause) {
      showError(
        cause instanceof Error ? cause.message : "対局名を変更できませんでした",
      );
      // 別の操作で対局名が変わっていたときは、一覧を取り直して、次の変更の基準にする
      if (cause instanceof APIError && cause.status === 409)
        await listGames()
          .then(setSavedGames)
          .catch(() => undefined);
      return false;
    } finally {
      setBusy(false);
    }
  }

  const reorderRequests = useRef(createSerialRequests<GameResponse[]>());
  async function reorderSavedGames(ids: string[]) {
    updateSavedGames((games) =>
      ids.flatMap((id) => {
        const entry = games.find((candidate) => candidate.game.id === id);
        return entry ? [entry] : [];
      }),
    );
    // 表示には、最後に始めた並べ替えの応答だけを使う
    const outcome = await reorderRequests.current(() => reorderGames(ids));
    if (outcome.ok) {
      if (outcome.latest) updateSavedGames(outcome.value);
      return;
    }
    showError(
      outcome.error instanceof Error
        ? outcome.error.message
        : "対局の並び順を保存できませんでした",
    );
    if (outcome.latest)
      await listGames()
        .then(updateSavedGames)
        .catch(() => undefined);
  }

  async function play(coordinate: string) {
    if (!state || busy || aiThinking || !controls.humanTurn) return;
    const previousState = state;
    const humanMove: Move = { color: state.game.currentPlayer, coordinate };
    const optimisticGame = {
      ...state.game,
      currentPlayer:
        state.game.currentPlayer === "B" ? ("W" as const) : ("B" as const),
      moves: [...state.game.moves, humanMove],
      stones: optimisticStones(state.game, humanMove),
    };

    setState({ ...state, game: optimisticGame });
    setBusy(true);
    try {
      const response = await playMove(state.game.id, coordinate);
      setState((current) =>
        applyOptimisticMoveResponse(
          current,
          optimisticGame,
          previousState,
          response.game,
        ),
      );
    } catch (cause) {
      setState((current) =>
        rollbackOptimisticMove(current, optimisticGame, previousState),
      );
      showError(
        cause instanceof Error ? cause.message : "着手できませんでした",
      );
    } finally {
      setBusy(false);
    }
  }

  const humanTurn = controls.humanTurn;
  const interactionBusy = busy;
  const canUndo = controls.undo;
  const currentTreeNode = state?.game.treeNodes.find(
    (node) => node.id === state.game.currentNodeId,
  );
  const variationAllowed = controls.variation;
  const previousNodeId = currentTreeNode?.parentId;
  const mainLineNodeIds = useMemo(
    () =>
      new Set(
        treePathToNode(
          state?.game.treeNodes ?? [],
          state?.game.mainLineNodeId ?? "root",
        ),
      ),
    [state?.game.mainLineNodeId, state?.game.treeNodes],
  );
  const nextNodeId = state
    ? nextTreeNodeId(
        state.game.treeNodes,
        state.game.currentNodeId,
        mainLineNodeIds,
      )
    : undefined;
  const upperBranchNodeId = state
    ? adjacentBranchNodeId(
        state.game.treeNodes,
        state.game.currentNodeId,
        mainLineNodeIds,
        -1,
      )
    : undefined;
  const lowerBranchNodeId = state
    ? adjacentBranchNodeId(
        state.game.treeNodes,
        state.game.currentNodeId,
        mainLineNodeIds,
        1,
      )
    : undefined;

  const handleShortcut = useEffectEvent((event: KeyboardEvent) => {
    // モーダルのダイアログを開いている間は、背後の画面をショートカットで操作しない。
    // ショートカットの一覧を開いているときは、一覧を閉じる「?」だけを受け付ける。
    if (
      document.querySelector("[role='dialog'][aria-modal='true']") &&
      !shortcutHelpOpened
    )
      return;
    const target = event.target;
    if (
      target instanceof HTMLElement &&
      (target.isContentEditable ||
        target.closest("input, textarea, select, [role='textbox']"))
    )
      return;
    const key = event.key.toLowerCase();
    const hasCommandModifier = event.ctrlKey || event.metaKey;

    if (
      (key === "?" || (event.code === "Slash" && event.shiftKey)) &&
      !hasCommandModifier
    ) {
      event.preventDefault();
      setShortcutHelpOpened((opened) => !opened);
      return;
    }
    if (event.defaultPrevented || event.altKey || !state) return;
    if (shortcutHelpOpened) return;
    let destination: string | undefined;
    let navigationShortcut = false;

    if (
      (key === "arrowleft" && !event.ctrlKey) ||
      (key === "z" && !event.shiftKey && !hasCommandModifier)
    ) {
      navigationShortcut = true;
      destination = event.metaKey
        ? "root"
        : walkTree(
            state.game.treeNodes,
            state.game.currentNodeId,
            mainLineNodeIds,
            "back",
            event.shiftKey ? 10 : 1,
          );
    } else if (
      (key === "arrowright" && !event.ctrlKey) ||
      (key === "x" && !event.shiftKey && !hasCommandModifier)
    ) {
      navigationShortcut = true;
      destination = event.metaKey
        ? state.game.mainLineNodeId
        : walkTree(
            state.game.treeNodes,
            state.game.currentNodeId,
            mainLineNodeIds,
            "forward",
            event.shiftKey ? 10 : 1,
          );
    } else if (key === "arrowup" && !event.shiftKey && !hasCommandModifier) {
      navigationShortcut = true;
      destination = upperBranchNodeId;
    } else if (key === "arrowdown" && !event.shiftKey && !hasCommandModifier) {
      navigationShortcut = true;
      destination = lowerBranchNodeId;
    } else if (key === "home" && !event.shiftKey) {
      navigationShortcut = true;
      destination = "root";
    } else if (key === "end" && !event.shiftKey) {
      navigationShortcut = true;
      destination = state.game.mainLineNodeId;
    } else if (
      key === "p" &&
      !event.shiftKey &&
      !hasCommandModifier &&
      !interactionBusy &&
      (workspaceMode === "analysis" ? variationAllowed : humanTurn)
    ) {
      event.preventDefault();
      void (workspaceMode === "analysis" ? addVariation("pass") : play("pass"));
      return;
    } else if (key === "t" && !event.shiftKey && !hasCommandModifier) {
      event.preventDefault();
      setOwnershipVisible((visible) => !visible);
      return;
    } else if (key === "e" && !event.shiftKey && !hasCommandModifier) {
      event.preventDefault();
      setCandidatesVisible((visible) => !visible);
      return;
    } else if (
      key === "pageup" &&
      !event.shiftKey &&
      !hasCommandModifier &&
      !interactionBusy &&
      state.game.currentNodeId !== "root" &&
      !mainLineNodeIds.has(state.game.currentNodeId)
    ) {
      event.preventDefault();
      void promoteVariation(state.game.currentNodeId);
      return;
    } else if (
      (key === "backspace" || key === "delete") &&
      event.metaKey &&
      !event.ctrlKey &&
      !event.shiftKey &&
      !interactionBusy &&
      state.game.currentNodeId !== "root"
    ) {
      event.preventDefault();
      void removeVariation(state.game.currentNodeId);
      return;
    }

    if (navigationShortcut) event.preventDefault();

    if (
      destination &&
      destination !== state.game.currentNodeId &&
      !interactionBusy
    ) {
      void goToNode(destination);
    }
  });
  useEffect(() => {
    window.addEventListener("keydown", handleShortcut);
    return () => window.removeEventListener("keydown", handleShortcut);
  }, []);

  // 対局モードへは、AIのレベルと人の石の色が設定されているときだけ切り替える。
  // 未設定なら設定のModalを出し、設定してから切り替える。
  async function selectWorkspaceMode(value: string) {
    if (value === "play" && state && !settingsConfigured(state)) {
      setSettingsModal({ playModeAfterSave: true });
      return;
    }
    if (value === "analysis") {
      // 中止の応答を待つ間の操作も、分析モードの操作として扱う。
      changeWorkspaceMode(value);
      await stopAIForScreenChange();
      return;
    }
    setAIPaused(false);
    changeWorkspaceMode(value);
  }

  async function saveGameSettings(settings: ConfiguredSettings) {
    if (!state || busy) return;
    const switchToPlayMode = settingsModal?.playModeAfterSave ?? false;
    await stopAIThinking();
    setBusy(true);
    try {
      setState(await updateGameSettings(state.game.id, settings));
      setSettingsModal(null);
      if (switchToPlayMode) {
        setAIPaused(false);
        changeWorkspaceMode("play");
      }
    } catch (cause) {
      showError(
        cause instanceof Error
          ? cause.message
          : "対局の設定を変更できませんでした",
      );
    } finally {
      setBusy(false);
    }
  }

  function changeWorkspaceMode(value: string) {
    if (value !== "play" && value !== "analysis") return;
    if (value === "analysis") aiRequest.current?.abort();
    setCandidatesVisible(value === "analysis");
    setCandidatePanelVisible(value === "analysis");
    setWorkspaceMode(value);
    if (
      !writeStoredValue(
        localStorage,
        workspaceModeKey,
        serializedWorkspaceMode(value),
      )
    )
      showError("モードをブラウザに保存できませんでした。");
  }

  async function downloadRecord() {
    if (!state || busy) return;
    try {
      const contents = await getGameJSON(state.game.id);
      const url = URL.createObjectURL(contents);
      const link = document.createElement("a");
      link.href = url;
      link.download = `go-practice-${state.game.boardSize}x${state.game.boardSize}-level${state.aiLevel}-${state.game.id}.json`;
      link.click();
      URL.revokeObjectURL(url);
    } catch (cause) {
      showError(
        cause instanceof Error ? cause.message : "JSONを保存できませんでした",
      );
    }
  }

  async function downloadSGF() {
    if (!state || busy) return;
    try {
      const contents = await getGameSGF(state.game.id);
      const url = URL.createObjectURL(contents);
      const link = document.createElement("a");
      link.href = url;
      link.download = `go-practice-${state.game.boardSize}x${state.game.boardSize}-level${state.aiLevel}-${state.game.id}.sgf`;
      link.click();
      URL.revokeObjectURL(url);
    } catch (cause) {
      showError(
        cause instanceof Error ? cause.message : "SGFを保存できませんでした",
      );
    }
  }

  async function resign() {
    if (
      !state ||
      busy ||
      aiThinking ||
      !controls.resign ||
      !window.confirm("投了しますか？")
    )
      return;
    setBusy(true);
    try {
      setState(await resignGame(state.game.id));
    } catch (cause) {
      showError(
        cause instanceof Error ? cause.message : "投了できませんでした",
      );
    } finally {
      setBusy(false);
    }
  }

  async function undo() {
    if (!state || busy || !canUndo) return;
    const stopped = await stopAIThinking();
    setBusy(true);
    try {
      const response = await undoMove(state.game.id);
      setState(response);
      if (stopped) setAIPaused(false);
    } catch (cause) {
      showError(
        cause instanceof Error ? cause.message : "待ったに失敗しました",
      );
    } finally {
      setBusy(false);
    }
  }

  async function goToNode(nodeId: string) {
    if (!state || busy || nodeId === state.game.currentNodeId) return;
    const stopped = await stopAIThinking();
    setBusy(true);
    try {
      const response = await navigateGameTree(state.game.id, nodeId);
      setState(response);
      if (
        stopped &&
        response.game.currentNodeId === response.game.mainLineNodeId
      )
        setAIPaused(false);
    } catch (cause) {
      showError(
        cause instanceof Error
          ? cause.message
          : "棋譜の局面へ移動できませんでした",
      );
    } finally {
      setBusy(false);
    }
  }

  async function removeVariation(nodeId: string) {
    if (!state || busy || nodeId === "root") return;
    await stopAIThinking();
    const node = state.game.treeNodes.find(
      (candidate) => candidate.id === nodeId,
    );
    const coordinate = node?.move?.coordinate ?? "この手";
    const promotionNotice = mainLineNodeIds.has(nodeId)
      ? "\n残っている変化があれば主分岐に繰り上がります。"
      : "";
    const nodeIds = variationSubtreeNodeIds(state.game.treeNodes, nodeId);
    if (
      !(await beginScreenInteraction("delete-variation", {
        kind: "delete-variation",
        gameId: state.game.id,
        nodeId,
        nodeIds,
      }))
    )
      return;
    if (
      !window.confirm(
        `「${coordinate}」以降を削除しますか？\nこの手の後続手もすべて削除されます。${promotionNotice}`,
      )
    ) {
      endScreenInteraction("delete-variation");
      return;
    }
    setBusy(true);
    try {
      const response = await deleteGameTreeVariation(
        state.game.id,
        nodeId,
        nodeIds,
      );
      setState(response);
      const remaining = new Set(
        response.game.treeNodes.map((candidate) => candidate.id),
      );
      const next = Object.fromEntries(
        Object.entries(evaluationHistoryRef.current).filter(([id]) =>
          remaining.has(id),
        ),
      );
      replaceEvaluationHistory(next);
      if (
        !writeStoredValue(
          localStorage,
          `go-practice.evaluations.${state.game.id}`,
          serializedEvaluations(next),
        )
      )
        showError("評価履歴をブラウザに保存できませんでした。");
    } catch (cause) {
      showError(
        cause instanceof Error ? cause.message : "変化を削除できませんでした",
      );
    } finally {
      endScreenInteraction("delete-variation");
      setBusy(false);
    }
  }

  async function promoteVariation(nodeId: string) {
    if (!state || busy || mainLineNodeIds.has(nodeId)) return;
    await stopAIThinking();
    setBusy(true);
    try {
      setState(await promoteGameTreeVariation(state.game.id, nodeId));
    } catch (cause) {
      showError(
        cause instanceof Error ? cause.message : "主分岐を変更できませんでした",
      );
    } finally {
      setBusy(false);
    }
  }

  async function addVariation(coordinate: string) {
    if (!state || busy || !variationAllowed) return false;
    await stopAIThinking();
    setBusy(true);
    try {
      setState(await playVariation(state.game.id, coordinate));
      return true;
    } catch (cause) {
      showError(
        cause instanceof Error ? cause.message : "変化へ着手できませんでした",
      );
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function saveReview() {
    if (!state || busy || savingReview) return;
    const target = reviewTarget ?? reviewTargetFor(state);
    setSavingReview(true);
    try {
      if (
        !(await beginScreenInteraction("review", {
          kind: "review",
          gameId: target.gameId,
          nodeId: target.nodeId,
        }))
      )
        return;
      const response = await updateGameTreeNode(target.gameId, target.nodeId, {
        note,
        important,
        expectedNote: target.expectedNote,
        expectedImportant: target.expectedImportant,
      });
      setState((current) =>
        current
          ? mergeReviewResponse(current, response, target.nodeId)
          : current,
      );
      updateSavedGames((games) =>
        games.map((entry) =>
          entry.game.id === response.game.id ? response : entry,
        ),
      );
      if (stillShowing(target)) {
        setReviewTarget(null);
        endScreenInteraction("review");
      }
    } catch (cause) {
      if (
        cause instanceof APIError &&
        cause.status === 409 &&
        stillShowing(target)
      ) {
        try {
          const latest = await getGame(target.gameId);
          const conflict = applyReviewConflict(target, latest, {
            note,
            important,
          });
          setState((current) =>
            current
              ? mergeReviewResponse(current, latest, target.nodeId)
              : current,
          );
          updateSavedGames((games) =>
            games.map((entry) =>
              entry.game.id === latest.game.id ? latest : entry,
            ),
          );
          setReviewTarget(conflict.target);
          setNote(conflict.note);
          setImportant(conflict.important);
          showError(`${cause.message} ${conflict.details}`);
          return;
        } catch {
          // 最新状態を取得できない場合は、元の競合理由を表示する。
        }
      }
      showError(
        cause instanceof Error
          ? cause.message
          : "メモと重要な局面を保存できませんでした",
      );
    } finally {
      setSavingReview(false);
    }
  }

  return (
    <ScaleToFit>
      <div className="app-shell">
        <Modal
          opened={shortcutHelpOpened}
          onClose={() => setShortcutHelpOpened(false)}
          title="キーボードショートカット"
          centered
          closeOnClickOutside
          closeOnEscape
          withCloseButton
          size="lg"
        >
          <Table striped withRowBorders={false} verticalSpacing="xs">
            <Table.Tbody>
              {[
                ["← または Z", "1手戻る"],
                ["→ または X", "1手進む"],
                ["Shift + ← / →", "10手戻る／進む"],
                ["↑ / ↓", "同じ手数の上下の分岐へ移動"],
                ["⌘ + ← / →", "開始局面／主分岐の末端へ移動"],
                ["Home / End", "開始局面／主分岐の末端へ移動"],
                ["P", "パス"],
                ["T", "ownershipを表示・非表示"],
                ["E", "盤上候補手を表示・非表示"],
                ["PageUp", "現在の変化を主分岐にする"],
                ["⌘ + Delete", "現在の変化以降を削除"],
                ["?", "この一覧を開く"],
              ].map(([shortcut, description]) => (
                <Table.Tr key={shortcut}>
                  <Table.Td>
                    <Text ff="monospace" fw={600}>
                      {shortcut}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">{description}</Text>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Modal>
        <GameSettingsModal
          game={state}
          opened={settingsModal !== null}
          busy={busy}
          onClose={() => setSettingsModal(null)}
          onSubmit={(settings) => void saveGameSettings(settings)}
          onInvalid={showError}
        />
        <header className="app-header">
          <Group h="100%" wrap="nowrap" gap="sm">
            <Title order={3} className="app-title">
              Go Practice
            </Title>
            <Popover
              width={360}
              position="bottom-start"
              withArrow
              shadow="md"
              opened={newGameOpened}
              onChange={setNewGameOpened}
            >
              <Popover.Target>
                <Button
                  leftSection={
                    state ? (
                      <IconRefresh size={18} />
                    ) : (
                      <IconPlayerPlay size={18} />
                    )
                  }
                  disabled={interactionBusy || restoring}
                  onClick={() => setNewGameOpened((opened) => !opened)}
                >
                  新しい対局
                </Button>
              </Popover.Target>
              <Popover.Dropdown>
                <Stack gap="sm">
                  <Group grow>
                    <Select
                      label="盤サイズ"
                      data={boardSizes}
                      value={boardSize}
                      onChange={changeBoardSize}
                      comboboxProps={{ withinPortal: false }}
                    />
                    <Select
                      label="あなた"
                      data={humanColors}
                      value={humanColor}
                      onChange={(value) =>
                        value && setHumanColor(value as "B" | "W")
                      }
                      comboboxProps={{ withinPortal: false }}
                    />
                  </Group>
                  <Group grow>
                    <Select
                      label="AIレベル"
                      data={aiLevels}
                      value={aiLevel}
                      onChange={(value) => value && setAILevel(value)}
                      comboboxProps={{ withinPortal: false }}
                    />
                    <Select
                      label="置き碁"
                      data={handicapOptions(Number(boardSize))}
                      value={handicap}
                      onChange={changeHandicap}
                      comboboxProps={{ withinPortal: false }}
                    />
                  </Group>
                  <NumberInput
                    label="コミ"
                    description="白に加える点数"
                    value={komi}
                    onChange={setKomi}
                    step={0.5}
                    decimalScale={1}
                    allowNegative
                    min={minimumKomi}
                    max={maximumKomi}
                    suffix="目"
                  />
                  <Text size="xs" c="dimmed">
                    {aiLevelDescriptions[aiLevel]}
                  </Text>
                  <Button loading={restoring || busy} onClick={startGame}>
                    この条件で開始
                  </Button>
                </Stack>
              </Popover.Dropdown>
            </Popover>
            {/* 項目を押してメニューが閉じた後も、FileButtonの入力要素を残して選んだファイルを受け取る */}
            <Menu
              shadow="md"
              width={180}
              keepMounted
              keepMountedMode="display-none"
            >
              <Menu.Target>
                <Button
                  variant="default"
                  leftSection={<IconFileImport size={18} />}
                  disabled={interactionBusy || restoring}
                >
                  インポート
                </Button>
              </Menu.Target>
              <Menu.Dropdown>
                <FileButton
                  onChange={importSGF}
                  accept=".sgf,application/x-go-sgf"
                  inputProps={{ "aria-label": "SGFファイル" }}
                >
                  {(props) => <Menu.Item {...props}>SGFを読み込む</Menu.Item>}
                </FileButton>
                <FileButton
                  onChange={importJSON}
                  accept=".json,application/json"
                  inputProps={{ "aria-label": "JSONファイル" }}
                >
                  {(props) => <Menu.Item {...props}>JSONを読み込む</Menu.Item>}
                </FileButton>
              </Menu.Dropdown>
            </Menu>
            {state && (
              <Menu shadow="md" width={180}>
                <Menu.Target>
                  <Button
                    variant="default"
                    leftSection={<IconDownload size={16} />}
                    disabled={interactionBusy}
                  >
                    エクスポート
                  </Button>
                </Menu.Target>
                <Menu.Dropdown>
                  <Menu.Item onClick={downloadSGF}>SGFで保存</Menu.Item>
                  <Menu.Item onClick={downloadRecord}>JSONで保存</Menu.Item>
                </Menu.Dropdown>
              </Menu>
            )}
            <div className="header-spacer" />
          </Group>
        </header>

        <aside className="saved-games-side" aria-label="保存した対局">
          <SavedGameList
            games={savedGames.map((entry) => ({
              id: entry.game.id,
              name: gameName(entry),
            }))}
            currentId={state?.game.id ?? null}
            disabled={interactionBusy || restoring}
            onOpen={(id) => void openSavedGame(id)}
            onRename={changeSavedGameName}
            onDelete={removeSavedGame}
            onReorder={(ids) => void reorderSavedGames(ids)}
            onInteractionStart={async (kind, gameId) => {
              // 開いている対局の削除では、削除の確認を出す前にAIの思考を中止する
              if (kind === "delete-game" && gameId === state?.game.id)
                await stopAIThinking();
              return beginScreenInteraction(`saved-${kind}`, {
                kind,
                gameId,
              });
            }}
            onInteractionEnd={(kind) => endScreenInteraction(`saved-${kind}`)}
          />
        </aside>
        <div className="app-main">
          {state ? (
            <main className="review-workspace">
              <Paper withBorder p="sm" className="board-pane">
                <div className="board-frame">
                  <Board
                    game={state.game}
                    ownership={ownershipVisible ? ownership : undefined}
                    ownershipIntensity={ownershipIntensity}
                    candidates={
                      candidatesVisible ? analysis?.candidates : undefined
                    }
                    disabled={interactionBusy || !controls.board}
                    onPlay={workspaceMode === "analysis" ? addVariation : play}
                  />
                </div>
                <Group justify="center" gap="xs" className="board-controls">
                  {aiThinking && (
                    <>
                      <Loader size="sm" />
                      <Text size="sm">AI思考中</Text>
                      <Button
                        size="compact-sm"
                        variant="default"
                        onClick={pauseAI}
                      >
                        中止
                      </Button>
                    </>
                  )}
                  {aiPaused && workspaceMode === "play" && controls.aiTurn && (
                    <Button
                      size="compact-sm"
                      variant="default"
                      leftSection={<IconPlayerPlay size={16} />}
                      disabled={busy}
                      onClick={() => void requestAIMove(state)}
                    >
                      AIの思考を再開
                    </Button>
                  )}
                  <Button
                    size="compact-sm"
                    variant="default"
                    leftSection={<IconPlayerSkipForward size={16} />}
                    disabled={interactionBusy || !controls.pass}
                    onClick={() =>
                      workspaceMode === "analysis"
                        ? addVariation("pass")
                        : play("pass")
                    }
                  >
                    パス
                  </Button>
                  <Button
                    size="compact-sm"
                    variant="default"
                    leftSection={<IconArrowBackUp size={16} />}
                    disabled={interactionBusy || !controls.undo}
                    onClick={undo}
                  >
                    待った
                  </Button>
                  <Button
                    size="compact-sm"
                    color="red"
                    variant="light"
                    leftSection={<IconFlag size={16} />}
                    disabled={interactionBusy || !controls.resign}
                    onClick={resign}
                  >
                    投了
                  </Button>
                </Group>
                <Group
                  justify="space-between"
                  gap="xs"
                  className="board-footer"
                >
                  <Switch
                    size="sm"
                    label="ownership"
                    checked={ownershipVisible}
                    onChange={(event) => {
                      setOwnershipVisible(event.currentTarget.checked);
                      setOwnership(
                        event.currentTarget.checked
                          ? (analysis?.ownership ?? [])
                          : [],
                      );
                    }}
                  />
                  {ownershipVisible && (
                    <Group gap="xs" className="ownership-slider">
                      <Text size="xs" c="dimmed">
                        濃さ
                      </Text>
                      <Slider
                        thumbLabel="ownershipの濃さ"
                        value={ownershipIntensity}
                        onChange={setOwnershipIntensity}
                        min={10}
                        max={100}
                        step={5}
                        w={120}
                      />
                    </Group>
                  )}
                  <Switch
                    size="sm"
                    label="候補手"
                    checked={candidatesVisible}
                    onChange={(event) =>
                      setCandidatesVisible(event.currentTarget.checked)
                    }
                  />
                </Group>
              </Paper>

              <section className="analysis-pane">
                <div className="workspace-mode-control">
                  <SegmentedControl
                    fullWidth
                    size="xs"
                    value={workspaceMode}
                    onChange={selectWorkspaceMode}
                    disabled={busy}
                    data={[
                      { value: "play", label: "対局" },
                      { value: "analysis", label: "分析" },
                    ]}
                  />
                </div>
                <Paper
                  withBorder
                  component="button"
                  type="button"
                  className="game-summary-card"
                  aria-label="対局の設定を変更"
                  disabled={interactionBusy}
                  onClick={() => setSettingsModal({ playModeAfterSave: false })}
                >
                  <div
                    className={`player-summary${state.game.status === "playing" && state.game.currentPlayer === "W" ? " is-active" : ""}`}
                  >
                    <span
                      className="player-stone white-stone"
                      aria-hidden="true"
                    />
                    <div>
                      <Text size="sm" fw={600}>
                        {playerLabel(state, "W")}
                      </Text>
                      <Text size="xs" c="dimmed">
                        アゲハマ {state.game.captures.W}子
                      </Text>
                    </div>
                  </div>
                  <div className="position-summary">
                    <Text size="sm" fw={700}>
                      {state.game.moves.length}手目・
                      {state.game.status === "finished"
                        ? resultDescription(state.game.result)
                        : `${state.game.currentPlayer === "B" ? "黒" : "白"}番`}
                    </Text>
                    <Text size="xs" c="dimmed">
                      中国ルール
                    </Text>
                    <Text size="xs" c="dimmed">
                      コミ {state.game.komi}目
                    </Text>
                  </div>
                  <div
                    className={`player-summary black-player${state.game.status === "playing" && state.game.currentPlayer === "B" ? " is-active" : ""}`}
                  >
                    <div>
                      <Text size="sm" fw={600}>
                        {playerLabel(state, "B")}
                      </Text>
                      <Text size="xs" c="dimmed">
                        アゲハマ {state.game.captures.B}子
                      </Text>
                    </div>
                    <span
                      className="player-stone black-stone"
                      aria-hidden="true"
                    />
                  </div>
                </Paper>
                <Paper withBorder p="sm" className="evaluation-card">
                  <Group justify="space-between" align="flex-start">
                    <div>
                      <Text size="xs" c="dimmed">
                        黒の評価
                      </Text>
                      <Text fz={28} fw={700}>
                        {analysis
                          ? `${analysis.scoreLead >= 0 ? "+" : ""}${analysis.scoreLead.toFixed(1)}目`
                          : "—"}
                      </Text>
                    </div>
                    <div>
                      <Text size="xs" c="dimmed">
                        黒の勝率
                      </Text>
                      <Text fz={28} fw={700}>
                        {analysis
                          ? `${(analysis.winrate * 100).toFixed(1)}%`
                          : "—"}
                      </Text>
                    </div>
                    <div>
                      <Text size="xs" c="dimmed">
                        visits
                      </Text>
                      <Text fw={600}>
                        {analysis?.visits ?? (analyzing ? "解析中" : "—")}
                      </Text>
                    </div>
                  </Group>
                  <EvaluationChart
                    nodes={state.game.treeNodes}
                    currentNodeId={state.game.currentNodeId}
                    mainLineNodeIds={mainLineNodeIds}
                    history={evaluationHistory}
                    disabled={interactionBusy}
                    onSelect={goToNode}
                  />
                </Paper>

                <Paper withBorder p="sm" className="candidate-card">
                  <Group justify="space-between" mb={2}>
                    <Group gap="xs" wrap="nowrap">
                      <Text fw={600}>候補手</Text>
                      <Switch
                        size="xs"
                        aria-label="候補手パネルの表示"
                        checked={candidatePanelVisible}
                        onChange={(event) =>
                          setCandidatePanelVisible(event.currentTarget.checked)
                        }
                      />
                    </Group>
                    <Text size="xs" c="dimmed">
                      勝率・visits・目差は手番側
                    </Text>
                  </Group>
                  {candidatePanelVisible && analysis ? (
                    <Table striped highlightOnHover verticalSpacing={2}>
                      <Table.Tbody>
                        {analysis.candidates.slice(0, 5).map((candidate) => (
                          <Table.Tr
                            key={candidate.move}
                            onDoubleClick={async () => {
                              const previousMode = workspaceMode;
                              const previousCandidatesVisible =
                                candidatesVisible;
                              const previousCandidatePanelVisible =
                                candidatePanelVisible;
                              if (previousMode !== "analysis")
                                changeWorkspaceMode("analysis");
                              setCandidatesVisible(true);
                              setCandidatePanelVisible(true);
                              if (await addVariation(candidate.move)) return;
                              if (previousMode !== "analysis")
                                changeWorkspaceMode(previousMode);
                              setCandidatesVisible(previousCandidatesVisible);
                              setCandidatePanelVisible(
                                previousCandidatePanelVisible,
                              );
                            }}
                          >
                            <Table.Td>
                              <Group gap={6} wrap="nowrap">
                                <span
                                  className="candidate-swatch"
                                  style={{
                                    background: candidateColor(
                                      candidateScoreLoss(
                                        analysis.candidates[0],
                                        candidate,
                                      ),
                                    ),
                                  }}
                                />
                                <Text fw={600}>{candidate.move}</Text>
                              </Group>
                            </Table.Td>
                            <Table.Td>
                              <Group
                                gap="sm"
                                wrap="nowrap"
                                className="candidate-metrics"
                              >
                                <Text size="xs">
                                  <span>勝率</span>{" "}
                                  {formatWinrate(candidateWinrate(candidate))}
                                </Text>
                                <Text size="xs">
                                  <span>visits</span>{" "}
                                  {formatVisits(candidate.visits)}
                                </Text>
                                <Text size="xs">
                                  <span>目差</span>{" "}
                                  {formatScoreLead(
                                    candidateScoreLead(candidate),
                                  )}
                                  目
                                </Text>
                                <HoverCard
                                  width={320}
                                  position="top"
                                  withArrow
                                  openDelay={150}
                                  closeDelay={100}
                                  shadow="md"
                                >
                                  <HoverCard.Target>
                                    <Text
                                      size="xs"
                                      c="dimmed"
                                      className="candidate-pv"
                                    >
                                      読み筋{" "}
                                      {candidate.pv.slice(0, 4).join(" → ") ||
                                        "—"}
                                    </Text>
                                  </HoverCard.Target>
                                  <HoverCard.Dropdown>
                                    <Text size="xs" fw={600}>
                                      読み筋
                                    </Text>
                                    <Text size="xs">
                                      {candidate.pv.join(" → ") || "—"}
                                    </Text>
                                  </HoverCard.Dropdown>
                                </HoverCard>
                              </Group>
                            </Table.Td>
                          </Table.Tr>
                        ))}
                      </Table.Tbody>
                    </Table>
                  ) : candidatePanelVisible ? (
                    <div className="candidate-placeholder">
                      {analyzing && <Loader size="sm" />}
                      <Text size="sm" c="dimmed">
                        {analyzing ? "KataGoで解析中…" : "解析結果はありません"}
                      </Text>
                    </div>
                  ) : (
                    <Table striped verticalSpacing={2} aria-hidden="true">
                      <Table.Tbody>
                        {Array.from(
                          { length: 5 },
                          (_, index) => `candidate-placeholder-${index}`,
                        ).map((key) => (
                          <Table.Tr key={key}>
                            <Table.Td>&nbsp;</Table.Td>
                            <Table.Td />
                          </Table.Tr>
                        ))}
                      </Table.Tbody>
                    </Table>
                  )}
                </Paper>

                <Paper withBorder p="sm" className="tree-card">
                  <Group justify="space-between" mb="xs">
                    <Text fw={600}>棋譜・変化</Text>
                    <Group gap={4}>
                      <Button
                        size="compact-xs"
                        variant="default"
                        leftSection={<IconChevronLeft size={14} />}
                        disabled={interactionBusy || !previousNodeId}
                        onClick={() =>
                          previousNodeId && void goToNode(previousNodeId)
                        }
                      >
                        前
                      </Button>
                      <Button
                        size="compact-xs"
                        variant="default"
                        rightSection={<IconChevronRight size={14} />}
                        disabled={interactionBusy || !nextNodeId}
                        onClick={() => nextNodeId && void goToNode(nextNodeId)}
                      >
                        次
                      </Button>
                    </Group>
                  </Group>
                  <GameTree
                    nodes={state.game.treeNodes}
                    currentNodeId={state.game.currentNodeId}
                    mainLineNodeIds={mainLineNodeIds}
                    disabled={interactionBusy}
                    onSelect={goToNode}
                    onDelete={removeVariation}
                    onPromote={promoteVariation}
                  />
                </Paper>

                <Paper withBorder className="review-panel">
                  <Text fw={600} px="xs" pt="xs">
                    メモ・重要な局面
                  </Text>
                  <ScrollArea
                    className="review-tab-scroll"
                    type="auto"
                    offsetScrollbars
                  >
                    <Stack gap="xs" p="xs">
                      <Textarea
                        label="この局面のメモ"
                        value={note}
                        onChange={(event) => {
                          const target = reviewTarget ?? reviewTargetFor(state);
                          if (!reviewTarget) setReviewTarget(target);
                          void beginScreenInteraction("review", {
                            kind: "review",
                            gameId: target.gameId,
                            nodeId: target.nodeId,
                          });
                          setNote(event.currentTarget.value);
                        }}
                        autosize
                        minRows={3}
                      />
                      <Group justify="space-between">
                        <Checkbox
                          size="xs"
                          label="重要な局面"
                          checked={important}
                          onChange={(event) => {
                            const target =
                              reviewTarget ?? reviewTargetFor(state);
                            if (!reviewTarget) setReviewTarget(target);
                            void beginScreenInteraction("review", {
                              kind: "review",
                              gameId: target.gameId,
                              nodeId: target.nodeId,
                            });
                            setImportant(event.currentTarget.checked);
                          }}
                        />
                        <Button
                          size="compact-sm"
                          loading={savingReview}
                          disabled={interactionBusy}
                          onClick={saveReview}
                        >
                          保存
                        </Button>
                      </Group>
                    </Stack>
                  </ScrollArea>
                </Paper>
              </section>
            </main>
          ) : (
            <Paper withBorder p="xl" className="empty-state">
              <Text ta="center" c="dimmed">
                {restoring
                  ? "前回の対局を復元しています。"
                  : "「新しい対局」から条件を選ぶか、SGFまたはJSONを読み込んでください。"}
              </Text>
            </Paper>
          )}
        </div>
      </div>
    </ScaleToFit>
  );
}

function EvaluationChart({
  nodes,
  currentNodeId,
  mainLineNodeIds,
  history,
  disabled,
  onSelect,
}: {
  nodes: GameResponse["game"]["treeNodes"];
  currentNodeId: string;
  mainLineNodeIds: Set<string>;
  history: Record<
    string,
    Pick<PositionAnalysis, "scoreLead" | "winrate" | "visits">
  >;
  disabled: boolean;
  onSelect: (nodeId: string) => void;
}) {
  const children = new Map<string, string>();
  for (const node of nodes) {
    if (node.parentId && mainLineNodeIds.has(node.id))
      children.set(node.parentId, node.id);
  }
  const mainLinePath = ["root"];
  while (true) {
    const child = children.get(mainLinePath[mainLinePath.length - 1]);
    if (!child) break;
    mainLinePath.push(child);
  }
  const path = mainLineNodeIds.has(currentNodeId)
    ? mainLinePath
    : extendSelectedVariation(nodes, treePathToNode(nodes, currentNodeId));
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  let playedMoves = 0;
  const moveNumbers = path.map((nodeId) => {
    if (nodeById.get(nodeId)?.move) playedMoves++;
    return playedMoves;
  });
  const scorePoints = path
    .map((nodeId, move) => ({
      nodeId,
      move,
      value: history[nodeId]?.scoreLead,
    }))
    .filter(
      (point): point is typeof point & { value: number } =>
        typeof point.value === "number",
    );
  const winratePoints = path
    .map((nodeId, move) => ({ nodeId, move, value: history[nodeId]?.winrate }))
    .filter(
      (point): point is typeof point & { value: number } =>
        typeof point.value === "number",
    );
  if (scorePoints.length < 2 && winratePoints.length < 2)
    return (
      <div className="evaluation-placeholder">
        <Text size="xs" c="dimmed">
          局面を進めると、黒の目差と勝率の推移をここに表示します。
        </Text>
      </div>
    );
  const width = 480,
    height = 104,
    left = 28,
    right = 30,
    top = 8,
    bottom = 22;
  const maximum = Math.max(
    1,
    ...scorePoints.map((point) => Math.abs(point.value)),
  );
  const lastMove = Math.max(1, path.length - 1);
  const x = (move: number) => left + (move / lastMove) * (width - left - right);
  const plotHeight = height - top - bottom;
  const y = (value: number) =>
    top + plotHeight / 2 - (value / maximum) * (plotHeight / 2);
  const winrateY = (value: number) => top + (1 - value) * plotHeight;
  const tickStep = Math.max(1, Math.ceil(lastMove / 6));
  const ticks = Array.from(
    { length: Math.floor(lastMove / tickStep) + 1 },
    (_, index) => index * tickStep,
  );
  if (ticks[ticks.length - 1] !== lastMove) ticks.push(lastMove);
  const currentMove = path.indexOf(currentNodeId);
  const selectMoveAt = (clientX: number, element: SVGSVGElement) => {
    if (disabled) return;
    const bounds = element.getBoundingClientRect();
    const chartX = ((clientX - bounds.left) / bounds.width) * width;
    const move = Math.round(
      ((Math.min(width - right, Math.max(left, chartX)) - left) /
        (width - left - right)) *
        lastMove,
    );
    onSelect(path[move]);
  };
  const selectMoveByKey = (key: string) => {
    if (disabled) return;
    const move = Math.max(currentMove, 0);
    if (key === "ArrowLeft") onSelect(path[Math.max(0, move - 1)]);
    else if (key === "ArrowRight") onSelect(path[Math.min(lastMove, move + 1)]);
    else if (key === "Home") onSelect(path[0]);
    else if (key === "End") onSelect(path[lastMove]);
    else return;
  };
  return (
    // biome-ignore-start lint/a11y/useSemanticElements: SVG全体を1つのグラフ操作面として扱う。
    // biome-ignore-start lint/a11y/noNoninteractiveElementToInteractiveRole: SVG全体を1つのグラフ操作面として扱う。
    <svg
      className={`evaluation-chart${disabled ? " is-disabled" : ""}`}
      viewBox={`0 0 ${width} ${height}`}
      // クリックと矢印キーで、手順のどの局面かを選ぶ部品なので、sliderとして示す
      role="slider"
      tabIndex={disabled ? -1 : 0}
      aria-disabled={disabled}
      aria-valuemin={0}
      aria-valuemax={path.length - 1}
      aria-valuenow={Math.max(currentMove, 0)}
      aria-valuetext={`${moveNumbers[Math.max(currentMove, 0)]}手目`}
      aria-label="黒の目差と勝率の推移。クリックまたは左右矢印キーで局面を移動できます"
      onClick={(event) => selectMoveAt(event.clientX, event.currentTarget)}
      onKeyDown={(event) => {
        if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key))
          event.preventDefault();
        selectMoveByKey(event.key);
      }}
    >
      {/* biome-ignore-end lint/a11y/noNoninteractiveElementToInteractiveRole: SVGの開始タグだけを対象にする。 */}
      {/* biome-ignore-end lint/a11y/useSemanticElements: SVGの開始タグだけを対象にする。 */}
      <line
        x1={left}
        y1={y(0)}
        x2={width - right}
        y2={y(0)}
        className="evaluation-zero"
      />
      {ticks.map((move) => (
        <g key={move}>
          <line
            x1={x(move)}
            y1={height - bottom}
            x2={x(move)}
            y2={height - bottom + 4}
            className="evaluation-tick"
          />
          <text
            x={x(move)}
            y={height - 5}
            textAnchor="middle"
            className="evaluation-label"
          >
            {moveNumbers[move]}手
          </text>
        </g>
      ))}
      <text
        x={left - 4}
        y={top + 5}
        textAnchor="end"
        className="evaluation-label"
      >
        +{maximum.toFixed(0)}
      </text>
      <text
        x={left - 4}
        y={y(0) + 3}
        textAnchor="end"
        className="evaluation-label"
      >
        0
      </text>
      <text
        x={left - 4}
        y={height - bottom}
        textAnchor="end"
        className="evaluation-label"
      >
        -{maximum.toFixed(0)}
      </text>
      <text
        x={width - right + 4}
        y={top + 5}
        textAnchor="start"
        className="evaluation-label"
      >
        100%
      </text>
      <text
        x={width - right + 4}
        y={winrateY(0.5) + 3}
        textAnchor="start"
        className="evaluation-label"
      >
        50%
      </text>
      <text
        x={width - right + 4}
        y={height - bottom}
        textAnchor="start"
        className="evaluation-label"
      >
        0%
      </text>
      <g>
        <line
          x1={left + 6}
          y1={top + 5}
          x2={left + 19}
          y2={top + 5}
          className="evaluation-score-line"
        />
        <text x={left + 23} y={top + 8} className="evaluation-label">
          目差
        </text>
        <line
          x1={left + 54}
          y1={top + 5}
          x2={left + 67}
          y2={top + 5}
          className="evaluation-winrate-line"
        />
        <text x={left + 71} y={top + 8} className="evaluation-label">
          勝率
        </text>
      </g>
      {currentMove >= 0 && (
        <line
          x1={x(currentMove)}
          y1={top}
          x2={x(currentMove)}
          y2={height - bottom}
          className="evaluation-current-line"
        >
          <title>現在: {moveNumbers[currentMove]}手目</title>
        </line>
      )}
      {scorePoints.length >= 2 && (
        <polyline
          points={scorePoints
            .map((point) => `${x(point.move)},${y(point.value)}`)
            .join(" ")}
          className="evaluation-score-line"
        />
      )}
      {winratePoints.length >= 2 && (
        <polyline
          points={winratePoints
            .map((point) => `${x(point.move)},${winrateY(point.value)}`)
            .join(" ")}
          className="evaluation-winrate-line"
        />
      )}
      {scorePoints.map((point) => (
        <circle
          key={`score-${point.nodeId}`}
          cx={x(point.move)}
          cy={y(point.value)}
          r={point.nodeId === currentNodeId ? 4 : 2.5}
          className={
            point.nodeId === currentNodeId
              ? "evaluation-score-current"
              : "evaluation-score-point"
          }
        >
          <title>
            {moveNumbers[point.move]}手目: 黒の目差{" "}
            {point.value >= 0 ? "+" : ""}
            {point.value.toFixed(1)}目（クリックで移動）
          </title>
        </circle>
      ))}
      {winratePoints.map((point) => (
        <circle
          key={`winrate-${point.nodeId}`}
          cx={x(point.move)}
          cy={winrateY(point.value)}
          r={point.nodeId === currentNodeId ? 4 : 2.5}
          className={
            point.nodeId === currentNodeId
              ? "evaluation-winrate-current"
              : "evaluation-winrate-point"
          }
        >
          <title>
            {moveNumbers[point.move]}手目: 黒の勝率{" "}
            {(point.value * 100).toFixed(1)}
            %（クリックで移動）
          </title>
        </circle>
      ))}
    </svg>
  );
}

function pickEvaluation(analysis: PositionAnalysis) {
  return {
    scoreLead: analysis.scoreLead,
    winrate: analysis.winrate,
    visits: analysis.visits,
  };
}

function GameTree({
  nodes,
  currentNodeId,
  mainLineNodeIds,
  disabled,
  onSelect,
  onDelete,
  onPromote,
}: {
  nodes: GameTreeNode[];
  currentNodeId: string;
  mainLineNodeIds: Set<string>;
  disabled: boolean;
  onSelect: (nodeId: string) => void;
  onDelete: (nodeId: string) => void;
  onPromote: (nodeId: string) => void;
}) {
  const currentRef = useRef<HTMLButtonElement | null>(null);
  const [contextMenu, setContextMenu] = useState<{
    nodeId: string;
    x: number;
    y: number;
  } | null>(null);
  const layout = useMemo(
    () => layoutGameTree(nodes, mainLineNodeIds),
    [nodes, mainLineNodeIds],
  );

  // biome-ignore lint/correctness/useExhaustiveDependencies: 現在の局面が変わったときだけ、その局面までスクロールする
  useEffect(() => {
    currentRef.current?.scrollIntoView({ block: "center", inline: "center" });
  }, [currentNodeId]);

  return (
    <>
      <ScrollArea
        className="game-tree-scroll"
        type="auto"
        offsetScrollbars
        onClick={() => setContextMenu(null)}
      >
        <div
          className="game-tree-canvas"
          style={{ width: layout.width, height: layout.height }}
        >
          <svg
            className="game-tree-lines"
            width={layout.width}
            height={layout.height}
            aria-hidden="true"
          >
            {layout.items.map((item) => {
              const parent = item.parent;
              if (!parent) return null;
              return (
                <path
                  key={item.node.id}
                  className={item.isMainLine ? "is-main-line" : "is-variation"}
                  d={`M ${parent.x} ${parent.y} C ${(parent.x + item.x) / 2} ${parent.y}, ${(parent.x + item.x) / 2} ${item.y}, ${item.x} ${item.y}`}
                />
              );
            })}
          </svg>
          {layout.items.map((item) => {
            const selected = item.node.id === currentNodeId;
            const coordinate =
              item.node.move?.coordinate ??
              (item.node.result?.type === "resignation"
                ? "投了"
                : item.node.result
                  ? "終局"
                  : item.depth === 0
                    ? "開始"
                    : "着手なし");
            const color =
              item.node.move?.color ??
              (item.node.result && "loser" in item.node.result
                ? item.node.result.loser
                : undefined);
            const details = [
              item.node.origin === "agent"
                ? `AI agent${item.node.variationTitle ? `: ${item.node.variationTitle}` : ""}`
                : "",
              item.node.important ? "重要な局面" : "",
            ]
              .filter(Boolean)
              .join("・");
            const description = `${item.depth === 0 ? "開始局面" : item.node.move ? `${item.moveNumber}手目 ${color === "B" ? "黒" : "白"}${coordinate}` : `${item.moveNumber === 0 ? "開始局面" : `${item.moveNumber}手目`}の後・${item.node.result?.type === "resignation" ? `${color === "B" ? "黒" : "白"}の` : ""}${coordinate}`}${details ? `・${details}` : ""}`;
            return (
              <button
                key={item.node.id}
                ref={selected ? currentRef : undefined}
                type="button"
                className={`game-tree-node${selected ? " is-current" : ""}${item.isMainLine ? " is-main-line" : " is-variation"}${item.node.highlighted ? " is-highlighted" : ""}${color === "B" ? " is-black" : color === "W" ? " is-white" : ""}`}
                aria-current={selected ? "true" : undefined}
                aria-label={description}
                style={{ left: item.x, top: item.y }}
                disabled={disabled}
                title={description}
                onClick={() => {
                  if (!selected) onSelect(item.node.id);
                }}
                onContextMenu={(event) => {
                  event.preventDefault();
                  setContextMenu({
                    nodeId: item.node.id,
                    x: event.clientX,
                    y: event.clientY,
                  });
                }}
              >
                <span className="game-tree-stone">
                  {item.node.move ? item.moveNumber : ""}
                </span>
                <span className="game-tree-label">
                  {coordinate}
                  {item.node.important ? " ★" : ""}
                </span>
              </button>
            );
          })}
        </div>
      </ScrollArea>
      <Menu
        opened={contextMenu !== null}
        onChange={(opened) => {
          if (!opened) setContextMenu(null);
        }}
        position="bottom-start"
        shadow="md"
        width={190}
      >
        <Menu.Target>
          <span
            className="game-tree-context-target"
            style={{ left: contextMenu?.x ?? 0, top: contextMenu?.y ?? 0 }}
          />
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Item
            disabled={!contextMenu || mainLineNodeIds.has(contextMenu.nodeId)}
            onClick={() => {
              if (contextMenu) onPromote(contextMenu.nodeId);
              setContextMenu(null);
            }}
          >
            こちらを主分岐に
          </Menu.Item>
          <Menu.Divider />
          <Menu.Item
            color="red"
            leftSection={<IconTrash size={14} />}
            disabled={!contextMenu || contextMenu.nodeId === "root"}
            onClick={() => {
              if (contextMenu) onDelete(contextMenu.nodeId);
              setContextMenu(null);
            }}
          >
            この手以降を削除
          </Menu.Item>
        </Menu.Dropdown>
      </Menu>
    </>
  );
}
