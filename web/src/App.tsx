import { useCallback, useEffect, useState } from "react";
import { Link, Navigate, Route, Routes, useLocation } from "react-router";
import { Boundary } from "./components/Boundary";
import { ErrorState } from "./components/ErrorState";
import { Button, CloseIcon, MenuIcon, MoonIcon, Skeleton, SunIcon, cx } from "./components/ui";
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
  const [menuOpen, setMenuOpen] = useState(false);

  // The overview is at "/" and its detail pages are at "/servers/:id", so this tab is lit
  // by two unrelated paths. NavLink's `end` matches only the first and its default
  // matches everything including /subscribers; an explicit predicate is shorter than
  // either workaround and does not mislead a reader into thinking one route is involved.
  const onServers = pathname === "/" || pathname.startsWith("/servers");
  const onSubscribers = pathname.startsWith("/subscribers");

  // Arriving somewhere new folds the menu away. Tapping the tab you are already on leaves
  // the path alone, so the links close it themselves as well.
  useEffect(() => {
    setMenuOpen(false);
  }, [pathname]);

  return (
    <div className="min-h-full">
      {/* Escape closes the menu. A listener on the header rather than the document is
          enough: the only way to have opened it is to have pressed its button, so focus
          is inside here. */}
      <header
        className="border-b border-line bg-card"
        onKeyDown={(e) => {
          if (e.key === "Escape") setMenuOpen(false);
        }}
      >
        <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-4 px-4 py-3">
          <Link to="/" className="flex items-center gap-2">
            <img src="/favicon.svg" alt="" width={26} height={26} />
            <span className="font-semibold">vlessvmore</span>
          </Link>

          <Button
            variant="ghost"
            className="ml-auto sm:hidden"
            aria-expanded={menuOpen}
            aria-controls="site-menu"
            aria-label={menuOpen ? "Close the menu" : "Open the menu"}
            onClick={() => setMenuOpen((open) => !open)}
          >
            {menuOpen ? <CloseIcon /> : <MenuIcon />}
          </Button>

          {/*
            One copy of the tabs and the account controls, not a phone set and a desktop
            set. The row above is `flex-wrap`, so at phone width this box takes a full line
            of its own and stacks below the wordmark; from `sm` up it collapses back into
            the row and the burger disappears.

            Rendering the two layouts as separate subtrees would have been easier to read
            and wrong: every control would exist twice in the accessible tree, and the copy
            hidden by a media query is still a duplicate name for anything that searches
            the DOM rather than looks at the screen.
          */}
          <div
            id="site-menu"
            className={cx(
              menuOpen ? "flex w-full flex-col gap-3 pb-1" : "hidden",
              "sm:flex sm:w-auto sm:flex-1 sm:flex-row sm:items-center sm:gap-4 sm:pb-0",
            )}
          >
            <nav className="flex flex-col gap-1 text-sm sm:ml-4 sm:flex-row">
              <Link
                to="/"
                onClick={() => setMenuOpen(false)}
                className={cx(
                  "rounded-lg px-3 py-2 sm:px-2 sm:py-1",
                  onServers ? "bg-line" : "text-muted hover:text-ink",
                )}
              >
                Servers
              </Link>
              <Link
                to="/subscribers"
                onClick={() => setMenuOpen(false)}
                className={cx(
                  "rounded-lg px-3 py-2 sm:px-2 sm:py-1",
                  onSubscribers ? "bg-line" : "text-muted hover:text-ink",
                )}
              >
                Subscribers
              </Link>
            </nav>

            <div className="flex items-center gap-2 border-t border-line pt-3 sm:ml-auto sm:border-t-0 sm:pt-0">
              {/* Hidden in the bar at phone width for want of room, but there is room for
                  it here — and inside a menu that signs you out, whose account this is
                  matters. */}
              <span className="min-w-0 truncate text-sm text-muted">{username}</span>
              <div className="ml-auto flex items-center gap-2 sm:ml-0">
                <Button
                  variant="ghost"
                  onClick={toggle}
                  aria-label={theme === "dark" ? "Switch to the light theme" : "Switch to the dark theme"}
                >
                  {theme === "dark" ? <MoonIcon /> : <SunIcon />}
                </Button>
                <Button
                  onClick={() => logout.mutate(undefined, { onSuccess: onSignedOut })}
                  disabled={logout.isPending}
                >
                  Sign out
                </Button>
              </div>
            </div>
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
