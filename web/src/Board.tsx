import { useElementSize } from "@mantine/hooks";
import {
  createGoban,
  type GobanRenderer,
  type JGOFMove,
  JGOFNumericPlayerColor,
  setGobanRenderer,
} from "goban";
import { useCallback, useEffect, useRef } from "react";
import {
  candidateColor,
  candidateScoreLead,
  candidateScoreLoss,
  candidateWinrate,
  formatScoreLead,
  formatVisits,
  formatWinrate,
} from "./analysisDisplay";
import type { Game } from "./api";

type Props = {
  game: Game;
  ownership?: number[];
  ownershipIntensity?: number;
  candidates?: Array<{
    move: string;
    visits: number;
    scoreLead: number;
    winrate: number;
  }>;
  disabled: boolean;
  onPlay: (coordinate: string) => void;
};

const columns = "ABCDEFGHJKLMNOPQRST";

setGobanRenderer("svg");

function toJgofMove(
  coordinate: string,
  color: "B" | "W",
  boardSize: number,
): JGOFMove {
  const stoneColor =
    color === "B" ? JGOFNumericPlayerColor.BLACK : JGOFNumericPlayerColor.WHITE;
  if (coordinate === "pass") return { x: -1, y: -1, color: stoneColor };

  return {
    x: columns.indexOf(coordinate[0]),
    y: boardSize - Number(coordinate.slice(1)),
    color: stoneColor,
  };
}

function toGtpCoordinate(x: number, y: number, boardSize: number): string {
  return `${columns[x]}${boardSize - y}`;
}

type Intersection = { x: number; y: number };

type HoverableRenderer = GobanRenderer & {
  last_hover_square?: Intersection;
  drawSquare: (x: number, y: number) => void;
};

function showHoverStone(
  renderer: GobanRenderer,
  intersection: Intersection | null,
) {
  const hoverable = renderer as HoverableRenderer;
  const previous = hoverable.last_hover_square;
  if (intersection) hoverable.last_hover_square = intersection;
  else delete hoverable.last_hover_square;
  if (previous) hoverable.drawSquare(previous.x, previous.y);
  if (intersection) hoverable.drawSquare(intersection.x, intersection.y);
}

function initialKeyboardIntersection(game: Game): Intersection {
  const lastMove = game.moves.at(-1);
  if (lastMove && lastMove.coordinate !== "pass") {
    const move = toJgofMove(
      lastMove.coordinate,
      lastMove.color,
      game.boardSize,
    );
    return { x: move.x, y: move.y };
  }
  const center = Math.floor(game.boardSize / 2);
  return { x: center, y: center };
}

function encodeSetup(game: Game, color: "B" | "W"): string {
  return Object.entries(game.setupStones)
    .filter(([, stoneColor]) => stoneColor === color)
    .map(([coordinate]) => {
      const x = columns.indexOf(coordinate[0]);
      const y = game.boardSize - Number(coordinate.slice(1));
      return String.fromCharCode(97 + x, 97 + y);
    })
    .join("");
}

function correctScaledMouseEvents(element: HTMLElement) {
  const eventTypes = [
    "mousedown",
    "mouseup",
    "mousemove",
    "mouseout",
    "click",
    "dblclick",
    "contextmenu",
  ] as const;
  const handle = (event: MouseEvent) => {
    if (!event.isTrusted) return;
    const rect = element.getBoundingClientRect();
    const scaleX = rect.width / element.offsetWidth;
    const scaleY = rect.height / element.offsetHeight;
    if (
      !Number.isFinite(scaleX) ||
      !Number.isFinite(scaleY) ||
      (Math.abs(scaleX - 1) < 0.001 && Math.abs(scaleY - 1) < 0.001)
    )
      return;
    const target = event.target;
    if (!(target instanceof Element)) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    target.dispatchEvent(
      new MouseEvent(event.type, {
        bubbles: true,
        cancelable: true,
        composed: true,
        view: window,
        detail: event.detail,
        screenX: event.screenX,
        screenY: event.screenY,
        clientX: rect.left + (event.clientX - rect.left) / scaleX,
        clientY: rect.top + (event.clientY - rect.top) / scaleY,
        ctrlKey: event.ctrlKey,
        shiftKey: event.shiftKey,
        altKey: event.altKey,
        metaKey: event.metaKey,
        button: event.button,
        buttons: event.buttons,
        relatedTarget: event.relatedTarget,
      }),
    );
  };
  for (const eventType of eventTypes)
    element.addEventListener(eventType, handle, true);
  return () => {
    for (const eventType of eventTypes)
      element.removeEventListener(eventType, handle, true);
  };
}

export function Board({
  game,
  ownership,
  ownershipIntensity = 50,
  candidates = [],
  disabled,
  onPlay,
}: Props) {
  const { ref: resizeRef, width } = useElementSize<HTMLDivElement>();
  const boardRef = useRef<HTMLDivElement | null>(null);
  const rendererRef = useRef<GobanRenderer | null>(null);
  const keyboardIntersectionRef = useRef<Intersection | null>(null);
  const onPlayRef = useRef(onPlay);
  onPlayRef.current = onPlay;
  const setBoardRef = useCallback(
    (node: HTMLDivElement | null) => {
      boardRef.current = node;
      resizeRef(node);
    },
    [resizeRef],
  );

  useEffect(() => {
    if (!boardRef.current || width < 1) return;
    const boardElement = boardRef.current;

    const initialMoveCount = game.moves.length;
    const renderer: GobanRenderer = createGoban({
      board_div: boardElement,
      width: game.boardSize,
      height: game.boardSize,
      display_width: Math.floor(width),
      moves: game.moves.map((move) =>
        toJgofMove(move.coordinate, move.color, game.boardSize),
      ),
      initial_state: {
        black: encodeSetup(game, "B"),
        white: encodeSetup(game, "W"),
      },
      initial_player: game.initialPlayer === "W" ? "white" : "black",
      // 置石はinitial_stateに含まれる。置石数も渡すとgobanが追加配置を待ち、白の初手後に手番が変わらない。
      handicap: 0,
      rules: "chinese",
      komi: game.komi,
      mode: "analyze",
      interactive: !disabled,
      draw_top_labels: true,
      draw_left_labels: true,
      draw_bottom_labels: true,
      draw_right_labels: true,
    });

    if (disabled) renderer.disableStonePlacement();
    const removeScaledMouseCorrection = correctScaledMouseEvents(boardElement);

    const deadStones = new Set(game.result?.deadStones ?? []);
    const coloredCircles =
      ownership?.length === game.boardSize * game.boardSize
        ? ownership.flatMap((value, index) => {
            const strength = Math.abs(value);
            if (strength < 0.03) return [];
            if (
              deadStones.has(
                toGtpCoordinate(
                  index % game.boardSize,
                  Math.floor(index / game.boardSize),
                  game.boardSize,
                ),
              )
            )
              return [];
            const alpha = Math.min(
              0.95,
              strength * (ownershipIntensity / 100) * 0.9,
            );
            return [
              {
                move: {
                  x: index % game.boardSize,
                  y: Math.floor(index / game.boardSize),
                },
                // KataGoのownershipは常に黒視点であり、正が黒、負が白を示す。
                color:
                  value > 0
                    ? `rgba(0, 0, 0, ${alpha})`
                    : `rgba(255, 255, 255, ${alpha})`,
                border_width: 0,
              },
            ];
          })
        : [];

    const visibleCandidates = candidates.slice(0, 5);
    const maximumVisits = Math.max(
      1,
      ...visibleCandidates.map((candidate) => candidate.visits),
    );
    for (const candidate of visibleCandidates) {
      if (candidate.move === "pass") continue;
      const move = toJgofMove(
        candidate.move,
        game.currentPlayer,
        game.boardSize,
      );
      const loss = candidateScoreLoss(visibleCandidates[0], candidate);
      const visitRatio = candidate.visits / maximumVisits;
      const alpha = 0.5 + Math.sqrt(visitRatio) * 0.45;
      const existing = coloredCircles.findIndex(
        (circle) => circle.move.x === move.x && circle.move.y === move.y,
      );
      const candidateCircle = {
        move: { x: move.x, y: move.y },
        color: candidateColor(loss, alpha),
        border_color: candidateColor(loss),
        border_width: loss < 0.5 ? 0.13 : 0.07,
      };
      if (existing >= 0) coloredCircles[existing] = candidateCircle;
      else coloredCircles.push(candidateCircle);
    }
    renderer.setColoredCircles(coloredCircles, true);
    for (const coordinate of game.result?.deadStones ?? []) {
      const move = toJgofMove(coordinate, "B", game.boardSize);
      renderer.setForRemoval(move.x, move.y, true, false);
      renderer.setMark(
        move.x,
        move.y,
        game.stones[coordinate] === "B" ? "score-white" : "score-black",
        true,
      );
    }
    renderer.redraw(true);

    const svgRenderer = renderer as GobanRenderer & {
      cells?: Array<Array<{ g: SVGGElement }>>;
      metrics?: { mid: number };
      square_size?: number;
    };
    if (svgRenderer.cells && svgRenderer.metrics && svgRenderer.square_size) {
      for (const candidate of visibleCandidates) {
        if (candidate.move === "pass") continue;
        const move = toJgofMove(
          candidate.move,
          game.currentPlayer,
          game.boardSize,
        );
        const group = svgRenderer.cells[move.y]?.[move.x]?.g;
        if (!group) continue;
        const text = document.createElementNS(
          "http://www.w3.org/2000/svg",
          "text",
        );
        const mid = svgRenderer.metrics.mid;
        const lineHeight = svgRenderer.square_size * 0.2;
        const loss = candidateScoreLoss(visibleCandidates[0], candidate);
        const lightText = loss >= 6;
        text.setAttribute("x", String(mid));
        text.setAttribute("text-anchor", "middle");
        text.setAttribute("dominant-baseline", "central");
        text.setAttribute("font-size", String(svgRenderer.square_size * 0.18));
        text.setAttribute("font-weight", "700");
        text.setAttribute("fill", lightText ? "white" : "#111");
        text.setAttribute(
          "stroke",
          lightText ? "rgba(0, 0, 0, 0.7)" : "rgba(255, 255, 255, 0.7)",
        );
        text.setAttribute(
          "stroke-width",
          String(svgRenderer.square_size * 0.02),
        );
        text.setAttribute("paint-order", "stroke");
        text.setAttribute("pointer-events", "none");
        const lines = [
          formatWinrate(candidateWinrate(candidate)),
          `v${formatVisits(candidate.visits)}`,
          `${formatScoreLead(candidateScoreLead(candidate))}目`,
        ];
        lines.forEach((line, index) => {
          const span = document.createElementNS(
            "http://www.w3.org/2000/svg",
            "tspan",
          );
          span.setAttribute("x", String(mid));
          span.setAttribute("y", String(mid + (index - 1) * lineHeight));
          span.textContent = line;
          text.appendChild(span);
        });
        group.appendChild(text);
      }
    }

    const lastMove = game.moves.at(-1);
    if (lastMove && lastMove.coordinate !== "pass") {
      const marker = toJgofMove(
        lastMove.coordinate,
        lastMove.color,
        game.boardSize,
      );
      renderer.setMark(marker.x, marker.y, "circle");
    }

    const handleUpdate = () => {
      const move = renderer.engine.cur_move;
      if (move.move_number <= initialMoveCount || move.x < 0 || move.y < 0)
        return;
      renderer.disableStonePlacement();
      onPlayRef.current(toGtpCoordinate(move.x, move.y, game.boardSize));
    };

    renderer.on("update", handleUpdate);
    rendererRef.current = renderer;
    const keyboardIntersection = keyboardIntersectionRef.current;
    if (keyboardIntersection) {
      const last = game.boardSize - 1;
      keyboardIntersectionRef.current = {
        x: Math.min(last, keyboardIntersection.x),
        y: Math.min(last, keyboardIntersection.y),
      };
      if (!disabled && boardElement.matches(":focus-visible"))
        showHoverStone(renderer, keyboardIntersectionRef.current);
    }
    return () => {
      rendererRef.current = null;
      removeScaledMouseCorrection();
      renderer.off("update", handleUpdate);
      renderer.destroy();
      boardElement.replaceChildren();
    };
  }, [candidates, disabled, game, ownership, ownershipIntensity, width]);

  const moveKeyboardIntersection = (dx: number, dy: number) => {
    const current =
      keyboardIntersectionRef.current ?? initialKeyboardIntersection(game);
    const last = game.boardSize - 1;
    const next = keyboardIntersectionRef.current
      ? {
          x: Math.min(last, Math.max(0, current.x + dx)),
          y: Math.min(last, Math.max(0, current.y + dy)),
        }
      : current;
    keyboardIntersectionRef.current = next;
    if (rendererRef.current) showHoverStone(rendererRef.current, next);
  };

  return (
    <div
      className="goban"
      ref={setBoardRef}
      role="application"
      tabIndex={disabled ? -1 : 0}
      aria-disabled={disabled}
      aria-label={`${game.boardSize}路盤。矢印キーで交点を選び、EnterキーかSpaceキーで着手します`}
      onFocus={(event) => {
        if (!event.currentTarget.matches(":focus-visible")) {
          // マウスで押したときは、盤がフォーカスを受けない場合と同じく選択中の要素を外すだけにする。
          event.currentTarget.blur();
          return;
        }
        if (!disabled) moveKeyboardIntersection(0, 0);
      }}
      onBlur={() => {
        if (rendererRef.current) showHoverStone(rendererRef.current, null);
      }}
      onKeyDown={(event) => {
        if (disabled || event.altKey || event.ctrlKey || event.metaKey) return;
        const direction: Record<string, [number, number]> = {
          ArrowLeft: [-1, 0],
          ArrowRight: [1, 0],
          ArrowUp: [0, -1],
          ArrowDown: [0, 1],
        };
        if (event.key in direction) {
          event.preventDefault();
          const [dx, dy] = direction[event.key];
          moveKeyboardIntersection(dx, dy);
          return;
        }
        if (event.key !== "Enter" && event.key !== " ") return;
        event.preventDefault();
        const intersection = keyboardIntersectionRef.current;
        const renderer = rendererRef.current;
        if (!intersection) {
          moveKeyboardIntersection(0, 0);
          return;
        }
        if (renderer) {
          renderer.disableStonePlacement();
          showHoverStone(renderer, null);
        }
        onPlayRef.current(
          toGtpCoordinate(intersection.x, intersection.y, game.boardSize),
        );
      }}
    />
  );
}
