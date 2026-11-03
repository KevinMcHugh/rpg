import { useEffect, useState } from "react";
import { NavLink, Route, Routes, useLocation } from "react-router-dom";
import { api, must } from "./api/client";
import { FlashContext, MeContext, useLoad, type Flash } from "./hooks";
import { Loading } from "./components/ui";
import Home from "./pages/Home";
import CharacterPage from "./pages/Character";
import ConversationPage from "./pages/Conversation";
import WorldPage from "./pages/World";
import QuestPage from "./pages/Quest";
import NotesPage from "./pages/Notes";
import BuildPage from "./pages/Build";

export default function App() {
  const me = useLoad(async () => must(await api.GET("/me")), []);
  const [flash, setFlash] = useState<Flash>(null);
  const loc = useLocation();

  // A message lasts until the next page, or a few seconds.
  useEffect(() => setFlash(null), [loc.pathname]);
  useEffect(() => {
    if (!flash || flash.error) return;
    const t = setTimeout(() => setFlash(null), 3000);
    return () => clearTimeout(t);
  }, [flash]);

  if (!me.data) return <Loading error={me.error} />;
  return (
    <MeContext.Provider value={me.data}>
      <FlashContext.Provider value={setFlash}>
        <header className="top">
          <NavLink to="/" className="brand">
            RPG
          </NavLink>
          <nav>
            <NavLink to="/" end>
              Content
            </NavLink>
            <NavLink to="/world">World</NavLink>
            <NavLink to="/notes">Notes</NavLink>
            <NavLink to="/build">Build</NavLink>
            <a href="/play">Playtest</a>
          </nav>
          <span className="who muted">{me.data.name}</span>
          <form method="POST" action="/logout">
            <button className="ghost small">Sign out</button>
          </form>
        </header>
        <main>
          {!me.data.human && (
            <p className="flash err">
              You are signed in with an agent key: you can read everything and leave notes, but only a person may change the
              game's content.
            </p>
          )}
          {flash && (
            <p className={"flash" + (flash.error ? " err" : "")} role="status">
              {flash.text}
            </p>
          )}
          <Routes>
            <Route path="/" element={<Home />} />
            <Route path="/characters/:id" element={<CharacterPage />} />
            <Route path="/conversations/:id" element={<ConversationPage />} />
            <Route path="/world" element={<WorldPage />} />
            <Route path="/quests/:id" element={<QuestPage />} />
            <Route path="/notes" element={<NotesPage />} />
            <Route path="/build" element={<BuildPage />} />
            <Route path="*" element={<p className="muted">No such page.</p>} />
          </Routes>
        </main>
      </FlashContext.Provider>
    </MeContext.Provider>
  );
}
