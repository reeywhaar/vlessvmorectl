import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { OverviewPage } from "./OverviewPage";
import { Boundary } from "../components/Boundary";
import { makeFake, makeServer, makeUser, renderAt } from "../test/fake";

const spb = makeServer({ id: "aaa111", url: "https://spb.example.com" });
const nl = makeServer({ id: "bbb222", url: "https://nl.example.com" });

function renderPage(down: string[] = []) {
  const fake = makeFake({
    servers: [spb, nl],
    users: {
      [spb.id]: [makeUser({ id: "u_alice", name: "alice-phone" })],
      [nl.id]: [makeUser({ id: "u_bob", name: "bob-laptop" })],
    },
    down,
  });
  // The page suspends on /api/servers and relies on the shell's boundary, which renderAt has
  // no equivalent of.
  renderAt(
    <Boundary pending={null} fallback={({ error }) => <p>page failed: {String(error)}</p>}>
      <OverviewPage />
    </Boundary>,
    { fake, route: "/", path: "/" },
  );
  return fake;
}

const dialog = () => within(document.querySelector("dialog[open]") as HTMLElement);
const boxes = () => dialog().getAllByRole("checkbox") as HTMLInputElement[];

async function openDialog(u: ReturnType<typeof userEvent.setup>) {
  await u.click(await screen.findByRole("button", { name: "Add user" }));
  await waitFor(() => expect(document.querySelector("dialog[open]")).toBeTruthy());
  await waitFor(() => expect(boxes()).toHaveLength(2));
}

describe("OverviewPage add user", () => {
  // Nothing ticked to start: adding to every node is a choice rather than a default.
  it("offers every node, none ticked, and Select all takes them", async () => {
    const u = userEvent.setup();
    renderPage();
    await openDialog(u);

    expect(boxes().some((b) => b.checked)).toBe(false);
    expect(
      (dialog().getByRole("button", { name: "Create" }) as HTMLButtonElement).disabled,
    ).toBe(true);

    await u.click(dialog().getByRole("button", { name: "Select all" }));
    expect(boxes().every((b) => b.checked)).toBe(true);

    // And the same control gives them back, so an accidental Select all is one click to undo.
    await u.click(dialog().getByRole("button", { name: "Clear" }));
    expect(boxes().some((b) => b.checked)).toBe(false);
  });

  // The collision rule. A node that already has the name cannot take it again, so it is taken
  // out of the submission rather than left to fail at the node.
  it("disables a node that already has the name", async () => {
    const u = userEvent.setup();
    renderPage();
    await openDialog(u);

    await u.click(dialog().getByRole("button", { name: "Select all" }));
    await u.type(dialog().getByLabelText(/^Name/), "alice-phone");

    await waitFor(() => expect(boxes()[0]!.disabled).toBe(true));
    expect(boxes()[0]!.checked).toBe(false);
    expect(boxes()[1]!.disabled).toBe(false);
    expect(boxes()[1]!.checked).toBe(true);
    expect(dialog().getByText(/already has this name/)).toBeTruthy();
  });

  it("creates only on the nodes still ticked", async () => {
    const u = userEvent.setup();
    const fake = renderPage();
    await openDialog(u);

    await u.click(dialog().getByRole("button", { name: "Select all" }));
    await u.type(dialog().getByLabelText(/^Name/), "alice-phone");
    await waitFor(() => expect(boxes()[0]!.disabled).toBe(true));
    await u.click(dialog().getByRole("button", { name: "Create" }));

    await waitFor(() => expect(fake.calls).toContain(`NODE ${nl.id} POST /api/users`));
    expect(fake.calls).not.toContain(`NODE ${spb.id} POST /api/users`);
  });

  // Every node colliding leaves nothing to do, and the button says so rather than posting.
  it("will not submit when no node can take the name", async () => {
    const u = userEvent.setup();
    renderPage();
    await openDialog(u);

    await u.click(dialog().getByRole("button", { name: "Select all" }));
    await u.type(dialog().getByLabelText(/^Name/), "alice-phone");
    await waitFor(() => expect(boxes()[0]!.disabled).toBe(true));
    await u.click(boxes()[1]!);

    expect((dialog().getByRole("button", { name: "Create" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
    expect(dialog().getByText(/Every node you picked already has this name/)).toBeTruthy();
  });

  // The dialog sits outside the per-card boundaries, so a node that cannot be reached has to
  // degrade to its host rather than take the page with it.
  it("still opens when a node is down", async () => {
    const u = userEvent.setup();
    renderPage([nl.id]);
    await openDialog(u);

    expect(dialog().getByText("nl.example.com")).toBeTruthy();
    expect(boxes()).toHaveLength(2);
  });
});
