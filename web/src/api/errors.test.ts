import { describe, expect, it } from "vitest";
import { classify } from "./errors";

function res(body: string, init: { status: number; contentType?: string; headers?: HeadersInit }) {
  const headers = new Headers(init.headers);
  if (init.contentType) headers.set("content-type", init.contentType);
  return new Response(body, { status: init.status, headers });
}

describe("classify", () => {
  /**
   * The single most important behaviour in the client.
   *
   * vlessvmore has no 401: every refusal — no token, revoked token, unknown path, wrong
   * method — is Go's stdlib text/plain 404. Every genuine not-found is JSON. Getting this
   * backwards means telling an operator "no such user" when their token is wrong, and
   * sending them to look at entirely the wrong thing.
   */
  it("reads a stdlib text/plain 404 as a rejected token", async () => {
    const f = await classify(
      res("404 page not found\n", { status: 404, contentType: "text/plain; charset=utf-8" }),
    );
    expect(f).toEqual({ kind: "refused", likely: "bad-token" });
  });

  it("reads a JSON 404 as a genuine not-found", async () => {
    const f = await classify(
      res(`{"error":"user \\"bob\\": not found"}`, {
        status: 404,
        contentType: "application/json; charset=utf-8",
      }),
    );
    expect(f).toEqual({ kind: "not-found", message: 'user "bob": not found' });
  });

  /**
   * A reverse proxy or CDN in front of a node returns its own HTML 404, which is neither
   * JSON nor the stdlib string. Calling that a bad token sends the operator hunting for a
   * credential problem that does not exist.
   */
  it("distinguishes a 404 that did not come from vlessvmore", async () => {
    const f = await classify(
      res("<html><body>404 Not Found</body></html>", { status: 404, contentType: "text/html" }),
    );
    expect(f).toEqual({ kind: "refused", likely: "not-vlessvmore" });
  });

  /**
   * X-Proxy-Error is checked before the status code, so an upstream 502 is never mistaken
   * for "we could not reach the node" — which would tell an operator their server is down
   * when it answered perfectly well.
   */
  it("takes X-Proxy-Error over the status code", async () => {
    const ours = await classify(
      res(`{"error":"dial tcp: connection refused","proxy_error":"refused"}`, {
        status: 502,
        contentType: "application/json",
        headers: { "x-proxy-error": "1" },
      }),
    );
    expect(ours).toEqual({
      kind: "unreachable",
      reason: "refused",
      detail: "dial tcp: connection refused",
    });

    const theirs = await classify(
      res("<html>gateway</html>", { status: 502, contentType: "text/html" }),
    );
    expect(theirs.kind).toBe("server-error");
  });

  it("classifies the remaining statuses", async () => {
    const json = { contentType: "application/json" } as const;

    expect(await classify(res(`{"error":"bad uuid"}`, { status: 400, ...json }))).toEqual({
      kind: "bad-request",
      message: "bad uuid",
    });
    expect(await classify(res(`{"error":"name taken"}`, { status: 409, ...json }))).toEqual({
      kind: "conflict",
      message: "name taken",
    });
    expect((await classify(res(`{"error":"disk"}`, { status: 500, ...json }))).kind).toBe(
      "server-error",
    );
  });

  it("carries no_admins off our own 401, which drives the setup card", async () => {
    expect(
      await classify(
        res(`{"error":"not authenticated","no_admins":true}`, {
          status: 401,
          contentType: "application/json",
        }),
      ),
    ).toEqual({ kind: "unauthorized", noAdmins: true });

    expect(
      await classify(
        res(`{"error":"not authenticated"}`, { status: 401, contentType: "application/json" }),
      ),
    ).toEqual({ kind: "unauthorized" });
  });
});
