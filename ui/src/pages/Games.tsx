import { Link, useNavigate } from "react-router-dom";
import { all, api, maxLimit, must } from "../api/client";
import { rememberGame, rulesetNames, useAct, useLoad, useMe } from "../hooks";
import { Badge, Form, Key, Loading, str } from "../components/ui";
import type { S } from "../api/client";

export default function GamesPage() {
  const me = useMe();
  const act = useAct();
  const nav = useNavigate();
  const { data, error } = useLoad(() => all("games", async (after) => must(await api.GET("/games", { params: { query: { after, limit: maxLimit } } }))), []);
  if (!data) return <Loading error={error} />;

  const create = (f: FormData) =>
    act(async () => {
      const g = must(
        await api.POST("/games", { body: { name: str(f, "name"), key: str(f, "key"), ruleset: str(f, "ruleset") as S["Ruleset"] } }),
      );
      rememberGame(g.id);
      nav("/games/" + g.id);
    });

  return (
    <>
      <h1>Games</h1>
      <p className="muted">
        Each game has its own characters, conversations, world and notes, and its own ruleset for checks. You see the games
        you made and the ones you were invited to.
      </p>
      <div className="list">
        {data.map((g) => (
          <Link key={g.id} to={"/games/" + g.id} className="card link-card" onClick={() => rememberGame(g.id)}>
            <div className="row spread">
              <h3>{g.name}</h3>
              <span className="row tight">
                <Badge>{rulesetNames[g.ruleset] ?? g.ruleset}</Badge>
                {g.role === "owner" ? <Badge tone="ok">owner</Badge> : <Badge>writer</Badge>}
                <Key k={g.key} />
              </span>
            </div>
          </Link>
        ))}
        {!data.length && <p className="muted">You are in no games yet. Make one, or ask someone to send you an invite link.</p>}
      </div>
      {me.human && (
        <details className="panel" open={!data.length}>
          <summary>New game</summary>
          <Form onSubmit={create}>
            <div className="row">
              <input name="name" placeholder="Name (authoring only)" aria-label="Name" required maxLength={100} className="grow" />
              <input className="script" name="key" placeholder="key (optional)" aria-label="Key" />
            </div>
            <label className="field">
              Ruleset
              <select name="ruleset" defaultValue="classic">
                {Object.entries(rulesetNames).map(([k, v]) => (
                  <option key={k} value={k}>
                    {v}
                  </option>
                ))}
              </select>
            </label>
            <div>
              <button>Create</button>
            </div>
          </Form>
        </details>
      )}
    </>
  );
}
