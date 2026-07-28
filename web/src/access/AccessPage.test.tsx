import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AccessPage } from "./AccessPage";
import type { AccessResponse } from "./types";

/**
 * The island's tests, and the most valuable of them is the first: this page must reach
 * exactly one endpoint. Everything else on this origin is behind a session, and a page
 * that quietly asked for /api/me would render a login form to somebody holding a share
 * link — the failure the whole two-bundle split exists to prevent.
 *
 * fetch is stubbed rather than a Transport being faked, because fetchAccess deliberately
 * does not go through the dispatcher, and stubbing the real thing is what proves it.
 */

const TOKEN = "QK7M2XA9TESTTKEN0123456789ABCDEF";
const SUB_URL = "https://amsterdam.example.com/sub/ABC";
const VLESS_LINK = "vless://uuid@amsterdam.example.com:8443?type=tcp";
// A 2x2 matrix is enough: these tests care that a code is drawn, not what it encodes.
const QR = { size: 2, rows: ["10", "01"], quiet_zone: 4 };

function payload(over: Partial<AccessResponse> = {}): AccessResponse {
  return {
    subscriber: { name: "Ivan" },
    fetched_at: "2026-07-28T12:00:00Z",
    entries: [
      {
        id: "e1",
        server_label: "Amsterdam",
        label: "phone",
        available: true,
        name: "ivan-phone",
        enabled: true,
        quota_bytes: 1000,
        usage: { window_up: 10, window_down: 90, window_total: 100, quota_remaining: 900 },
        link: VLESS_LINK,
        subscription_url: SUB_URL,
        install_url: "https://amsterdam.example.com/show/ABC",
        qr: QR,
        subscription_qr: QR,
      },
    ],
    ...over,
  };
}

let calls: string[];

function stubFetch(handler: (url: string) => Response) {
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      calls.push(url);
      return Promise.resolve(handler(url));
    }),
  );
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json; charset=utf-8" },
  });

beforeEach(() => {
  calls = [];
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("AccessPage", () => {
  it("renders a share link and touches no other endpoint", async () => {
    stubFetch(() => json(payload()));
    render(<AccessPage token={TOKEN} />);

    expect(await screen.findByText("Ivan")).toBeTruthy();
    expect(screen.getByText("Amsterdam")).toBeTruthy();

    // The assertion that matters. Exactly one request, to exactly one path.
    expect(calls).toHaveLength(1);
    expect(calls[0]).toContain(`/api/access/${TOKEN}`);
    for (const forbidden of ["/api/me", "/api/servers", "/api/proxy", "/api/subscribers"]) {
      expect(calls.some((c) => c.includes(forbidden))).toBe(false);
    }
  });

  it("does not poll", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    stubFetch(() => json(payload()));
    render(<AccessPage token={TOKEN} />);
    await waitFor(() => expect(screen.getByText("Ivan")).toBeTruthy());

    // A share link left open on a phone would otherwise be permanent, unauthenticated
    // load on every node the panel manages.
    await vi.advanceTimersByTimeAsync(120_000);
    expect(calls).toHaveLength(1);
  });

  it("shows the incomplete-link message when the URL was truncated, without asking", async () => {
    stubFetch(() => json(payload()));
    render(<AccessPage token="" />);

    expect(await screen.findByText(/link looks incomplete/i)).toBeTruthy();
    expect(calls).toHaveLength(0);
  });

  it("treats a JSON 404 as a dead link, and offers no retry", async () => {
    stubFetch(() => json({ error: "this link is not valid" }, 404));
    render(<AccessPage token={TOKEN} />);

    expect(await screen.findByText(/doesn't work any more/i)).toBeTruthy();
    // Retrying a revoked token cannot succeed, and a dead link forwarded around a group
    // chat would turn into a steady trickle of requests.
    expect(screen.queryByRole("button", { name: /try again/i })).toBeNull();
  });

  it("says the same thing for a text/plain 404", async () => {
    // classify() turns a non-JSON 404 into {kind:"refused", likely:"bad-token"}, whose
    // stock copy is about this panel's bearer token. A subscriber must never see it.
    stubFetch(
      () =>
        new Response("404 page not found", {
          status: 404,
          headers: { "content-type": "text/plain; charset=utf-8" },
        }),
    );
    render(<AccessPage token={TOKEN} />);

    expect(await screen.findByText(/doesn't work any more/i)).toBeTruthy();
    expect(screen.queryByText(/token/i)).toBeNull();
  });

  it("offers a retry for a transient failure", async () => {
    stubFetch(() => json({ error: "boom" }, 500));
    render(<AccessPage token={TOKEN} />);

    expect(await screen.findByText(/something went wrong/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: /try again/i })).toBeTruthy();
  });

  it("shows an unreachable entry without showing stale credentials", async () => {
    stubFetch(() =>
      json(
        payload({
          entries: [
            { id: "e1", server_label: "Berlin", available: false, reason: "unavailable" },
          ],
        }),
      ),
    );
    render(<AccessPage token={TOKEN} />);

    expect(await screen.findByText(/temporarily unavailable/i)).toBeTruthy();
    expect(screen.getByText(/probably still working/i)).toBeTruthy();
  });

  it("renders an empty subscriber as an empty state, not an error", async () => {
    stubFetch(() => json(payload({ entries: [] })));
    render(<AccessPage token={TOKEN} />);

    expect(await screen.findByText(/nothing here yet/i)).toBeTruthy();
  });

  /**
   * The shoulder-surfing case, and the most valuable assertions in this file.
   *
   * Somebody opens this link on a train or in a café. The links are on the page — that is
   * what they came for — but nothing is legible over a shoulder, and nothing is scannable
   * from across the room, until they decide otherwise.
   */
  it("blurs every credential, and draws no QR at all, on load", async () => {
    stubFetch(() => json(payload()));
    render(<AccessPage token={TOKEN} />);
    await screen.findByText("Ivan");

    // The figures somebody opened the page to check are plainly there, on one line.
    expect(screen.getByText("Amsterdam")).toBeTruthy();
    expect(screen.getByText(/100 B of 1000 B used · never expires/)).toBeTruthy();

    // The links are present and inline — no dialog to get through — but blurred.
    // Asserted on the class rather than only on the Reveal control, so dropping the blur
    // while keeping the button would fail.
    expect(screen.getByText("Subscription link")).toBeTruthy();
    expect(screen.getByText(SUB_URL).className).toContain("blur-");
    expect(screen.getByText(VLESS_LINK).className).toContain("blur-");

    // The QR is the one thing a stranger can capture without touching the device, so it
    // is not in the document until its button is pressed. QrMatrix renders role="img".
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("unblurs a link only when its own Reveal is pressed", async () => {
    const u = userEvent.setup();
    stubFetch(() => json(payload()));
    render(<AccessPage token={TOKEN} />);
    await screen.findByText("Ivan");

    expect(screen.getByText(SUB_URL).className).toContain("blur-");
    await u.click(screen.getAllByRole("button", { name: "Reveal" })[0]!);
    expect(screen.getByText(SUB_URL).className).not.toContain("blur-");
    // Each row reveals independently; one Reveal does not unmask the other.
    expect(screen.getByText(VLESS_LINK).className).toContain("blur-");
  });

  it("draws a QR only after its button, and in a dialog of its own", async () => {
    const u = userEvent.setup();
    stubFetch(() => json(payload()));
    render(<AccessPage token={TOKEN} />);
    await screen.findByText("Ivan");

    expect(screen.queryByRole("img")).toBeNull();
    await u.click(screen.getByRole("button", { name: /Show subscription link as a QR code/i }));

    expect(await screen.findByRole("img")).toBeTruthy();
    expect(screen.getByText(/Anyone who can see this code/i)).toBeTruthy();
    // One modal, not two: the links are inline, so this is the only thing that opens.
    expect(document.querySelectorAll("dialog[open]")).toHaveLength(1);
  });

  it("does not offer details for an entry it could not confirm", async () => {
    stubFetch(() =>
      json(
        payload({
          entries: [
            { id: "e1", server_label: "Berlin", available: false, reason: "unavailable" },
          ],
        }),
      ),
    );
    render(<AccessPage token={TOKEN} />);
    await screen.findByText(/temporarily unavailable/i);

    // Nothing we could not confirm is shown as though it were current.
    expect(screen.queryByText("Subscription link")).toBeNull();
    expect(screen.queryByRole("button", { name: /as a QR code/i })).toBeNull();
  });
});
