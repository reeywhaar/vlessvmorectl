import type { Server } from "./types";

export type Method = "GET" | "POST" | "PATCH" | "DELETE";

export interface RequestOptions {
  query?: Record<string, string | number | undefined>;
  body?: unknown;
  signal?: AbortSignal;
}

/**
 * The only thing that knows how a request physically reaches its destination.
 *
 * Everything above this line — requests, the dispatcher, every query hook, every
 * component — is transport-agnostic and must stay that way. Two rules keep it honest: a
 * Transport returns a raw Response and never a parsed body, and nothing outside this
 * file may mention /api/proxy or a bearer token.
 *
 * There is one implementation today. If a direct-from-browser mode is ever added — for
 * the case where this panel's host cannot reach a node but the operator's browser can —
 * it is a second class implementing this interface, handed to a different ApiDispatcher
 * at client init. Nothing else in the application changes.
 */
export interface Transport {
  /** Our own backend: login, logout, me, servers. */
  panel(method: Method, path: string, opts?: RequestOptions): Promise<Response>;
  /** A managed vlessvmore node. */
  node(server: Server, method: Method, path: string, opts?: RequestOptions): Promise<Response>;
}

/**
 * Routes node calls through our own backend, which attaches the node's bearer token.
 *
 * The target is a whole URL in one query parameter; URLSearchParams handles the nesting,
 * so the node's own query string survives intact.
 */
export class ProxyTransport implements Transport {
  constructor(private readonly origin: string = window.location.origin) {}

  panel(method: Method, path: string, opts: RequestOptions = {}): Promise<Response> {
    const url = ProxyTransport.withQuery(new URL(path, this.origin), opts.query);
    return fetch(url, { ...ProxyTransport.init(opts), method });
  }

  node(server: Server, method: Method, path: string, opts: RequestOptions = {}): Promise<Response> {
    const target = ProxyTransport.withQuery(new URL(path, server.url), opts.query);
    const url = new URL("/api/proxy", this.origin);
    url.searchParams.set("url", target.toString());
    return fetch(url, { ...ProxyTransport.init(opts), method });
  }

  private static init(opts: RequestOptions): RequestInit {
    const headers: Record<string, string> = { Accept: "application/json" };
    const init: RequestInit = {
      // Our session cookie is the only credential the browser holds.
      credentials: "same-origin",
      ...(opts.signal ? { signal: opts.signal } : {}),
    };
    if (opts.body !== undefined) {
      init.body = JSON.stringify(opts.body);
      headers["Content-Type"] = "application/json";
    }
    init.headers = headers;
    return init;
  }

  private static withQuery(url: URL, query: RequestOptions["query"]): URL {
    for (const [k, v] of Object.entries(query ?? {})) {
      if (v !== undefined) url.searchParams.set(k, String(v));
    }
    return url;
  }
}
