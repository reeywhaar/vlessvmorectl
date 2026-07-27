import {
  useMutation,
  useQueryClient,
  useSuspenseQueries,
  useSuspenseQuery,
  type QueryClient,
} from "@tanstack/react-query";
import { useApiCall, type ApiCall } from "../api/ApiProvider";
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

/** Fan-out across nodes. useSuspenseQueries keeps the hook count stable as the list
 *  changes, and each card reads only its own slot. */
export function useAllUsers(servers: Server[]) {
  const callApi = useApiCall();
  return useSuspenseQueries({
    queries: servers.map((s) => ({
      queryKey: qk.users(s.id),
      queryFn: ({ signal }: { signal: AbortSignal }) => callApi(getUsers(s), signal),
      refetchInterval: pollWithBackoff(),
    })),
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

export { POLL_MS, UserPatch };
