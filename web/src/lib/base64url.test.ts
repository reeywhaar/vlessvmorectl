import { describe, expect, it } from "vitest";
import { fromBase64Url, toBase64Url } from "./base64url";

describe("base64url", () => {
  it("round-trips arbitrary bytes", () => {
    const bytes = new Uint8Array(256);
    for (let i = 0; i < 256; i++) bytes[i] = i;
    expect(Array.from(fromBase64Url(toBase64Url(bytes)))).toEqual(Array.from(bytes));
  });

  it("round-trips every length up to 8, so the padding cases are all covered", () => {
    for (let n = 0; n <= 8; n++) {
      const bytes = new Uint8Array(n).fill(0xfb);
      expect(Array.from(fromBase64Url(toBase64Url(bytes)))).toEqual(Array.from(bytes));
    }
  });

  it("emits no padding and no + or /", () => {
    // 0xfb 0xff picks the two characters that differ between base64 and base64url.
    const encoded = toBase64Url(new Uint8Array([0xfb, 0xff, 0xfe, 0xfd, 0xfc]));
    expect(encoded).not.toMatch(/[+/=]/);
    expect(Array.from(fromBase64Url(encoded))).toEqual([0xfb, 0xff, 0xfe, 0xfd, 0xfc]);
  });

  it("accepts an ArrayBuffer as well as a view", () => {
    const bytes = new Uint8Array([1, 2, 3]);
    expect(toBase64Url(bytes.buffer)).toBe(toBase64Url(bytes));
  });

  it("handles the empty string", () => {
    expect(toBase64Url(new Uint8Array())).toBe("");
    expect(Array.from(fromBase64Url(""))).toEqual([]);
  });
});
