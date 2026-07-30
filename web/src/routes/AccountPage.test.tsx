import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AccountPage } from "./AccountPage";
import { FAKE_PASSWORD, makeFake, renderAt } from "../test/fake";

function render() {
  const fake = makeFake();
  renderAt(<AccountPage />, { fake, route: "/account", path: "/account" });
  return fake;
}

describe("AccountPage", () => {
  it("shows the signed-in username", async () => {
    render();
    expect(await screen.findByRole("heading", { name: "Account" })).toBeTruthy();
    expect(screen.getByDisplayValue("alice")).toBeTruthy();
  });

  // No current password: a username is not a secret, and the session already says who you are.
  it("posts a username change with no password", async () => {
    const fake = render();
    await screen.findByRole("heading", { name: "Account" });

    const field = screen.getByLabelText("Username");
    await userEvent.clear(field);
    await userEvent.type(field, "carol");
    await userEvent.click(screen.getByRole("button", { name: "Change username" }));

    expect(await screen.findByText("Username changed.")).toBeTruthy();
    expect(fake.calls).toContain("PANEL POST /api/account/username");
    expect(fake.bodies).toContainEqual({ username: "carol" });
    // And the card asks for exactly one thing.
    expect(screen.getAllByLabelText("Current password")).toHaveLength(1);
  });

  it("keeps the username button disabled until the name actually differs", async () => {
    render();
    await screen.findByRole("heading", { name: "Account" });
    expect(screen.getByRole("button", { name: "Change username" })).toHaveProperty("disabled", true);

    await userEvent.type(screen.getByLabelText("Username"), "x");
    expect(screen.getByRole("button", { name: "Change username" })).toHaveProperty("disabled", false);
  });

  it("posts a password change and says the other devices are out", async () => {
    const fake = render();
    await screen.findByRole("heading", { name: "Account" });

    await userEvent.type(screen.getByLabelText("Current password"), FAKE_PASSWORD);
    await userEvent.type(screen.getByLabelText(/^New password/), "brandnewpassword");
    await userEvent.type(screen.getByLabelText("Repeat new password"), "brandnewpassword");
    await userEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(
      await screen.findByText("Password changed. Your other devices have been signed out."),
    ).toBeTruthy();
    expect(fake.calls).toContain("PANEL POST /api/account/password");
    expect(fake.bodies).toContainEqual({
      current_password: FAKE_PASSWORD,
      new_password: "brandnewpassword",
    });
  });

  // The server never sees the second field, so a typo there would otherwise set a password
  // the operator cannot reproduce.
  it("refuses to submit when the two new passwords differ", async () => {
    const fake = render();
    await screen.findByRole("heading", { name: "Account" });

    await userEvent.type(screen.getByLabelText("Current password"), FAKE_PASSWORD);
    await userEvent.type(screen.getByLabelText(/^New password/), "brandnewpassword");
    await userEvent.type(screen.getByLabelText("Repeat new password"), "brandnewpasswmrd");

    expect(screen.getByText("The two new passwords do not match.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Change password" })).toHaveProperty("disabled", true);
    expect(fake.calls).not.toContain("PANEL POST /api/account/password");
  });

  it("reports a wrong current password without clearing what was typed", async () => {
    render();
    await screen.findByRole("heading", { name: "Account" });

    await userEvent.type(screen.getByLabelText("Current password"), "notitnotit");
    await userEvent.type(screen.getByLabelText(/^New password/), "brandnewpassword");
    await userEvent.type(screen.getByLabelText("Repeat new password"), "brandnewpassword");
    await userEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByText("That is not your current password.")).toBeTruthy();
    // What was typed survives, so only the wrong field has to be fixed.
    expect(screen.getByLabelText(/^New password/)).toHaveProperty("value", "brandnewpassword");
  });

  it("clears the password fields after a success, so nothing is left sitting in the DOM", async () => {
    render();
    await screen.findByRole("heading", { name: "Account" });

    const current = screen.getByLabelText("Current password");
    await userEvent.type(current, FAKE_PASSWORD);
    await userEvent.type(screen.getByLabelText(/^New password/), "brandnewpassword");
    await userEvent.type(screen.getByLabelText("Repeat new password"), "brandnewpassword");
    await userEvent.click(screen.getByRole("button", { name: "Change password" }));

    await screen.findByText("Password changed. Your other devices have been signed out.");
    await waitFor(() => {
      expect(current).toHaveProperty("value", "");
      expect(screen.getByLabelText(/^New password/)).toHaveProperty("value", "");
    });
  });
});
