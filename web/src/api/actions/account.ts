import { createApiDispatcherAction } from "../request";

/**
 * The signed-in administrator's own credentials. Always scoped "panel", never a node.
 *
 * Both endpoints take the current password. A username is half of what you type at the
 * login prompt, so changing it counts as a credential change and not a profile edit.
 */

/**
 * POST /api/account/password
 *
 * 204, and a replacement session cookie: the change signs this administrator out of every
 * other device, and hands the acting tab a new session so it carries on.
 */
export function postAccountPassword(currentPassword: string, newPassword: string) {
  return createApiDispatcherAction((d) =>
    d.call<void>({
      scope: "panel",
      method: "POST",
      path: "/api/account/password",
      body: { current_password: currentPassword, new_password: newPassword },
    }),
  );
}

/**
 * POST /api/account/username
 *
 * Signs nobody out, not even on other devices: a session names the administrator's
 * permanent id, so the name is only a label.
 *
 * No current password, unlike the endpoint above. A username is not a secret and changing it
 * grants nothing, so the session is proof enough.
 */
export function postAccountUsername(username: string) {
  return createApiDispatcherAction((d) =>
    d
      .call<{ username: string }>({
        scope: "panel",
        method: "POST",
        path: "/api/account/username",
        body: { username },
      })
      .then((r) => r.username),
  );
}
