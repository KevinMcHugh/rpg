import { useCallback, useEffect, useRef, useState } from "react";
import { Navigate, NavLink, Route, Routes, useLocation } from "react-router-dom";
import { api, must } from "./api/client";
import { FlashContext, GameContext, MeContext, lastGame, rememberGame, rulesetNames, useLoad, type Flash, type GameRef } from "./hooks";
import { Badge, Loading } from "./components/ui";
import GamesPage from "./pages/Games";
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
  const [game, setGame] = useState<GameRef>(null);
  const loc = useLocation();

  // A message lasts until the next page, or a few seconds.
  useEffect(() => setFlash(null), [loc.pathname]);
  useEffect(() => {
    if (!flash || flash.error) return;
    const t = setTimeout(() => setFlash(null), 3000);
    return () => clearTimeout(t);
  }, [flash]);

  // Pages name their game; this fetches it once per change, and ignores an
  // answer that arrives after the page moved on to another game.
  const wanted = useRef<string>("");
  const setGameId = useCallback((id: string) => {
    if (wanted.current === id) return;
    wanted.current = id;
    api.GET("/games/{gameId}", { params: { path: { gameId: id } } }).then((r) => {
      if (r.data && wanted.current === id) {
        setGame({ id: r.data.id, name: r.data.name, ruleset: r.data.ruleset });
        rememberGame(r.data.id);
      }
    });
  }, []);

  if (!me.data) return <Loading error={me.error} />;
  const g = game ? "/games/" + game.id : "";
  return (
    <MeContext.Provider value={me.data}>
      <GameContext.Provider value={{ game, setGameId }}>
        <FlashContext.Provider value={setFlash}>
          <header className="top">
            <NavLink to="/games" end className="brand">
              RPG
            </NavLink>
            {game && (
              <span className="row tight game-name">
                <NavLink to={g} end>
                  {game.name}
                </NavLink>
                <Badge>{rulesetNames[game.ruleset] ?? game.ruleset}</Badge>
              </span>
            )}
            <nav>
              {game && (
                <>
                  <NavLink to={g} end>
                    Content
                  </NavLink>
                  <NavLink to={g + "/world"}>World</NavLink>
                  <NavLink to={g + "/notes"}>Notes</NavLink>
                  <NavLink to={g + "/build"}>Build</NavLink>
                  <a href={"/play?game=" + game.id}>Playtest</a>
                </>
              )}
              <NavLink to="/games" end>
                All games
              </NavLink>
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
              <Route path="/" element={<Navigate to={lastGame() ? "/games/" + lastGame() : "/games"} replace />} />
              <Route path="/games" element={<GamesPage />} />
              <Route path="/games/:gameId" element={<Home />} />
              <Route path="/games/:gameId/world" element={<WorldPage />} />
              <Route path="/games/:gameId/notes" element={<NotesPage />} />
              <Route path="/games/:gameId/build" element={<BuildPage />} />
              <Route path="/characters/:id" element={<CharacterPage />} />
              <Route path="/conversations/:id" element={<ConversationPage />} />
              <Route path="/quests/:id" element={<QuestPage />} />
              {/* From before there were games. */}
              <Route path="/world" element={<Navigate to="/games" replace />} />
              <Route path="/notes" element={<Navigate to="/games" replace />} />
              <Route path="/build" element={<Navigate to="/games" replace />} />
              <Route path="*" element={<p className="muted">No such page.</p>} />
            </Routes>
          </main>
        </FlashContext.Provider>
      </GameContext.Provider>
    </MeContext.Provider>
  );
}
