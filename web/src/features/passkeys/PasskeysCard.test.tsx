import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PasskeysCard } from "./PasskeysCard";
import { makeFake, makePasskey, renderAt } from "../../test/fake";
import { stubAuthenticator } from "../../test/authenticator";

afterEach(() => vi.unstubAllGlobals());

function render(opts: Parameters<typeof makeFake>[0] = {}) {
  const fake = makeFake({ passkeysEnabled: true, ...opts });
  renderAt(<PasskeysCard enabled />, { fake, route: "/account", path: "/account" });
  return fake;
}

describe("PasskeysCard", () => {
  // jsdom has no PublicKeyCredential, which is exactly the browser-cannot case.
  it("explains rather than offering when the browser cannot do WebAuthn", async () => {
    const fake = makeFake({ passkeysEnabled: true });
    renderAt(<PasskeysCard enabled />, { fake, route: "/account", path: "/account" });

    expect(await screen.findByText("This browser cannot use passkeys")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Add a passkey" })).toBeNull();
    // And it does not even ask the server.
    expect(fake.calls).not.toContain("PANEL GET /api/passkeys");
  });

  // The deliberate asymmetry with the sign-in screen: an authenticated operator gets told
  // which variable to set.
  it("names the environment variable when the feature is switched off", async () => {
    stubAuthenticator();
    const fake = makeFake({ passkeysEnabled: false });
    renderAt(<PasskeysCard enabled={false} />, { fake, route: "/account", path: "/account" });

    expect(await screen.findByText("Passkeys are switched off")).toBeTruthy();
    expect(screen.getByText("VLESSVMORE_PASSKEY_ORIGIN")).toBeTruthy();
  });

  it("lists enrolled passkeys with their state", async () => {
    stubAuthenticator();
    render({
      passkeys: [
        makePasskey({ id: "a", label: "iPhone", synced: true, last_used_at: "2026-07-30T07:00:00Z" }),
        makePasskey({ id: "b", label: "YubiKey", synced: false }),
      ],
    });

    expect(await screen.findByText("iPhone")).toBeTruthy();
    expect(screen.getByText("YubiKey")).toBeTruthy();
    expect(screen.getByText("Synced")).toBeTruthy();
    expect(screen.getByText("This device")).toBeTruthy();
    expect(screen.getByText(/never used/)).toBeTruthy();
  });

  it("says so when there are none", async () => {
    stubAuthenticator();
    render({ passkeys: [] });
    expect(await screen.findByText("No passkeys yet")).toBeTruthy();
  });

  it("enrols a passkey, decoding the challenge to a buffer on the way out", async () => {
    const auth = stubAuthenticator();
    const fake = render({ passkeys: [] });
    await screen.findByText("No passkeys yet");

    await userEvent.click(screen.getByRole("button", { name: "Add a passkey" }));
    const name = await screen.findByLabelText(/^Name/);
    await userEvent.clear(name);
    await userEvent.type(name, "Work laptop");
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByText("Work laptop")).toBeTruthy();
    expect(fake.calls).toContain("PANEL POST /api/passkeys/register/begin");
    expect(fake.calls).toContain("PANEL POST /api/passkeys/register/finish");

    // The bug this catches is silent: an ArrayBuffer that reaches JSON.stringify becomes
    // {"0":1,…} rather than throwing, and the server then complains about the wrong thing.
    const options = auth.create.mock.calls[0]![0].publicKey;
    expect(options.challenge).toBeInstanceOf(Uint8Array);
    expect(options.user.id).toBeInstanceOf(Uint8Array);
    expect(options.excludeCredentials).toEqual([]);

    // And what we posted back is base64url strings throughout.
    const posted = fake.bodies.find(
      (b): b is { label: string; credential: { response: { clientDataJSON: string } } } =>
        typeof b === "object" && b !== null && "credential" in b,
    );
    expect(posted!.label).toBe("Work laptop");
    expect(typeof posted!.credential.response.clientDataJSON).toBe("string");
    expect(posted!.credential.response.clientDataJSON).not.toMatch(/[+/=]/);
  });

  // Storing a passkey that can never sign in without saying so would be worse than useless.
  it("warns when the authenticator did not store a discoverable credential", async () => {
    stubAuthenticator({ residentKey: false });
    render({ passkeys: [] });
    await screen.findByText("No passkeys yet");

    await userEvent.click(screen.getByRole("button", { name: "Add a passkey" }));
    await userEvent.click(await screen.findByRole("button", { name: "Continue" }));

    expect(
      await screen.findByText(/did not store a discoverable passkey/),
    ).toBeTruthy();
  });

  it("reports a cancelled prompt without closing the dialog", async () => {
    stubAuthenticator({ createRejects: new DOMException("no", "NotAllowedError") });
    render({ passkeys: [] });
    await screen.findByText("No passkeys yet");

    await userEvent.click(screen.getByRole("button", { name: "Add a passkey" }));
    await userEvent.click(await screen.findByRole("button", { name: "Continue" }));

    expect(await screen.findByText(/Cancelled, or your device took too long/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Continue" })).toBeTruthy();
  });

  it("reports an already-enrolled device distinctly", async () => {
    stubAuthenticator({ createRejects: new DOMException("dup", "InvalidStateError") });
    render({ passkeys: [] });
    await screen.findByText("No passkeys yet");

    await userEvent.click(screen.getByRole("button", { name: "Add a passkey" }));
    await userEvent.click(await screen.findByRole("button", { name: "Continue" }));

    expect(await screen.findByText("This device already has a passkey for this panel.")).toBeTruthy();
  });

  it("renames a passkey", async () => {
    stubAuthenticator();
    const fake = render({ passkeys: [makePasskey({ id: "a", label: "iPhone" })] });
    await screen.findByText("iPhone");

    await userEvent.click(screen.getByRole("button", { name: "Rename iPhone" }));
    const field = screen.getByLabelText("Name for iPhone");
    await userEvent.clear(field);
    await userEvent.type(field, "iPad");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("iPad")).toBeTruthy();
    expect(fake.calls).toContain("PANEL PATCH /api/passkeys/a");
    expect(fake.bodies).toContainEqual({ label: "iPad" });
  });

  // Confirm, not ConfirmDelete: no name to type, because losing a passkey is recoverable.
  it("removes a passkey after a plain confirmation", async () => {
    stubAuthenticator();
    const fake = render({ passkeys: [makePasskey({ id: "a", label: "iPhone" })] });
    await screen.findByText("iPhone");

    await userEvent.click(screen.getByRole("button", { name: "Remove iPhone" }));
    await userEvent.click(await screen.findByRole("button", { name: "Remove" }));

    await waitFor(() => expect(screen.queryByText("iPhone")).toBeNull());
    expect(fake.calls).toContain("PANEL DELETE /api/passkeys/a");
    expect(await screen.findByText("No passkeys yet")).toBeTruthy();
  });
});
