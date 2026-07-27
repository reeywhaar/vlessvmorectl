/**
 * The share token, read out of the URL.
 *
 * A function rather than an inline expression so it can be tested without a browser, and
 * so the one place that decides what counts as a token is not spread across an entry
 * point.
 *
 * Returns "" for anything that is not /access/<something>, which the page renders as
 * "that link looks incomplete" — the state somebody lands in when a messaging app has
 * truncated the URL, which is common enough to deserve its own answer rather than an
 * error.
 */
export function tokenFromPath(pathname: string): string {
  // The separator in the pattern is load-bearing: without it "/accessories" matches the
  // prefix and yields "ories". The Go router would never send that path here, but a
  // function that is only correct because of what calls it is one refactor from being
  // wrong.
  const m = /^\/access(?:\/(.*))?$/.exec(pathname);
  if (!m) return "";
  // Only the first segment. A trailing slash or a stray suffix is not part of the token,
  // and passing one through would turn a recoverable copy-paste error into a 404.
  const first = (m[1] ?? "").split("/")[0] ?? "";
  return decodeURIComponent(first).trim();
}
