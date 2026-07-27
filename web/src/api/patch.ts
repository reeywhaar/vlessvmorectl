/**
 * Bodies for vlessvmore's user endpoints.
 *
 * Two properties of that API make a plain object literal dangerous:
 *
 *  - It decodes with DisallowUnknownFields, so one unmapped key is a hard 400.
 *  - `expires_at` is three-valued on PATCH: absent leaves the expiry alone, a timestamp
 *    sets it, and an explicit `null` clears it.
 *
 * The second is the trap. In JavaScript `{ expires_at: undefined }` serialises to an
 * absent key, so the obvious way to write "clear the expiry" silently means "change
 * nothing" — and the user keeps expiring. Tri makes that unrepresentable rather than
 * merely documented, and `exactOptionalPropertyTypes` in tsconfig.json closes the last
 * hole by rejecting an explicit `undefined` too.
 */

export type TriOp = "keep" | "clear" | "set";

/**
 * A three-valued field.
 *
 * The constructor is private so the only way to build one is through the three named
 * factories, which means there is no way to express a fourth state or to forget which of
 * the three you meant.
 */
export class Tri<T> {
  private constructor(
    readonly op: TriOp,
    readonly value?: T,
  ) {}

  /** Leave the field alone. */
  static keep<T>(): Tri<T> {
    return new Tri<T>("keep");
  }

  /** Remove the field's value. Serialises to an explicit null. */
  static clear<T>(): Tri<T> {
    return new Tri<T>("clear");
  }

  static set<T>(value: T): Tri<T> {
    return new Tri<T>("set", value);
  }
}

/** Thrown rather than sent. See UserPatch.toBody. */
export class EmptyPatchError extends Error {
  override name = "EmptyPatchError";
}

/** The keys vlessvmore's PatchUserRequest and CreateUserRequest accept. */
const WRITABLE = ["name", "uuid", "enabled", "quota_bytes", "expires_at", "note"] as const;

/**
 * A cheap guard against a field being added to one of these classes without a mapping
 * in toBody. The failure it prevents is a 400 from the node whose message names a key
 * the author never knowingly wrote.
 */
function assertWritable(body: Record<string, unknown>): Record<string, unknown> {
  for (const k of Object.keys(body)) {
    if (!(WRITABLE as readonly string[]).includes(k)) {
      throw new Error(`vlessvmore rejects unknown fields; "${k}" is not one it accepts`);
    }
  }
  return body;
}

export interface UserPatchFields {
  name?: string;
  uuid?: string;
  enabled?: boolean;
  /** 0 means unlimited. */
  quotaBytes?: number;
  note?: string;
  /** `null` and `undefined` are both type errors here; use Tri.keep/clear/set. */
  expiresAt?: Tri<Date>;
}

export class UserPatch {
  constructor(readonly fields: UserPatchFields) {}

  /** Convenience for the most common single-field change. */
  static enabled(enabled: boolean): UserPatch {
    return new UserPatch({ enabled });
  }

  toBody(): Record<string, unknown> {
    const f = this.fields;
    const body: Record<string, unknown> = {};

    if (f.name !== undefined) body.name = f.name;
    if (f.uuid !== undefined) body.uuid = f.uuid;
    if (f.enabled !== undefined) body.enabled = f.enabled;
    if (f.quotaBytes !== undefined) body.quota_bytes = f.quotaBytes;
    if (f.note !== undefined) body.note = f.note;

    switch (f.expiresAt?.op ?? "keep") {
      case "keep":
        break; // key absent
      case "clear":
        body.expires_at = null; // explicit null
        break;
      case "set":
        body.expires_at = f.expiresAt?.value?.toISOString();
        break;
    }

    // An empty PATCH is a no-op in the node's store, but its reloadAfterChange still
    // runs — and a reload drops every established connection on that node. Never send
    // one.
    if (Object.keys(body).length === 0) throw new EmptyPatchError("nothing to change");

    return assertWritable(body);
  }
}

export interface UserCreateFields {
  name: string;
  uuid?: string;
  quotaBytes?: number;
  note?: string;
  enabled?: boolean;
  expiresAt?: Date;
}

export class UserCreate {
  constructor(readonly fields: UserCreateFields) {}

  toBody(): Record<string, unknown> {
    const f = this.fields;
    const body: Record<string, unknown> = { name: f.name };

    if (f.uuid) body.uuid = f.uuid;
    if (f.quotaBytes !== undefined) body.quota_bytes = f.quotaBytes;
    if (f.note) body.note = f.note;
    if (f.enabled !== undefined) body.enabled = f.enabled;
    if (f.expiresAt) body.expires_at = f.expiresAt.toISOString();

    return assertWritable(body);
  }
}
