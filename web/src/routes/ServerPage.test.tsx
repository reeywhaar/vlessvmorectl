import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { ServerPage } from "./ServerPage";
import { expectedLink, makeFake, makeServer, makeUser, renderAt } from "../test/fake";

const server = makeServer({ id: "aaa111", url: "https://ams.example.com" });
const alice = makeUser({ id: "u_alice", name: "alice-phone" });
const bob = makeUser({ id: "u_bob", name: "bob-laptop", quota_bytes: 1000 });

function renderPage(route = `/servers/${server.id}`) {
  const fake = makeFake({
    servers: [server],
    users: { [server.id]: [alice, bob] },
  });
  renderAt(<ServerPage />, { fake, route, path: "/servers/:serverId" });
  return fake;
}

/**
 * Scope queries to the open dialog, so the table behind it cannot match.
 *
 * `dialog[open]`, not `dialog`: CreateUserDialog is mounted unconditionally with
 * open={false}, and a closed <dialog> keeps its children in the document. Selecting on the
 * tag alone finds that one and every assertion looks at the wrong tree.
 */
const openDialogs = () => Array.from(document.querySelectorAll("dialog[open]")) as HTMLElement[];
const drawer = () => within(openDialogs()[0]!);
const topDialog = () => within(openDialogs()[openDialogs().length - 1]!);

describe("ServerPage", () => {
  it("lists the node's users", async () => {
    renderPage();
    expect(await screen.findByText("alice-phone")).toBeTruthy();
    expect(screen.getByText("bob-laptop")).toBeTruthy();
  });

  /**
   * The deep link, and the reason the drawer's selection moved out of useState.
   *
   * A subscriber's connection row links straight here. If this stops working the link
   * silently does nothing, which is the sort of failure nobody reports — they just stop
   * using it.
   */
  it("opens a user's drawer straight from the URL", async () => {
    renderPage(`/servers/${server.id}?user=u_bob`);

    await waitFor(() => expect(openDialogs().length).toBeGreaterThan(0));
    expect(drawer().getByText("Connection")).toBeTruthy();
    // The drawer's own heading, not the row in the table behind it.
    expect(drawer().getAllByText("bob-laptop").length).toBeGreaterThan(0);
  });

  it("puts the open user in the URL, so it survives a reload and Back closes it", async () => {
    const u = userEvent.setup();
    renderPage();
    await screen.findByText("alice-phone");

    await u.click(screen.getByText("alice-phone"));
    await waitFor(() => expect(openDialogs().length).toBeGreaterThan(0));
    expect(await drawer().findByText("Connection")).toBeTruthy();
  });

  it("ignores a ?user= that names nobody, rather than showing an empty drawer", async () => {
    renderPage(`/servers/${server.id}?user=u_ghost`);
    await screen.findByText("alice-phone");
    expect(openDialogs()).toHaveLength(0);
  });

  // Reopening is the half that matters: anything done at mount passes the first and fails this.
  it("focuses the name field each time the add-user dialog opens", async () => {
    const u = userEvent.setup();
    renderPage();
    await screen.findByText("alice-phone");

    for (const attempt of ["first open", "reopened"]) {
      await u.click(screen.getByRole("button", { name: "Add user" }));
      await waitFor(() => expect(openDialogs().length).toBeGreaterThan(0));

      const name = topDialog().getByLabelText(/^Name/);
      expect(document.activeElement, attempt).toBe(name);

      await u.click(topDialog().getByRole("button", { name: "Close" }));
      await waitFor(() => expect(openDialogs()).toHaveLength(0));
    }
  });
});

describe("UserDrawer credentials", () => {
  /**
   * The shoulder-surfing case, on the operator's side.
   *
   * The QR used to be drawn here the moment the drawer opened, at scanning size, beside
   * URLs that were blurred against exactly the same threat. An operator with this open is
   * very often screen-sharing.
   */
  it("draws no QR until the button is pressed", async () => {
    const u = userEvent.setup();
    renderPage(`/servers/${server.id}?user=u_alice`);
    await waitFor(() => expect(openDialogs().length).toBeGreaterThan(0));
    await drawer().findByText("Connection");

    expect(drawer().queryByRole("img")).toBeNull();

    await u.click(
      drawer().getByRole("button", { name: /Show subscription url as a QR code/i }),
    );
    expect(await topDialog().findByRole("img")).toBeTruthy();
    expect(screen.getByText(/Anyone who can see this code can connect as/i)).toBeTruthy();
    // Two dialogs now, and the QR lives in the inner one.
    expect(openDialogs()).toHaveLength(2);
  });

  it("blurs each credential until it is revealed", async () => {
    const u = userEvent.setup();
    renderPage(`/servers/${server.id}?user=u_alice`);
    await waitFor(() => expect(openDialogs().length).toBeGreaterThan(0));

    const value = await drawer().findByText(expectedLink(alice, server));
    expect(value.className).toContain("blur-");

    // Each row has its own Reveal; the vless:// one is under that heading.
    await u.click(drawer().getAllByRole("button", { name: "Reveal" })[1]!);
    expect(value.className).not.toContain("blur-");
  });

  it("offers no QR button for a credential the node sent no code for", async () => {
    const fake = makeFake({
      servers: [server],
      users: { [server.id]: [alice] },
      // An older node, or one that could not encode it.
      links: { u_alice: { subscription_qr: undefined as never, qr: undefined as never } },
    });
    renderAt(<ServerPage />, {
      fake,
      route: `/servers/${server.id}?user=u_alice`,
      path: "/servers/:serverId",
    });

    await waitFor(() => expect(openDialogs().length).toBeGreaterThan(0));
    await drawer().findByText("Connection");
    expect(drawer().queryByRole("button", { name: /as a QR code/i })).toBeNull();
    // The links themselves are still there — a missing code is not a missing credential.
    expect(drawer().getByText("Subscription URL")).toBeTruthy();
  });
});
