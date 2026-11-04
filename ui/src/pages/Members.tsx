import { useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api, must } from "../api/client";
import { useAct, useGame, useGameId, useLoad, useMe } from "../hooks";
import { Badge, Form, Loading, str } from "../components/ui";

const lifetimes: [number, string][] = [
  [24, "a day"],
  [168, "a week"],
  [720, "30 days"],
];

const when = (iso: string) => new Date(iso).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });

/** A game's members and, for its owner, the links that bring people in. */
export default function MembersPage() {
  const { gameId = "" } = useParams();
  useGameId(gameId);
  const me = useMe();
  const game = useGame();
  const act = useAct();
  const nav = useNavigate();
  const [link, setLink] = useState<string>();
  const members = useLoad(
    async () => must(await api.GET("/games/{gameId}/members", { params: { path: { gameId } } })).members,
    [gameId],
  );
  const owner = game?.id === gameId && game.role === "owner";
  const invites = useLoad(
    async () =>
      owner ? must(await api.GET("/games/{gameId}/invites", { params: { path: { gameId } } })).invites : [],
    [gameId, owner],
  );
  if (!members.data) return <Loading error={members.error} />;

  const myId = me.user?.id;
  const remove = (userId: string, name: string) => {
    const self = userId === myId;
    if (!confirm(self ? "Leave this game? You will need a new invite to come back." : `Take ${name} out of this game?`)) return;
    act(async () => {
      must(await api.DELETE("/games/{gameId}/members/{userId}", { params: { path: { gameId, userId } } }));
      if (self) nav("/games");
      else members.reload();
    }, self ? "You left the game." : "Removed.");
  };
  const invite = (f: FormData) =>
    act(async () => {
      const inv = must(
        await api.POST("/games/{gameId}/invites", {
          params: { path: { gameId } },
          body: { note: str(f, "note"), expiresInHours: Number(str(f, "hours")) },
        }),
      );
      setLink(inv.url);
      invites.reload();
    }, "");
  const revoke = (id: string) =>
    confirm("Revoke this link? Nobody will be able to use it.") &&
    act(async () => {
      must(await api.DELETE("/invites/{inviteId}", { params: { path: { inviteId: id } } }));
      invites.reload();
    }, "Revoked.");

  return (
    <>
      <h1>Members</h1>
      <p className="muted">
        Only members see this game. Writers write its content and leave notes; its owner may also invite people and take
        them out. Each member's Claude sees what they see.
      </p>
      <div className="list">
        {members.data.map((m) => (
          <div key={m.userId} className="card row spread">
            <span className="row tight">
              <strong>{m.name}</strong>
              {m.userId === myId && <span className="muted">(you)</span>}
              <Badge tone={m.role === "owner" ? "ok" : undefined}>{m.role}</Badge>
            </span>
            <span className="row tight">
              <span className="muted small">joined {when(m.joinedAt)}</span>
              {me.human && m.role !== "owner" && (owner || m.userId === myId) && (
                <button className="danger small" onClick={() => remove(m.userId, m.name)}>
                  {m.userId === myId ? "Leave" : "Remove"}
                </button>
              )}
            </span>
          </div>
        ))}
      </div>

      {owner && me.human && (
        <section className="panel">
          <h2>Invite someone</h2>
          <p className="muted">
            A link works once, for one person, who joins as a writer. They sign in with Google (which makes their account if
            they are new) or with an api key. The link is shown only now: copy it and send it to them.
          </p>
          <Form onSubmit={invite}>
            <div className="row">
              <input name="note" placeholder="Who it is for (only you see this)" aria-label="Who it is for" maxLength={200} className="grow" />
              <label className="row tight">
                works for
                <select name="hours" defaultValue="168">
                  {lifetimes.map(([h, l]) => (
                    <option key={h} value={h}>
                      {l}
                    </option>
                  ))}
                </select>
              </label>
              <button>Make a link</button>
            </div>
          </Form>
          {link && (
            <div className="stack" style={{ marginTop: "1rem" }}>
              <label className="field">
                The link
                <input readOnly value={link} onFocus={(e) => e.currentTarget.select()} className="script" aria-label="Invite link" />
              </label>
              <div className="row tight">
                <button
                  className="ghost small"
                  onClick={() => navigator.clipboard?.writeText(link).then(() => act(async () => {}, "Copied."))}
                >
                  Copy
                </button>
                <button className="ghost small" onClick={() => setLink(undefined)}>
                  Done
                </button>
              </div>
            </div>
          )}
          {!!invites.data?.length && (
            <>
              <h3>Links not used yet</h3>
              <div className="list">
                {invites.data.map((i) => (
                  <div key={i.id} className="card row spread">
                    <span>{i.note || <span className="muted">No note</span>}</span>
                    <span className="row tight">
                      <span className="muted small">expires {when(i.expiresAt)}</span>
                      <button className="danger small" onClick={() => revoke(i.id)}>
                        Revoke
                      </button>
                    </span>
                  </div>
                ))}
              </div>
            </>
          )}
        </section>
      )}
      {!owner && game?.id === gameId && <p className="muted">Only the game's owner can invite people.</p>}
    </>
  );
}
