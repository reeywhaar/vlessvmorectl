/**
 * Wire types, mirroring vlessvmore's API.md and its Go structs.
 *
 * Field names are the JSON ones, snake_case, deliberately unmapped. A camelCase layer
 * would mean two names for every field and a translation to keep in step with a
 * project that lives in another repository.
 */

/** One of our own managed nodes. Note the absence of a token: the browser never sees one. */
export interface Server {
  id: string;
  url: string;
}

export interface Session {
  username: string;
  expires_at: string;
  /** Whether VLESSVMORE_PASSKEY_ORIGIN is set. Configuration, never a count. */
  passkeys_enabled: boolean;
}

// ---- panel-owned: passkeys ----

/** One enrolled authenticator, as the panel exposes it. No key material, by construction. */
export interface Passkey {
  id: string;
  label: string;
  algorithm: string;
  /**
   * Which keychain this lives in — "Apple Passwords", "Bitwarden" — the authenticator id it was
   * resolved from, and where to draw its logo from.
   *
   * All absent when the client withheld the id. `provider` alone is absent for an id the
   * community list does not name, and `logo` alone is absent for one it names but ships no image
   * for — most hardware keys, which get the panel's own key glyph. `logo_dark` appears only for
   * the providers that ship a second image for dark mode; otherwise `logo` suits both.
   */
  provider?: string;
  aaguid?: string;
  logo?: string;
  logo_dark?: string;
  /** In a keychain rather than tied to one device, from the authenticator's backup flag. */
  synced: boolean;
  created_at: string;
  last_used_at?: string;
}

/**
 * The WebAuthn option objects, as the server serialises them.
 *
 * Every buffer is base64url here and a BufferSource in the browser's own types, which is the
 * whole reason these are separate interfaces rather than the DOM ones. Only the fields that
 * need decoding are named; the rest ride through untouched, so a server-side library upgrade
 * that adds one needs no change here. See features/passkeys/webauthn.ts.
 */
export interface CredentialDescriptorJSON {
  id: string;
  type: string;
  transports?: string[];
}

export interface CredentialCreationOptionsJSON {
  challenge: string;
  user: { id: string; name: string; displayName: string };
  excludeCredentials?: CredentialDescriptorJSON[];
  [key: string]: unknown;
}

export interface CredentialRequestOptionsJSON {
  challenge: string;
  allowCredentials?: CredentialDescriptorJSON[];
  [key: string]: unknown;
}

// ---- panel-owned: subscribers ----

/**
 * A person who holds VPN accounts, possibly on several nodes.
 *
 * The panel's own concept — no vlessvmore node knows one exists. An entry is a pair of
 * ids resolved at read time, which is what lets an operator attach an account on a node
 * that is currently down, and what makes a dangling reference a render-time condition
 * rather than a corrupt record.
 */
export interface Subscriber {
  id: string;
  name: string;
  note?: string;

  /** The share capability, minted once at creation and never rotated. */
  token: string;
  /**
   * Relative, e.g. "/access/QK7M…", and joined against window.location.origin by
   * whatever displays it.
   *
   * The backend refuses to build an absolute URL because Host and X-Forwarded-Host are
   * both client-supplied; the browser is the only party that reliably knows this panel's
   * public origin.
   */
  access_path: string;

  /** With no rotation, this is the revocation switch. Reversible, and disconnects nobody. */
  disabled: boolean;
  entries: SubscriberEntry[];
  created_at: string;
  updated_at: string;
}

export interface SubscriberEntry {
  id: string;
  server_id: string;
  vless_user_id: string;
  /** The operator's own word for this account: "phone", "the laptop". */
  label?: string;
  added_at: string;
  /**
   * Whether server_id is still in VLESSVMORE_SERVERS. Computed by the backend on every
   * read, never stored — an orphan is what an operator gets after changing a node's URL,
   * since the id is derived from its origin.
   */
  server_configured: boolean;
}

/** POST …/entries returns the updated subscriber, plus a note when it could not verify. */
export interface AttachResult {
  subscriber: Subscriber;
  warning?: string;
}

// ---- vlessvmore ----

export interface VlessUser {
  id: string;
  name: string;
  uuid: string;
  enabled: boolean;
  /** 0 means unlimited. */
  quota_bytes: number;
  /** Absent means never. */
  expires_at?: string;
  usage_reset_at: string;
  /** Present only when enforcement turned the user off. */
  disabled_reason?: "quota" | "expired";
  sub_token?: string;
  note?: string;
  created_at: string;
  updated_at: string;
  usage?: UsageSummary;
  subscription_url?: string;
  install_url?: string;
}

export interface UsageSummary {
  up: number;
  down: number;
  /** Lifetime. */
  total: number;
  /** Since usage_reset_at — this is what the quota is measured against. */
  window_up: number;
  window_down: number;
  window_total: number;
  quota_bytes: number;
  /** 0 when unlimited, not "unlimited remaining". */
  quota_remaining: number;
}

export interface SingBoxStatus {
  running: boolean;
  pid?: number;
  started_at?: string;
  config_path: string;
  active_users: number;
  last_reload?: string;
  last_error?: string;
}

export interface ServerStatus {
  sing_box: SingBoxStatus;
  /**
   * The full `sing-box version` output. Worth reading rather than displaying: without
   * `with_v2ray_api` in its build tags there are no per-user counters at all, so usage
   * stays at zero and quotas never fire — silently.
   */
  sing_box_version?: string;
  users: number;
  active_users: number;
  tokens: number;
  data_dir: string;
}

export interface ServerInfo {
  /**
   * The operator's label for this node, from its config.json.
   *
   * Absent when unset, which is not the same as empty — vlessvmore tags it `omitempty`,
   * and a node with no name configured leaves clients showing the user's own name
   * instead. Use serverLabel() rather than reading this directly.
   */
  name?: string;
  host: string;
  port: number;
  sni: string;
  public_key: string;
  short_id: string;
  flow: string;
  fingerprint: string;
  handshake: string;
}

export interface QRMatrix {
  size: number;
  /** `size` strings of `size` characters, '0' light and '1' dark, top row first. */
  rows: string[];
  /** Modules of light margin to add around the matrix. Without it, scanners fail. */
  quiet_zone: number;
}

export interface UserLink {
  user_id: string;
  name: string;
  link: string;
  subscription_url?: string;
  install_url?: string;
  /** The `vless://` URI as a QR. ~250 characters, so a 69×69 code. */
  qr?: QRMatrix;
  /**
   * The subscription URL as a QR, and the one to show by default.
   *
   * Preferred over `qr` for two reasons. A subscription keeps working across config and
   * key changes because the client re-fetches it every 24 hours, and its
   * `Subscription-Userinfo` headers are what make a client display remaining traffic and
   * an expiry date in its own UI — vlessvmore's API.md calls that out as the reason to
   * prefer a subscription over a pasted link, and its own install page encodes this one.
   * It is also ~40 characters against ~250, so a 37×37 code instead of 69×69: markedly
   * easier for a phone to lock onto off a screen.
   */
  subscription_qr?: QRMatrix;
}

export interface UsagePoint {
  bucket: string;
  up: number;
  down: number;
}

export interface UsageSeries {
  user_id: string;
  name: string;
  from: string;
  to: string;
  bucket: "hour" | "day";
  /** Sparse: empty intervals are omitted, not zero-filled. */
  series: UsagePoint[];
  summary: UsageSummary;
}

export interface VlessToken {
  id: string;
  label: string;
  created_at: string;
  last_used_at?: string;
  revoked_at?: string;
}

/**
 * The envelope around every mutation that rewrites sing-box's config.
 *
 * The status is 2xx even when `reloaded` is false: the change is saved and will apply
 * on the next successful reload, but it is *not live*. Anything that unwraps this must
 * surface that, which is why useReloadAware exists rather than callers reaching for
 * `.result` themselves.
 *
 * Not every mutation is wrapped, despite what API.md implies — rotate-sub, reload and
 * token creation return bare bodies. The client's signatures encode which is which.
 */
export interface Reloaded<T> {
  result: T;
  reloaded: boolean;
  reload_error?: string;
}

export interface DeletedUser {
  deleted: string;
  name: string;
}
