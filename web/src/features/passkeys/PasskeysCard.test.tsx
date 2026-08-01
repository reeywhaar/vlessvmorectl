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

  it("names the provider and draws the logo the listing gave it", async () => {
    stubAuthenticator();
    render({
      passkeys: [
        makePasskey({
          id: "a",
          label: "iPhone",
          provider: "Apple Passwords",
          logo: "/assets/icons/webauthn/apple_passwords_light.9d2487ab5b1d.svg",
        }),
      ],
    });

    expect(await screen.findByText(/Apple Passwords/)).toBeTruthy();
    // Decorative on purpose: the name is already in the row, so the logo has an empty alt and is
    // found by role="presentation" rather than by an accessible name.
    const logo = screen.getByRole("presentation");
    expect(logo.getAttribute("src")).toBe(
      "/assets/icons/webauthn/apple_passwords_light.9d2487ab5b1d.svg",
    );
  });

  // The dark theme is what the panel starts in, so a provider shipping a second image gets it.
  it("prefers the dark logo when the provider ships one", async () => {
    stubAuthenticator();
    render({
      passkeys: [
        makePasskey({
          id: "a",
          label: "Laptop",
          provider: "1Password",
          logo: "/assets/icons/webauthn/1password_light.c8448a372019.svg",
          logo_dark: "/assets/icons/webauthn/1password_dark.3c30e5f53516.svg",
        }),
      ],
    });

    await screen.findByText(/1Password/);
    expect(screen.getByRole("presentation").getAttribute("src")).toBe(
      "/assets/icons/webauthn/1password_dark.3c30e5f53516.svg",
    );
  });

  // The hardware key case: named, but we hold no logo for it. The name still shows, and no
  // request goes out for an image the listing never offered.
  it("names an authenticator we have no logo for, without asking for one", async () => {
    stubAuthenticator();
    render({
      passkeys: [
        {
          id: "a",
          label: "YubiKey",
          algorithm: "ES256",
          provider: "YubiKey 5 Series",
          aaguid: "cb69481e-8ff7-4039-93ec-0a2729a154a8",
          synced: false,
          created_at: "2026-07-01T09:12:33Z",
        },
      ],
    });

    expect(await screen.findByText(/YubiKey 5 Series/)).toBeTruthy();
    expect(screen.queryByRole("presentation")).toBeNull();
  });

  // And a client that stripped the id altogether, which is common for a security key. The row
  // stands on its own — there is simply nothing to look up.
  //
  // Written out rather than overriding makePasskey's defaults with undefined, which is what the
  // server actually sends: the three fields are absent, not present and empty.
  it("falls back to the key glyph when the authenticator did not identify itself", async () => {
    stubAuthenticator();
    render({
      passkeys: [
        {
          id: "a",
          label: "Security key",
          algorithm: "ES256",
          synced: false,
          created_at: "2026-07-01T09:12:33Z",
        },
      ],
    });

    expect(await screen.findByText("Security key")).toBeTruthy();
    expect(screen.queryByRole("presentation")).toBeNull();
  });

  // One click and the authenticator is invoked: nothing is asked first, because the only thing a
  // form could collect is a name for something not yet identified.
  it("enrols a passkey, decoding the challenge to a buffer on the way out", async () => {
    const auth = stubAuthenticator();
    const fake = render({ passkeys: [] });
    await screen.findByText("No passkeys yet");

    await userEvent.click(screen.getByRole("button", { name: "Add a passkey" }));

    expect(await screen.findByText("Apple Passwords")).toBeTruthy();
    expect(fake.calls).toContain("PANEL POST /api/passkeys/register/begin");
    expect(fake.calls).toContain("PANEL POST /api/passkeys/register/finish");

    // The bug this catches is silent: an ArrayBuffer that reaches JSON.stringify becomes
    // {"0":1,…} rather than throwing, and the server then complains about the wrong thing.
    const options = auth.create.mock.calls[0]![0].publicKey;
    expect(options.challenge).toBeInstanceOf(Uint8Array);
    expect(options.user.id).toBeInstanceOf(Uint8Array);
    expect(options.excludeCredentials).toEqual([]);

    // And what we posted back is base64url strings throughout, with no name among it.
    const posted = fake.bodies.find(
      (b): b is { credential: { response: { clientDataJSON: string } } } =>
        typeof b === "object" && b !== null && "credential" in b,
    );
    expect(posted).not.toHaveProperty("label");
    expect(typeof posted!.credential.response.clientDataJSON).toBe("string");
    expect(posted!.credential.response.clientDataJSON).not.toMatch(/[+/=]/);
  });

  // Storing a passkey that can never sign in without saying so would be worse than useless.
  it("warns when the authenticator did not store a discoverable credential", async () => {
    stubAuthenticator({ residentKey: false });
    render({ passkeys: [] });
    await screen.findByText("No passkeys yet");

    await userEvent.click(screen.getByRole("button", { name: "Add a passkey" }));

    expect(await screen.findByText(/did not store a discoverable passkey/)).toBeTruthy();
  });

  it("reports a cancelled prompt, leaving the button ready to try again", async () => {
    stubAuthenticator({ createRejects: new DOMException("no", "NotAllowedError") });
    render({ passkeys: [] });
    await screen.findByText("No passkeys yet");

    await userEvent.click(screen.getByRole("button", { name: "Add a passkey" }));

    expect(await screen.findByText(/Cancelled, or your device took too long/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Add a passkey" })).toBeTruthy();
  });

  it("reports an already-enrolled device distinctly", async () => {
    stubAuthenticator({ createRejects: new DOMException("dup", "InvalidStateError") });
    render({ passkeys: [] });
    await screen.findByText("No passkeys yet");

    await userEvent.click(screen.getByRole("button", { name: "Add a passkey" }));

    expect(await screen.findByText("This device already has a passkey for this panel.")).toBeTruthy();
  });

  // The field shows what is stored, which for an unnamed credential is nothing. Seeding it with
  // the provider's name would look like somebody had already chosen that.
  it("opens an empty field for an unnamed passkey, with the fallback as a placeholder", async () => {
    stubAuthenticator();
    render({ passkeys: [makePasskey({ id: "a", label: "" })] });
    await screen.findByText("Apple Passwords");

    await userEvent.click(screen.getByRole("button", { name: "Rename Apple Passwords" }));
    const field = screen.getByLabelText("Name for Apple Passwords") as HTMLInputElement;
    expect(field.value).toBe("");
    expect(field.getAttribute("placeholder")).toBe("Apple Passwords");
  });

  // Clearing is an edit, not an invalid one: it puts the credential back to unnamed.
  it("clears a name back to the provider's", async () => {
    stubAuthenticator();
    const fake = render({ passkeys: [makePasskey({ id: "a", label: "Work laptop" })] });
    await screen.findByText("Work laptop");

    await userEvent.click(screen.getByRole("button", { name: "Rename Work laptop" }));
    await userEvent.clear(screen.getByLabelText("Name for Work laptop"));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Apple Passwords")).toBeTruthy();
    expect(screen.queryByText("Work laptop")).toBeNull();
    expect(fake.bodies).toContainEqual({ label: "" });
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
