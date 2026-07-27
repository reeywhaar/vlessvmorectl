import { VlessError, classify } from "./errors";
import type { ApiDispatcherRequest } from "./request";
import type { Transport } from "./transport";

/**
 * Executes API requests.
 *
 * Configured once at client init with the transport it should use, and handed to the
 * application through <ApiProvider>. Nothing reaches for a module-level singleton, so a
 * test renders a subtree against a fake transport without touching global state, and the
 * knowledge of *how* a request travels stays in exactly one object.
 */
export class ApiDispatcher {
  constructor(
    private readonly transport: Transport,
    private readonly signal?: AbortSignal,
  ) {}

  /**
   * A clone of this dispatcher bound to a cancellation signal.
   *
   * Cancellation belongs to the *caller* — TanStack Query hands a query function an
   * AbortSignal and expects it to be honoured — but it is nothing to do with what a
   * request is. Carrying it on the dispatcher means an action is `(d) => d.call(...)` and
   * never has to accept, name or forward a signal it does not care about; forgetting to
   * thread one through becomes impossible rather than merely unlikely.
   *
   * Signals *merge* rather than replace. A composite action that already runs under a
   * caller's signal can narrow further — a per-step timeout, say — and both remain live:
   * whichever aborts first wins, which is the only behaviour that is ever correct.
   * Replacing would silently discard the outer cancellation and leave a request running
   * after its owner had given up on it.
   */
  withSignal(signal: AbortSignal): ApiDispatcher {
    const merged = this.signal ? AbortSignal.any([this.signal, signal]) : signal;
    return new ApiDispatcher(this.transport, merged);
  }

  /**
   * Run a request and either return its parsed body or throw a VlessError.
   *
   * Throwing rather than returning a result union is deliberate, and matches how the UI
   * is built: every data component suspends, and its nearest Boundary renders the
   * failure. A component that swallowed an error and rendered nothing would leave an
   * operator looking at an empty card, unable to tell "no users" from "this broke".
   */
  async call<T>(request: ApiDispatcherRequest): Promise<T> {
    const { scope, method, path, query, body } = request;

    const opts = {
      ...(query ? { query } : {}),
      ...(body !== undefined ? { body } : {}),
      ...(this.signal ? { signal: this.signal } : {}),
    };
    const serverId = scope === "panel" ? undefined : scope.id;

    let res: Response;
    try {
      res =
        scope === "panel"
          ? await this.transport.panel(method, path, opts)
          : await this.transport.node(scope, method, path, opts);
    } catch (e) {
      if (e instanceof DOMException && e.name === "AbortError") {
        throw VlessError.create({ kind: "aborted" }, serverId);
      }
      // With the proxy in front of us this is a failure to reach *our own origin* — the
      // page is already loaded, so it means the panel went away or the tab is offline.
      // In a direct-call design this was the common CORS failure; here it is genuinely
      // unusual, which is why the reason is not guessed at any harder than this.
      throw VlessError.create(
        {
          kind: "unreachable",
          reason: navigator.onLine === false ? "offline" : "unknown",
          detail: e instanceof Error ? e.message : String(e),
        },
        serverId,
      );
    }

    if (!res.ok) throw VlessError.create(await classify(res), serverId);

    return (await ApiDispatcher.parse(res, serverId)) as T;
  }

  private static async parse(res: Response, serverId: string | undefined): Promise<unknown> {
    if (res.status === 204) return undefined;
    const text = await res.text();
    if (!text) return undefined;
    try {
      return JSON.parse(text);
    } catch {
      throw VlessError.create(
        {
          kind: "server-error",
          status: res.status,
          message: `expected JSON, got ${text.slice(0, 120)}`,
        },
        serverId,
      );
    }
  }
}
