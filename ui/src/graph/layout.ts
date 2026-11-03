// layout places a conversation's lines for the graph view: left to right in
// columns by distance from the start line, ordered within each column to
// cross as few edges as it cheaply can. It is pure, so it is easy to reason
// about; Graph.tsx draws what it returns.
import type { Graph } from "../api/client";

export const BOX_W = 220;
export const BOX_H = 84;
// Wide enough for a choice's label between columns.
const GAP_X = 200;
const GAP_Y = 36;
// Room at the edges for loops back to the first column.
const PAD = 90;
export const END_R = 16;

export type Kind = "next" | "fail";

export type Box = {
  id: string;
  x: number; // top-left
  y: number;
  column: number;
  reachable: boolean;
};

export type Edge = {
  from: string;
  to: string; // a node id, or "end:<node id>" for a choice that ends the conversation
  choiceId: string;
  kind: Kind;
  back: boolean; // leads to the same column or an earlier one: drawn as a loop
};

export type End = { id: string; x: number; y: number }; // centre

export type Layout = { boxes: Map<string, Box>; ends: Map<string, End>; edges: Edge[]; width: number; height: number };

export function layout(g: Graph): Layout {
  const ids = g.nodes.map((n) => n.id);
  const live = new Set(ids);
  const out = new Map<string, string[]>(ids.map((id) => [id, []]));
  const edges: Omit<Edge, "back">[] = [];
  for (const n of g.nodes) {
    for (const c of n.choices) {
      const targets: [string, Kind][] = [[c.toNodeId, "next"]];
      if (c.checkSkillId) targets.push([c.failNodeId, "fail"]);
      for (const [to, kind] of targets) {
        // A target that is missing (deleted) or empty ends the conversation.
        const target = to && live.has(to) ? to : "end:" + n.id;
        edges.push({ from: n.id, to: target, choiceId: c.id, kind });
        if (live.has(target)) out.get(n.id)!.push(target);
      }
    }
  }

  // Columns: breadth-first distance from the start. Lines no route reaches
  // go in a last column of their own, marked unreachable.
  const column = new Map<string, number>();
  const start = g.conversation.startNodeId;
  if (live.has(start)) {
    column.set(start, 0);
    const queue = [start];
    while (queue.length) {
      const id = queue.shift()!;
      for (const to of out.get(id)!) {
        if (!column.has(to)) {
          column.set(to, column.get(id)! + 1);
          queue.push(to);
        }
      }
    }
  }
  const reached = column.size;
  const lastReached = Math.max(-1, ...column.values());
  for (const id of ids) if (!column.has(id)) column.set(id, lastReached + 1);

  // Order within columns: start in authoring order, then a few sweeps
  // placing each line at the average position of the lines that lead to
  // it (and, on the way back, of the lines it leads to).
  const columns: string[][] = [];
  for (const id of ids) (columns[column.get(id)!] ??= []).push(id);
  const preds = new Map<string, string[]>(ids.map((id) => [id, []]));
  for (const [from, tos] of out) for (const to of tos) if (column.get(to)! > column.get(from)!) preds.get(to)!.push(from);
  const succs = new Map<string, string[]>(ids.map((id) => [id, out.get(id)!.filter((to) => column.get(to)! > column.get(id)!)]));
  const pos = new Map<string, number>();
  const index = () => columns.forEach((col) => col.forEach((id, i) => pos.set(id, i)));
  index();
  const sweep = (neighbours: Map<string, string[]>, order: number[]) => {
    for (const c of order) {
      const col = columns[c];
      if (!col) continue;
      const weight = (id: string) => {
        const ns = neighbours.get(id)!;
        return ns.length ? ns.reduce((s, n) => s + pos.get(n)!, 0) / ns.length : pos.get(id)!;
      };
      col.sort((a, b) => weight(a) - weight(b));
      col.forEach((id, i) => pos.set(id, i));
    }
  };
  const forward = columns.map((_, i) => i).slice(1);
  for (let i = 0; i < 4; i++) {
    sweep(preds, forward);
    sweep(succs, [...forward].reverse().map((c) => c - 1));
  }

  // Coordinates: columns side by side, each centred on the tallest.
  const tallest = Math.max(1, ...columns.map((c) => c?.length ?? 0));
  const boxes = new Map<string, Box>();
  columns.forEach((col, c) => {
    const offset = ((tallest - col.length) * (BOX_H + GAP_Y)) / 2;
    col.forEach((id, i) => {
      boxes.set(id, {
        id,
        column: c,
        x: PAD + c * (BOX_W + GAP_X),
        y: PAD + offset + i * (BOX_H + GAP_Y),
        reachable: reached === 0 || c <= lastReached,
      });
    });
  });

  // An end marker sits just right of the line whose choices end there.
  const ends = new Map<string, End>();
  for (const e of edges) {
    if (live.has(e.to) || ends.has(e.to)) continue;
    const b = boxes.get(e.from)!;
    ends.set(e.to, { id: e.to, x: b.x + BOX_W + GAP_X - END_R * 2, y: b.y + BOX_H / 2 });
  }

  const width = PAD * 2 + Math.max(1, columns.length) * (BOX_W + GAP_X);
  const height = PAD * 2 + tallest * (BOX_H + GAP_Y);
  return {
    boxes,
    ends,
    edges: edges.map((e) => ({ ...e, back: live.has(e.to) && column.get(e.to)! <= column.get(e.from)! })),
    width,
    height,
  };
}
