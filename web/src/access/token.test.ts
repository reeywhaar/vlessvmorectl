import { describe, expect, it } from "vitest";
import { tokenFromPath } from "./token";

describe("tokenFromPath", () => {
  it("reads the token out of a share link", () => {
    expect(tokenFromPath("/access/QK7M2XA9TESTTKEN0123456789ABCDEF")).toBe(
      "QK7M2XA9TESTTKEN0123456789ABCDEF",
    );
  });

  it("takes only the first segment", () => {
    // A stray suffix is not part of the token, and passing one through would turn a
    // recoverable copy-paste error into a 404.
    expect(tokenFromPath("/access/ABC/extra")).toBe("ABC");
    expect(tokenFromPath("/access/ABC/")).toBe("ABC");
  });

  it("returns empty for a truncated link", () => {
    // What a messaging app leaves behind when it cuts a long URL. The page answers this
    // with "that link looks incomplete" rather than making a doomed request.
    expect(tokenFromPath("/access")).toBe("");
    expect(tokenFromPath("/access/")).toBe("");
  });

  it("returns empty for anything that is not an access path", () => {
    expect(tokenFromPath("/")).toBe("");
    expect(tokenFromPath("/servers/abc123")).toBe("");
    // "/accessories" starts with "/access" as a string but is a different route.
    expect(tokenFromPath("/accessories")).toBe("");
  });

  it("decodes and trims", () => {
    expect(tokenFromPath("/access/%20ABC%20")).toBe("ABC");
  });
});
