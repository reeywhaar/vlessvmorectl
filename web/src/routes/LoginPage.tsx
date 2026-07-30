import { useCallback, useState } from "react";
import { isVlessError } from "../api/errors";
import { useLogin, usePasskeyLogin } from "../queries/hooks";
import { Button, Card, Field, Input, KeyIcon } from "../components/ui";
import { useConditionalPasskey } from "../features/passkeys/useConditionalPasskey";
import { passkeyFailure, passkeysSupported } from "../features/passkeys/webauthn";

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
  passkeysEnabled,
  onSignedIn,
}: {
  noAdmins: boolean;
  /**
   * Whether the backend has a passkey origin configured, from the same 401 body as noAdmins.
   *
   * This does disclose "this panel accepts passkeys" to an anonymous visitor, which is the
   * same class of disclosure as noAdmins and unavoidable if the button is to exist for the
   * operator who is, right now, anonymous. It reflects configuration only, never a count.
   */
  passkeysEnabled: boolean;
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
  const passkeyLogin = usePasskeyLogin();

  const usePasskeys = passkeysEnabled && !noAdmins && passkeysSupported();

  // The autofill path hands a finished ceremony straight to the mutation, rather than
  // starting a second one the browser would refuse.
  const onAssertion = useCallback(
    (result: { state: string; credential: unknown }) => {
      passkeyLogin.mutate(result, { onSuccess: onSignedIn });
    },
    [passkeyLogin, onSignedIn],
  );
  const { stop, failure: conditionalFailure } = useConditionalPasskey({
    enabled: usePasskeys,
    onAssertion,
  });

  const message = login.error
    ? loginMessage(login.error)
    : passkeyLogin.error
      ? passkeyMessage(passkeyLogin.error)
      : conditionalFailure
        ? describePasskeyFailure(conditionalFailure)
        : null;

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
            onSubmit={async (e) => {
              e.preventDefault();
              // Before anything else. A conditional request still in flight would otherwise
              // be free to resolve later and sign in as whoever the autofill picked, racing
              // this password login — possibly for a different account.
              await stop();
              login.mutate({ username, password }, { onSuccess: onSignedIn });
            }}
            className="space-y-4"
          >
            <Field label="Username">
              <Input
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                // The "webauthn" token is what lets a browser offer a passkey from this
                // field's autofill. Added only when the feature is on, so a panel without it
                // keeps the plain value.
                autoComplete={usePasskeys ? "username webauthn" : "username"}
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

          {usePasskeys ? (
            <>
              <div className="my-4 flex items-center gap-3 text-xs text-muted">
                <span className="h-px flex-1 bg-line" />
                or
                <span className="h-px flex-1 bg-line" />
              </div>
              <Button
                type="button"
                className="w-full"
                disabled={passkeyLogin.isPending}
                onClick={async () => {
                  // Same reason as the form: only one ceremony may be outstanding, and the
                  // conditional one's challenge is dead once aborted — the mutation fetches
                  // a fresh one.
                  await stop();
                  passkeyLogin.mutate(undefined, { onSuccess: onSignedIn });
                }}
              >
                <KeyIcon />
                {passkeyLogin.isPending ? "Waiting for your device…" : "Sign in with a passkey"}
              </Button>
            </>
          ) : null}
        </Card>
      )}
    </main>
  );
}

/** A passkey sign-in can fail in the browser or at the server; both land here. */
function passkeyMessage(error: unknown): string {
  if (!isVlessError(error)) return describePasskeyFailure(passkeyFailure(error));
  switch (error.failure.kind) {
    case "unauthorized":
      // The backend says only "that passkey was not accepted" — never which part was wrong —
      // and neither do we.
      return "That passkey was not accepted. Try your password instead.";
    case "server-error":
      return error.failure.status === 429
        ? "Too many attempts. Wait a minute and try again."
        : error.failure.message;
    default:
      return error.message;
  }
}

function describePasskeyFailure(f: ReturnType<typeof passkeyFailure>): string {
  switch (f.kind) {
    case "cancelled":
      return "Cancelled, or your device took too long.";
    case "misconfigured":
      return "This panel's passkey origin does not match the address you are using. An operator needs to fix VLESSVMORE_PASSKEY_ORIGIN.";
    case "already-registered":
      return "That device already has a passkey for this panel.";
    case "unsupported":
      return "This browser cannot use passkeys.";
    case "aborted":
      return "That attempt was interrupted.";
    default:
      return f.message;
  }
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
