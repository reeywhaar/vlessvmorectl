import { createApiDispatcherAction } from "../request";
import type { AttachResult, Subscriber } from "../types";

/**
 * The panel's own subscriber store. Every call here is `scope: "panel"`.
 *
 * Two things to know before adding to this file.
 *
 * None of these responses is wrapped in `Reloaded<T>`, and that is not an oversight: a
 * subscriber change never touches a node, never rewrites a sing-box config and never
 * drops a connection. There is nothing to reload, which is why the hooks below do not go
 * near useReloadAware.
 *
 * The backend decodes with DisallowUnknownFields, exactly as vlessvmore does — see
 * api/patch.ts for the trap that creates. A body with a key the handler does not know is
 * a 400, and `{note: undefined}` serialises to `{}`, which means "change nothing" rather
 * than "clear it". The patch functions below take explicit shapes for that reason.
 */

const base = "/api/subscribers";
const byId = (id: string) => `${base}/${encodeURIComponent(id)}`;
const entries = (id: string) => `${byId(id)}/entries`;
const entryById = (id: string, entryId: string) =>
  `${entries(id)}/${encodeURIComponent(entryId)}`;

/** GET /api/subscribers */
export function getSubscribers() {
  return createApiDispatcherAction((d) =>
    d
      .call<{ subscribers: Subscriber[] }>({ scope: "panel", method: "GET", path: base })
      .then((r) => r.subscribers),
  );
}

/** GET /api/subscribers/:id */
export function getSubscribersById(id: string) {
  return createApiDispatcherAction((d) =>
    d.call<Subscriber>({ scope: "panel", method: "GET", path: byId(id) }),
  );
}

/** POST /api/subscribers */
export function postSubscribers(input: { name: string; note: string }) {
  return createApiDispatcherAction((d) =>
    d.call<Subscriber>({ scope: "panel", method: "POST", path: base, body: input }),
  );
}

/**
 * PATCH /api/subscribers/:id
 *
 * An omitted key means "unchanged" and an empty string means "clear", which is the whole
 * reason the argument is built by the caller rather than being three optional parameters.
 */
export function patchSubscribersById(
  id: string,
  patch: { name?: string; note?: string; disabled?: boolean },
) {
  return createApiDispatcherAction((d) =>
    d.call<Subscriber>({ scope: "panel", method: "PATCH", path: byId(id), body: patch }),
  );
}

/** DELETE /api/subscribers/:id — 204, no body. */
export function deleteSubscribersById(id: string) {
  return createApiDispatcherAction((d) =>
    d.call<void>({ scope: "panel", method: "DELETE", path: byId(id) }),
  );
}

/**
 * POST /api/subscribers/:id/entries
 *
 * The backend checks the account exists before storing the reference, and keeps nothing
 * from the answer. A node that is down yields a `warning` rather than a failure: an
 * operator has to be able to hand somebody a link during the incident that made them ask
 * for it.
 */
export function postSubscribersByIdEntries(
  id: string,
  input: { server_id: string; vless_user_id: string; label: string },
) {
  return createApiDispatcherAction((d) =>
    d.call<AttachResult>({ scope: "panel", method: "POST", path: entries(id), body: input }),
  );
}

/** PATCH /api/subscribers/:id/entries/:entryId — the label only. */
export function patchSubscribersByIdEntriesByEntryId(id: string, entryId: string, label: string) {
  return createApiDispatcherAction((d) =>
    d.call<Subscriber>({
      scope: "panel",
      method: "PATCH",
      path: entryById(id, entryId),
      body: { label },
    }),
  );
}

/** DELETE /api/subscribers/:id/entries/:entryId */
export function deleteSubscribersByIdEntriesByEntryId(id: string, entryId: string) {
  return createApiDispatcherAction((d) =>
    d.call<Subscriber>({ scope: "panel", method: "DELETE", path: entryById(id, entryId) }),
  );
}
