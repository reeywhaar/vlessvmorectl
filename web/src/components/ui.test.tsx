import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { CopyButton, SecretField } from "./ui";

/**
 * fireEvent rather than userEvent throughout this file, deliberately.
 *
 * userEvent.setup() installs its own navigator.clipboard stub, which would overwrite the
 * one each test here is trying to install — and the whole point is to control what the
 * clipboard API does, including making it absent.
 */
const original = Object.getOwnPropertyDescriptor(navigator, "clipboard");

function setClipboard(value: unknown) {
  Object.defineProperty(navigator, "clipboard", {
    value,
    configurable: true,
    writable: true,
  });
}

afterEach(() => {
  if (original) Object.defineProperty(navigator, "clipboard", original);
  else setClipboard(undefined);
  vi.restoreAllMocks();
});

describe("CopyButton", () => {
  it("copies when the browser lets it", async () => {
    const writeText = vi.fn(() => Promise.resolve());
    setClipboard({ writeText });

    render(<CopyButton value="hunter2" />);
    fireEvent.click(screen.getByRole("button"));

    expect(writeText).toHaveBeenCalledWith("hunter2");
    expect(await screen.findByText("Copied")).toBeTruthy();
  });

  /**
   * navigator.clipboard is undefined outside a secure context, and an error thrown in a
   * React event handler is not caught by an error boundary. Unguarded, the whole failure
   * is a button labelled "Copy" that does nothing, for ever, with only a console message.
   *
   * On the share page that lands on somebody reaching a plain-HTTP deployment from
   * whatever browser their phone came with, and copying the link is the only thing they
   * came to do.
   */
  it("does not throw or silently do nothing when the clipboard API is absent", async () => {
    setClipboard(undefined);

    render(<CopyButton value="hunter2" />);
    fireEvent.click(screen.getByRole("button"));

    expect(await screen.findByText("Select it")).toBeTruthy();
  });

  it("recovers the same way when writeText rejects", async () => {
    setClipboard({ writeText: () => Promise.reject(new Error("not allowed")) });

    render(<CopyButton value="hunter2" />);
    fireEvent.click(screen.getByRole("button"));

    expect(await screen.findByText("Select it")).toBeTruthy();
  });
});

describe("SecretField", () => {
  it("blurs by default, for an operator's screen-share", () => {
    render(<SecretField label="Share link" value="https://panel/access/ABC" />);
    expect(screen.getByRole("button", { name: "Reveal" })).toBeTruthy();
  });

  it("shows the value outright when masking is off", () => {
    // The share page. Somebody looking at their own credential on their own screen is not
    // the threat model the blur exists for, and making them tap Reveal to copy their own
    // link is friction bought for nothing.
    render(<SecretField label="Subscription link" value="https://n/sub/ABC" masked={false} />);
    expect(screen.queryByRole("button", { name: "Reveal" })).toBeNull();
    expect(screen.getByText("https://n/sub/ABC")).toBeTruthy();
  });
});
