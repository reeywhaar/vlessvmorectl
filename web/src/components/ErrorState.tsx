import type { VlessFailure } from "../api/errors";
import { Button, Card } from "./ui";

/**
 * Turns a typed failure into something an operator can act on.
 *
 * The whole reason the error taxonomy exists is to make this component possible. "Could
 * not load" is useless; "the node rejected this panel's token" tells someone exactly
 * which environment variable to go and look at.
 */
export function ErrorState({
  failure,
  error,
  retry,
  context,
}: {
  failure: VlessFailure | null;
  error: unknown;
  retry: () => void;
  /** e.g. the node's hostname, so a card in a grid says which one broke. */
  context?: string;
}) {
  const { title, body, hint } = explain(failure, error, context);

  return (
    <Card className="border-danger/40">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="font-semibold text-danger">{title}</h3>
          <p className="mt-1 text-sm text-muted">{body}</p>
          {hint ? <p className="mt-2 text-sm text-muted">{hint}</p> : null}
        </div>
        <Button onClick={retry}>Retry</Button>
      </div>
    </Card>
  );
}

interface Explanation {
  title: string;
  body: string;
  hint?: string;
}

function explain(failure: VlessFailure | null, error: unknown, context?: string): Explanation {
  const where = context ? ` on ${context}` : "";

  if (!failure) {
    return {
      title: "Something went wrong",
      body: error instanceof Error ? error.message : String(error),
    };
  }

  switch (failure.kind) {
    case "refused":
      if (failure.likely === "bad-token") {
        return {
          title: `Token rejected${where}`,
          body:
            "The node answered, but it does not recognise the token this panel is using. " +
            "vlessvmore has no 401 — every refusal looks like a 404 — so this is what that means.",
          hint:
            "Check the token for this node in VLESSVMORE_SERVERS. Mint a fresh one on the node with " +
            "`docker exec vlessvmore vlessvmore token create panel --raw`, then restart this panel.",
        };
      }
      return {
        title: `Unexpected response${where}`,
        body:
          "Something answered with a 404 that does not look like vlessvmore — most likely a " +
          "reverse proxy or CDN in front of it, returning its own error page.",
        hint: "Check the URL for this node, and anything sitting in front of it.",
      };

    case "unreachable":
      return { title: `Cannot reach the node${where}`, ...unreachableBody(failure.reason, failure.detail) };

    case "not-found":
      return { title: "Not found", body: failure.message };

    case "bad-request":
      return {
        title: "The node rejected that",
        body: failure.message,
        hint: "vlessvmore refuses unknown fields outright, so this is usually a version mismatch.",
      };

    case "conflict":
      return { title: "Already taken", body: failure.message };

    case "server-error":
      return {
        title: `The node failed${where}`,
        body: failure.message || `It returned ${failure.status}.`,
        hint: "Check the node's own logs — this happened inside vlessvmore, not in this panel.",
      };

    case "unauthorized":
      return { title: "Session expired", body: "Sign in again to continue." };

    case "forbidden":
      return {
        title: "Refused by this panel",
        body: failure.message,
        hint: "This is a bug in the panel rather than a configuration problem — it asked to proxy a URL it does not manage.",
      };

    case "aborted":
      return { title: "Cancelled", body: "The request was cancelled." };
  }
}

function unreachableBody(reason: VlessFailure extends { kind: "unreachable" } ? never : string, detail: string): { body: string; hint?: string } {
  switch (reason) {
    case "dns":
      return {
        body: "The hostname does not resolve from this panel's host.",
        hint: `Check the URL in VLESSVMORE_SERVERS. (${detail})`,
      };
    case "refused":
      return {
        body: "Nothing is listening at that address, or the connection was refused.",
        hint: `The node may be down, or its management port may not be reachable from this container's network. (${detail})`,
      };
    case "tls":
      return {
        body: "The TLS handshake failed.",
        hint: `Usually an expired or untrusted certificate, or an https:// URL pointing at a plaintext port. (${detail})`,
      };
    case "timeout":
      return {
        body: "The node did not answer in time.",
        hint: `It may be overloaded, or a firewall may be dropping packets silently. (${detail})`,
      };
    case "canceled":
      return { body: "The request was cancelled before it finished." };
    case "offline":
      return { body: "Your browser reports it is offline." };
    // Not reachable while every call goes through the proxy. Kept so the switch is
    // exhaustive if a direct-from-browser transport is ever added.
    case "cors":
      return {
        body: "The node is up, but the browser blocked its response.",
        hint: "Add this panel's origin to cors_origins in that node's config.json and restart it.",
      };
    case "mixed-content":
      return {
        body: "This panel is on https:// and the node is configured as http://, so the browser blocked the request.",
      };
    default:
      return { body: detail || "The reason is not clear." };
  }
}
