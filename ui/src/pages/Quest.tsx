import { useEffect, useState } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import { all, api, maxLimit, must, type S } from "../api/client";
import { useAct, useGameId, useLoad, useMe } from "../hooks";
import { Badge, Form, Id, Key, Loading, Notes, notesFor, num, str } from "../components/ui";

type Stage = S["QuestStage"];

export default function QuestPage() {
  const { id = "" } = useParams();
  const me = useMe();
  const act = useAct();
  const nav = useNavigate();
  const loc = useLocation();
  const [editing, setEditing] = useState(false);
  const { data, error, reload } = useLoad(async () => {
    const q = must(await api.GET("/quests/{questId}", { params: { path: { questId: id } } }));
    const notes = await all("notes", async (after) =>
      must(
        await api.GET("/games/{gameId}/notes", {
          params: { path: { gameId: q.gameId }, query: { targetKind: "quest", targetId: id, after, limit: maxLimit } },
        }),
      ),
    );
    return { q, notes };
  }, [id]);
  useGameId(data?.q.gameId);
  useEffect(() => {
    if (data && loc.hash) document.getElementById(loc.hash.slice(1))?.scrollIntoView({ block: "center" });
  }, [data, loc.hash]);
  if (!data) return <Loading error={error} />;
  const { q } = data;
  const next = Math.max(0, ...q.stages.map((s) => s.number)) + 1;

  const save = (f: FormData) =>
    act(async () => {
      must(
        await api.PATCH("/quests/{questId}", {
          params: { path: { questId: id } },
          body: { name: str(f, "name"), key: str(f, "key"), summary: str(f, "summary"), updatedAt: q.updatedAt },
        }),
      );
      setEditing(false);
      reload();
    });
  const remove = () =>
    confirm(`Delete ${q.name} and its stages?`) &&
    act(async () => {
      must(await api.DELETE("/quests/{questId}", { params: { path: { questId: id } } }));
      nav("/games/" + q.gameId + "/world#quests");
    }, "Deleted.");
  const addStage = (f: FormData) =>
    act(async () => {
      must(
        await api.POST("/quests/{questId}/stages", {
          params: { path: { questId: id } },
          body: { number: num(f, "number"), journal: str(f, "journal"), completes: f.get("completes") === "on" },
        }),
      );
      reload();
    });

  return (
    <>
      <p>
        <Link to={"/games/" + q.gameId + "/world#quests"}>← World</Link>
      </p>
      <div className="row spread">
        <h1>{q.name}</h1>
        {me.human && !editing && <button onClick={() => setEditing(true)}>Edit</button>}
      </div>
      <p className="row tight muted small">
        <Key k={q.key} />
        <Id id={q.id} />
      </p>
      {editing ? (
        <Form onSubmit={save} className="panel stack" reset={false}>
          <div className="row">
            <input name="name" defaultValue={q.name} aria-label="Name" required maxLength={100} className="grow" />
            <input className="script" name="key" defaultValue={q.key} aria-label="Key" required />
          </div>
          <textarea name="summary" defaultValue={q.summary} aria-label="Summary" rows={3} />
          <div className="row tight">
            <button>Save</button>
            <button type="button" className="ghost" onClick={() => setEditing(false)}>
              Cancel
            </button>
            <span className="spacer" />
            <button type="button" className="danger" onClick={remove}>
              Delete quest
            </button>
          </div>
        </Form>
      ) : (
        q.summary && <div className="panel prose">{q.summary}</div>
      )}

      <h2>Stages</h2>
      <p className="muted">
        Scripts move the quest to a stage by number: <Key k={`quest ${q.key} = 1`} />. The journal shows the entry for the stage it is at;
        0 is not started.
      </p>
      <div className="list">
        {q.stages.map((s) => (
          <StageCard key={s.id} s={s} reload={reload} focused={loc.hash === "#stage-" + s.id} />
        ))}
        {!q.stages.length && <p className="muted">No stages yet.</p>}
      </div>
      {me.human && (
        <Form onSubmit={addStage} className="panel stack">
          <input name="number" type="number" defaultValue={next} min={1} aria-label="Number" style={{ width: "6rem" }} key={next} />
          <textarea name="journal" placeholder="The journal entry at this stage" aria-label="Journal entry" required rows={2} />
          <label className="check">
            <input type="checkbox" name="completes" /> Completes the quest
          </label>
          <div>
            <button>Add stage</button>
          </div>
        </Form>
      )}
      <h2>Notes</h2>
      <Notes game={q.gameId} kind="quest" id={q.id} notes={notesFor(data.notes, "quest", q.id)} onChange={reload} />
    </>
  );
}

function StageCard({ s, reload, focused }: { s: Stage; reload: () => void; focused: boolean }) {
  const me = useMe();
  const act = useAct();
  const [editing, setEditing] = useState(false);
  const save = (f: FormData) =>
    act(async () => {
      must(
        await api.PATCH("/stages/{stageId}", {
          params: { path: { stageId: s.id } },
          body: { number: num(f, "number"), journal: str(f, "journal"), completes: f.get("completes") === "on", updatedAt: s.updatedAt },
        }),
      );
      setEditing(false);
      reload();
    });
  const remove = () =>
    confirm(`Delete stage ${s.number}?`) &&
    act(async () => {
      must(await api.DELETE("/stages/{stageId}", { params: { path: { stageId: s.id } } }));
      reload();
    }, "Deleted.");
  return (
    <article className={"card" + (focused ? " focused" : "")} id={"stage-" + s.id}>
      {editing ? (
        <Form onSubmit={save} reset={false}>
          <input name="number" type="number" defaultValue={s.number} min={1} aria-label="Number" style={{ width: "6rem" }} />
          <textarea name="journal" defaultValue={s.journal} aria-label="Journal entry" required rows={3} className="line-input" />
          <label className="check">
            <input type="checkbox" name="completes" defaultChecked={s.completes} /> Completes the quest
          </label>
          <div className="row tight">
            <button className="small">Save</button>
            <button type="button" className="ghost small" onClick={() => setEditing(false)}>
              Cancel
            </button>
            <span className="spacer" />
            <button type="button" className="danger small" onClick={remove}>
              Delete stage
            </button>
          </div>
        </Form>
      ) : (
        <>
          <div className="row spread">
            <div className="row tight">
              <strong>{s.number}</strong>
              {s.completes && <Badge tone="ok">completes the quest</Badge>}
            </div>
            {me.human && (
              <button className="ghost small" onClick={() => setEditing(true)}>
                Edit
              </button>
            )}
          </div>
          <div className="line">{s.journal}</div>
        </>
      )}
    </article>
  );
}
