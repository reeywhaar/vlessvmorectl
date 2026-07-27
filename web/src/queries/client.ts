import { QueryClient, type Query } from "@tanstack/react-query";
import { isVlessError, isTerminal, isTransient } from "../api/errors";

/** The specified polling interval. */
export const POLL_MS = 10_000;
const POLL_CAP_MS = 120_000;

/**
 * Poll every ten seconds, backing off while a node is failing.
 *
 * Two behaviours matter here. A node that is merely down decays 10s → 20s → 40s → 80s →
 * 120s and snaps straight back to 10s on the first success, so ten dead nodes cost five
 * requests a minute rather than sixty. A node that has *rejected our token* stops
 * entirely: it cannot fix itself, every attempt costs it ~100ms of deliberate refusal
 * padding, and each one writes a warning into the operator's log. That case gets a
 * manual retry button instead.
 */
export function pollWithBackoff(base = POLL_MS, cap = POLL_CAP_MS) {
  // Generic in the query's own type parameters so it satisfies refetchInterval on any
  // query, rather than only on one whose data happens to be `unknown`.
  return <TData, TError, TQueryData, TKey extends readonly unknown[]>(
    query: Query<TData, TError, TQueryData, TKey>,
  ): number | false => {
    const err = query.state.error;
    if (isVlessError(err) && isTerminal(err.failure)) return false;

    const failures = query.state.fetchFailureCount;
    if (failures === 0) return base;
    return Math.min(base * 2 ** failures, cap);
  };
}

export function makeQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Half the poll interval, so a window-focus refetch is not a second stampede
        // on top of the timer.
        staleTime: POLL_MS / 2,
        gcTime: 5 * 60_000,

        retry: (count, error) => {
          if (!isVlessError(error)) return false;
          // One retry for things that might just have been unlucky; the poll interval
          // is the real retry loop. Never for a rejected token or a 4xx, which cannot
          // recover without someone changing something.
          return isTransient(error.failure) ? count < 1 : false;
        },
        retryDelay: 1_000,

        refetchOnWindowFocus: true,
        refetchOnReconnect: true,
        // Load-bearing, and stated rather than left to the default: the interval timer
        // does not fire while the tab is hidden, so a panel left open overnight is not
        // 8,640 requests per node. refetchOnWindowFocus covers the return.
        refetchIntervalInBackground: false,

        // Every data component suspends and every failure reaches a Boundary. A
        // component that silently rendered nothing would leave an operator unable to
        // tell "no users" from "this broke".
        throwOnError: true,
      },
      mutations: {
        retry: false,
      },
    },
  });
}
