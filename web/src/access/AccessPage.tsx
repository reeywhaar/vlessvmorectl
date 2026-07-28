import { useCallback, useEffect, useState } from "react";
import { Button, Card, EmptyState, MoonIcon, Skeleton, SunIcon } from "../components/ui";
import { isVlessError, type VlessFailure } from "../api/errors";
import { formatRelative } from "../lib/format";
import { useTheme } from "../lib/theme";
import { AccessEntryCard } from "./AccessEntryCard";
import { fetchAccess } from "./fetchAccess";
import type { AccessResponse } from "./types";

type State =
  | { status: "loading" }
  | { status: "ok"; data: AccessResponse }
  | { status: "incomplete" }
  | { status: "error"; failure: VlessFailure | null };

/**
 * Everything a person can reach with a share link.
 *
 * Does not poll: the endpoint has no session and fans out to one call per account, so a
 * tab left open would be permanent unauthenticated load on every node. One fetch, a
 * stated age, and a Refresh button.
 *
 * No Shell either — its nav and sign-out belong to an operator, not to someone here to
 * copy a link.
 */
export function AccessPage({ token }: { token: string }) {
  const [state, setState] = useState<State>({ status: "loading" });
  const [fetchedAtMs, setFetchedAtMs] = useState(0);

  const load = useCallback(
    (signal?: AbortSignal) => {
      if (!token) {
        // Reached by /access with nothing after it, which is what a share link looks like
        // once a messaging app has truncated it. Answered without a request: there is
        // nothing to ask about, and "incomplete" is a different and more useful thing to
        // say than "this link doesn't work".
        setState({ status: "incomplete" });
        return;
      }
      setState({ status: "loading" });
      fetchAccess(token, signal).then(
        (data) => {
          setState({ status: "ok", data });
          setFetchedAtMs(Date.now());
        },
        (err: unknown) => {
          if (isVlessError(err) && err.failure.kind === "aborted") return;
          setState({ status: "error", failure: isVlessError(err) ? err.failure : null });
        },
      );
    },
    [token],
  );

  useEffect(() => {
    const ac = new AbortController();
    load(ac.signal);
    return () => ac.abort();
  }, [load]);

  useEffect(() => {
    if (state.status === "ok") document.title = `${state.data.subscriber.name} — VPN access`;
  }, [state]);

  return (
    <div className="min-h-full">
      <Chrome />
      <main className="mx-auto max-w-3xl px-4 py-6 pb-16">
        {state.status === "loading" ? <Pending /> : null}
        {state.status === "incomplete" ? <LinkIncomplete /> : null}
        {state.status === "error" ? <AccessError failure={state.failure} onRetry={() => load()} /> : null}
        {state.status === "ok" ? (
          <Loaded data={state.data} fetchedAtMs={fetchedAtMs} onRefresh={() => load()} />
        ) : null}
      </main>
    </div>
  );
}

function Loaded({
  data,
  fetchedAtMs,
  onRefresh,
}: {
  data: AccessResponse;
  fetchedAtMs: number;
  onRefresh: () => void;
}) {
  return (
    <>
      <header className="mb-6">
        <h1 className="text-xl font-semibold">{data.subscriber.name}</h1>
        <p className="text-sm text-muted">
          {data.entries.length === 1 ? "1 connection" : `${data.entries.length} connections`}
        </p>
      </header>

      {data.entries.length === 0 ? (
        <EmptyState title="Nothing here yet">
          This link works, but no connections have been added to it. Whoever gave you the
          link will need to add one.
        </EmptyState>
      ) : (
        <div className="space-y-4">
          {data.entries.map((e) => (
            <AccessEntryCard key={e.id} entry={e} />
          ))}
        </div>
      )}

      <div className="mt-6 flex items-center justify-between gap-3 text-xs text-muted">
        {/* Stated rather than implied. See the component comment. */}
        <span>Updated {fetchedAtMs ? formatRelative(new Date(fetchedAtMs).toISOString()) : "just now"}</span>
        <Button onClick={onRefresh}>Refresh</Button>
      </div>
    </>
  );
}

function Chrome() {
  const { theme, toggle } = useTheme();
  return (
    <header className="border-b border-line bg-card">
      <div className="mx-auto flex max-w-3xl items-center gap-2 px-4 py-3">
        <img src="/favicon.svg" alt="" width={26} height={26} />
        <span className="font-semibold">Your VPN access</span>
        <Button
          variant="ghost"
          className="ml-auto"
          onClick={toggle}
          aria-label={theme === "dark" ? "Switch to the light theme" : "Switch to the dark theme"}
        >
          {theme === "dark" ? <MoonIcon /> : <SunIcon />}
        </Button>
      </div>
    </header>
  );
}

function LinkIncomplete() {
  return (
    <Card className="p-6 text-center">
      <p className="font-medium">That link looks incomplete</p>
      <p className="mx-auto mt-2 max-w-sm text-sm text-muted">
        Share links end in a long code. Check you copied the whole thing — some messaging
        apps cut them short.
      </p>
    </Card>
  );
}

function Pending() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-7 w-40" />
      <Skeleton className="h-64 w-full" />
      <Skeleton className="h-64 w-full" />
    </div>
  );
}

/**
 * Failure, in the reader's terms.
 *
 * Not ErrorState, which is written for an operator and names nodes, proxies and tokens.
 *
 * `refused` is handled alongside `not-found` as defence in depth. A non-JSON 404 —
 * which is what an http.NotFound fallthrough on the backend would produce — classifies as
 * `{kind:"refused", likely:"bad-token"}`, and that copy is about *this panel's* bearer
 * token. A subscriber must never be shown it.
 */
function AccessError({
  failure,
  onRetry,
}: {
  failure: VlessFailure | null;
  onRetry: () => void;
}) {
  const dead = failure?.kind === "not-found" || failure?.kind === "refused";

  return (
    <Card className="p-6 text-center">
      <p className="font-medium">{dead ? "This link doesn't work any more" : "Something went wrong"}</p>
      <p className="mx-auto mt-2 max-w-sm text-sm text-muted">
        {dead
          ? "It may have been replaced, or it may have been copied incompletely. Ask whoever sent it for a new one."
          : "We couldn't load your connections just now. Try again in a minute."}
      </p>
      {/* No retry on a dead link: retrying a revoked token cannot succeed, and a link
          forwarded around a group chat would turn into a steady trickle of requests. */}
      {dead ? null : (
        <Button variant="primary" className="mt-4" onClick={onRetry}>
          Try again
        </Button>
      )}
    </Card>
  );
}
