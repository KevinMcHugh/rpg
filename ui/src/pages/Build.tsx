import { Link } from "react-router-dom";
import { api, must, type S } from "../api/client";
import { useLoad } from "../hooks";
import { Badge, Loading, TargetLink, label } from "../components/ui";


function Issues({ title, issues, tone }: { title: string; issues: S["BuildIssue"][]; tone?: "err" }) {
  if (!issues.length) return null;
  return (
    <>
      <h2>{title}</h2>
      <div className="list">
        {issues.map((is, i) => (
          <div key={i} className="card row tight">
            <Badge tone={tone}>{label(is.code)}</Badge>
            <TargetLink url={is.url}>{is.message}</TargetLink>
          </div>
        ))}
      </div>
    </>
  );
}

export default function BuildPage() {
  const { data, error } = useLoad(async () => must(await api.GET("/build")), []);
  if (!data) return <Loading error={error} />;
  const s = data.stats;
  return (
    <>
      <h1>Build</h1>
      {data.ok ? (
        <p className="flash">The content builds. Every line of it was written by a person.</p>
      ) : (
        <p className="flash err">
          {data.errors.length} error{data.errors.length === 1 ? "" : "s"} stop the content from building.
        </p>
      )}
      <p className="muted">
        {s.characters} characters · {s.conversations} conversations · {s.nodes} lines · {s.choices} choices · {s.flags} flags ·{" "}
        {s.skills} skills · {s.quests} quests · {s.words} words · <Link to="/notes">{s.openNotes} open notes</Link>
      </p>
      <Issues title="Errors" issues={data.errors} tone="err" />
      <Issues title="Warnings" issues={data.warnings} />
      <p className="muted">
        Write the bundle the game runs on with <code>make bundle</code>. It refuses while there are errors.
      </p>
    </>
  );
}
