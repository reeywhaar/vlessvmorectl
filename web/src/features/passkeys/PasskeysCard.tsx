import { useState } from "react";
import {
  Badge,
  Banner,
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
import { useAppliedTheme } from "../../lib/theme";
import {
  useDeletePasskey,
  usePasskeys,
  useRegisterPasskey,
  useRenamePasskey,
} from "../../queries/hooks";
import type { Passkey } from "../../api/types";
import { passkeyFailure, passkeysSupported } from "./webauthn";

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
  const register = useRegisterPasskey();
  const remove = useDeletePasskey();
  const [pendingDelete, setPendingDelete] = useState<Passkey | null>(null);
  const [notDiscoverable, setNotDiscoverable] = useState(false);

  // Straight to the authenticator, with nothing asked first. The one thing a dialog could have
  // collected is the name, and the server fills that in from the authenticator that answers —
  // which is a better answer than the operator could give *before* knowing which one that is.
  const add = () => {
    setNotDiscoverable(false);
    register.mutate(undefined, {
      onSuccess: ({ discoverable }) => setNotDiscoverable(!discoverable),
    });
  };

  return (
    <Card>
      <Header
        action={
          <Button variant="primary" onClick={add} disabled={register.isPending}>
            {register.isPending ? "Waiting for your device…" : "Add a passkey"}
          </Button>
        }
      />

      {notDiscoverable ? (
        <Banner tone="warn" title="That authenticator did not store a discoverable passkey">
          It was saved, but it cannot be used to sign in without a username, which is the only way
          this panel offers. A different authenticator — your phone, or a security key that
          supports resident credentials — will work.
        </Banner>
      ) : null}

      {register.error ? (
        <p role="alert" className="mb-3 text-sm text-danger">
          {registerMessage(register.error)}
        </p>
      ) : null}

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


      {/*
        Confirm rather than ConfirmDelete: losing a passkey is recoverable by signing in with
        the password and adding another, so making somebody type its name is friction bought
        for nothing. Deleting the last one, or the one this session came from, needs no special
        warning for the same reason.
      */}
      <Confirm
        open={pendingDelete !== null}
        title={`Remove ${pendingDelete ? displayName(pendingDelete) : ""}?`}
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
  const name = displayName(passkey);
  // The stored label, not the name on screen — which is usually empty, and should be. Seeding it
  // with the provider's name would make the field look like somebody had already chosen that,
  // and clearing it would then read as deleting a name rather than as never having set one.
  const [label, setLabel] = useState(passkey.label);
  const rename = useRenamePasskey();

  return (
    <li className="flex flex-wrap items-center gap-3 py-3">
      <ProviderLogo passkey={passkey} />

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
            aria-label={`Name for ${name}`}
            // What it falls back to, shown where a typed name would be. Empty is a real answer
            // here, so the field is neither required nor does it block Save: clearing it is how
            // a rename is undone, and the row goes back to its provider's name.
            placeholder={name}
            maxLength={64}
            autoFocus
          />
          <Button type="submit" variant="primary" disabled={rename.isPending}>
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
              <span className="truncate font-medium">{name}</span>
              <Badge tone={passkey.synced ? "accent" : "muted"}>
                {passkey.synced ? "Synced" : "This device"}
              </Badge>
            </div>
            <p className="mt-0.5 text-xs text-muted">
              {/* Only when it says something the heading does not. An unnamed credential is
                  already titled after its provider, so printing it twice reads as a bug. */}
              {passkey.provider && passkey.provider !== name ? `${passkey.provider} · ` : null}
              {passkey.algorithm} · added <time dateTime={passkey.created_at}>{formatDateTime(passkey.created_at)}</time>
              {" · "}
              {passkey.last_used_at
                ? `last used ${formatRelative(passkey.last_used_at)}`
                : "never used"}
            </p>
          </div>
          <div className="ml-auto flex items-center gap-1">
            <IconButton label={`Rename ${name}`} onClick={() => setEditing(true)}>
              <PencilIcon />
            </IconButton>
            <IconButton label={`Remove ${name}`} onClick={onDelete}>
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

/**
 * The provider's logo, or the panel's own key glyph.
 *
 * `alt=""` rather than the provider's name: the name is already beside this in the same row, and
 * a screen reader announcing "Bitwarden Bitwarden" is worse than one that skips the picture.
 *
 * Falling back to logo when logo_dark is absent is the common path, not the exception — most
 * providers ship one image that suits either theme, and only the ones that do not get a second.
 */
function ProviderLogo({ passkey }: { passkey: Passkey }) {
  const theme = useAppliedTheme();
  const [broken, setBroken] = useState(false);

  const src = (theme === "dark" ? passkey.logo_dark : undefined) ?? passkey.logo;
  if (!src || broken) {
    return (
      <span className="text-muted">
        <KeyIcon />
      </span>
    );
  }
  return (
    <img
      src={src}
      alt=""
      width={20}
      height={20}
      // object-contain because these are somebody else's logos: several are wider than they are
      // tall, and none of them should be stretched into our square.
      className="size-5 shrink-0 object-contain"
      // Only reached for a logo the listing offered, so a failure here is the network rather
      // than a missing file. Falling back to the glyph beats a broken-image icon in a list of
      // credentials, where it reads as something being wrong with the credential itself.
      onError={() => setBroken(true)}
    />
  );
}

/**
 * What to call a passkey.
 *
 * An empty label is the normal state, not a missing one: enrolment stores no name, so a
 * credential is called after the provider it lives in until somebody renames it. Computed here
 * rather than filled in at enrolment so that a provider whose name we learn later — the community
 * list gains an entry, or corrects one — is picked up by credentials already enrolled.
 */
function displayName(passkey: Passkey): string {
  return passkey.label || passkey.provider || "Unknown";
}

function message(error: unknown): string {
  if (!isVlessError(error)) {
    return error instanceof Error ? error.message : "That did not work.";
  }
  if (error.failure.kind === "not-found") return "That passkey is already gone.";
  return describeFailure(error.failure);
}

function registerMessage(error: unknown): string {
  // A WebAuthn refusal is a DOMException rather than one of our failures, so it is checked
  // first — describeFailure would only ever say "unknown" about it.
  if (!isVlessError(error)) {
    const f = passkeyFailure(error);
    switch (f.kind) {
      case "cancelled":
        return "Cancelled, or your device took too long. Try again.";
      case "already-registered":
        return "This device already has a passkey for this panel.";
      case "misconfigured":
        return "This panel's passkey origin does not match the address you are using. An operator needs to fix VLESSVMORE_PASSKEY_ORIGIN.";
      case "unsupported":
        return "This browser cannot create passkeys.";
      case "aborted":
        return "That attempt was interrupted. Try again.";
      default:
        return f.message;
    }
  }
  switch (error.failure.kind) {
    case "conflict":
      return error.failure.message;
    case "bad-request":
      return error.failure.message;
    case "unauthorized":
      return "Your session has expired. Reload and sign in again.";
    default:
      return error.message;
  }
}
