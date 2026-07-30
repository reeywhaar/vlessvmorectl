import { useCallback, useEffect, useRef, useState } from "react";
import { useApiCall } from "../../api/ApiProvider";
import { postPasskeysLoginBegin } from "../../api/actions/passkeys";
import {
  abortPendingCeremony,
  conditionalMediationAvailable,
  getPasskeyAssertion,
  passkeyFailure,
  passkeysSupported,
  type PasskeyFailure,
} from "./webauthn";

/**
 * Offers a passkey from the username field's autofill, without the visitor asking.
 *
 * The fiddly part of the whole feature. A document may have only one WebAuthn request in
 * flight, so this opportunistic one has to be abortable and every competing flow has to wait
 * for it to let go first — hence `stop`, which callers must **await**.
 *
 * Four things want to stop it: clicking the explicit button, submitting the password form,
 * unmounting, and the effect re-running. Miss the second and a request that resolves later
 * signs in as whoever the autofill picked, racing a password login that may be for a
 * different account.
 */
export function useConditionalPasskey({
  enabled,
  onAssertion,
}: {
  enabled: boolean;
  /** Called with a completed ceremony, for the caller's own mutation to finish. */
  onAssertion: (result: { state: string; credential: unknown }) => void;
}): { stop: () => Promise<void>; failure: PasskeyFailure | null } {
  const callApi = useApiCall();
  const [failure, setFailure] = useState<PasskeyFailure | null>(null);

  // Held in a ref so `stop` is stable and the effect below does not re-run when it changes.
  const onAssertionRef = useRef(onAssertion);
  onAssertionRef.current = onAssertion;

  const stop = useCallback(() => abortPendingCeremony(), []);

  useEffect(() => {
    if (!enabled || !passkeysSupported()) return;

    let cancelled = false;

    (async () => {
      if (!(await conditionalMediationAvailable())) return;
      // Re-checked after every await. React 19 StrictMode invokes effects twice in
      // development, so without this the first invocation's continuation starts a request
      // after its own cleanup has run — and the browser answers NotAllowedError, which
      // surfaces as a mystery in the console.
      if (cancelled) return;

      try {
        const { state, options } = await callApi(postPasskeysLoginBegin());
        if (cancelled) return;

        const credential = await getPasskeyAssertion(options, { conditional: true });
        if (cancelled) return;

        onAssertionRef.current({ state, credential });
      } catch (e) {
        if (cancelled) return;
        const f = passkeyFailure(e);
        // Silent for the ordinary outcomes. A conditional request rejects with
        // NotAllowedError whenever somebody ignores the suggestion and types a password
        // instead, and showing "Cancelled" under a form they are happily filling in would
        // be a bug rather than feedback.
        if (f.kind === "aborted" || f.kind === "cancelled" || f.kind === "unsupported") return;
        setFailure(f);
      }
    })();

    return () => {
      cancelled = true;
      void abortPendingCeremony();
    };
  }, [enabled, callApi]);

  return { stop, failure };
}
