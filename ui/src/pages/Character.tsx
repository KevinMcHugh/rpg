import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { all, api, maxLimit, must } from "../api/client";
import { useAct, useGameId, useLoad, useMe } from "../hooks";
import { Badge, Form, Id, Key, Loading, Notes, notesFor, str } from "../components/ui";

export default function CharacterPage() {
  const { id = "" } = useParams();
  const me = useMe();
  const act = useAct();
  const nav = useNavigate();
  const [editing, setEditing] = useState(false);
  const { data, error, reload } = useLoad(async () => {
    const c = must(await api.GET("/characters/{characterId}", { params: { path: { characterId: id } } }));
    const notes = await all("notes", async (after) =>
      must(
        await api.GET("/games/{gameId}/notes", {
          params: { path: { gameId: c.gameId }, query: { targetKind: "character", targetId: id, after, limit: maxLimit } },
        }),
      ),
    );
    return { c, notes };
  }, [id]);
  useGameId(data?.c.gameId);
  if (!data) return <Loading error={error} />;
  const { c } = data;

  const save = (f: FormData) =>
    act(async () => {
      must(
        await api.PATCH("/characters/{characterId}", {
          params: { path: { characterId: id } },
          body: { name: str(f, "name"), key: str(f, "key"), bible: str(f, "bible"), companion: f.get("companion") === "on", updatedAt: c.updatedAt },
        }),
      );
      setEditing(false);
      reload();
    });
  const remove = () =>
    confirm(`Delete ${c.name}? Lines they speak stay, and the build will report them.`) &&
    act(async () => {
      must(await api.DELETE("/characters/{characterId}", { params: { path: { characterId: id } } }));
      nav("/games/" + c.gameId);
    }, "Deleted.");

  return (
    <>
      <p>
        <Link to={"/games/" + c.gameId}>← Content</Link>
      </p>
      <div className="row spread">
        <h1>{c.name}</h1>
        {me.human && !editing && <button onClick={() => setEditing(true)}>Edit</button>}
      </div>
      <p className="row tight muted small">
        {c.companion && <Badge tone="ok">companion</Badge>}
        <Key k={c.key} hint={`What scripts call this character: approval ${c.key} > 5`} />
        <Id id={c.id} />
      </p>
      {editing ? (
        <Form onSubmit={save} className="panel stack" reset={false}>
          <input name="name" defaultValue={c.name} aria-label="Name" required maxLength={100} />
          <input className="script" name="key" defaultValue={c.key} aria-label="Key" required />
          <label className="check">
            <input type="checkbox" name="companion" defaultChecked={c.companion} /> Companion (can join the party; has approval)
          </label>
          <textarea name="bible" defaultValue={c.bible} aria-label="Bible" rows={16} />
          <div className="row tight">
            <button>Save</button>
            <button type="button" className="ghost" onClick={() => setEditing(false)}>
              Cancel
            </button>
            <span className="spacer" />
            <button type="button" className="danger" onClick={remove}>
              Delete
            </button>
          </div>
        </Form>
      ) : (
        <>
          <h2>Bible</h2>
          <div className="panel prose">
            {c.bible || (
              <span className="muted">
                No bible yet. Claude reads it before commenting on anything this character says, so the more specific, the better.
              </span>
            )}
          </div>
        </>
      )}
      <h2>Notes</h2>
      <Notes game={c.gameId} kind="character" id={c.id} notes={notesFor(data.notes, "character", c.id)} onChange={reload} />
    </>
  );
}
