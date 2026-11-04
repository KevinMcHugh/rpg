import { useEffect, useMemo, useRef, useState, type PointerEvent, type WheelEvent } from "react";
import type { Choice, Graph } from "../api/client";
import { BOX_H, BOX_W, END_R, layout, type Box, type Edge } from "./layout";

type View = { x: number; y: number; w: number; h: number };

/** wrap breaks text into at most lines lines of about width characters,
 *  ending the last with "…" when it does not fit. */
function wrap(text: string, width: number, lines: number): string[] {
  const words = text.replace(/\s+/g, " ").trim().split(" ");
  const out: string[] = [];
  let line = "";
  for (const w of words) {
    if ((line + " " + w).trim().length > width && line) {
      out.push(line);
      line = w;
      if (out.length === lines) break;
    } else {
      line = (line + " " + w).trim();
    }
  }
  if (out.length < lines && line) out.push(line);
  const full = out.join(" ").length < text.replace(/\s+/g, " ").trim().length;
  if (full && out.length) out[out.length - 1] = out[out.length - 1].replace(/.{0,2}$/, "…");
  return out;
}

const short = (s: string, n: number) => (s.length <= n ? s : s.slice(0, n - 1) + "…");

/** GraphView draws a conversation as boxes and arrows. Clicking a line
 *  selects it; the page shows its editor beside the graph. */
export default function GraphView({
  g,
  selected,
  onSelect,
  openNotes,
}: {
  g: Graph;
  selected: string;
  onSelect: (id: string) => void;
  openNotes: Map<string, number>; // node id → open notes on it
}) {
  const l = useMemo(() => layout(g), [g]);
  const choices = useMemo(() => new Map<string, Choice>(g.nodes.flatMap((n) => n.choices.map((c) => [c.id, c]))), [g]);
  const nodes = useMemo(() => new Map(g.nodes.map((n) => [n.id, n])), [g]);
  const fit: View = useMemo(() => ({ x: 0, y: 0, w: l.width, h: l.height }), [l]);
  const [view, setView] = useState<View>(fit);
  const svg = useRef<SVGSVGElement>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = svg.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setWidth(el.clientWidth));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  // As tall as the graph needs at this width (never larger than life), within
  // a small minimum and most of the window.
  const height = Math.round(
    Math.min(Math.max(fit.h * Math.min(1, width / fit.w || 1), 240), typeof window === "undefined" ? 700 : window.innerHeight * 0.72),
  );
  const drag = useRef<{ x: number; y: number; view: View; moved: boolean } | null>(null);

  // Refit when the shape of the graph changes, not on every edit.
  const shape = `${l.width}x${l.height}`;
  useEffect(() => setView(fit), [shape]); // eslint-disable-line react-hooks/exhaustive-deps

  // Bring the selected line into view if it is off screen.
  useEffect(() => {
    const b = l.boxes.get(selected);
    if (!b) return;
    setView((v) => {
      const inside = b.x >= v.x && b.y >= v.y && b.x + BOX_W <= v.x + v.w && b.y + BOX_H <= v.y + v.h;
      return inside ? v : { ...v, x: b.x + BOX_W / 2 - v.w / 2, y: b.y + BOX_H / 2 - v.h / 2 };
    });
  }, [selected, l]);

  // Screen pixels to graph units.
  const scale = () => view.w / (svg.current?.clientWidth || view.w);

  const zoom = (factor: number, cx = view.x + view.w / 2, cy = view.y + view.h / 2) =>
    setView((v) => {
      const w = Math.min(Math.max(v.w * factor, 200), fit.w * 4);
      const k = w / v.w;
      return { x: cx - (cx - v.x) * k, y: cy - (cy - v.y) * k, w, h: v.h * k };
    });

  const onWheel = (e: WheelEvent<SVGSVGElement>) => {
    const r = svg.current!.getBoundingClientRect();
    const s = scale();
    zoom(e.deltaY > 0 ? 1.12 : 1 / 1.12, view.x + (e.clientX - r.left) * s, view.y + (e.clientY - r.top) * s);
  };
  const onDown = (e: PointerEvent<SVGSVGElement>) => {
    drag.current = { x: e.clientX, y: e.clientY, view, moved: false };
  };
  const onMove = (e: PointerEvent<SVGSVGElement>) => {
    const d = drag.current;
    if (!d) return;
    const dx = e.clientX - d.x;
    const dy = e.clientY - d.y;
    if (!d.moved && Math.hypot(dx, dy) < 4) return;
    if (!d.moved) svg.current!.setPointerCapture(e.pointerId);
    d.moved = true;
    const s = scale();
    setView({ ...d.view, x: d.view.x - dx * s, y: d.view.y - dy * s });
  };
  const onUp = () => {
    // A drag that moved is a pan, not a click on what it started over.
    setTimeout(() => (drag.current = null));
  };
  const click = (id: string) => {
    if (drag.current?.moved) return;
    onSelect(id);
  };

  // Several choices leave one line: spread where they leave it.
  const slot = new Map<Edge, number>();
  const perNode = new Map<string, Edge[]>();
  for (const e of l.edges) (perNode.get(e.from) ?? perNode.set(e.from, []).get(e.from)!).push(e);
  for (const es of perNode.values()) es.forEach((e, i) => slot.set(e, (i + 1) / (es.length + 1)));

  const path = (e: Edge) => {
    const a = l.boxes.get(e.from)!;
    const sx = a.x + BOX_W;
    const sy = a.y + BOX_H * slot.get(e)!;
    const end = l.ends.get(e.to);
    if (end) {
      const tx = end.x - END_R;
      return `M${sx},${sy} C${sx + 30},${sy} ${tx - 30},${end.y} ${tx},${end.y}`;
    }
    const b = l.boxes.get(e.to)!;
    const tx = b.x;
    const ty = b.y + BOX_H / 2;
    if (!e.back) {
      const mid = (sx + tx) / 2;
      return `M${sx},${sy} C${mid},${sy} ${mid},${ty} ${tx},${ty}`;
    }
    // A loop back: out to the right, under both boxes, in from the left.
    const below = Math.max(a.y, b.y) + BOX_H + 22;
    return `M${sx},${sy} C${sx + 70},${sy} ${sx + 70},${below} ${sx},${below} L${tx},${below} C${tx - 70},${below} ${tx - 70},${ty} ${tx},${ty}`;
  };

  const labelAt = (e: Edge): [number, number] => {
    const a = l.boxes.get(e.from)!;
    const sy = a.y + BOX_H * slot.get(e)!;
    const end = l.ends.get(e.to);
    if (end) return [a.x + BOX_W + 6, sy - 5];
    const b = l.boxes.get(e.to)!;
    if (e.back) return [Math.min(a.x, b.x) + BOX_W / 2, Math.max(a.y, b.y) + BOX_H + 18];
    return [a.x + BOX_W + 6, sy - 5];
  };

  const edgeLabel = (e: Edge) => {
    const c = choices.get(e.choiceId)!;
    if (e.kind === "fail") return "✗ failed";
    if (e.kind === "critSuccess") return "★ critical success";
    if (e.kind === "critFail") return "✗✗ critical failure";
    const check = c.checkSkillId ? `[${c.checkSkillName} ${c.checkDifficulty}] ` : "";
    return check + short(c.text, c.checkSkillId ? 18 : 28);
  };

  const box = (b: Box) => {
    const n = nodes.get(b.id)!;
    const start = b.id === g.conversation.startNodeId;
    const notes = openNotes.get(b.id) ?? 0;
    const cls = ["box", start && "start", b.id === selected && "selected", !b.reachable && "unreachable"].filter(Boolean).join(" ");
    return (
      <g
        key={b.id}
        className={cls}
        transform={`translate(${b.x},${b.y})`}
        role="button"
        tabIndex={0}
        aria-label={(n.speakerName || "Narrator") + ": " + n.text}
        onClick={() => click(b.id)}
        onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && onSelect(b.id)}
      >
        <title>{(n.speakerName || "Narrator") + ": " + n.text}</title>
        <rect width={BOX_W} height={BOX_H} rx={10} />
        <text x={12} y={20} className={"who" + (n.speakerName ? "" : " narrator")}>
          {g.nodes.indexOf(n) + 1}. {n.speakerName ? n.speakerName.toUpperCase() : "Narrator"}
        </text>
        {wrap(n.text, 31, 3).map((line, i) => (
          <text key={i} x={12} y={40 + i * 16} className="said">
            {line}
          </text>
        ))}
        {start && (
          <text x={BOX_W - 12} y={20} className="tag" textAnchor="end">
            start
          </text>
        )}
        {!b.reachable && (
          <text x={BOX_W - 12} y={20} className="tag warn" textAnchor="end">
            unreachable
          </text>
        )}
        {notes > 0 && (
          <g transform={`translate(${BOX_W - 14},${BOX_H - 14})`} className="notecount">
            <circle r={9} />
            <text textAnchor="middle" y={4}>
              {notes}
            </text>
          </g>
        )}
        {n.effects && <circle cx={BOX_W - 14} cy={BOX_H - (notes ? 34 : 14)} r={4} className="fx" />}
      </g>
    );
  };

  return (
    <div className="graph">
      <div className="graph-tools row tight">
        <button className="ghost small" onClick={() => zoom(1 / 1.25)} aria-label="Zoom in">
          +
        </button>
        <button className="ghost small" onClick={() => zoom(1.25)} aria-label="Zoom out">
          −
        </button>
        <button className="ghost small" onClick={() => setView(fit)}>
          Fit
        </button>
        <span className="legend muted small">
          <i className="k next" /> leads to <i className="k cond" /> if a condition holds <i className="k fail" /> on a failed check{" "}
          <i className="dot" /> has effects
        </span>
      </div>
      <svg
        ref={svg}
        viewBox={`${view.x} ${view.y} ${view.w} ${view.h}`}
        height={height}
        onWheel={onWheel}
        onPointerDown={onDown}
        onPointerMove={onMove}
        onPointerUp={onUp}
        onPointerLeave={onUp}
        role="img"
        aria-label="The conversation as a graph"
      >
        <defs>
          {["next", "fail"].map((k) => (
            <marker key={k} id={"arrow-" + k} viewBox="0 0 10 10" refX={9} refY={5} markerWidth={7} markerHeight={7} orient="auto-start-reverse">
              <path d="M0,0 L10,5 L0,10 z" className={"head " + k} />
            </marker>
          ))}
        </defs>
        {l.edges.map((e, i) => {
          const c = choices.get(e.choiceId)!;
          const cls = ["edge", e.kind === "critFail" ? "fail" : e.kind === "critSuccess" ? "next crit" : e.kind, c.condition && e.kind === "next" && "cond", (e.from === selected || e.to === selected) && "hot"]
            .filter(Boolean)
            .join(" ");
          return (
            <path key={i} d={path(e)} className={cls} markerEnd={`url(#arrow-${e.kind === "critFail" ? "fail" : "next"})`}>
              <title>{c.text + (c.condition ? "\nif " + c.condition : "") + (c.effects ? "\nthen " + c.effects.replace(/\n/g, "; ") : "")}</title>
            </path>
          );
        })}
        {[...l.ends.values()].map((e) => (
          <g key={e.id} className="end" transform={`translate(${e.x},${e.y})`}>
            <circle r={END_R} />
            <text textAnchor="middle" y={4}>
              end
            </text>
          </g>
        ))}
        {[...l.boxes.values()].map(box)}
        {l.edges.map((e, i) => {
          const [x, y] = labelAt(e);
          return (
            <text key={"l" + i} x={x} y={y} className={"edge-label " + (e.kind === "critFail" ? "fail" : e.kind)}>
              {edgeLabel(e)}
            </text>
          );
        })}
      </svg>
    </div>
  );
}
