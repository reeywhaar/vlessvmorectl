import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";

import { App } from "./App";
import { ApiProvider } from "./api/ApiProvider";
import { ApiDispatcher } from "./api/dispatcher";
import { makeQueryClient } from "./queries/client";
import { ReloadWatch, ReloadWatchProvider } from "./queries/reloadWatch";
import { makeFake } from "./test/fake";
import type { Method, RequestOptions, Transport } from "./api/transport";
import type { Server } from "./api/types";

/**
 * A stand-in for the backend that flips between signed-out and signed-in, so the two
 * transitions can be driven without a network.
 */
function fakeTransport() {
  const state = { signedIn: false };

  const json = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), {
      status,
      headers: { "content-type": "application/json; charset=utf-8" },
    });

  const transport: Transport = {
    panel(_method: Method, path: string, opts: RequestOptions = {}) {
      if (path === "/api/me") {
        return Promise.resolve(
          state.signedIn
            ? json({ username: "alice", expires_at: "2026-08-06T00:00:00Z" })
            : json({ error: "not authenticated" }, 401),
        );
      }
      if (path === "/api/login") {
        state.signedIn = true;
        return Promise.resolve(json({ user: { username: "alice" } }));
      }
      if (path === "/api/logout") {
        state.signedIn = false;
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (path === "/api/servers") {
        return Promise.resolve(
          state.signedIn ? json({ servers: [] }) : json({ error: "not authenticated" }, 401),
        );
      }
      void opts;
      return Promise.resolve(json({ error: "no such endpoint" }, 404));
    },
    node(_server: Server, _method: Method, _path: string) {
      return Promise.resolve(json({ error: "not used in this test" }, 404));
    },
  };

  return { transport, state };
}

function renderApp() {
  const { transport, state } = fakeTransport();
  const client = makeQueryClient();

  render(
    <ApiProvider dispatcher={new ApiDispatcher(transport)}>
      <QueryClientProvider client={client}>
        <ReloadWatchProvider value={new ReloadWatch()}>
          <MemoryRouter>
            <App />
          </MemoryRouter>
        </ReloadWatchProvider>
      </QueryClientProvider>
    </ApiProvider>,
  );
  return { state };
}

describe("sign in and out", () => {
  /**
   * The regression this exists for.
   *
   * The login form is rendered *inside* an error boundary's fallback, because a 401 from
   * /api/me is what puts it there. A successful login therefore has to clear that
   * boundary's error state — invalidating queries does not, and without it the form just
   * sits there looking like nothing happened until the page is reloaded.
   */
  it("shows the app immediately after signing in, with no reload", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByLabelText("Username");

    await user.type(screen.getByLabelText("Username"), "alice");
    await user.type(screen.getByLabelText("Password"), "hunter2hunter2");
    await user.click(screen.getByRole("button", { name: /sign in/i }));

    await waitFor(() => {
      expect(screen.getByRole("button", { name: /sign out/i })).toBeTruthy();
    });
    expect(screen.queryByLabelText("Password")).toBeNull();
  });

  /** The mirror image: signing out must return to the form without a reload. */
  it("returns to the login form immediately after signing out", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByLabelText("Username");
    await user.type(screen.getByLabelText("Username"), "alice");
    await user.type(screen.getByLabelText("Password"), "hunter2hunter2");
    await user.click(screen.getByRole("button", { name: /sign in/i }));

    const signOut = await screen.findByRole("button", { name: /sign out/i });
    await user.click(signOut);

    await waitFor(() => {
      expect(screen.getByLabelText("Password")).toBeTruthy();
    });
    expect(screen.queryByRole("button", { name: /sign out/i })).toBeNull();
  });

  it("keeps the form up and explains why when the credentials are wrong", async () => {
    const user = userEvent.setup();
    const { transport } = fakeTransport();
    const spy = vi.spyOn(transport, "panel").mockImplementation((_method, path) => {
      if (path === "/api/login") {
        return Promise.resolve(
          new Response(JSON.stringify({ error: "invalid username or password" }), {
            status: 401,
            headers: { "content-type": "application/json" },
          }),
        );
      }
      return Promise.resolve(
        new Response(JSON.stringify({ error: "not authenticated" }), {
          status: 401,
          headers: { "content-type": "application/json" },
        }),
      );
    });

    render(
      <ApiProvider dispatcher={new ApiDispatcher(transport)}>
        <QueryClientProvider client={makeQueryClient()}>
          <ReloadWatchProvider value={new ReloadWatch()}>
            <MemoryRouter>
              <App />
            </MemoryRouter>
          </ReloadWatchProvider>
        </QueryClientProvider>
      </ApiProvider>,
    );

    await screen.findByLabelText("Username");
    await user.type(screen.getByLabelText("Username"), "alice");
    await user.type(screen.getByLabelText("Password"), "wrongwrongwrong");
    await user.click(screen.getByRole("button", { name: /sign in/i }));

    await waitFor(() => {
      expect(screen.getByRole("alert").textContent).toMatch(/incorrect username or password/i);
    });
    expect(screen.getByLabelText("Password")).toBeTruthy();
    spy.mockRestore();
  });
});

/**
 * Only the state machine, because that is all jsdom can see: which layout the header is
 * wearing is decided by a media query, and there is no stylesheet here. What is worth
 * protecting is that the button reports its state and that following a link puts the menu
 * away — a menu still standing open over the page it just navigated to is the classic bug
 * in this pattern.
 */
describe("the phone menu", () => {
  function renderSignedIn() {
    const fake = makeFake({ servers: [], users: {}, subscribers: [] });
    render(
      <ApiProvider dispatcher={new ApiDispatcher(fake.transport)}>
        <QueryClientProvider client={makeQueryClient()}>
          <ReloadWatchProvider value={new ReloadWatch()}>
            <MemoryRouter>
              <App />
            </MemoryRouter>
          </ReloadWatchProvider>
        </QueryClientProvider>
      </ApiProvider>,
    );
  }

  it("opens, and closes again on navigation", async () => {
    const user = userEvent.setup();
    renderSignedIn();

    const opener = await screen.findByRole("button", { name: "Open the menu" });
    expect(opener.getAttribute("aria-expanded")).toBe("false");

    await user.click(opener);
    const closer = screen.getByRole("button", { name: "Close the menu" });
    expect(closer.getAttribute("aria-expanded")).toBe("true");
    expect(closer.getAttribute("aria-controls")).toBe("site-menu");

    await user.click(screen.getByRole("link", { name: "Subscribers" }));

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Open the menu" })).toBeTruthy();
    });
  });

  it("closes on Escape", async () => {
    const user = userEvent.setup();
    renderSignedIn();

    await user.click(await screen.findByRole("button", { name: "Open the menu" }));
    await user.keyboard("{Escape}");

    expect(screen.getByRole("button", { name: "Open the menu" })).toBeTruthy();
  });
});
