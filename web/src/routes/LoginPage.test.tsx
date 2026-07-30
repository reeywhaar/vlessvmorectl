import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LoginPage } from "./LoginPage";
import { makeFake, renderAt } from "../test/fake";
import { stubAuthenticator } from "../test/authenticator";

afterEach(() => vi.unstubAllGlobals());

function render(
  props: { noAdmins?: boolean; passkeysEnabled?: boolean } = {},
  onSignedIn = vi.fn(),
) {
  const fake = makeFake({ passkeysEnabled: props.passkeysEnabled ?? false });
  renderAt(
    <LoginPage
      noAdmins={props.noAdmins ?? false}
      passkeysEnabled={props.passkeysEnabled ?? false}
      onSignedIn={onSignedIn}
    />,
    { fake, route: "/", path: "/" },
  );
  return { fake, onSignedIn };
}

const passkeyButton = () => screen.queryByRole("button", { name: /passkey/i });

describe("LoginPage", () => {
  it("offers no passkey affordance when the server has not configured one", async () => {
    stubAuthenticator();
    const { fake } = render({ passkeysEnabled: false });

    expect(await screen.findByLabelText("Username")).toBeTruthy();
    expect(passkeyButton()).toBeNull();
    expect(screen.getByLabelText("Username").getAttribute("autocomplete")).toBe("username");
    expect(fake.calls).not.toContain("PANEL POST /api/passkeys/login/begin");
  });

  // jsdom's default: the server offers passkeys but this browser has no WebAuthn at all.
  it("offers no passkey affordance when the browser cannot do WebAuthn", async () => {
    const { fake } = render({ passkeysEnabled: true });

    expect(await screen.findByLabelText("Username")).toBeTruthy();
    expect(passkeyButton()).toBeNull();
    expect(fake.calls).not.toContain("PANEL POST /api/passkeys/login/begin");
  });

  it("shows the button when both sides support it", async () => {
    stubAuthenticator();
    render({ passkeysEnabled: true });

    expect(await screen.findByRole("button", { name: "Sign in with a passkey" })).toBeTruthy();
    // The token that lets a browser offer a passkey from the username field's autofill.
    expect(screen.getByLabelText("Username").getAttribute("autocomplete")).toBe(
      "username webauthn",
    );
  });

  // A fresh install has nobody to sign in as, by either means.
  it("shows the setup card and no passkey button when there are no administrators", async () => {
    stubAuthenticator();
    render({ passkeysEnabled: true, noAdmins: true });

    expect(await screen.findByText("No administrators yet")).toBeTruthy();
    expect(passkeyButton()).toBeNull();
  });

  it("signs in with the button", async () => {
    const auth = stubAuthenticator();
    const { fake, onSignedIn } = render({ passkeysEnabled: true });

    await userEvent.click(await screen.findByRole("button", { name: "Sign in with a passkey" }));

    await waitFor(() => expect(onSignedIn).toHaveBeenCalled());
    expect(fake.calls).toContain("PANEL POST /api/passkeys/login/begin");
    expect(fake.calls).toContain("PANEL POST /api/passkeys/login/finish");

    // The challenge reached the browser as a buffer, not a string.
    expect(auth.get.mock.calls[0]![0].publicKey.challenge).toBeInstanceOf(Uint8Array);
    // And the assertion came back as base64url.
    const posted = fake.bodies.find(
      (b): b is { state: string; credential: { response: { signature: string } } } =>
        typeof b === "object" && b !== null && "credential" in b,
    );
    expect(posted!.state).toBe("login-state");
    expect(posted!.credential.response.signature).not.toMatch(/[+/=]/);
  });

  it("reports a refused passkey without saying which part was wrong", async () => {
    stubAuthenticator({ getRejects: new DOMException("nope", "NotAllowedError") });
    render({ passkeysEnabled: true });

    await userEvent.click(await screen.findByRole("button", { name: "Sign in with a passkey" }));
    expect(await screen.findByText(/Cancelled, or your device took too long/)).toBeTruthy();
  });

  describe("conditional mediation", () => {
    it("starts a ceremony on mount and signs in when the autofill is used", async () => {
      stubAuthenticator({ conditional: true });
      const { fake, onSignedIn } = render({ passkeysEnabled: true });

      await waitFor(() => expect(onSignedIn).toHaveBeenCalled());
      expect(fake.calls.filter((c) => c.includes("login/begin"))).toHaveLength(1);
      expect(fake.calls).toContain("PANEL POST /api/passkeys/login/finish");
    });

    it("asks for conditional mediation, not a modal prompt", async () => {
      const auth = stubAuthenticator({ conditional: true, getHangs: true });
      render({ passkeysEnabled: true });

      await waitFor(() => expect(auth.get).toHaveBeenCalled());
      expect(auth.get.mock.calls[0]![0].mediation).toBe("conditional");
    });

    // The non-obvious bug: a conditional request left running can resolve later and sign in
    // as whoever the autofill picked, racing a password login for a different account.
    it("aborts the outstanding request when the password form is submitted", async () => {
      const auth = stubAuthenticator({ conditional: true, getHangs: true });
      const { fake } = render({ passkeysEnabled: true });
      await waitFor(() => expect(auth.get).toHaveBeenCalled());

      await userEvent.type(await screen.findByLabelText("Username"), "alice");
      await userEvent.type(screen.getByLabelText("Password"), "hunter2hunter2");
      await userEvent.click(screen.getByRole("button", { name: "Sign in" }));

      expect(auth.signals[0]!.aborted).toBe(true);
      await waitFor(() => expect(fake.calls).toContain("PANEL POST /api/login"));
    });

    // And the explicit button must fetch a *fresh* challenge: the aborted one is dead.
    it("aborts the outstanding request and re-begins when the button is pressed", async () => {
      const auth = stubAuthenticator({ conditional: true, getHangs: true });
      const { fake } = render({ passkeysEnabled: true });
      await waitFor(() => expect(auth.get).toHaveBeenCalled());

      await userEvent.click(screen.getByRole("button", { name: "Sign in with a passkey" }));

      expect(auth.signals[0]!.aborted).toBe(true);
      await waitFor(() =>
        expect(fake.calls.filter((c) => c.includes("login/begin"))).toHaveLength(2),
      );
    });

    // Ignoring the suggestion and typing a password rejects with NotAllowedError. Showing
    // "Cancelled" under a form somebody is happily filling in would be a bug, not feedback.
    it("stays silent when the conditional request is refused", async () => {
      stubAuthenticator({
        conditional: true,
        getRejects: new DOMException("ignored", "NotAllowedError"),
      });
      render({ passkeysEnabled: true });

      await screen.findByLabelText("Username");
      await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
      expect(screen.queryByText(/Cancelled/)).toBeNull();
    });
  });
});
