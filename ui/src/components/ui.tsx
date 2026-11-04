import { useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { api, must, type Note } from "../api/client";
import { useAct, useMe } from "../hooks";

export const noteKinds = ["gap", "continuity", "plot_idea", "pacing", "mechanics", "question", "other"] as const;
type NoteKind = (typeof noteKinds)[number];
type TargetKind = Note["targetKind"];

export const label = (s: string) => s.replace(/_/g, " ");

export function Id({ id }: { id: string }) {
  return (
    <code className="id" title="Click to select. This is the id the MCP tools take.">
      {id}
    </code>
  );
}

export function Key({ k, hint }: { k: string; hint?: string }) {
  return (
    <code className="key" title={hint}>
      {k}
    </code>
  );
}

export function Badge({ children, tone }: { children: ReactNode; tone?: "ok" | "err" | "agent" }) {
  return <span className={"badge" + (tone ? " " + tone : "")}>{children}</span>;
}

export function Loading({ error }: { error?: string }) {
  return error ? <p className="flash err">{error}</p> : <p className="muted">Loading…</p>;
}

/** A form that submits through onSubmit and clears itself when it worked. */
export function Form({
  onSubmit,
  children,
  className,
  reset = true,
}: {
  onSubmit: (f: FormData) => Promise<boolean>;
  children: ReactNode;
  className?: string;
  reset?: boolean;
}) {
  const [busy, setBusy] = useState(false);
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = e.currentTarget;
    setBusy(true);
    const ok = await onSubmit(new FormData(form));
    setBusy(false);
    if (ok && reset) form.reset();
  }
  return (
    <form onSubmit={submit} className={className ?? "stack"}>
      <fieldset disabled={busy}>{children}</fieldset>
    </form>
  );
}

/** str reads a text field; num a whole number. */
export const str = (f: FormData, k: string) => String(f.get(k) ?? "");
export const num = (f: FormData, k: string) => Number.parseInt(str(f, k), 10) || 0;

/** A script textarea: conditions or effects, as authors type them. */
export function Script({ name, value, placeholder }: { name: string; value?: string; placeholder: string }) {
  return (
    <textarea
      className="script"
      name={name}
      defaultValue={value}
      placeholder={placeholder}
      aria-label={placeholder}
      rows={Math.max(2, (value ?? "").split("\n").length)}
      spellCheck={false}
    />
  );
}

/** A rule shown under content: "if met_mayor", "then quest x = 1". */
export function Rule({ word, children }: { word: string; children: ReactNode }) {
  return (
    <div className="rule">
      <b>{word}</b>
      {children}
    </div>
  );
}

/** Notes: the thread on one target, and adding to it. Notes are feedback,
 *  never content, so they look nothing like it. */
export function Notes({
  game,
  kind,
  id,
  notes,
  onChange,
  compact,
}: {
  game: string;
  kind: TargetKind;
  id: string;
  notes: Note[];
  onChange: () => void;
  compact?: boolean;
}) {
  const me = useMe();
  const act = useAct();
  const open = notes.filter((n) => !n.parentId && n.status === "open").length;
  const [shown, setShown] = useState(!compact || open > 0);
  const [adding, setAdding] = useState(false);
  const replies = (p: Note) => notes.filter((n) => n.parentId === p.id);

  const status = (n: Note, s: Note["status"]) =>
    act(async () => must(await api.PATCH("/notes/{noteId}", { params: { path: { noteId: n.id } }, body: { status: s } })), "").then(onChange);
  const add = (f: FormData) =>
    act(async () => {
      must(
        await api.POST("/games/{gameId}/notes", {
          params: { path: { gameId: game } },
          body: { targetKind: kind, targetId: id, kind: str(f, "kind") as NoteKind, body: str(f, "body") },
        }),
      );
      setAdding(false);
      onChange();
    }, "Noted.");
  const reply = (p: Note) => (f: FormData) =>
    act(async () => {
      must(await api.POST("/games/{gameId}/notes", { params: { path: { gameId: game } }, body: { parentId: p.id, body: str(f, "body") } }));
      onChange();
    }, "Noted.");

  if (compact && !shown) {
    return (
      <div className="notes-toggle">
        <button className="link" onClick={() => setShown(true)}>
          {notes.length ? `${notes.filter((n) => !n.parentId).length} note${notes.length === 1 ? "" : "s"}` : "Add a note"}
        </button>
      </div>
    );
  }
  return (
    <div className="notes">
      {notes
        .filter((n) => !n.parentId)
        .map((n) => (
          <div key={n.id} className={"note" + (n.status !== "open" ? " closed" : "")} id={"note-" + n.id}>
            <NoteLine n={n} />
            {me.human && (
              <div className="row tight">
                {n.status === "open" ? (
                  <>
                    <button className="ghost small" onClick={() => status(n, "resolved")}>Resolve</button>
                    <button className="ghost small" onClick={() => status(n, "dismissed")}>Dismiss</button>
                  </>
                ) : (
                  <button className="ghost small" onClick={() => status(n, "open")}>Reopen</button>
                )}
              </div>
            )}
            {replies(n).map((r) => (
              <div key={r.id} className="note reply">
                <NoteLine n={r} />
              </div>
            ))}
            <details>
              <summary>Reply</summary>
              <Form onSubmit={reply(n)}>
                <textarea name="body" required aria-label="Reply" rows={2} />
                <div>
                  <button className="small">Reply</button>
                </div>
              </Form>
            </details>
          </div>
        ))}
      {adding ? (
        <Form onSubmit={add}>
          <select name="kind" aria-label="Kind" defaultValue="question">
            {noteKinds.map((k) => (
              <option key={k} value={k}>
                {label(k)}
              </option>
            ))}
          </select>
          <textarea name="body" required aria-label="Note" rows={3} autoFocus />
          <div className="row tight">
            <button className="small">Add note</button>
            <button type="button" className="ghost small" onClick={() => setAdding(false)}>
              Cancel
            </button>
          </div>
        </Form>
      ) : (
        <div>
          <button className="link" onClick={() => setAdding(true)}>
            Add a note
          </button>
        </div>
      )}
    </div>
  );
}

function NoteLine({ n }: { n: Note }) {
  return (
    <>
      <div className="meta">
        <strong>{n.authorName}</strong>
        {!n.byHuman && <Badge tone="agent">agent</Badge>}
        {!n.parentId && <Badge>{label(n.kind)}</Badge>}
        {n.status !== "open" && <Badge>{n.status}</Badge>}
        <span>{new Date(n.createdAt).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}</span>
      </div>
      <div className="text">{n.body}</div>
    </>
  );
}

/** notesFor is the notes on one target, from a list of all of them. */
export const notesFor = (all: Note[] | undefined, kind: string, id: string) =>
  (all ?? []).filter((n) => n.targetKind === kind && n.targetId === id);

/** TargetLink goes to what a note or build issue is about. The API gives a
 *  full URL; most are pages of this app, but /nodes/, /choices/ and
 *  /stages/ are server redirects to the page that holds them, so they need a
 *  real page load. */
export function TargetLink({ url, children }: { url: string; children: ReactNode }) {
  let path = url;
  try {
    const u = new URL(url, location.origin);
    path = u.pathname + u.hash;
  } catch {
    /* keep it as given */
  }
  if (/^\/(nodes|choices|stages)\//.test(path)) return <a href={path}>{children}</a>;
  return <Link to={path}>{children}</Link>;
}
