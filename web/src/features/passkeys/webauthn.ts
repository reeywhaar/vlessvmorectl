import { fromBase64Url, toBase64Url } from "../../lib/base64url";
import type { CredentialCreationOptionsJSON, CredentialRequestOptionsJSON } from "../../api/types";

/**
 * The only file in the app that mentions `navigator.credentials` or `PublicKeyCredential`,
 * the same rule transport.ts states about `fetch`.
 *
 * The option and response conversions are hand-rolled rather than using
 * `PublicKeyCredential.parseCreationOptionsFromJSON` and `credential.toJSON()`. Those are
 * newer than passkey support itself, so using them would mean a capability branch and two
 * code paths, one of which would never run in a test. This is thirty lines and works
 * everywhere that has passkeys at all.
 */

/** Whether this browser can do WebAuthn at all. jsdom cannot, which is a useful default. */
export function passkeysSupported(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.PublicKeyCredential === "function" &&
    typeof navigator.credentials?.create === "function" &&
    typeof navigator.credentials?.get === "function"
  );
}

/**
 * Whether the browser can offer a passkey from the username field's autofill.
 *
 * Feature-tested rather than called directly: `isConditionalMediationAvailable` is newer
 * than `PublicKeyCredential` itself, so it is missing on browsers that otherwise work.
 */
export async function conditionalMediationAvailable(): Promise<boolean> {
  if (!passkeysSupported()) return false;
  const C = window.PublicKeyCredential as typeof PublicKeyCredential & {
    isConditionalMediationAvailable?: () => Promise<boolean>;
  };
  if (typeof C.isConditionalMediationAvailable !== "function") return false;
  return C.isConditionalMediationAvailable().catch(() => false);
}

export type PasskeyFailure =
  | { kind: "unsupported" }
  /** The visitor dismissed the prompt, or it timed out. Also what an ignored autofill looks like. */
  | { kind: "cancelled" }
  /** excludeCredentials matched: this device already holds a passkey for the panel. */
  | { kind: "already-registered" }
  /** The origin or rpId is wrong — an operator's misconfiguration, not the visitor's problem. */
  | { kind: "misconfigured" }
  | { kind: "aborted" }
  | { kind: "unknown"; message: string };

export function passkeyFailure(e: unknown): PasskeyFailure {
  if (!passkeysSupported()) return { kind: "unsupported" };
  if (e instanceof DOMException) {
    switch (e.name) {
      case "AbortError":
        return { kind: "aborted" };
      case "NotAllowedError":
        return { kind: "cancelled" };
      case "InvalidStateError":
        return { kind: "already-registered" };
      case "SecurityError":
        return { kind: "misconfigured" };
    }
  }
  return { kind: "unknown", message: e instanceof Error ? e.message : String(e) };
}

/**
 * The one outstanding ceremony, if any.
 *
 * A document may have only one WebAuthn request in flight, and the invariant lives here
 * rather than in the callers so that none of them can forget it.
 */
let pending: { controller: AbortController; settled: Promise<unknown> } | null = null;

/** Aborts whatever ceremony is outstanding, and waits for the browser to let go of it. */
export async function abortPendingCeremony(): Promise<void> {
  const p = pending;
  if (!p) return;
  pending = null;
  p.controller.abort(new DOMException("superseded", "AbortError"));
  // Awaited, not fired and forgotten. The browser frees the slot only once the previous
  // promise has actually rejected, and until then the next call throws NotAllowedError.
  // This await is the whole trick.
  await p.settled.catch(() => {});
}

async function exclusive<T>(run: (signal: AbortSignal) => Promise<T>): Promise<T> {
  await abortPendingCeremony();
  const controller = new AbortController();
  const settled = run(controller.signal);
  pending = { controller, settled };
  try {
    return await settled;
  } finally {
    if (pending?.controller === controller) pending = null;
  }
}

export interface RegistrationResult {
  credential: unknown;
  /** False when the authenticator stored no discoverable credential, so it can never sign in. */
  discoverable: boolean;
}

export async function createPasskey(
  options: CredentialCreationOptionsJSON,
): Promise<RegistrationResult> {
  return exclusive(async (signal) => {
    const credential = (await navigator.credentials.create({
      publicKey: toCreationOptions(options),
      signal,
    })) as PublicKeyCredential | null;
    // The spec permits null; every browser rejects instead, but the type admits it.
    if (!credential) throw new DOMException("no credential was created", "NotAllowedError");

    const response = credential.response as AuthenticatorAttestationResponse;
    const extensions = credential.getClientExtensionResults() as {
      credProps?: { rk?: boolean };
    };

    return {
      credential: {
        id: credential.id,
        rawId: toBase64Url(credential.rawId),
        type: credential.type,
        ...(credential.authenticatorAttachment
          ? { authenticatorAttachment: credential.authenticatorAttachment }
          : {}),
        clientExtensionResults: extensions,
        response: {
          clientDataJSON: toBase64Url(response.clientDataJSON),
          attestationObject: toBase64Url(response.attestationObject),
          transports: response.getTransports?.() ?? [],
        },
      },
      discoverable: extensions.credProps?.rk !== false,
    };
  });
}

export async function getPasskeyAssertion(
  options: CredentialRequestOptionsJSON,
  opts: { conditional?: boolean } = {},
): Promise<unknown> {
  return exclusive(async (signal) => {
    const credential = (await navigator.credentials.get({
      publicKey: toRequestOptions(options),
      ...(opts.conditional ? { mediation: "conditional" as CredentialMediationRequirement } : {}),
      signal,
    })) as PublicKeyCredential | null;
    if (!credential) throw new DOMException("no assertion was produced", "NotAllowedError");

    const response = credential.response as AuthenticatorAssertionResponse;
    return {
      id: credential.id,
      rawId: toBase64Url(credential.rawId),
      type: credential.type,
      clientExtensionResults: credential.getClientExtensionResults(),
      response: {
        clientDataJSON: toBase64Url(response.clientDataJSON),
        authenticatorData: toBase64Url(response.authenticatorData),
        signature: toBase64Url(response.signature),
        ...(response.userHandle ? { userHandle: toBase64Url(response.userHandle) } : {}),
      },
    };
  });
}

// An unrecognised transport throws a TypeError in Chrome, so anything the server has stored
// from a newer authenticator is dropped rather than passed through.
const KNOWN_TRANSPORTS = new Set(["usb", "nfc", "ble", "hybrid", "internal", "smart-card"]);

function descriptors(
  list: { id: string; type: string; transports?: string[] }[] | undefined,
): PublicKeyCredentialDescriptor[] | undefined {
  if (!list) return undefined;
  return list.map((d) => {
    const transports = d.transports?.filter((t) => KNOWN_TRANSPORTS.has(t));
    return {
      id: fromBase64Url(d.id),
      type: d.type as PublicKeyCredentialType,
      ...(transports?.length ? { transports: transports as AuthenticatorTransport[] } : {}),
    };
  });
}

/**
 * Decodes exactly the fields that must become BufferSource, and copies everything else
 * verbatim. The verbatim copy is what makes a server-side library upgrade that adds an
 * option field free here — and the reason the result is cast through `unknown`: the rest of
 * the object is an index signature, so nothing else can be checked against the DOM type.
 */
function toCreationOptions(o: CredentialCreationOptionsJSON): PublicKeyCredentialCreationOptions {
  const { challenge, user, excludeCredentials, ...rest } = o;
  return {
    ...rest,
    challenge: fromBase64Url(challenge),
    user: { ...user, id: fromBase64Url(user.id) },
    ...(excludeCredentials ? { excludeCredentials: descriptors(excludeCredentials) } : {}),
  } as unknown as PublicKeyCredentialCreationOptions;
}

function toRequestOptions(o: CredentialRequestOptionsJSON): PublicKeyCredentialRequestOptions {
  const { challenge, allowCredentials, ...rest } = o;
  return {
    ...rest,
    challenge: fromBase64Url(challenge),
    ...(allowCredentials ? { allowCredentials: descriptors(allowCredentials) } : {}),
  } as unknown as PublicKeyCredentialRequestOptions;
}
