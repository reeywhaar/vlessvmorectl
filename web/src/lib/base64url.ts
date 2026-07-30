/**
 * base64url, the encoding every WebAuthn buffer crosses the wire as.
 *
 * Small enough to hand-roll, and worth hand-rolling: the failure mode of getting this wrong
 * is silent. `JSON.stringify(new Uint8Array([1, 2]))` produces `{"0":1,"1":2}` rather than
 * throwing, so a buffer that never reached one of these functions arrives at the server as
 * an object, and the error it provokes points nowhere near the cause.
 */

export function toBase64Url(data: ArrayBuffer | Uint8Array): string {
  const bytes = data instanceof Uint8Array ? data : new Uint8Array(data);
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/**
 * The return type is pinned to `Uint8Array<ArrayBuffer>` rather than the default
 * `ArrayBufferLike`, because that is what `BufferSource` requires: a plain `Uint8Array` also
 * admits a SharedArrayBuffer backing, which the DOM's credential types reject.
 */
export function fromBase64Url(s: string): Uint8Array<ArrayBuffer> {
  const padded = s.replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}
