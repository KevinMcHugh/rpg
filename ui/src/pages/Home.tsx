import { Link, useNavigate, useParams } from "react-router-dom";
import { api, must } from "../api/client";
import { useAct, useGameId, useLoad, useMe } from "../hooks";
import { Badge, Form, Key, Loading, Notes, notesFor, str } from "../components/ui";

export default function Home() {
  const { gameId = "" } = useParams();
  useGameId(gameId);
  const me = useMe();
  const act = useAct();
  const nav = useNavigate();
  const { data, error, reload } = useLoad(async () => {
    const path = { params: { path: { gameId } } };
    const [convs, chars, report, notes] = await Promise.all([
      api.GET("/games/{gameId}/conversations", path).then(must),
      api.GET("/games/{gameId}/characters", path).then(must),
      api.GET("/games/{gameId}/build", path).then(must),
      api.GET("/games/{gameId}/notes", path).then(must),
    ]);
    return { convs: convs.conversations, chars: chars.characters, report, notes: notes.notes };
  }, [gameId]);
  if (!data) return <Loading error={error} />;
  const { convs, chars, report, notes } = data;
  const s = report.stats;

  const newConversation = (f: FormData) =>
    act(async () => {
      const g = must(
        await api.POST("/games/{gameId}/conversations", {
          params: { path: { gameId } },
          body: { title: str(f, "title"), key: str(f, "key"), summary: str(f, "summary") },
        }),
      );
      nav("/conversations/" + g.conversation.id);
    });
  const newCharacter = (f: FormData) =>
    act(async () => {
      const c = must(
        await api.POST("/games/{gameId}/characters", {
          params: { path: { gameId } },
          body: { name: str(f, "name"), key: str(f, "key"), bible: str(f, "bible"), companion: f.get("companion") === "on" },
        }),
      );
      nav("/characters/" + c.id);
    });

  return (
    <>
      <div className="row spread">
        <h1>Content</h1>
        <Link to={"/games/" + gameId + "/build"}>
          {report.ok ? <Badge tone="ok">builds</Badge> : <Badge tone="err">{report.errors.length} build errors</Badge>}
        </Link>
      </div>
      <p className="muted">
        {s.conversations} conversations · {s.nodes} lines · {s.choices} choices · {s.words} words · {s.quests} quests ·{" "}
        <Link to={"/games/" + gameId + "/notes"}>{s.openNotes} open notes</Link>
      </p>

      <div className="grid2">
        <section>
          <h2>Conversations</h2>
          <div className="list">
            {convs.map((c) => (
              <Link key={c.id} to={"/conversations/" + c.id} className="card link-card">
                <div className="row spread">
                  <h3>{c.title}</h3>
                  <Key k={c.key} />
                </div>
                {c.label && <div className="quote">“{c.label}”</div>}
                {c.summary && <div className="excerpt muted">{c.summary}</div>}
                <div className="row tight">
                  {c.opening && <Badge tone="ok">opens the game</Badge>}
                  {c.condition && <code className="key">if {c.condition}</code>}
                  {!c.startNodeId && <Badge tone="err">no start</Badge>}
                </div>
              </Link>
            ))}
            {!convs.length && <p className="muted">No conversations yet.</p>}
          </div>
          {me.human && (
            <details className="panel" open={!convs.length}>
              <summary>New conversation</summary>
              <Form onSubmit={newConversation}>
                <input name="title" placeholder="Title (authoring only)" aria-label="Title" required maxLength={200} />
                <input className="script" name="key" placeholder="key for scripts (optional; made from the title)" aria-label="Key" />
                <textarea name="summary" placeholder="What is this conversation for? (authoring only)" aria-label="Summary" rows={2} />
                <div>
                  <button>Create</button>
                </div>
              </Form>
            </details>
          )}
        </section>

        <section>
          <h2>Characters</h2>
          <div className="list">
            {chars.map((c) => (
              <Link key={c.id} to={"/characters/" + c.id} className="card link-card">
                <div className="row spread">
                  <h3>{c.name}</h3>
                  <span className="row tight">
                    {c.companion && <Badge tone="ok">companion</Badge>}
                    <Key k={c.key} />
                  </span>
                </div>
                {c.bible && <div className="excerpt muted">{c.bible}</div>}
              </Link>
            ))}
            {!chars.length && <p className="muted">No characters yet.</p>}
          </div>
          {me.human && (
            <details className="panel" open={!chars.length}>
              <summary>New character</summary>
              <Form onSubmit={newCharacter}>
                <input name="name" placeholder="Name" aria-label="Name" required maxLength={100} />
                <input className="script" name="key" placeholder="key for scripts (optional; made from the name)" aria-label="Key" />
                <textarea name="bible" placeholder="Bible: voice, wants, secrets (authoring only)" aria-label="Bible" rows={3} />
                <label className="check">
                  <input type="checkbox" name="companion" /> Companion
                </label>
                <div>
                  <button>Create</button>
                </div>
              </Form>
            </details>
          )}
        </section>
      </div>

      <section>
        <h2>Notes on the whole game</h2>
        <Notes game={gameId} kind="game" id="" notes={notesFor(notes, "game", "")} onChange={reload} />
      </section>
    </>
  );
}
