import { useState } from "react";
import { Boundary } from "../components/Boundary";
import { ErrorState } from "../components/ErrorState";
import { Button, Card, Field, Input, PageHeader, Skeleton } from "../components/ui";
import { describeFailure, isVlessError } from "../api/errors";
import { PasskeysCard } from "../features/passkeys/PasskeysCard";
import { useChangePassword, useChangeUsername, useSession } from "../queries/hooks";

/**
 * The administrator's own account — the first self-service surface in the panel.
 *
 * Both forms ask for the current password. That is not ceremony: a session borrowed from an
 * unlocked laptop should not be enough to take the account over, and a username is half of
 * what you type at the login prompt.
 */
export function AccountPage() {
  const { data: session } = useSession();

  return (
    <>
      <PageHeader title="Account" subtitle={session.username} />
      <div className="grid gap-4 lg:grid-cols-2">
        <UsernameCard current={session.username} />
        <PasswordCard />
        <div className="lg:col-span-2">
          <Boundary
            pending={<Skeleton className="h-40 w-full" />}
            fallback={({ error, retry, failure }) => (
              <ErrorState error={error} retry={retry} failure={failure} />
            )}
          >
            <PasskeysCard enabled={session.passkeys_enabled} />
          </Boundary>
        </div>
      </div>
    </>
  );
}

function UsernameCard({ current }: { current: string }) {
  const [username, setUsername] = useState(current);
  const [done, setDone] = useState(false);
  const change = useChangeUsername();

  const unchanged = username.trim() === current;

  return (
    <Card>
      <h2 className="font-semibold">Username</h2>
      <p className="mt-1 mb-4 text-sm text-muted">
        Changing this signs nobody out, here or on your other devices.
      </p>
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          setDone(false);
          change.mutate({ username: username.trim() }, { onSuccess: () => setDone(true) });
        }}
      >
        <Field label="Username">
          <Input
            value={username}
            onChange={(e) => {
              setUsername(e.target.value);
              setDone(false);
            }}
            autoComplete="username"
            required
          />
        </Field>

        <FormNote error={change.error} done={done} doneMessage="Username changed." />

        <Button
          type="submit"
          variant="primary"
          disabled={change.isPending || unchanged || !username.trim()}
        >
          {change.isPending ? "Saving…" : "Change username"}
        </Button>
      </form>
    </Card>
  );
}

function PasswordCard() {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  const [done, setDone] = useState(false);
  const change = useChangePassword();

  // Checked here as well as on the server, because the server cannot see the second field
  // at all — a typo in it would otherwise set a password the operator cannot reproduce.
  const mismatch = repeat !== "" && newPassword !== repeat;
  const ready = currentPassword !== "" && newPassword !== "" && newPassword === repeat;

  function reset() {
    setCurrentPassword("");
    setNewPassword("");
    setRepeat("");
  }

  return (
    <Card>
      <h2 className="font-semibold">Password</h2>
      <p className="mt-1 mb-4 text-sm text-muted">
        Signs you out on every other device. This tab stays signed in.
      </p>
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          setDone(false);
          change.mutate(
            { currentPassword, newPassword },
            {
              onSuccess: () => {
                reset();
                setDone(true);
              },
            },
          );
        }}
      >
        <Field label="Current password">
          <Input
            type="password"
            value={currentPassword}
            onChange={(e) => setCurrentPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
        </Field>
        <Field label="New password" hint="At least 8 characters.">
          <Input
            type="password"
            value={newPassword}
            onChange={(e) => {
              setNewPassword(e.target.value);
              setDone(false);
            }}
            autoComplete="new-password"
            required
          />
        </Field>
        <Field label="Repeat new password">
          <Input
            type="password"
            value={repeat}
            onChange={(e) => setRepeat(e.target.value)}
            autoComplete="new-password"
            required
          />
        </Field>

        {mismatch ? (
          <p role="alert" className="text-sm text-danger">
            The two new passwords do not match.
          </p>
        ) : (
          <FormNote
            error={change.error}
            done={done}
            doneMessage="Password changed. Your other devices have been signed out."
          />
        )}

        <Button type="submit" variant="primary" disabled={change.isPending || !ready}>
          {change.isPending ? "Saving…" : "Change password"}
        </Button>
      </form>
    </Card>
  );
}

/** One place for both outcomes, so a stale success can never sit next to a fresh error. */
function FormNote({
  error,
  done,
  doneMessage,
}: {
  error: unknown;
  done: boolean;
  doneMessage: string;
}) {
  if (error) {
    return (
      <p role="alert" className="text-sm text-danger">
        {accountMessage(error)}
      </p>
    );
  }
  if (done) {
    return (
      <p role="status" className="text-sm text-ok">
        {doneMessage}
      </p>
    );
  }
  return null;
}

function accountMessage(error: unknown): string {
  if (!isVlessError(error)) {
    return error instanceof Error ? error.message : "That did not work.";
  }
  switch (error.failure.kind) {
    // The backend answers 403 for a wrong current password specifically, to keep it
    // distinct from the 401 that means "your session is gone".
    case "forbidden":
      return "That is not your current password.";
    case "unauthorized":
      return "Your session has expired. Reload and sign in again.";
    case "server-error":
      return error.failure.status === 429
        ? "Too many attempts. Wait a minute and try again."
        : error.failure.message;
    default:
      return describeFailure(error.failure);
  }
}
