import {
  useMutation,
  useQueries,
  useQueryClient,
  useSuspenseQuery,
  type QueryClient,
} from "@tanstack/react-query";
import { useApiCall, type ApiCall } from "../api/ApiProvider";
import { isVlessError, type VlessFailure } from "../api/errors";
import {
  deleteSubscribersById,
  deleteSubscribersByIdEntriesByEntryId,
  getSubscribers,
  patchSubscribersById,
  patchSubscribersByIdEntriesByEntryId,
  postSubscribers,
  postSubscribersByIdEntries,
} from "../api/actions/subscribers";
import type { Subscriber } from "../api/types";
import { getMe, postLogin, postLogout } from "../api/actions/auth";
import { postAccountPassword, postAccountUsername } from "../api/actions/account";
import {
  deletePasskeysById,
  getPasskeys,
  patchPasskeysById,
  postPasskeysLoginBegin,
  postPasskeysLoginFinish,
  postPasskeysRegisterBegin,
  postPasskeysRegisterFinish,
} from "../api/actions/passkeys";
import { createPasskey, getPasskeyAssertion } from "../features/passkeys/webauthn";
import { getServers } from "../api/actions/servers";
import { getServer } from "../api/actions/server";
import { getStatus } from "../api/actions/status";
import { postReload } from "../api/actions/reload";
import { getTokens } from "../api/actions/tokens";
import {
  deleteUsersById,
  getUsers,
  getUsersByIdLink,
  getUsersByIdUsage,
  patchUsersById,
  postUsers,
  postUsersByIdResetUsage,
  postUsersByIdRotateSub,
} from "../api/actions/users";
import type { Reloaded, Server, VlessUser } from "../api/types";
import { UserPatch, type UserCreate } from "../api/patch";
import type { SnappedRange } from "../lib/range";
import { qk } from "./keys";
import { POLL_MS, pollWithBackoff } from "./client";
import { useReloadWatch } from "./reloadWatch";

// ---- our own backend ----

export function useSession() {
  const callApi = useApiCall();
  return useSuspenseQuery({
    queryKey: qk.session,
    queryFn: ({ signal }) => callApi(getMe(), signal),
    staleTime: 60_000,
    refetchInterval: 5 * 60_000,
    retry: false,
  });
}

export function useServers() {
  const callApi = useApiCall();
  return useSuspenseQuery({
    queryKey: qk.servers,
    queryFn: ({ signal }) => callApi(getServers(), signal),
    staleTime: 5 * 60_000,
  });
}

// ---- a managed node ----

/**
 * Read-only operator configuration — API.md is explicit that there is no PATCH for it —
 * so this is fetched once per node per session and never polled.
 */
export function useServerInfo(server: Server) {
  const callApi = useApiCall();
  return useSuspenseQuery({
    queryKey: qk.serverInfo(server.id),
    queryFn: ({ signal }) => callApi(getServer(server), signal),
    staleTime: Infinity,
    gcTime: Infinity,
    refetchOnWindowFocus: false,
  });
}

export function useServerStatus(server: Server) {
  const callApi = useApiCall();
  return useSuspenseQuery({
    queryKey: qk.serverStatus(server.id),
    queryFn: ({ signal }) => callApi(getStatus(server), signal),
    refetchInterval: pollWithBackoff(),
  });
}

/**
 * The 10-second usage poll.
 *
 * Fetched with `?include=usage` on the overview as well as the server page. That costs
 * two indexed SQLite aggregates per user on the node, which is nothing at the scale these
 * run at, and it buys two things: the overview's counts and throughput are real rather
 * than approximated from /api/status, and it warms the exact cache key the server page
 * reads, so clicking into a node is instant. Extra tabs cost the node nothing — the proxy
 * coalesces identical in-flight GETs.
 */
export function useUsers(server: Server) {
  const callApi = useApiCall();
  return useSuspenseQuery({
    queryKey: qk.users(server.id),
    queryFn: ({ signal }) => callApi(getUsers(server), signal),
    refetchInterval: pollWithBackoff(),
  });
}

/** One node's slot in the tolerant fan-out below. */
export interface NodeUsers {
  server: Server;
  /** null while loading, and null when this node did not answer. */
  users: VlessUser[] | null;
  failure: VlessFailure | null;
}

/**
 * Every node's user list, tolerant of any of them failing.
 *
 * Not useSuspenseQueries: node data on the subscriber screens is enrichment beside data
 * the panel already holds, so one unreachable node must degrade its own rows rather than
 * blank the page. Shares qk.users(serverId) with useUsers, so arriving from the overview
 * costs no requests.
 */
export function useUsersByServer(servers: Server[]): NodeUsers[] {
  const callApi = useApiCall();
  const results = useQueries({
    queries: servers.map((s) => ({
      queryKey: qk.users(s.id),
      queryFn: ({ signal }: { signal: AbortSignal }) => callApi(getUsers(s), signal),
      refetchInterval: pollWithBackoff(),
      // The whole point. Without this the error propagates to the nearest boundary and
      // the tolerance above is a comment rather than a behaviour.
      throwOnError: false,
    })),
  });

  return servers.map((server, i) => {
    const r = results[i];
    const error = r?.error;
    return {
      server,
      users: r?.data ?? null,
      failure: isVlessError(error) ? error.failure : null,
    };
  });
}

/**
 * Each node's display name, best-effort, keyed by server id.
 *
 * Tolerant like useUsersByServer above, and for the same reason: the caller wants a caption, and
 * a node that is down must not cost it the ones that answered. The suspense-and-throw hook is
 * wrong wherever the name is a nicety rather than the content — a boundary per row inside a
 * dialog is both more machinery and, at least once, a render loop.
 */
export function useServerNames(servers: Server[]): Record<string, string> {
  const callApi = useApiCall();
  const results = useQueries({
    queries: servers.map((s) => ({
      queryKey: qk.serverInfo(s.id),
      queryFn: ({ signal }: { signal: AbortSignal }) => callApi(getServer(s), signal),
      staleTime: Infinity,
      gcTime: Infinity,
      refetchOnWindowFocus: false,
      throwOnError: false,
    })),
  });

  const names: Record<string, string> = {};
  servers.forEach((server, i) => {
    const name = results[i]?.data?.name;
    if (name) names[server.id] = name;
  });
  return names;
}

/** Holds sub_token, so it leaves memory shortly after the drawer closes. */
export function useUserLink(server: Server, userId: string) {
  const callApi = useApiCall();
  return useSuspenseQuery({
    queryKey: qk.userLink(server.id, userId),
    queryFn: ({ signal }) => callApi(getUsersByIdLink(server, userId), signal),
    staleTime: Infinity,
    gcTime: 30_000,
    refetchOnWindowFocus: false,
  });
}

/**
 * Usage history. Polled far slower than the live figures: the node collects into hourly
 * buckets every `stats_interval` (30s by default), so a 10-second refetch here would be
 * the same numbers over and over.
 */
export function useUserUsage(server: Server, userId: string, range: SnappedRange) {
  const callApi = useApiCall();
  return useSuspenseQuery({
    queryKey: qk.userUsage(server.id, userId, range),
    queryFn: ({ signal }) =>
      callApi(getUsersByIdUsage(server, userId, range.fromUnix, range.bucket), signal),
    refetchInterval: range.bucket === "hour" ? 60_000 : 300_000,
    staleTime: 30_000,
  });
}

export function useTokens(server: Server) {
  const callApi = useApiCall();
  return useSuspenseQuery({
    queryKey: qk.tokens(server.id),
    queryFn: ({ signal }) => callApi(getTokens(server), signal),
    staleTime: 30_000,
  });
}

// ---- mutations ----

/**
 * Every mutation that can trigger a reload goes through here, so `reload_error` cannot be
 * forgotten.
 *
 * The node returns 2xx even when the reload failed: the change is saved and will apply on
 * the next successful reload, but it is not live. That distinction is invisible in the
 * status code and easy to drop on the floor — and dropping it means an operator believes
 * they have cut off a user who is still connected.
 */
function useReloadAware<TVars, TResult>(
  server: Server,
  opts: {
    run: (callApi: ApiCall, vars: TVars) => Promise<Reloaded<TResult>>;
    optimistic?: (vars: TVars, qc: QueryClient) => (() => void) | void;
  },
) {
  const callApi = useApiCall();
  const qc = useQueryClient();
  const watch = useReloadWatch();

  return useMutation({
    mutationFn: (vars: TVars) => opts.run(callApi, vars),
    onMutate: async (vars: TVars) => {
      await qc.cancelQueries({ queryKey: qk.users(server.id) });
      return { rollback: opts.optimistic?.(vars, qc) };
    },
    onError: (_err, _vars, ctx) => ctx?.rollback?.(),
    onSuccess: (env) => {
      if (env.reloaded) {
        watch.clear(server.id);
        return;
      }
      watch.mark(server.id, env.reload_error ?? "the node did not say why");
    },
    // active_users moves too, so invalidate the whole node subtree rather than only the
    // user list.
    onSettled: () => qc.invalidateQueries({ queryKey: qk.server(server.id) }),
  });
}

export function useCreateUser(server: Server) {
  // No optimism: the node assigns the id, uuid and sub_token, so there is nothing honest
  // to render until it answers.
  return useReloadAware<UserCreate, VlessUser>(server, {
    run: (callApi, input) => callApi(postUsers(server, input)),
  });
}

/** One node's outcome from the fan-out below. */
export interface CreatedOnNode {
  server: Server;
  /** null when the node created the user. */
  error: unknown;
}

/**
 * Creates the same user on several nodes.
 *
 * Not useCreateUser in a loop, which would be a hook per node and break the moment the list
 * changed length. Failures are collected rather than thrown, so one unreachable node neither
 * hides the others' success nor loses the caller the chance to retry just that one.
 */
export function useCreateUserOnServers(servers: Server[]) {
  const callApi = useApiCall();
  const qc = useQueryClient();
  const watch = useReloadWatch();

  return useMutation({
    mutationFn: ({ ids, input }: { ids: string[]; input: UserCreate }) =>
      Promise.all(
        servers
          .filter((s) => ids.includes(s.id))
          .map(async (server): Promise<CreatedOnNode> => {
            try {
              const env = await callApi(postUsers(server, input));
              if (env.reloaded) watch.clear(server.id);
              else watch.mark(server.id, env.reload_error ?? "the node did not say why");
              return { server, error: null };
            } catch (error) {
              return { server, error };
            } finally {
              void qc.invalidateQueries({ queryKey: qk.server(server.id) });
            }
          }),
      ),
  });
}

export function usePatchUser(server: Server) {
  return useReloadAware<{ id: string; patch: UserPatch }, VlessUser>(server, {
    run: (callApi, { id, patch }) => callApi(patchUsersById(server, id, patch)),
    optimistic: ({ id, patch }, qc) => {
      const key = qk.users(server.id);
      const before = qc.getQueryData<VlessUser[]>(key);
      if (!before) return;
      const f = patch.fields;

      qc.setQueryData<VlessUser[]>(
        key,
        before.map((u) => {
          if (u.id !== id) return u;
          const next: VlessUser = { ...u };
          if (f.enabled !== undefined) {
            next.enabled = f.enabled;
            // Re-enabling clears the enforcement reason, mirroring what the node's store
            // does. A stale "over quota" badge on a re-enabled user gets reported as a
            // bug in the enforcement itself.
            if (f.enabled) delete next.disabled_reason;
          }
          if (f.name !== undefined) next.name = f.name;
          if (f.note !== undefined) next.note = f.note;
          if (f.quotaBytes !== undefined) next.quota_bytes = f.quotaBytes;
          return next;
        }),
      );
      return () => qc.setQueryData(key, before);
    },
  });
}

export function useDeleteUser(server: Server) {
  return useReloadAware<string, { deleted: string; name: string }>(server, {
    run: (callApi, id) => callApi(deleteUsersById(server, id)),
    optimistic: (id, qc) => {
      const key = qk.users(server.id);
      const before = qc.getQueryData<VlessUser[]>(key);
      if (!before) return;
      qc.setQueryData<VlessUser[]>(
        key,
        before.filter((u) => u.id !== id),
      );
      return () => qc.setQueryData(key, before);
    },
  });
}

export function useResetUsage(server: Server) {
  return useReloadAware<string, VlessUser>(server, {
    run: (callApi, id) => callApi(postUsersByIdResetUsage(server, id)),
  });
}

/** Bare, not wrapped: rotating the subscription token needs no reload, because sing-box's
 *  config does not depend on it. */
export function useRotateSubToken(server: Server) {
  const callApi = useApiCall();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => callApi(postUsersByIdRotateSub(server, id)),
    onSettled: () => qc.invalidateQueries({ queryKey: qk.users(server.id) }),
  });
}

export function useReloadNode(server: Server) {
  const callApi = useApiCall();
  const qc = useQueryClient();
  const watch = useReloadWatch();
  return useMutation({
    mutationFn: () => callApi(postReload(server)),
    onSuccess: () => watch.clear(server.id),
    onSettled: () => qc.invalidateQueries({ queryKey: qk.server(server.id) }),
  });
}

/**
 * Both of these clear the whole cache rather than invalidating it.
 *
 * On the way out it is a privacy matter — the cache holds subscription URLs and
 * sub_tokens, and none of that should outlive the session. On the way in it is
 * correctness: whatever is left over belongs to the previous identity, including the
 * *errored* session query whose 401 is what put the login form on screen in the first
 * place. Invalidating leaves that error in place for the next observer to inherit.
 *
 * Neither is enough on its own to move the page, though: see the `epoch` in App.tsx for
 * why the error boundary also has to be remounted.
 */
export function useLogin() {
  const callApi = useApiCall();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ username, password }: { username: string; password: string }) =>
      callApi(postLogin(username, password)),
    onSuccess: () => qc.clear(),
  });
}

export function useLogout() {
  const callApi = useApiCall();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => callApi(postLogout()),
    onSuccess: () => qc.clear(),
  });
}

// ---- panel-owned: this administrator's own credentials ----

/**
 * Deliberately not `qc.clear()`, unlike useLogin and useLogout.
 *
 * The server signs every *other* device out and hands this request a replacement cookie,
 * so the session survives and the identity has not changed. Clearing would tear the whole
 * shell down and re-fetch it to arrive back where it started.
 */
export function useChangePassword() {
  const callApi = useApiCall();
  return useMutation({
    mutationFn: ({ currentPassword, newPassword }: { currentPassword: string; newPassword: string }) =>
      callApi(postAccountPassword(currentPassword, newPassword)),
  });
}

/** Invalidates the session query so the header picks the new name up. */
export function useChangeUsername() {
  const callApi = useApiCall();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ username }: { username: string }) => callApi(postAccountUsername(username)),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.session }),
  });
}

// ---- panel-owned: passkeys ----

/**
 * Not polled. These are rows in this panel's own store that change only when this
 * administrator changes them, and every mutation below invalidates them.
 */
export function usePasskeys() {
  const callApi = useApiCall();
  return useSuspenseQuery({
    queryKey: qk.passkeys,
    queryFn: ({ signal }) => callApi(getPasskeys(), signal),
    staleTime: 30_000,
  });
}

/**
 * The whole three-step enrolment as one mutation, so `isPending` covers the time the
 * operating system's prompt is on screen too — which is most of the wall clock.
 */
export function useRegisterPasskey() {
  const callApi = useApiCall();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const { state, options } = await callApi(postPasskeysRegisterBegin());
      const { credential, discoverable } = await createPasskey(options);
      const passkey = await callApi(postPasskeysRegisterFinish(state, credential));
      return { passkey, discoverable };
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.passkeys }),
  });
}

export function useRenamePasskey() {
  const callApi = useApiCall();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, label }: { id: string; label: string }) =>
      callApi(patchPasskeysById(id, label)),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.passkeys }),
  });
}

export function useDeletePasskey() {
  const callApi = useApiCall();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id }: { id: string }) => callApi(deletePasskeysById(id)),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.passkeys }),
  });
}

/**
 * Signs in with a passkey. Mirrors useLogin exactly, including the qc.clear() — which is
 * why the server returns the same body shape as POST /api/login: the caller can hand this to
 * the same `onSuccess: onSignedIn` and App.tsx's epoch remount works unchanged.
 *
 * `assertion` lets the conditional-mediation hook hand in a ceremony it already completed,
 * rather than starting a second one the browser would refuse.
 */
export function usePasskeyLogin() {
  const callApi = useApiCall();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (vars?: { state: string; credential: unknown }) => {
      if (vars) return callApi(postPasskeysLoginFinish(vars.state, vars.credential));
      const { state, options } = await callApi(postPasskeysLoginBegin());
      const credential = await getPasskeyAssertion(options);
      return callApi(postPasskeysLoginFinish(state, credential));
    },
    onSuccess: () => qc.clear(),
  });
}

// ---- panel-owned: subscribers ----

/**
 * Not polled, unlike everything node-shaped above.
 *
 * These are rows in this panel's own store. They change only when an operator changes
 * them, and every mutation below invalidates them, so a ten-second poll would be this
 * process asking itself the same question 8,640 times a day.
 *
 * The list carries share tokens, so it inherits the cache hygiene the rest of the
 * credential-bearing queries use: it goes out of memory shortly after nothing is
 * observing it, and logging out clears it outright via qc.clear().
 */
export function useSubscribers() {
  const callApi = useApiCall();
  return useSuspenseQuery({
    queryKey: qk.subscribers,
    queryFn: ({ signal }) => callApi(getSubscribers(), signal),
    staleTime: 30_000,
    gcTime: 60_000,
  });
}

/**
 * Shared by every mutation below.
 *
 * Invalidating the ["subscribers"] root rather than one record: these are cheap, local
 * reads, and a mutation that changes one subscriber can change the list's ordering too.
 */
function useSubscriberMutation<TVars>(run: (callApi: ApiCall, vars: TVars) => Promise<unknown>) {
  const callApi = useApiCall();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: TVars) => run(callApi, vars),
    onSettled: () => qc.invalidateQueries({ queryKey: qk.subscribers }),
  });
}

/** No optimism: the backend assigns the id and mints the token. */
export function useCreateSubscriber() {
  return useSubscriberMutation<{ name: string; note: string }>((callApi, input) =>
    callApi(postSubscribers(input)),
  );
}

export function usePatchSubscriber() {
  const qc = useQueryClient();
  const callApi = useApiCall();
  return useMutation({
    mutationFn: (vars: { id: string; patch: { name?: string; note?: string; disabled?: boolean } }) =>
      callApi(patchSubscribersById(vars.id, vars.patch)),
    // Optimistic, because the two things it drives — a rename and the disabled toggle —
    // are both switches an operator expects to move under their finger. Rolled back on
    // failure, and reconciled by the invalidate in onSettled either way.
    onMutate: async ({ id, patch }) => {
      await qc.cancelQueries({ queryKey: qk.subscribers });
      const before = qc.getQueryData<Subscriber[]>(qk.subscribers);
      if (before) {
        qc.setQueryData<Subscriber[]>(
          qk.subscribers,
          before.map((s) => (s.id === id ? { ...s, ...patch } : s)),
        );
      }
      return { before };
    },
    onError: (_e, _v, ctx) => {
      if (ctx?.before) qc.setQueryData(qk.subscribers, ctx.before);
    },
    onSettled: () => qc.invalidateQueries({ queryKey: qk.subscribers }),
  });
}

export function useDeleteSubscriber() {
  return useSubscriberMutation<string>((callApi, id) => callApi(deleteSubscribersById(id)));
}

/** No optimism: the backend assigns the entry id, and may answer with a warning. */
export function useAttachEntry() {
  return useSubscriberMutation<{
    id: string;
    entry: { server_id: string; vless_user_id: string; label: string };
  }>((callApi, { id, entry }) => callApi(postSubscribersByIdEntries(id, entry)));
}

export function useRelabelEntry() {
  return useSubscriberMutation<{ id: string; entryId: string; label: string }>(
    (callApi, { id, entryId, label }) =>
      callApi(patchSubscribersByIdEntriesByEntryId(id, entryId, label)),
  );
}

export function useDetachEntry() {
  return useSubscriberMutation<{ id: string; entryId: string }>((callApi, { id, entryId }) =>
    callApi(deleteSubscribersByIdEntriesByEntryId(id, entryId)),
  );
}

export { POLL_MS, UserPatch };
