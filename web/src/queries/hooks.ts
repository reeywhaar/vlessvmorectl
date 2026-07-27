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
 * There used to be a useAllUsers here built on useSuspenseQueries. It was removed rather
 * than kept alongside this, because a suspense query always throws, and the shorter name
 * was a trap: one node with a rejected token would blank an entire page whose own data
 * came from this panel and was perfectly fine.
 *
 * That is exactly the subscriber screens' situation. The node data here is *enrichment* —
 * an account's name and a status badge next to a reference the panel already holds — so a
 * failure has to degrade one group of rows and say so, not take the page with it.
 *
 * Shares qk.users(serverId) with useUsers, so arriving from the overview costs nothing:
 * the ten-second poll has already warmed exactly these entries.
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
