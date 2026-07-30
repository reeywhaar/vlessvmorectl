import { createApiDispatcherAction } from "../request";
import type {
  CredentialCreationOptionsJSON,
  CredentialRequestOptionsJSON,
  Passkey,
} from "../types";

/**
 * The signed-in administrator's own passkeys, plus the two unauthenticated sign-in steps.
 *
 * Always scoped "panel". Every authenticated endpoint here acts on the caller's own account
 * only — there is no way to reach another administrator's passkeys.
 *
 * The two-step shape is WebAuthn's, not ours: `begin` mints a challenge the server remembers
 * for a few minutes, and `finish` echoes back the opaque `state` alongside whatever the
 * authenticator produced.
 */

/** GET /api/passkeys */
export function getPasskeys() {
  return createApiDispatcherAction((d) =>
    d
      .call<{ passkeys: Passkey[] }>({ scope: "panel", method: "GET", path: "/api/passkeys" })
      .then((r) => r.passkeys),
  );
}

/** POST /api/passkeys/register/begin — no body; the session says who is asking. */
export function postPasskeysRegisterBegin() {
  return createApiDispatcherAction((d) =>
    d.call<{ state: string; options: CredentialCreationOptionsJSON }>({
      scope: "panel",
      method: "POST",
      path: "/api/passkeys/register/begin",
    }),
  );
}

/** POST /api/passkeys/register/finish */
export function postPasskeysRegisterFinish(state: string, label: string, credential: unknown) {
  return createApiDispatcherAction((d) =>
    d
      .call<{ passkey: Passkey }>({
        scope: "panel",
        method: "POST",
        path: "/api/passkeys/register/finish",
        body: { state, label, credential },
      })
      .then((r) => r.passkey),
  );
}

/** PATCH /api/passkeys/:id */
export function patchPasskeysById(id: string, label: string) {
  return createApiDispatcherAction((d) =>
    d
      .call<{ passkey: Passkey }>({
        scope: "panel",
        method: "PATCH",
        path: `/api/passkeys/${encodeURIComponent(id)}`,
        body: { label },
      })
      .then((r) => r.passkey),
  );
}

/** DELETE /api/passkeys/:id */
export function deletePasskeysById(id: string) {
  return createApiDispatcherAction((d) =>
    d.call<void>({
      scope: "panel",
      method: "DELETE",
      path: `/api/passkeys/${encodeURIComponent(id)}`,
    }),
  );
}

/** POST /api/passkeys/login/begin — unauthenticated: this is how you sign in. */
export function postPasskeysLoginBegin() {
  return createApiDispatcherAction((d) =>
    d.call<{ state: string; options: CredentialRequestOptionsJSON }>({
      scope: "panel",
      method: "POST",
      path: "/api/passkeys/login/begin",
    }),
  );
}

/** POST /api/passkeys/login/finish — same response shape as POST /api/login, deliberately. */
export function postPasskeysLoginFinish(state: string, credential: unknown) {
  return createApiDispatcherAction((d) =>
    d
      .call<{ user: { username: string } }>({
        scope: "panel",
        method: "POST",
        path: "/api/passkeys/login/finish",
        body: { state, credential },
      })
      .then((r) => r.user),
  );
}
