import { useParams, useSearchParams } from "react-router-dom";
import { all, api, maxLimit, must, type Note } from "../api/client";
import { useAct, useGameId, useLoad, useMe } from "../hooks";
import { Badge, Loading, TargetLink, label } from "../components/ui";

const statuses = ["open", "resolved", "dismissed", "all"] as const;


export default function NotesPage() {
  const { gameId = "" } = useParams();
  useGameId(gameId);
  const me = useMe();
  const act = useAct();
  const [params, setParams] = useSearchParams();
  const status = (params.get("status") ?? "open") as (typeof statuses)[number];
  const { data, error, reload } = useLoad(
    async () =>
      all("notes", async (after) =>
        must(await api.GET("/games/{gameId}/notes", { params: { path: { gameId }, query: { ...(status === "all" ? {} : { status }), after, limit: maxLimit } } })),
      ),
    [gameId, status],
  );
  const set = (n: Note, s: Note["status"]) =>
    act(async () => must(await api.PATCH("/notes/{noteId}", { params: { path: { noteId: n.id } }, body: { status: s, updatedAt: n.updatedAt } })), "").then(reload);
  const remove = (n: Note) =>
    confirm("Delete this note?") &&
    act(async () => must(await api.DELETE("/notes/{noteId}", { params: { path: { noteId: n.id } } })), "Deleted.").then(reload);

  return (
    <>
      <h1>Notes</h1>
      <p className="row tight">
        {statuses.map((s) =>
          s === status ? (
            <strong key={s}>{s}</strong>
          ) : (
            <button key={s} className="link" onClick={() => setParams({ status: s })}>
              {s}
            </button>
          ),
        )}
      </p>
      {!data ? (
        <Loading error={error} />
      ) : (
        <div className="list">
          {data.map((n) => (
            <article key={n.id} className="card">
              <div className="row tight muted small">
                <strong>{n.authorName}</strong>
                {!n.byHuman && <Badge tone="agent">agent</Badge>}
                <Badge>{label(n.kind)}</Badge>
                {n.parentId && <Badge>reply</Badge>}
                {n.status !== "open" && <Badge>{n.status}</Badge>}
                {n.targetKind === "game" ? <span>on the whole game</span> : <TargetLink url={n.url}>on a {n.targetKind}</TargetLink>}
              </div>
              <div className="prose">{n.body}</div>
              {me.human && !n.parentId && (
                <div className="row tight">
                  {n.status === "open" ? (
                    <>
                      <button className="ghost small" onClick={() => set(n, "resolved")}>Resolve</button>
                      <button className="ghost small" onClick={() => set(n, "dismissed")}>Dismiss</button>
                    </>
                  ) : (
                    <button className="ghost small" onClick={() => set(n, "open")}>Reopen</button>
                  )}
                  <span className="spacer" />
                  <button className="danger small" onClick={() => remove(n)}>
                    Delete
                  </button>
                </div>
              )}
            </article>
          ))}
          {!data.length && <p className="muted">No {status === "all" ? "" : status + " "}notes.</p>}
        </div>
      )}
    </>
  );
}
