import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { AttachEntryDialog } from "./AttachEntryDialog";
import { ApiProvider } from "../../api/ApiProvider";
import { ApiDispatcher } from "../../api/dispatcher";
import { makeQueryClient } from "../../queries/client";
import type { Method, RequestOptions, Transport } from "../../api/transport";
import type { Subscriber } from "../../api/types";
import { makeFake, makeServer, makeUser } from "../../test/fake";

const user = (id: string, name: string) => makeUser({ id, name });

const ams = makeServer({ id: "aaa111", url: "https://ams.example.com" });
const ber = makeServer({ id: "bbb222", url: "https://ber.example.com" });

function subscriber(entries: Subscriber["entries"] = []): Subscriber {
  return {
    id: "sub1",
    name: "Ivan",
    token: "QK7M2XA9TESTTKEN0123456789ABCDEF",
    access_path: "/access/QK7M2XA9TESTTKEN0123456789ABCDEF",
    disabled: false,
    entries,
    created_at: "2026-06-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
  };
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json; charset=utf-8" },
  });

/**
 * Wraps the shared fake with the two things only this dialog needs: the attach POSTs it
 * made, and the ability to make one account refuse.
 *
 * Layered rather than folded into makeFake, because "reject exactly this vless_user_id"
 * is a partial-failure scenario for one screen, not a property of a stand-in backend.
 */
function fakeTransport(rejectFor: string[] = []) {
  const base = makeFake({
    servers: [ams, ber],
    users: {
      [ams.id]: [user("u_a1", "alice-phone"), user("u_a2", "alice-laptop")],
      [ber.id]: [user("u_b1", "bob-phone")],
    },
    // Named, because one test filters on the server label rather than an account name.
    infos: { [ams.id]: { name: "Amsterdam" }, [ber.id]: { name: "Berlin" } },
  });
  const attaches: { server_id: string; vless_user_id: string; label: string }[] = [];

  const transport: Transport = {
    panel(method: Method, path: string, opts: RequestOptions = {}) {
      if (method === "POST" && path.endsWith("/entries")) {
        const body = opts.body as { server_id: string; vless_user_id: string; label: string };
        if (rejectFor.includes(body.vless_user_id)) {
          return Promise.resolve(json({ error: "already attached" }, 409));
        }
        attaches.push(body);
        return Promise.resolve(json({ subscriber: subscriber() }));
      }
      return base.transport.panel(method, path, opts);
    },
    node: base.transport.node,
  };

  return { transport, attaches };
}

function renderDialog(opts: { rejectFor?: string[]; entries?: Subscriber["entries"] } = {}) {
  const { transport, attaches } = fakeTransport(opts.rejectFor ?? []);
  const onClose = vi.fn();
  render(
    <ApiProvider dispatcher={new ApiDispatcher(transport)}>
      <QueryClientProvider client={makeQueryClient()}>
        <AttachEntryDialog
          subscriber={subscriber(opts.entries ?? [])}
          servers={[ams, ber]}
          open
          onClose={onClose}
        />
      </QueryClientProvider>
    </ApiProvider>,
  );
  return { attaches, onClose };
}

const box = (name: string) => screen.getByRole("checkbox", { name: new RegExp(name) });

describe("AttachEntryDialog", () => {
  it("attaches several accounts across nodes in one go", async () => {
    const u = userEvent.setup();
    const { attaches, onClose } = renderDialog();

    await screen.findByText("alice-phone");
    await u.click(box("alice-phone"));
    await u.click(box("bob-phone"));

    // The button counts, so the operator can see what they are about to do.
    const submit = screen.getByRole("button", { name: "Attach 2" });
    await u.click(submit);

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(attaches).toHaveLength(2);
    expect(attaches.map((a) => a.vless_user_id).sort()).toEqual(["u_a1", "u_b1"]);
    // Two different servers, which is the situation the whole feature exists for.
    expect(new Set(attaches.map((a) => a.server_id)).size).toBe(2);
  });

  it("deselects on a second click", async () => {
    const u = userEvent.setup();
    renderDialog();
    await screen.findByText("alice-phone");

    await u.click(box("alice-phone"));
    expect(screen.getByRole("button", { name: "Attach" })).toBeTruthy();
    await u.click(box("alice-phone"));
    // Nothing selected: the submit button goes back to being disabled.
    expect(screen.getByRole("button", { name: "Attach" }).hasAttribute("disabled")).toBe(true);
  });

  it("hides the label field once more than one is selected", async () => {
    const u = userEvent.setup();
    renderDialog();
    await screen.findByText("alice-phone");

    await u.click(box("alice-phone"));
    expect(screen.getByLabelText(/^Label/)).toBeTruthy();

    // One label cannot describe three connections; they get labelled individually later.
    await u.click(box("alice-laptop"));
    expect(screen.queryByLabelText(/^Label/)).toBeNull();
    expect(screen.getByText(/2 selected/)).toBeTruthy();
  });

  it("keeps going after one fails, and names the one that did not land", async () => {
    const u = userEvent.setup();
    const { attaches, onClose } = renderDialog({ rejectFor: ["u_a2"] });
    await screen.findByText("alice-phone");

    await u.click(box("alice-phone"));
    await u.click(box("alice-laptop"));
    await u.click(box("bob-phone"));
    await u.click(screen.getByRole("button", { name: "Attach 3" }));

    // The two good ones went through rather than being dropped with the bad one.
    await waitFor(() => expect(attaches).toHaveLength(2));
    expect(await screen.findByText(/could not be attached/)).toBeTruthy();
    expect(screen.getByText(/alice-laptop: already attached to this person/)).toBeTruthy();
    // Still open, with the failure still selected, so retrying is one click.
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Attach" })).toBeTruthy();
  });

  it("disables an account that is already attached rather than hiding it", async () => {
    renderDialog({
      entries: [
        {
          id: "e1",
          server_id: ams.id,
          vless_user_id: "u_a1",
          added_at: "2026-07-01T00:00:00Z",
          server_configured: true,
        },
      ],
    });
    await screen.findByText("alice-phone");

    // "Why isn't Bob's phone in this list" is a much worse question than a greyed row.
    expect(box("alice-phone").hasAttribute("disabled")).toBe(true);
    expect(screen.getByText("Attached")).toBeTruthy();
  });

  it("filters on the server label as well as the account name", async () => {
    const u = userEvent.setup();
    renderDialog();
    await screen.findByText("alice-phone");

    await u.type(screen.getByLabelText("Filter accounts"), "berlin");
    await waitFor(() => expect(screen.queryByText("alice-phone")).toBeNull());
    expect(screen.getByText("bob-phone")).toBeTruthy();
  });
});
