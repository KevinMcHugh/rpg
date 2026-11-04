import { useEffect, useMemo, useState } from "react";
import { Link, useLocation, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, must, type Character, type Choice, type Graph, type Node, type Note, type Skill } from "../api/client";
import { hasCriticals, useAct, useGame, useGameId, useLoad, useMe } from "../hooks";
import { Badge, Form, Id, Key, Loading, Notes, Rule, Script, notesFor, num, str } from "../components/ui";
import GraphView from "../graph/Graph";

// Everything a line or choice editor needs to know about the conversation.
type Ctx = {
  g: Graph;
  chars: Character[];
  skills: Skill[];
  notes: Note[];
  labels: Map<string, string>; // node id → "3. Aria: You came back…"
  reload: () => void;
};

const excerpt = (s: string, n: number) => {
  s = s.replace(/\s+/g, " ").trim();
  return s.length <= n ? s : s.slice(0, n) + "…";
};

export default function ConversationPage() {
  const { id = "" } = useParams();
  const loc = useLocation();
  const nav = useNavigate();
  const [params, setParams] = useSearchParams();
  const graph = params.get("view") === "graph";
  const { data, error, reload } = useLoad(async () => {
    const g = must(await api.GET("/conversations/{conversationId}", { params: { path: { conversationId: id } } }));
    const path = { params: { path: { gameId: g.conversation.gameId } } };
    const [chars, skills, notes] = await Promise.all([
      api.GET("/games/{gameId}/characters", path).then(must),
      api.GET("/games/{gameId}/skills", path).then(must),
      api.GET("/games/{gameId}/notes", path).then(must),
    ]);
    return { g, chars: chars.characters, skills: skills.skills, notes: notes.notes };
  }, [id]);
  useGameId(data?.g.conversation.gameId);

  const labels = useMemo(() => {
    const m = new Map<string, string>();
    data?.g.nodes.forEach((n, i) => m.set(n.id, `${i + 1}. ${n.speakerName || "Narrator"}: ${excerpt(n.text, 48)}`));
    return m;
  }, [data]);

  // Links into the page (#node-…, #choice-…) scroll to their target once
  // it is drawn, and mark it.
  const focus = loc.hash.slice(1);
  useEffect(() => {
    if (focus && data && !graph) document.getElementById(focus)?.scrollIntoView({ block: "center" });
  }, [focus, data, graph]);

  if (!data) return <Loading error={error} />;
  const ctx: Ctx = { ...data, labels, reload };
  const c = data.g.conversation;
  const openNotes = new Map<string, number>();
  for (const n of data.notes) {
    if (n.targetKind === "node" && n.status === "open" && !n.parentId) openNotes.set(n.targetId, (openNotes.get(n.targetId) ?? 0) + 1);
  }
  // In the graph, the selected line is the one the URL points at, or the start.
  const selectedId = focus.startsWith("node-") ? focus.slice(5) : c.startNodeId;
  const selected = data.g.nodes.find((n) => n.id === selectedId);
  const setView = (v: "list" | "graph") => setParams(v === "graph" ? { view: "graph" } : {});

  return (
    <>
      <p>
        <Link to={"/games/" + c.gameId}>← Content</Link>
      </p>
      <Header ctx={ctx} />
      <div className="tabs" role="tablist">
        <button role="tab" aria-selected={!graph} className={graph ? "" : "on"} onClick={() => setView("list")}>
          Lines
        </button>
        <button role="tab" aria-selected={graph} className={graph ? "on" : ""} onClick={() => setView("graph")}>
          Graph
        </button>
      </div>
      {graph ? (
        <div className="graph-editor">
          <GraphView g={data.g} selected={selectedId} onSelect={(n) => nav({ search: "?view=graph", hash: "node-" + n }, { replace: true })} openNotes={openNotes} />
          <div className="graph-side">
            {selected ? (
              <NodeCard key={selected.id} n={selected} index={data.g.nodes.indexOf(selected)} ctx={ctx} focused={false} focus={focus} />
            ) : (
              <p className="muted">{data.g.nodes.length ? "Click a line to edit it here." : "No lines yet."}</p>
            )}
            <AddNode ctx={ctx} />
          </div>
        </div>
      ) : (
      <div className="editor">
        <div className="lines">
          {data.g.nodes.map((n, i) => (
            <NodeCard key={n.id} n={n} index={i} ctx={ctx} focused={focus === "node-" + n.id} focus={focus} />
          ))}
          {!data.g.nodes.length && <p className="muted">No lines yet. The first line you add is where the conversation starts.</p>}
          <AddNode ctx={ctx} />
        </div>
        <Outline g={data.g} focus={focus} />
      </div>
      )}
      <h2>Notes on this conversation</h2>
      <Notes game={c.gameId} kind="conversation" id={c.id} notes={notesFor(data.notes, "conversation", c.id)} onChange={reload} />
    </>
  );
}

function Header({ ctx }: { ctx: Ctx }) {
  const me = useMe();
  const act = useAct();
  const nav = useNavigate();
  const [editing, setEditing] = useState(false);
  const c = ctx.g.conversation;
  const nodes = ctx.g.nodes;

  const save = (f: FormData) =>
    act(async () => {
      must(
        await api.PATCH("/conversations/{conversationId}", {
          params: { path: { conversationId: c.id } },
          body: {
            title: str(f, "title"),
            key: str(f, "key"),
            summary: str(f, "summary"),
            label: str(f, "label"),
            condition: str(f, "condition"),
            startNodeId: str(f, "start"),
            repeatable: f.get("repeatable") === "on",
            opening: f.get("opening") === "on",
            updatedAt: c.updatedAt,
          },
        }),
      );
      setEditing(false);
      ctx.reload();
    });
  const remove = () =>
    confirm(`Delete “${c.title}”? Its lines stop shipping.`) &&
    act(async () => {
      must(await api.DELETE("/conversations/{conversationId}", { params: { path: { conversationId: c.id } } }));
      nav("/games/" + c.gameId);
    }, "Deleted.");

  return (
    <>
      <div className="row spread">
        <h1>{c.title}</h1>
        <div className="row tight">
          {c.startNodeId && (
            <a className="btn ghost" href={"/play?game=" + c.gameId + "&node=" + c.startNodeId}>
              Playtest
            </a>
          )}
          {me.human && !editing && <button onClick={() => setEditing(true)}>Edit</button>}
        </div>
      </div>
      <p className="row tight muted small">
        <span>
          {nodes.length} line{nodes.length === 1 ? "" : "s"}
        </span>
        <Key k={c.key} hint={`What scripts call this conversation: done ${c.key}`} />
        {c.opening && <Badge tone="ok">opens the game</Badge>}
        {c.repeatable && <Badge>repeatable</Badge>}
        {!c.startNodeId && <Badge tone="err">no start line</Badge>}
        <Id id={c.id} />
      </p>
      {editing ? (
        <Form onSubmit={save} className="panel stack" reset={false}>
          <div className="row">
            <input name="title" defaultValue={c.title} aria-label="Title" required maxLength={200} className="grow" />
            <input className="script" name="key" defaultValue={c.key} aria-label="Key" required />
          </div>
          <textarea name="summary" defaultValue={c.summary} placeholder="What is this conversation for? (authoring only)" aria-label="Summary" rows={2} />
          <label className="field">
            Listed between conversations as
            <input name="label" defaultValue={c.label} placeholder="Empty: not listed; only the opening or a script starts it" maxLength={500} />
          </label>
          <label className="field">
            Available if
            <Script name="condition" value={c.condition} placeholder="Script; empty is always" />
          </label>
          <label className="field">
            Starts at
            <select name="start" defaultValue={c.startNodeId}>
              <option value="">(none)</option>
              {nodes.map((n) => (
                <option key={n.id} value={n.id}>
                  {ctx.labels.get(n.id)}
                </option>
              ))}
            </select>
          </label>
          <div className="row">
            <label className="check">
              <input type="checkbox" name="repeatable" defaultChecked={c.repeatable} /> Repeatable
            </label>
            <label className="check">
              <input type="checkbox" name="opening" defaultChecked={c.opening} /> Opens the game
            </label>
          </div>
          <div className="row tight">
            <button>Save</button>
            <button type="button" className="ghost" onClick={() => setEditing(false)}>
              Cancel
            </button>
            <span className="spacer" />
            <button type="button" className="danger" onClick={remove}>
              Delete conversation
            </button>
          </div>
        </Form>
      ) : (
        (c.summary || c.label || c.condition) && (
          <div className="panel">
            {c.summary && <div className="prose">{c.summary}</div>}
            {c.label && <Rule word="Listed as">“{c.label}”</Rule>}
            {c.condition && <Rule word="Available if">{c.condition}</Rule>}
          </div>
        )
      )}
    </>
  );
}

/** Outline is the shape of the conversation at a glance: each line, and
 *  where its choices go. */
function Outline({ g, focus }: { g: Graph; focus: string }) {
  const num = new Map(g.nodes.map((n, i) => [n.id, i + 1]));
  const where = (id: string) => (id ? (num.has(id) ? String(num.get(id)) : "?") : "end");
  return (
    <aside className="outline">
      <h3>Outline</h3>
      <ol>
        {g.nodes.map((n, i) => (
          <li key={n.id} className={focus === "node-" + n.id ? "on" : ""}>
            <a href={"#node-" + n.id}>
              <span className="n">{i + 1}</span> {n.id === g.conversation.startNodeId && "▸ "}
              {excerpt(n.text, 34)}
            </a>
            {n.choices.length > 0 && (
              <div className="arrows">
                {n.choices.map((c) => (
                  <span key={c.id} title={c.text} className={c.condition ? "cond" : ""}>
                    → {where(c.toNodeId)}
                    {c.checkSkillId && <> / ✗ {where(c.failNodeId)}</>}
                  </span>
                ))}
              </div>
            )}
          </li>
        ))}
      </ol>
    </aside>
  );
}

function NodeCard({ n, index, ctx, focused, focus }: { n: Node; index: number; ctx: Ctx; focused: boolean; focus: string }) {
  const me = useMe();
  const act = useAct();
  const [editing, setEditing] = useState(false);
  const [adding, setAdding] = useState(false);
  const start = n.id === ctx.g.conversation.startNodeId;

  const save = (f: FormData) =>
    act(async () => {
      must(
        await api.PATCH("/nodes/{nodeId}", {
          params: { path: { nodeId: n.id } },
          body: { speakerId: str(f, "speaker"), text: str(f, "text"), effects: str(f, "effects"), updatedAt: n.updatedAt },
        }),
      );
      setEditing(false);
      ctx.reload();
    });
  const remove = () =>
    confirm("Delete this line and the choices it offers? Choices that lead here will point nowhere until you change them.") &&
    act(async () => {
      must(await api.DELETE("/nodes/{nodeId}", { params: { path: { nodeId: n.id } } }));
      ctx.reload();
    }, "Deleted.");

  return (
    <article className={"card node" + (focused ? " focused" : "")} id={"node-" + n.id}>
      <div className="row spread">
        <div className="row tight">
          <span className="muted">{index + 1}.</span>
          {n.speakerName ? <span className="speaker">{n.speakerName}</span> : <span className="speaker narrator">Narrator</span>}
          {start && <Badge tone="ok">start</Badge>}
        </div>
        <div className="row tight small">
          <a href={"/play?game=" + ctx.g.conversation.gameId + "&node=" + n.id}>Playtest from here</a>
          {me.human && !editing && (
            <button className="ghost small" onClick={() => setEditing(true)}>
              Edit
            </button>
          )}
          <Id id={n.id} />
        </div>
      </div>

      {editing ? (
        <Form onSubmit={save} reset={false}>
          <SpeakerSelect chars={ctx.chars} value={n.speakerId} />
          <textarea name="text" defaultValue={n.text} aria-label="Line" required rows={3} className="line-input" autoFocus />
          <Script name="effects" value={n.effects} placeholder="Then, when the player reaches this line (script, one per line)" />
          <div className="row tight">
            <button className="small">Save line</button>
            <button type="button" className="ghost small" onClick={() => setEditing(false)}>
              Cancel
            </button>
            <span className="spacer" />
            <button type="button" className="danger small" onClick={remove}>
              Delete line
            </button>
          </div>
        </Form>
      ) : (
        <>
          <div className="line">{n.text}</div>
          {n.effects && <Rule word="then">{n.effects}</Rule>}
        </>
      )}

      {n.choices.length > 0 ? (
        <ol className="choices">
          {n.choices.map((c) => (
            <ChoiceRow key={c.id} c={c} ctx={ctx} focused={focus === "choice-" + c.id} />
          ))}
        </ol>
      ) : (
        <p className="to">No choices: the conversation ends after this line.</p>
      )}

      {me.human &&
        (adding ? (
          <ChoiceForm ctx={ctx} nodeId={n.id} order={n.choices.length} onDone={() => setAdding(false)} />
        ) : (
          <div>
            <button className="link" onClick={() => setAdding(true)}>
              + Add a choice
            </button>
          </div>
        ))}
      <Notes game={ctx.g.conversation.gameId} kind="node" id={n.id} notes={notesFor(ctx.notes, "node", n.id)} onChange={ctx.reload} compact />
    </article>
  );
}

function ChoiceRow({ c, ctx, focused }: { c: Choice; ctx: Ctx; focused: boolean }) {
  const me = useMe();
  const [editing, setEditing] = useState(false);
  const to = (id: string) =>
    id ? <a href={"#node-" + id}>{ctx.labels.get(id) ?? "a missing line " + id}</a> : <span>ends the conversation</span>;
  if (editing) {
    return (
      <li id={"choice-" + c.id} className="editing">
        <ChoiceForm ctx={ctx} choice={c} onDone={() => setEditing(false)} />
      </li>
    );
  }
  return (
    <li id={"choice-" + c.id} className={focused ? "focused" : ""}>
      <div className="row tight">
        {c.checkSkillId && (
          <Badge>
            {c.checkSkillName} {c.checkDifficulty}
          </Badge>
        )}
        <span className="choice-text">{c.text}</span>
        <span className="to">→ {to(c.toNodeId)}</span>
        {me.human && (
          <button className="ghost small" onClick={() => setEditing(true)}>
            Edit
          </button>
        )}
      </div>
      {c.checkSkillId && <div className="to">on failure → {to(c.failNodeId)}</div>}
      {c.critSuccessNodeId && <div className="to">on a critical success → {to(c.critSuccessNodeId)}</div>}
      {c.critFailNodeId && <div className="to">on a critical failure → {to(c.critFailNodeId)}</div>}
      {c.condition && <Rule word="if">{c.condition}</Rule>}
      {c.effects && <Rule word="then">{c.effects}</Rule>}
      {notesFor(ctx.notes, "choice", c.id).length > 0 && (
        <Notes game={ctx.g.conversation.gameId} kind="choice" id={c.id} notes={notesFor(ctx.notes, "choice", c.id)} onChange={ctx.reload} compact />
      )}
    </li>
  );
}

/** ChoiceForm adds a choice to nodeId, or edits choice. */
function ChoiceForm({ ctx, nodeId, choice, order, onDone }: { ctx: Ctx; nodeId?: string; choice?: Choice; order?: number; onDone: () => void }) {
  const act = useAct();
  const crits = hasCriticals(useGame()?.ruleset);
  const [check, setCheck] = useState(Boolean(choice?.checkSkillId));
  const submit = (f: FormData) =>
    act(async () => {
      const body = {
        text: str(f, "text"),
        toNodeId: str(f, "to"),
        sortOrder: num(f, "order"),
        condition: str(f, "condition"),
        effects: str(f, "effects"),
        checkSkillId: check ? str(f, "skill") : "",
        checkDifficulty: check ? num(f, "difficulty") : 0,
        failNodeId: check ? str(f, "fail") : "",
        // Under a ruleset with no criticals the fields are not shown, so
        // keep what is stored; the build warns about it.
        ...(crits || !check
          ? { critSuccessNodeId: check ? str(f, "critSuccess") : "", critFailNodeId: check ? str(f, "critFail") : "" }
          : {}),
      };
      if (choice) must(await api.PATCH("/choices/{choiceId}", { params: { path: { choiceId: choice.id } }, body: { ...body, updatedAt: choice.updatedAt } }));
      else must(await api.POST("/nodes/{nodeId}/choices", { params: { path: { nodeId: nodeId! } }, body }));
      onDone();
      ctx.reload();
    });
  const remove = () =>
    choice &&
    confirm("Delete this choice?") &&
    act(async () => {
      must(await api.DELETE("/choices/{choiceId}", { params: { path: { choiceId: choice.id } } }));
      onDone();
      ctx.reload();
    }, "Deleted.");

  return (
    <Form onSubmit={submit} className="stack subform" reset={false}>
      <input name="text" defaultValue={choice?.text} placeholder="What the player says" aria-label="Choice" required maxLength={500} autoFocus />
      <div className="row">
        <label className="field grow">
          Leads to
          <NodeSelect ctx={ctx} name="to" value={choice?.toNodeId} none="Ends the conversation" />
        </label>
        <label className="field">
          Order
          <input name="order" type="number" defaultValue={choice?.sortOrder ?? order ?? 0} style={{ width: "5rem" }} />
        </label>
      </div>
      <Script name="condition" value={choice?.condition} placeholder="Shows if (script; empty is always)" />
      <Script name="effects" value={choice?.effects} placeholder="Then (script, one per line)" />
      <label className="check">
        <input type="checkbox" checked={check} onChange={(e) => setCheck(e.target.checked)} /> Skill check
      </label>
      {check && (
        <div className="row">
          <label className="field">
            Skill
            <select name="skill" defaultValue={choice?.checkSkillId} required>
              {ctx.skills.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </label>
          <label className="field" title="2d6 plus the skill must reach this">
            Difficulty
            <input name="difficulty" type="number" min={1} max={30} defaultValue={choice?.checkDifficulty || 8} style={{ width: "5rem" }} />
          </label>
          <label className="field grow">
            On failure
            <NodeSelect ctx={ctx} name="fail" value={choice?.failNodeId} none="Ends the conversation" />
          </label>
        </div>
      )}
      {check && crits && (
        <div className="row">
          <label className="field grow">
            On a critical success
            <NodeSelect ctx={ctx} name="critSuccess" value={choice?.critSuccessNodeId} none="Same as success" />
          </label>
          <label className="field grow">
            On a critical failure
            <NodeSelect ctx={ctx} name="critFail" value={choice?.critFailNodeId} none="Same as failure" />
          </label>
        </div>
      )}
      {check && !ctx.skills.length && (
        <p className="muted small">
          There are no skills yet. Add one on the <Link to={"/games/" + ctx.g.conversation.gameId + "/world#skills"}>World</Link> page.
        </p>
      )}
      <div className="row tight">
        <button className="small">{choice ? "Save choice" : "Add choice"}</button>
        <button type="button" className="ghost small" onClick={onDone}>
          Cancel
        </button>
        <span className="spacer" />
        {choice && (
          <button type="button" className="danger small" onClick={remove}>
            Delete choice
          </button>
        )}
      </div>
    </Form>
  );
}

function AddNode({ ctx }: { ctx: Ctx }) {
  const me = useMe();
  const act = useAct();
  const nav = useNavigate();
  const loc = useLocation();
  if (!me.human) return null;
  const add = (f: FormData) =>
    act(async () => {
      const n = must(
        await api.POST("/conversations/{conversationId}/nodes", {
          params: { path: { conversationId: ctx.g.conversation.id } },
          body: { speakerId: str(f, "speaker"), text: str(f, "text"), effects: str(f, "effects") },
        }),
      );
      ctx.reload();
      // Keep the view (list or graph) and select the new line.
      nav({ search: loc.search, hash: "node-" + n.id }, { replace: true });
    });
  return (
    <section className="panel" id="new-line">
      <h3>Add a line</h3>
      <Form onSubmit={add}>
        <SpeakerSelect chars={ctx.chars} />
        <textarea name="text" aria-label="Line" required rows={3} className="line-input" />
        <Script name="effects" placeholder="Then, when the player reaches this line (script, one per line)" />
        <div>
          <button>Add line</button>
        </div>
      </Form>
      <p className="muted small">Add the line, then point a choice at it from the line before.</p>
    </section>
  );
}

function SpeakerSelect({ chars, value }: { chars: Character[]; value?: string }) {
  return (
    <select name="speaker" defaultValue={value ?? ""} aria-label="Speaker">
      <option value="">Narrator</option>
      {chars.map((c) => (
        <option key={c.id} value={c.id}>
          {c.name}
        </option>
      ))}
    </select>
  );
}

function NodeSelect({ ctx, name, value, none }: { ctx: Ctx; name: string; value?: string; none: string }) {
  return (
    <select name={name} defaultValue={value ?? ""}>
      <option value="">{none}</option>
      {ctx.g.nodes.map((n) => (
        <option key={n.id} value={n.id}>
          {ctx.labels.get(n.id)}
        </option>
      ))}
    </select>
  );
}
