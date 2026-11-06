// The API client, typed from api/openapi.yaml (npm run gen writes
// schema.ts). Every call goes through the same endpoints as MCP and the
// API's other clients, so the rules (who may write what, validation) live
// on the server and nowhere here.
import createClient from "openapi-fetch";
import type { components, paths } from "./schema";

export type S = components["schemas"];
export type Character = S["Character"];
export type Conversation = S["Conversation"];
export type Graph = S["ConversationGraph"];
export type Node = S["Node"];
export type Choice = S["Choice"];
export type Note = S["Note"];
export type Flag = S["Flag"];
export type Skill = S["Skill"];
export type Quest = S["Quest"];
export type BuildReport = S["BuildReport"];
export type Me = S["Me"];

export const api = createClient<paths>({ baseUrl: "/api", credentials: "same-origin" });

/** ApiError is a refused or failed call, with the server's message. */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

// A session that expired sends the browser to sign in again, and back.
api.use({
  onResponse({ response }) {
    if (response.status === 401) {
      window.location.assign("/login?next=" + encodeURIComponent(location.pathname + location.hash));
    }
    return response;
  },
});

/** The longest page the API returns (app.MaxLimit). */
export const maxLimit = 200;

/** all is a whole list, following its pages: page fetches the page that
 *  starts at after (undefined for the first), and field names the list in
 *  the response. Every list operation pages, so call this where a screen
 *  needs every item (a dropdown, the world), not a list's first page. */
export async function all<K extends string, T>(
  field: K,
  page: (after: string | undefined) => Promise<{ next: string } & { [k in K]: T[] }>,
): Promise<T[]> {
  const out: T[] = [];
  let after: string | undefined;
  for (;;) {
    const r = await page(after);
    out.push(...r[field]);
    if (!r.next) return out;
    after = r.next;
  }
}

/** must unwraps an openapi-fetch result, throwing ApiError on failure. */
export function must<T>(r: { data?: T; error?: unknown; response: Response }): T {
  if (r.error !== undefined || !r.response.ok) {
    const e = r.error as { error?: string } | string | undefined;
    const msg = typeof e === "string" ? e : (e?.error ?? `${r.response.status} ${r.response.statusText}`);
    // The server names the kind of refusal first ("bad input: …"); the
    // rest is the part a person needs. A conflict (409) is a save over
    // someone else's newer one, and its message asks them to reload.
    throw new ApiError(msg.trim().replace(/^(bad input|forbidden|not found|conflict): /, ""), r.response.status);
  }
  return r.data as T;
}
