import { useEffect, useState } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import { api, must, type Flag, type Note, type S, type Skill } from "../api/client";

type Ability = S["Ability"];
import { abilities, skillValueNames, useAct, useGame, useGameId, useLoad, useMe } from "../hooks";
import { Form, Key, Loading, Notes, notesFor, num, str } from "../components/ui";

export default function WorldPage() {
  const { gameId = "" } = useParams();
  useGameId(gameId);
  const me = useMe();
  const act = useAct();
  const nav = useNavigate();
  const loc = useLocation();
  const ruleset = useGame()?.ruleset ?? "classic";
  const d20 = ruleset !== "classic";
  const { data, error, reload } = useLoad(async () => {
    const path = { params: { path: { gameId } } };
    const [flags, skills, quests, notes] = await Promise.all([
      api.GET("/games/{gameId}/flags", path).then(must),
      api.GET("/games/{gameId}/skills", path).then(must),
      api.GET("/games/{gameId}/quests", path).then(must),
      api.GET("/games/{gameId}/notes", path).then(must),
    ]);
    return { flags: flags.flags, skills: skills.skills, quests: quests.quests, notes: notes.notes };
  }, [gameId]);
  useEffect(() => {
    if (data && loc.hash) document.getElementById(loc.hash.slice(1))?.scrollIntoView({ block: "center" });
  }, [data, loc.hash]);
  if (!data) return <Loading error={error} />;

  const addFlag = (f: FormData) =>
    act(async () => {
      must(await api.POST("/games/{gameId}/flags", { params: { path: { gameId } }, body: { key: str(f, "key"), description: str(f, "description"), initialValue: num(f, "initial") } }));
      reload();
    });
  const addSkill = (f: FormData) =>
    act(async () => {
      must(
        await api.POST("/games/{gameId}/skills", {
          params: { path: { gameId } },
          body: { name: str(f, "name"), key: str(f, "key"), initialLevel: num(f, "initial"), ...(str(f, "ability") ? { ability: str(f, "ability") as Ability } : {}) },
        }),
      );
      reload();
    });
  const addQuest = (f: FormData) =>
    act(async () => {
      const q = must(await api.POST("/games/{gameId}/quests", { params: { path: { gameId } }, body: { name: str(f, "name"), key: str(f, "key"), summary: str(f, "summary") } }));
      nav("/quests/" + q.id);
    });

  return (
    <>
      <h1>World</h1>
      <p className="muted">
        The state the story reads and changes. Scripts name these by key; renaming a key updates every script that uses it.
      </p>
      <details className="panel">
        <summary>The script language</summary>
        <div className="rule">
          <b>Conditions</b>met_mayor · not met_mayor · gold &gt;= 10 · skill persuasion &gt;= 3 · approval aria &gt; 10 · quest
          find_brother &gt;= 2 · quest find_brother done · done docks · a and (b or not c)
        </div>
        <div className="rule">
          <b>Effects</b>one per line, or ; between: met_mayor · met_mayor = false · gold += 10 · skill persuasion += 1 · approval aria
          -= 5 · quest find_brother = 2
        </div>
      </details>

      <section id="flags">
        <h2>Flags</h2>
        <p className="muted">A flag is a number; 0 is false. Say what it means, so the next person (or Claude) knows when to set it.</p>
        <table className="plain">
          <thead>
            <tr>
              <th>Key</th>
              <th>Starts at</th>
              <th>Means</th>
            </tr>
          </thead>
          <tbody>
            {data.flags.map((f) => (
              <FlagRow key={f.id} f={f} notes={data.notes} reload={reload} focused={loc.hash === "#flag-" + f.id} />
            ))}
          </tbody>
        </table>
        {!data.flags.length && <p className="muted">No flags yet.</p>}
        {me.human && (
          <Form onSubmit={addFlag} className="panel stack">
            <div className="row">
              <input className="script grow" name="key" placeholder="key, like met_mayor" aria-label="Key" required />
              <input name="initial" type="number" defaultValue={0} aria-label="Starts at" style={{ width: "6rem" }} />
            </div>
            <input name="description" placeholder="What it means (authoring only)" aria-label="Means" />
            <div>
              <button>Add flag</button>
            </div>
          </Form>
        )}
      </section>

      <section id="skills">
        <h2>Skills</h2>
        <p className="muted">
          A skill check rolls the dice this game's ruleset uses and adds what it allows: under {ruleset === "classic" ? "classic, 2d6 plus the skill's level" : "a d20 ruleset, the skill's ability and its value"}. The name shows on checks, so a person writes it, or it is imported with its license.
        </p>
        <table className="plain">
          <thead>
            <tr>
              <th>Name</th>
              <th>Key</th>
              {d20 && <th>Ability</th>}
              <th title={skillValueNames[ruleset]}>Starts at</th>
            </tr>
          </thead>
          <tbody>
            {data.skills.map((s) => (
              <SkillRow key={s.id} s={s} reload={reload} focused={loc.hash === "#skill-" + s.id} d20={d20} />
            ))}
          </tbody>
        </table>
        {!data.skills.length && <p className="muted">No skills yet.</p>}
        {me.human && (
          <Form onSubmit={addSkill} className="panel stack">
            <div className="row">
              <input name="name" placeholder="Name" aria-label="Name" required maxLength={100} className="grow" />
              <input className="script" name="key" placeholder="key (optional)" aria-label="Key" />
              {d20 && <AbilitySelect />}
              <input name="initial" type="number" defaultValue={0} aria-label="Starts at" title={skillValueNames[ruleset]} style={{ width: "6rem" }} />
            </div>
            <p className="muted small">Starts at: {skillValueNames[ruleset]}</p>
            <div>
              <button>Add skill</button>
            </div>
          </Form>
        )}
      </section>

      <section id="quests">
        <h2>Quests</h2>
        <div className="list">
          {data.quests.map((q) => (
            <Link key={q.id} to={"/quests/" + q.id} className="card link-card">
              <div className="row spread">
                <h3>{q.name}</h3>
                <Key k={q.key} />
              </div>
              <div className="muted small">
                {q.stages.length} stage{q.stages.length === 1 ? "" : "s"}
                {q.stages.some((s) => s.completes) ? "" : " · nothing completes it yet"}
              </div>
            </Link>
          ))}
          {!data.quests.length && <p className="muted">No quests yet.</p>}
        </div>
        {me.human && (
          <Form onSubmit={addQuest} className="panel stack">
            <div className="row">
              <input name="name" placeholder="Name (shown in the journal)" aria-label="Name" required maxLength={100} className="grow" />
              <input className="script" name="key" placeholder="key (optional)" aria-label="Key" />
            </div>
            <textarea name="summary" placeholder="What the quest is for (authoring only)" aria-label="Summary" rows={2} />
            <div>
              <button>Add quest</button>
            </div>
          </Form>
        )}
      </section>
    </>
  );
}

function FlagRow({ f, notes, reload, focused }: { f: Flag; notes: Note[]; reload: () => void; focused: boolean }) {
  const me = useMe();
  const act = useAct();
  const [editing, setEditing] = useState(false);
  const save = (fd: FormData) =>
    act(async () => {
      must(
        await api.PATCH("/flags/{flagId}", {
          params: { path: { flagId: f.id } },
          body: { key: str(fd, "key"), description: str(fd, "description"), initialValue: num(fd, "initial") },
        }),
      );
      setEditing(false);
      reload();
    });
  const remove = () =>
    confirm(`Delete ${f.key}? Scripts that use it stay, and the build will report them.`) &&
    act(async () => {
      must(await api.DELETE("/flags/{flagId}", { params: { path: { flagId: f.id } } }));
      reload();
    }, "Deleted.");
  if (editing) {
    return (
      <tr id={"flag-" + f.id}>
        <td colSpan={3}>
          <Form onSubmit={save} reset={false}>
            <div className="row">
              <input className="script grow" name="key" defaultValue={f.key} aria-label="Key" required />
              <input name="initial" type="number" defaultValue={f.initialValue} aria-label="Starts at" style={{ width: "6rem" }} />
            </div>
            <input name="description" defaultValue={f.description} aria-label="Means" />
            <div className="row tight">
              <button className="small">Save</button>
              <button type="button" className="ghost small" onClick={() => setEditing(false)}>
                Cancel
              </button>
              <span className="spacer" />
              <button type="button" className="danger small" onClick={remove}>
                Delete
              </button>
            </div>
          </Form>
        </td>
      </tr>
    );
  }
  return (
    <tr id={"flag-" + f.id} className={focused ? "focused" : ""}>
      <td>
        <Key k={f.key} />
      </td>
      <td>{f.initialValue}</td>
      <td>
        <div className="row spread">
          <span>{f.description}</span>
          {me.human && (
            <button className="ghost small" onClick={() => setEditing(true)}>
              Edit
            </button>
          )}
        </div>
        <Notes game={f.gameId} kind="flag" id={f.id} notes={notesFor(notes, "flag", f.id)} onChange={reload} compact />
      </td>
    </tr>
  );
}

function AbilitySelect({ value }: { value?: string }) {
  return (
    <select name="ability" defaultValue={value ?? ""} aria-label="Ability">
      <option value="">no ability</option>
      {abilities.map((a) => (
        <option key={a} value={a}>
          {a.toUpperCase()}
        </option>
      ))}
    </select>
  );
}

function SkillRow({ s, reload, focused, d20 }: { s: Skill; reload: () => void; focused: boolean; d20: boolean }) {
  const me = useMe();
  const act = useAct();
  const [editing, setEditing] = useState(false);
  const save = (fd: FormData) =>
    act(async () => {
      must(
        await api.PATCH("/skills/{skillId}", {
          params: { path: { skillId: s.id } },
          body: { name: str(fd, "name"), key: str(fd, "key"), initialLevel: num(fd, "initial"), ...(d20 ? { ability: str(fd, "ability") } : {}) },
        }),
      );
      setEditing(false);
      reload();
    });
  const remove = () =>
    confirm(`Delete ${s.name}? Checks on it stay, and the build will report them.`) &&
    act(async () => {
      must(await api.DELETE("/skills/{skillId}", { params: { path: { skillId: s.id } } }));
      reload();
    }, "Deleted.");
  if (editing) {
    return (
      <tr id={"skill-" + s.id}>
        <td colSpan={d20 ? 4 : 3}>
          <Form onSubmit={save} reset={false}>
            <div className="row">
              <input name="name" defaultValue={s.name} aria-label="Name" required className="grow" />
              <input className="script" name="key" defaultValue={s.key} aria-label="Key" required />
              {d20 && <AbilitySelect value={s.ability} />}
              <input name="initial" type="number" defaultValue={s.initialLevel} aria-label="Starts at" style={{ width: "6rem" }} />
            </div>
            <div className="row tight">
              <button className="small">Save</button>
              <button type="button" className="ghost small" onClick={() => setEditing(false)}>
                Cancel
              </button>
              <span className="spacer" />
              <button type="button" className="danger small" onClick={remove}>
                Delete
              </button>
            </div>
          </Form>
        </td>
      </tr>
    );
  }
  return (
    <tr id={"skill-" + s.id} className={focused ? "focused" : ""}>
      <td>
        <div className="row spread">
          <span>{s.name}</span>
          {me.human && (
            <button className="ghost small" onClick={() => setEditing(true)}>
              Edit
            </button>
          )}
        </div>
      </td>
      <td>
        <Key k={s.key} />
      </td>
      {d20 && <td>{s.ability ? s.ability.toUpperCase() : <span className="muted">none</span>}</td>}
      <td>{s.initialLevel}</td>
    </tr>
  );
}
