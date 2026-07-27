import { useCallback, useState } from "react";
import { Link, Navigate, Route, Routes, useLocation } from "react-router";
import { Boundary } from "./components/Boundary";
import { ErrorState } from "./components/ErrorState";
import { Button, Skeleton, cx } from "./components/ui";
import { LoginPage } from "./routes/LoginPage";
import { OverviewPage } from "./routes/OverviewPage";
import { ServerPage } from "./routes/ServerPage";
import { SubscribersPage } from "./routes/SubscribersPage";
import { useLogout, useSession } from "./queries/hooks";
import { isVlessError } from "./api/errors";
import { useTheme } from "./lib/theme";

/**
 * The operator's panel. Every route below requires a session.
 *
 * The subscriber's share page is deliberately *not* here. It is a second Vite entry —
 * access.html, src/access.tsx — with its own React root and its own bundle, so none of
 * this file's subtree is served to somebody holding a share link. See vite.config.ts for
 * why that is a separate build rather than a lazy route.
 *
 * The practical consequence for this file: it does not need a public branch above the
 * auth boundary, and the boundary can stay exactly where it has always been.
 */
export function App() {
  /**
   * Bumped whenever the signed-in identity changes, and used as the Boundary's `key`.
   *
   * This is load-bearing, and the reason is easy to miss: the login form is rendered
   * *inside* an error boundary's fallback, because a 401 from /api/me is what puts it
   * there. An error boundary latches — once it has caught, it keeps showing its fallback
   * until something resets it. Clearing the query cache does not reset it, so without
   * this a successful login left the form sitting there looking like nothing had
   * happened, and only a page reload got past it. Logout had the mirror-image problem.
   *
   * Changing the key remounts the subtree, which discards the boundary's error state and
   * re-runs /api/me from scratch. One mechanism, both directions.
   */
  const [epoch, setEpoch] = useState(0);
  const identityChanged = useCallback(() => setEpoch((e) => e + 1), []);

  return (
    <Boundary
      key={epoch}
      pending={<BootSkeleton />}
      fallback={({ error, retry, failure }) => {
        // An expired or absent session is not an error state — it is the login screen.
        // `no_admins` rides along on the same 401 so a fresh install gets the setup card
        // rather than a form nobody can satisfy.
        if (failure?.kind === "unauthorized") {
          return <LoginPage noAdmins={noAdminsFrom(error)} onSignedIn={identityChanged} />;
        }
        return (
          <div className="mx-auto max-w-2xl p-6">
            <ErrorState error={error} retry={retry} failure={failure} />
          </div>
        );
      }}
    >
      <Authenticated onSignedOut={identityChanged} />
    </Boundary>
  );
}

function Authenticated({ onSignedOut }: { onSignedOut: () => void }) {
  // Suspends until /api/me resolves; throws on 401, which the Boundary above turns into
  // the login screen.
  const { data: session } = useSession();
  return (
    <Shell username={session.username} onSignedOut={onSignedOut}>
      <Routes>
        <Route path="/" element={<OverviewPage />} />
        <Route path="/servers/:serverId" element={<ServerPage />} />
        <Route path="/subscribers" element={<SubscribersPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Shell>
  );
}

function Shell({
  username,
  onSignedOut,
  children,
}: {
  username: string;
  onSignedOut: () => void;
  children: React.ReactNode;
}) {
  const logout = useLogout();
  const { theme, toggle } = useTheme();
  const { pathname } = useLocation();

  // The overview is at "/" and its detail pages are at "/servers/:id", so this tab is lit
  // by two unrelated paths. NavLink's `end` matches only the first and its default
  // matches everything including /subscribers; an explicit predicate is shorter than
  // either workaround and does not mislead a reader into thinking one route is involved.
  const onServers = pathname === "/" || pathname.startsWith("/servers");
  const onSubscribers = pathname.startsWith("/subscribers");

  return (
    <div className="min-h-full">
      <header className="border-b border-line bg-card">
        <div className="mx-auto flex max-w-6xl items-center gap-4 px-4 py-3">
          <Link to="/" className="flex items-center gap-2">
            <img src="/favicon.svg" alt="" width={26} height={26} />
            <span className="font-semibold">vlessvmore</span>
          </Link>

          <nav className="ml-4 flex gap-1 text-sm">
            <Link
              to="/"
              className={cx("rounded-lg px-2 py-1", onServers ? "bg-line" : "text-muted hover:text-ink")}
            >
              Servers
            </Link>
            <Link
              to="/subscribers"
              className={cx("rounded-lg px-2 py-1", onSubscribers ? "bg-line" : "text-muted hover:text-ink")}
            >
              Subscribers
            </Link>
          </nav>

          <div className="ml-auto flex items-center gap-2">
            <span className="hidden text-sm text-muted sm:inline">{username}</span>
            <Button variant="ghost" onClick={toggle} aria-label="Toggle theme">
              {theme === "dark" ? "☾" : "☀"}
            </Button>
            <Button
              onClick={() => logout.mutate(undefined, { onSuccess: onSignedOut })}
              disabled={logout.isPending}
            >
              Sign out
            </Button>
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-4 py-6">{children}</main>
    </div>
  );
}

function BootSkeleton() {
  return (
    <div className="mx-auto max-w-6xl p-6">
      <Skeleton className="h-8 w-48" />
      <div className="mt-6 grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        <Skeleton className="h-40 w-full" />
        <Skeleton className="h-40 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    </div>
  );
}

/** The backend adds `no_admins` to its 401 body when admins.json is empty. */
function noAdminsFrom(error: unknown): boolean {
  if (!isVlessError(error)) return false;
  const f = error.failure;
  return f.kind === "unauthorized" && f.noAdmins === true;
}
