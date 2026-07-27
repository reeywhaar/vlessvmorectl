import { useState } from "react";
import { isVlessError } from "../api/errors";
import { useLogin } from "../queries/hooks";
import { Button, Card, Field, Input } from "../components/ui";

/**
 * `noAdmins` comes from our backend's 401 body when admins.json is empty.
 *
 * It does tell an anonymous visitor that this panel is unclaimed. That is a deliberate
 * trade: there is no web bootstrap to exploit — an administrator can only be created from
 * a shell on the host — and the alternative is an operator staring at a form that cannot
 * possibly succeed, with nothing on screen to say why.
 */
export function LoginPage({
  noAdmins,
  onSignedIn,
}: {
  noAdmins: boolean;
  /**
   * Called once the credentials are accepted.
   *
   * This screen renders inside an error boundary's fallback — a 401 from /api/me is what
   * put it there — and an error boundary keeps showing its fallback until something
   * resets it. So a successful login has to say so upward; nothing else would move the
   * page off this form.
   */
  onSignedIn: () => void;
}) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const login = useLogin();

  const message = login.error ? loginMessage(login.error) : null;

  return (
    <main className="mx-auto flex min-h-full max-w-md flex-col justify-center px-4 py-12">
      <div className="mb-6 flex items-center gap-3">
        <img src="/favicon.svg" alt="" width={40} height={40} />
        <div>
          <h1 className="text-xl font-semibold">vlessvmore</h1>
          <p className="text-sm text-muted">Control panel</p>
        </div>
      </div>

      {noAdmins ? (
        <Card>
          <h2 className="font-semibold">No administrators yet</h2>
          <p className="mt-2 text-sm text-muted">
            Nobody can sign in until one exists. There is deliberately no way to create the
            first account from here — that would be a race against whoever reaches this URL
            first — so create it from a shell on the host:
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg border border-line bg-bg p-3 text-xs">
            docker exec vlessvmorectl vlessvmorectl users add alice
          </pre>
          <p className="mt-3 text-sm text-muted">Then reload this page.</p>
        </Card>
      ) : (
        <Card>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              login.mutate({ username, password }, { onSuccess: onSignedIn });
            }}
            className="space-y-4"
          >
            <Field label="Username">
              <Input
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                autoFocus
                required
              />
            </Field>
            <Field label="Password">
              <Input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                required
              />
            </Field>

            {message ? (
              <p role="alert" className="text-sm text-danger">
                {message}
              </p>
            ) : null}

            <Button
              type="submit"
              variant="primary"
              className="w-full"
              disabled={login.isPending || !username || !password}
            >
              {login.isPending ? "Signing in…" : "Sign in"}
            </Button>
          </form>
        </Card>
      )}
    </main>
  );
}

function loginMessage(error: unknown): string {
  if (!isVlessError(error)) {
    return error instanceof Error ? error.message : "Sign-in failed.";
  }
  switch (error.failure.kind) {
    case "unauthorized":
      // The backend deliberately does not say which half was wrong, and neither do we:
      // otherwise this becomes a username oracle that no rate limit fully closes.
      return "Incorrect username or password.";
    case "server-error":
      return error.failure.status === 429
        ? "Too many attempts. Wait a minute and try again."
        : error.failure.message;
    default:
      return error.message;
  }
}
