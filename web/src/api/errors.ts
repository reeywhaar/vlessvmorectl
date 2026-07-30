/**
 * The failure taxonomy for calls to a vlessvmore node.
 *
 * Two facts from the node's source shape all of this, and neither is obvious:
 *
 *  1. vlessvmore has no 401. Every refusal — no token, revoked token, unknown path,
 *     wrong method — is Go's stdlib `404 page not found` with `Content-Type:
 *     text/plain`. Every *genuine* not-found comes from its writeStoreError and is
 *     `application/json` `{"error": ...}`. The content type is the only discriminator,
 *     and it is the difference between telling an operator "your token is wrong" and
 *     "that user does not exist".
 *
 *  2. Our own proxy passes those responses through byte for byte, and marks its *own*
 *     failures with `X-Proxy-Error: 1` and a 502. So a 502 with that header means we
 *     could not reach the node; a 502 without it means the node itself said 502.
 */

export type UnreachableReason =
  // Reported by the Go proxy, which sees the real transport error.
  | "dns"
  | "refused"
  | "tls"
  | "timeout"
  | "canceled"
  | "unknown"
  // Only a browser-side transport could produce these. Declared now so the copy in
  // ErrorState is already exhaustive if a direct mode is ever added; nothing emits
  // them today.
  | "cors"
  | "mixed-content"
  | "offline";

export type VlessFailure =
  /** The node answered 404 with the stdlib page: it rejected our bearer token. */
  | { kind: "refused"; likely: "bad-token" | "not-vlessvmore" }
  /** The node answered 404 with JSON: that user or token genuinely does not exist. */
  | { kind: "not-found"; message: string }
  | { kind: "bad-request"; message: string }
  | { kind: "conflict"; message: string }
  | { kind: "server-error"; status: number; message: string }
  /** Our proxy could not reach the node. */
  | { kind: "unreachable"; reason: UnreachableReason; detail: string }
  /**
   * Our own backend refused: the session is gone.
   *
   * `noAdmins` rides along because it arrives on this same 401 — a fresh install has
   * nobody to sign in as, and the SPA needs to show a setup card rather than a form that
   * cannot succeed. `passkeysEnabled` rides along for the same structural reason: this body
   * is the only thing the sign-in screen receives, and the passkey button has to know
   * whether to exist.
   */
  | { kind: "unauthorized"; noAdmins?: boolean; passkeysEnabled?: boolean }
  /** Our own backend refused the target URL. A bug in the client, not a state. */
  | { kind: "forbidden"; message: string }
  | { kind: "aborted" };

/**
 * The constructor is left as Error's own — `new VlessError(message)` behaves exactly
 * like any other Error, which is what anything catching it, logging it or serialising
 * it will assume. The extra fields are attached by the factory instead.
 */
export class VlessError extends Error {
  override name = "VlessError";
  failure!: VlessFailure;
  serverId: string | undefined;

  static create(failure: VlessFailure, serverId?: string): VlessError {
    const err = new VlessError(describeFailure(failure));
    err.failure = failure;
    err.serverId = serverId;
    return err;
  }
}

export function isVlessError(e: unknown): e is VlessError {
  return e instanceof VlessError;
}

/** True when retrying could plausibly succeed without anything being fixed first. */
export function isTransient(f: VlessFailure): boolean {
  return f.kind === "unreachable" || f.kind === "server-error";
}

/**
 * A rejected token can never fix itself, so polling must stop rather than back off:
 * every attempt costs the node ~100ms of deliberate refusal padding and writes a
 * warning line in the operator's log.
 */
export function isTerminal(f: VlessFailure): boolean {
  return f.kind === "refused" || f.kind === "forbidden" || f.kind === "unauthorized";
}

export function describeFailure(f: VlessFailure): string {
  switch (f.kind) {
    case "refused":
      return f.likely === "bad-token"
        ? "the server rejected this panel's token"
        : "something answered that does not look like vlessvmore";
    case "not-found":
      return f.message || "not found";
    case "bad-request":
      return f.message || "the server rejected the request";
    case "conflict":
      return f.message || "that name is already taken";
    case "server-error":
      return f.message || `the server returned ${f.status}`;
    case "unreachable":
      return `could not reach the server (${f.reason})`;
    case "unauthorized":
      return "your session has expired";
    case "forbidden":
      return f.message || "this panel does not manage that server";
    case "aborted":
      return "cancelled";
  }
}

/** The stdlib 404 body, verbatim. Anything else means something other than vlessvmore. */
const STDLIB_NOT_FOUND = "404 page not found";

/**
 * Turn a Response we actually hold into a failure.
 *
 * Only called for non-2xx. It is transport-agnostic on purpose: because the proxy
 * forwards upstream responses verbatim, this same function would classify a direct
 * browser call correctly too, so the logic never has to be written twice.
 */
export async function classify(res: Response): Promise<VlessFailure> {
  const raw = await res.text();
  const contentType = res.headers.get("content-type") ?? "";
  const isJson = contentType.includes("application/json");

  const message = (): string => {
    if (!isJson) return raw.trim();
    try {
      const parsed: unknown = JSON.parse(raw);
      if (parsed && typeof parsed === "object" && "error" in parsed) {
        return String((parsed as { error: unknown }).error);
      }
    } catch {
      /* fall through to the raw body */
    }
    return raw.trim();
  };

  // Our proxy's own marker, checked first — otherwise an upstream 502 and a
  // "we couldn't reach it" 502 are indistinguishable, and the panel would tell an
  // operator their node is down when it answered perfectly well.
  if (res.headers.get("x-proxy-error") === "1") {
    let reason: UnreachableReason = "unknown";
    let detail = raw.trim();
    try {
      const parsed = JSON.parse(raw) as { proxy_error?: string; error?: string };
      if (parsed.proxy_error) reason = parsed.proxy_error as UnreachableReason;
      if (parsed.error) detail = parsed.error;
    } catch {
      /* keep the defaults */
    }
    return { kind: "unreachable", reason, detail };
  }

  // Our own backend, not a node.
  if (res.status === 401) {
    try {
      const parsed = JSON.parse(raw) as { no_admins?: boolean; passkeys_enabled?: boolean };
      const flags = {
        ...(parsed.no_admins ? { noAdmins: true as const } : {}),
        ...(parsed.passkeys_enabled ? { passkeysEnabled: true as const } : {}),
      };
      return { kind: "unauthorized", ...flags };
    } catch {
      /* a 401 with no body is still a 401 */
    }
    return { kind: "unauthorized" };
  }
  if (res.status === 403) return { kind: "forbidden", message: message() };

  if (res.status === 404) {
    // The single most important branch in this file. See the note at the top.
    if (isJson) return { kind: "not-found", message: message() };
    return {
      kind: "refused",
      // A reverse proxy or CDN in front of the node returns its *own* HTML 404, which
      // is neither JSON nor the stdlib string. Calling that a bad token would send the
      // operator hunting entirely the wrong bug.
      likely: raw.trim() === STDLIB_NOT_FOUND ? "bad-token" : "not-vlessvmore",
    };
  }

  if (res.status === 400) return { kind: "bad-request", message: message() };
  if (res.status === 409) return { kind: "conflict", message: message() };

  return { kind: "server-error", status: res.status, message: message() };
}
