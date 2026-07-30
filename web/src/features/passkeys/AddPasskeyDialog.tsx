import { useEffect, useState } from "react";
import { Banner, Button, Dialog, Field, Input } from "../../components/ui";
import { isVlessError } from "../../api/errors";
import { useRegisterPasskey } from "../../queries/hooks";
import { passkeyFailure } from "./webauthn";

/**
 * Enrol an authenticator.
 *
 * The name is asked for *before* the authenticator is invoked, on purpose: the server holds
 * the ceremony's state for a few minutes only, and stopping to type a name in the middle of
 * that window is how an enrolment times out.
 */
export function AddPasskeyDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [label, setLabel] = useState(() => guessDeviceName());
  const register = useRegisterPasskey();
  const [notDiscoverable, setNotDiscoverable] = useState(false);

  // A fresh dialog starts clean rather than showing the last attempt's error.
  useEffect(() => {
    if (open) {
      setLabel(guessDeviceName());
      setNotDiscoverable(false);
      register.reset();
    }
    // register.reset is stable; depending on it would re-run this on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  if (notDiscoverable) {
    return (
      <Dialog open={open} onClose={onClose} title="Passkey added, with a catch">
        <Banner tone="warn" title="This authenticator did not store a discoverable passkey">
          It was saved, but it cannot be used to sign in without a username, which is the only
          way this panel offers. A different authenticator — your phone, or a security key that
          supports resident credentials — will work.
        </Banner>
        <div className="mt-4 flex justify-end">
          <Button variant="primary" onClick={onClose}>
            Close
          </Button>
        </div>
      </Dialog>
    );
  }

  return (
    <Dialog open={open} onClose={onClose} title="Add a passkey">
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          register.mutate(
            { label: label.trim() },
            {
              onSuccess: ({ discoverable }) => {
                if (discoverable) onClose();
                else setNotDiscoverable(true);
              },
            },
          );
        }}
      >
        <Field
          label="Name"
          hint="Which device this is, so you can tell them apart later."
        >
          <Input
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            maxLength={64}
            autoFocus
            required
          />
        </Field>

        {register.error ? (
          <p role="alert" className="text-sm text-danger">
            {registerMessage(register.error)}
          </p>
        ) : (
          <p className="text-sm text-muted">
            Your browser will ask you to confirm with Touch ID, Windows Hello, your phone or a
            security key.
          </p>
        )}

        <div className="flex justify-end gap-2">
          <Button type="button" onClick={onClose} disabled={register.isPending}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={register.isPending || !label.trim()}>
            {register.isPending ? "Waiting for your device…" : "Continue"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
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

/**
 * A first guess at the name, so the common case is one keystroke rather than a decision.
 * userAgent is the only thing available and is a hint, not a fact — hence "guess".
 */
function guessDeviceName(): string {
  const ua = typeof navigator === "undefined" ? "" : navigator.userAgent;
  if (/iPhone/i.test(ua)) return "iPhone";
  if (/iPad/i.test(ua)) return "iPad";
  if (/Android/i.test(ua)) return "Android phone";
  if (/Macintosh|Mac OS X/i.test(ua)) return "Mac";
  if (/Windows/i.test(ua)) return "Windows PC";
  if (/Linux/i.test(ua)) return "Linux PC";
  return "Passkey";
}
