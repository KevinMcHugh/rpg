import { createContext, useCallback, useContext, useEffect, useState } from "react";
import type { Me } from "./api/client";

/** useLoad runs load now and whenever reload is called or deps change. */
export function useLoad<T>(load: () => Promise<T>, deps: unknown[]) {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<string>();
  const [n, setN] = useState(0);
  useEffect(() => {
    let live = true;
    load().then(
      (d) => live && (setData(d), setError(undefined)),
      (e: Error) => live && setError(e.message),
    );
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, n]);
  const reload = useCallback(() => setN((x) => x + 1), []);
  return { data, error, reload };
}

/** The signed-in caller: who they are and whether they may change content. */
export const MeContext = createContext<Me>({ keyId: "", name: "", human: false });
export const useMe = () => useContext(MeContext);

type Flash = { text: string; error?: boolean } | null;
export const FlashContext = createContext<(f: Flash) => void>(() => {});
/** useAct wraps a write: it shows the server's refusal, or "Saved.", and
 *  reports whether it worked. */
export function useAct() {
  const flash = useContext(FlashContext);
  return useCallback(
    async (fn: () => Promise<unknown>, ok = "Saved."): Promise<boolean> => {
      try {
        await fn();
        if (ok) flash({ text: ok });
        return true;
      } catch (e) {
        flash({ text: (e as Error).message, error: true });
        return false;
      }
    },
    [flash],
  );
}

export type { Flash };

/** The game being worked on, which the header's links follow. Pages set it:
 *  a game's own pages from the URL, a character or conversation's page from
 *  what it belongs to. */
export type GameRef = { id: string; name: string; ruleset: string } | null;
export const GameContext = createContext<{ game: GameRef; setGameId: (id: string) => void }>({ game: null, setGameId: () => {} });
export const useGame = () => useContext(GameContext).game;

/** useGameId makes id the current game. */
export function useGameId(id: string | undefined) {
  const { setGameId } = useContext(GameContext);
  useEffect(() => {
    if (id) setGameId(id);
  }, [id, setGameId]);
}

const lastGameKey = "rpg.lastGame";
/** The game last worked on in this browser, for "/" to return to. */
export function lastGame(): string | null {
  try {
    return localStorage.getItem(lastGameKey);
  } catch {
    return null;
  }
}
export function rememberGame(id: string) {
  try {
    localStorage.setItem(lastGameKey, id);
  } catch {
    /* private window: nothing to remember */
  }
}

export const rulesetNames: Record<string, string> = {
  classic: "Classic (2d6)",
  srd52: "SRD 5.2",
  srd35: "3.5 SRD",
  pf2e: "Pathfinder 2e",
};
