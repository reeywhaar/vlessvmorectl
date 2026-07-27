import { VlessError, classify } from "../api/errors";
import type { AccessResponse } from "./types";

/**
 * The one request this island makes.
 *
 * Written out rather than routed through ApiDispatcher, and the reason is worth stating
 * because the dispatcher would have looked like the obvious reuse. Its Transport has two
 * methods: `panel`, which is a plain same-origin fetch, and `node`, which wraps the call
 * in /api/proxy. The proxy is behind requireSession, so for an anonymous visitor it
 * answers 401 — and 401 classifies as `unauthorized`, which the panel's query client
 * treats as terminal. The whole failure would present as a page that sits on its skeleton
 * for ever with nothing on screen naming the cause.
 *
 * Importing the dispatcher here would put that mistake one autocomplete away. Sixteen
 * lines of fetch cannot make it.
 *
 * `classify` *is* shared, and deliberately: it is the one place that knows a text/plain
 * 404 means something different from a JSON one, and this page needs that knowledge as
 * much as the panel does.
 */
export async function fetchAccess(token: string, signal?: AbortSignal): Promise<AccessResponse> {
  const url = new URL(`/api/access/${encodeURIComponent(token)}`, window.location.origin);

  let res: Response;
  try {
    res = await fetch(url, {
      method: "GET",
      headers: { Accept: "application/json" },
      // No credentials. An operator reading this page in a signed-in browser would
      // otherwise send their session cookie to an endpoint that must not vary on it, and
      // the omission documents that the page genuinely needs no identity.
      credentials: "omit",
      ...(signal ? { signal } : {}),
    });
  } catch (cause) {
    if (signal?.aborted) throw VlessError.create({ kind: "aborted" });
    throw VlessError.create({ kind: "unreachable", reason: "offline", detail: String(cause) });
  }

  if (!res.ok) {
    throw VlessError.create(await classify(res));
  }
  return (await res.json()) as AccessResponse;
}
