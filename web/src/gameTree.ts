import type { GameTreeNode } from "./api";

export function treePathToNode(nodes: GameTreeNode[], nodeId: string) {
  const byId = new Map(nodes.map((node) => [node.id, node]));
  const path: string[] = [];
  let current: string | undefined = nodeId;
  while (current) {
    path.unshift(current);
    current = byId.get(current)?.parentId;
  }
  return path.includes("root") ? path : ["root", ...path];
}

export function variationSubtreeNodeIds(nodes: GameTreeNode[], nodeId: string) {
  const children = new Map<string, string[]>();
  for (const node of nodes) {
    if (!node.parentId) continue;
    const values = children.get(node.parentId) ?? [];
    values.push(node.id);
    children.set(node.parentId, values);
  }
  const result: string[] = [];
  const pending = [nodeId];
  while (pending.length > 0) {
    const current = pending.shift();
    if (!current) continue;
    result.push(current);
    pending.push(...(children.get(current) ?? []));
  }
  return result;
}

function orderedChildren(
  nodes: GameTreeNode[],
  parentId: string,
  mainLineNodeIds: Set<string>,
) {
  return nodes
    .filter((node) => node.parentId === parentId)
    .sort(
      (first, second) =>
        Number(mainLineNodeIds.has(second.id)) -
          Number(mainLineNodeIds.has(first.id)) ||
        first.id.localeCompare(second.id, undefined, { numeric: true }),
    );
}

export function extendSelectedVariation(
  nodes: GameTreeNode[],
  selectedPath: string[],
) {
  const path = [...selectedPath];
  const included = new Set(path);
  let current = path.at(-1) ?? "root";
  while (true) {
    const continuation = orderedChildren(nodes, current, new Set()).find(
      (node) => !included.has(node.id),
    );
    if (!continuation) break;
    path.push(continuation.id);
    included.add(continuation.id);
    current = continuation.id;
  }
  return path;
}

type TreeLayoutItem = {
  node: GameTreeNode;
  depth: number;
  moveNumber: number;
  lane: number;
  x: number;
  y: number;
  parent?: { x: number; y: number };
  isMainLine: boolean;
};

export function layoutGameTree(
  nodes: GameTreeNode[],
  mainLineNodeIds: Set<string>,
) {
  const depthSpacing = 72;
  const laneSpacing = 54;
  const leftPadding = 40;
  const rightPadding = 68;
  const byId = new Map(nodes.map((node) => [node.id, node]));
  const root = byId.get("root") ?? ({ id: "root" } as GameTreeNode);
  const items: TreeLayoutItem[] = [];
  let nextLane = 1;
  let maxDepth = 0;
  let maxLane = 0;
  const visit = (
    node: GameTreeNode,
    depth: number,
    lane: number,
    parent?: TreeLayoutItem,
  ) => {
    const item: TreeLayoutItem = {
      node,
      depth,
      moveNumber: (parent?.moveNumber ?? 0) + (node.move ? 1 : 0),
      lane,
      x: leftPadding + depth * depthSpacing,
      y: 34 + lane * laneSpacing,
      parent: parent ? { x: parent.x, y: parent.y } : undefined,
      isMainLine: mainLineNodeIds.has(node.id),
    };
    items.push(item);
    maxDepth = Math.max(maxDepth, depth);
    maxLane = Math.max(maxLane, lane);
    orderedChildren(nodes, node.id, mainLineNodeIds).forEach((child, index) => {
      visit(child, depth + 1, index === 0 ? lane : nextLane++, item);
    });
  };
  visit(root, 0, 0);
  return {
    items,
    width: leftPadding + rightPadding + maxDepth * depthSpacing,
    height: 78 + maxLane * laneSpacing,
  };
}

export function adjacentBranchNodeId(
  nodes: GameTreeNode[],
  currentNodeId: string,
  mainLineNodeIds: Set<string>,
  direction: -1 | 1,
) {
  const layout = layoutGameTree(nodes, mainLineNodeIds);
  const current = layout.items.find((item) => item.node.id === currentNodeId);
  if (!current) return undefined;
  const sameDepth = layout.items
    .filter((item) => item.depth === current.depth)
    .sort((first, second) => first.lane - second.lane);
  const currentIndex = sameDepth.findIndex(
    (item) => item.node.id === currentNodeId,
  );
  return sameDepth[currentIndex + direction]?.node.id;
}

export function nextTreeNodeId(
  nodes: GameTreeNode[],
  currentNodeId: string,
  mainLineNodeIds: Set<string>,
) {
  return orderedChildren(nodes, currentNodeId, mainLineNodeIds).filter(
    (node) =>
      !mainLineNodeIds.has(currentNodeId) || mainLineNodeIds.has(node.id),
  )[0]?.id;
}

export function walkTree(
  nodes: GameTreeNode[],
  currentNodeId: string,
  mainLineNodeIds: Set<string>,
  direction: "back" | "forward",
  steps: number,
) {
  const byId = new Map(nodes.map((node) => [node.id, node]));
  let destination = currentNodeId;
  for (let index = 0; index < steps; index++) {
    const next =
      direction === "back"
        ? byId.get(destination)?.parentId
        : nextTreeNodeId(nodes, destination, mainLineNodeIds);
    if (!next) break;
    destination = next;
  }
  return destination;
}
