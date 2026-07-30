/**
 * base64url, the encoding every WebAuthn buffer crosses the wire as.
 *
 * Skipping one of these fails silently: `JSON.stringify` turns a Uint8Array into
 * `{"0":1,"1":2}` rather than throwing.
 */

export function toBase64Url(data: ArrayBuffer | Uint8Array): string {
  const bytes = data instanceof Uint8Array ? data : new Uint8Array(data);
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// Uint8Array<ArrayBuffer>, not the default ArrayBufferLike: BufferSource rejects a
// SharedArrayBuffer backing, so the narrower type is what the credential APIs accept.
export function fromBase64Url(s: string): Uint8Array<ArrayBuffer> {
  const padded = s.replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}
