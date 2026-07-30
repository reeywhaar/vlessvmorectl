import { vi } from "vitest";

/**
 * A stub `navigator.credentials`, for the tests that need one.
 *
 * Deliberately not in setup.ts. jsdom's default — no `PublicKeyCredential` at all — is itself
 * the feature-detection case, and it has to stay reachable so the "this browser cannot use
 * passkeys" paths can be tested.
 *
 * The buffers below need not be cryptographically real. The server's own verification is
 * tested against a genuine ES256 authenticator in Go; what these tests are for is the wiring
 * — that a challenge arrives at the browser as a BufferSource and leaves as base64url.
 */

export interface StubbedAuthenticator {
  create: ReturnType<typeof vi.fn>;
  get: ReturnType<typeof vi.fn>;
  /** The AbortSignals handed to each call, so a test can assert one was aborted. */
  signals: (AbortSignal | undefined)[];
}

export function stubAuthenticator(
  opts: {
    conditional?: boolean;
    /** Reject instead of resolving, e.g. new DOMException("", "NotAllowedError"). */
    createRejects?: unknown;
    getRejects?: unknown;
    /** credProps.rk, for the not-discoverable case. */
    residentKey?: boolean;
    /** Never settle, so a test can abort a request that is genuinely still in flight. */
    getHangs?: boolean;
  } = {},
): StubbedAuthenticator {
  const signals: (AbortSignal | undefined)[] = [];

  class FakePublicKeyCredential {
    static isConditionalMediationAvailable = async () => opts.conditional ?? false;
    static isUserVerifyingPlatformAuthenticatorAvailable = async () => true;
  }
  vi.stubGlobal("PublicKeyCredential", FakePublicKeyCredential);

  const create = vi.fn(async (o: CredentialCreationOptions) => {
    signals.push(o.signal);
    if (opts.createRejects) throw opts.createRejects;
    return fakeAttestation(opts.residentKey ?? true);
  });

  const get = vi.fn(async (o: CredentialRequestOptions) => {
    signals.push(o.signal);
    if (opts.getRejects) throw opts.getRejects;
    if (opts.getHangs) {
      return new Promise((_, reject) => {
        o.signal?.addEventListener("abort", () =>
          reject(o.signal?.reason ?? new DOMException("aborted", "AbortError")),
        );
      });
    }
    return fakeAssertion();
  });

  Object.defineProperty(navigator, "credentials", {
    value: { create, get },
    configurable: true,
    writable: true,
  });

  return { create, get, signals };
}

const bytes = (n: number) => new Uint8Array(new ArrayBuffer(n)).fill(7);

function fakeAttestation(rk: boolean) {
  return {
    id: "Y3JlZGVudGlhbC1pZA",
    rawId: bytes(32).buffer,
    type: "public-key",
    authenticatorAttachment: "platform",
    getClientExtensionResults: () => ({ credProps: { rk } }),
    response: {
      clientDataJSON: bytes(48).buffer,
      attestationObject: bytes(96).buffer,
      getTransports: () => ["internal"],
    },
  };
}

function fakeAssertion() {
  return {
    id: "Y3JlZGVudGlhbC1pZA",
    rawId: bytes(32).buffer,
    type: "public-key",
    getClientExtensionResults: () => ({}),
    response: {
      clientDataJSON: bytes(48).buffer,
      authenticatorData: bytes(37).buffer,
      signature: bytes(70).buffer,
      userHandle: bytes(32).buffer,
    },
  };
}
