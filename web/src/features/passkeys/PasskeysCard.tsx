import { useState } from "react";
import {
  Badge,
  Button,
  Card,
  Confirm,
  EmptyState,
  IconButton,
  Input,
  KeyIcon,
  PencilIcon,
  TrashIcon,
} from "../../components/ui";
import { describeFailure, isVlessError } from "../../api/errors";
import { formatDateTime, formatRelative } from "../../lib/format";
import { useDeletePasskey, usePasskeys, useRenamePasskey } from "../../queries/hooks";
import type { Passkey } from "../../api/types";
import { AddPasskeyDialog } from "./AddPasskeyDialog";
import { passkeysSupported } from "./webauthn";

/**
 * The passkeys half of the account page.
 *
 * When the feature is switched off this names the environment variable, which is a deliberate
 * asymmetry with the sign-in screen: that one is anonymous and discloses nothing, but an
 * authenticated operator staring at a missing feature deserves to be told what to set. The
 * empty-servers state does the same.
 */
export function PasskeysCard({ enabled }: { enabled: boolean }) {
  if (!enabled) {
    return (
      <Card>
        <Header />
        <EmptyState title="Passkeys are switched off">
          Set <code className="text-ink">VLESSVMORE_PASSKEY_ORIGIN</code> to the address you open
          this panel at — for example <code className="text-ink">https://panel.example.com</code> —
          and restart. It must be https, or localhost while developing.
        </EmptyState>
      </Card>
    );
  }
  if (!passkeysSupported()) {
    return (
      <Card>
        <Header />
        <EmptyState title="This browser cannot use passkeys">
          Passkeys need a secure context and a browser with WebAuthn. Your password still works.
        </EmptyState>
      </Card>
    );
  }
  return <PasskeyList />;
}

function Header({ action }: { action?: React.ReactNode }) {
  return (
    <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 className="font-semibold">Passkeys</h2>
        <p className="mt-1 text-sm text-muted">
          Sign in with Touch ID, Windows Hello, your phone or a security key. Your password keeps
          working either way.
        </p>
      </div>
      {action}
    </div>
  );
}

function PasskeyList() {
  const { data: passkeys } = usePasskeys();
  const [adding, setAdding] = useState(false);
  const remove = useDeletePasskey();
  const [pendingDelete, setPendingDelete] = useState<Passkey | null>(null);

  return (
    <Card>
      <Header
        action={
          <Button variant="primary" onClick={() => setAdding(true)}>
            Add a passkey
          </Button>
        }
      />

      {passkeys.length === 0 ? (
        <EmptyState title="No passkeys yet">
          Add one and you can sign in without typing a password.
        </EmptyState>
      ) : (
        <ul className="divide-y divide-line">
          {passkeys.map((p) => (
            <PasskeyRow key={p.id} passkey={p} onDelete={() => setPendingDelete(p)} />
          ))}
        </ul>
      )}

      {remove.error ? (
        <p role="alert" className="mt-3 text-sm text-danger">
          {message(remove.error)}
        </p>
      ) : null}

      <AddPasskeyDialog open={adding} onClose={() => setAdding(false)} />

      {/*
        Confirm rather than ConfirmDelete: losing a passkey is recoverable by signing in with
        the password and adding another, so making somebody type its name is friction bought
        for nothing. Deleting the last one, or the one this session came from, needs no special
        warning for the same reason.
      */}
      <Confirm
        open={pendingDelete !== null}
        title={`Remove ${pendingDelete?.label ?? ""}?`}
        confirmLabel="Remove"
        variant="danger"
        busy={remove.isPending}
        onCancel={() => setPendingDelete(null)}
        onConfirm={() => {
          const target = pendingDelete;
          if (!target) return;
          remove.mutate({ id: target.id }, { onSuccess: () => setPendingDelete(null) });
        }}
      >
        That device will no longer be able to sign in. Your password still works.
      </Confirm>
    </Card>
  );
}

function PasskeyRow({ passkey, onDelete }: { passkey: Passkey; onDelete: () => void }) {
  const [editing, setEditing] = useState(false);
  const [label, setLabel] = useState(passkey.label);
  const rename = useRenamePasskey();

  return (
    <li className="flex flex-wrap items-center gap-3 py-3">
      <span className="text-muted">
        <KeyIcon />
      </span>

      {editing ? (
        <form
          className="flex min-w-0 flex-1 items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            rename.mutate(
              { id: passkey.id, label: label.trim() },
              { onSuccess: () => setEditing(false) },
            );
          }}
        >
          <Input
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            aria-label={`Name for ${passkey.label}`}
            maxLength={64}
            autoFocus
            required
          />
          <Button type="submit" variant="primary" disabled={rename.isPending || !label.trim()}>
            Save
          </Button>
          <Button
            type="button"
            onClick={() => {
              setLabel(passkey.label);
              setEditing(false);
            }}
          >
            Cancel
          </Button>
        </form>
      ) : (
        <>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="truncate font-medium">{passkey.label}</span>
              <Badge tone={passkey.synced ? "accent" : "muted"}>
                {passkey.synced ? "Synced" : "This device"}
              </Badge>
            </div>
            <p className="mt-0.5 text-xs text-muted">
              {passkey.algorithm} · added <time dateTime={passkey.created_at}>{formatDateTime(passkey.created_at)}</time>
              {" · "}
              {passkey.last_used_at
                ? `last used ${formatRelative(passkey.last_used_at)}`
                : "never used"}
            </p>
          </div>
          <div className="ml-auto flex items-center gap-1">
            <IconButton label={`Rename ${passkey.label}`} onClick={() => setEditing(true)}>
              <PencilIcon />
            </IconButton>
            <IconButton label={`Remove ${passkey.label}`} onClick={onDelete}>
              <TrashIcon />
            </IconButton>
          </div>
        </>
      )}

      {rename.error ? (
        <p role="alert" className="w-full text-sm text-danger">
          {message(rename.error)}
        </p>
      ) : null}
    </li>
  );
}

function message(error: unknown): string {
  if (!isVlessError(error)) {
    return error instanceof Error ? error.message : "That did not work.";
  }
  if (error.failure.kind === "not-found") return "That passkey is already gone.";
  return describeFailure(error.failure);
}
