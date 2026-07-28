import { VlessError, classify } from "../api/errors";
import type { AccessResponse } from "./types";

/**
 * The one request this island makes.
 *
 * Not routed through ApiDispatcher: its Transport.node wraps calls in /api/proxy, which is
 * session-gated, and the resulting 401 classifies as terminal — the page would sit on its
 * skeleton for ever with nothing naming the cause. Plain fetch cannot make that mistake.
 *
 * `classify` is shared, because a text/plain 404 means something different from a JSON one
 * here too.
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
