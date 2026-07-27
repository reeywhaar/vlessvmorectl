import { createContext, useContext, useSyncExternalStore } from "react";

export interface ReloadProblem {
  markedAt: number;
  error: string;
}

type Listener = () => void;

// useSyncExternalStore compares snapshots by identity, so a stable "nothing" is required
// or every render looks like a change and the component loops.
const NONE: ReloadProblem | null = null;

/**
 * Remembers nodes whose last change was saved but did not go live.
 *
 * vlessvmore answers 2xx with `reloaded: false` when it wrote the change but could not
 * regenerate sing-box's config. A toast is not enough for that: an operator who disabled
 * someone believes they are disconnected, and they are not. So it becomes a sticky banner
 * that survives navigation and lives until the situation actually resolves.
 *
 * It self-heals. `markedAt` is compared against the node's `sing_box.last_reload` on the
 * next status poll, so *any* subsequent successful reload clears it — one triggered by
 * another operator, by the quota enforcement pass, or by reloads being coalesced. That is
 * correct: the question is "is the config live", not "did my particular button work".
 */
export class ReloadWatch {
  private readonly problems = new Map<string, ReloadProblem>();
  private readonly listeners = new Set<Listener>();

  /** Bound so it can be passed straight to useSyncExternalStore. */
  readonly subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  readonly get = (serverId: string): ReloadProblem | null => this.problems.get(serverId) ?? NONE;

  mark(serverId: string, error: string, now: number = Date.now()): void {
    this.problems.set(serverId, { markedAt: now, error });
    this.emit();
  }

  clear(serverId: string): void {
    if (this.problems.delete(serverId)) this.emit();
  }

  private emit(): void {
    for (const l of this.listeners) l();
  }
}

const ReloadWatchContext = createContext<ReloadWatch | null>(null);

export const ReloadWatchProvider = ReloadWatchContext.Provider;

export function useReloadWatch(): ReloadWatch {
  const watch = useContext(ReloadWatchContext);
  if (!watch) throw new Error("useReloadWatch must be used inside a ReloadWatchProvider");
  return watch;
}

/**
 * The current problem for a node, resolved against its last successful reload.
 */
export function useReloadProblem(serverId: string, lastReload?: string): ReloadProblem | null {
  const watch = useReloadWatch();
  const problem = useSyncExternalStore(
    watch.subscribe,
    () => watch.get(serverId),
    () => NONE,
  );

  if (problem && lastReload) {
    const at = Date.parse(lastReload);
    if (!Number.isNaN(at) && at > problem.markedAt) {
      // Something reloaded successfully since we flagged this. Mutating during render
      // would be a side effect, so report resolved now and let the store catch up.
      queueMicrotask(() => watch.clear(serverId));
      return NONE;
    }
  }
  return problem;
}
