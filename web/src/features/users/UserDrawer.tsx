import { Suspense, lazy, useMemo, useState, useTransition } from "react";
import { Boundary } from "../../components/Boundary";
import { ErrorState } from "../../components/ErrorState";
import { QrMatrix } from "../../components/QrMatrix";
import {
  Badge,
  Button,
  Confirm,
  ConfirmDelete,
  Dialog,
  Field,
  Input,
  SecretField,
  Skeleton,
  StatTile,
  cx,
} from "../../components/ui";
import {
  UserPatch,
  useDeleteUser,
  usePatchUser,
  useResetUsage,
  useRotateSubToken,
  useUserLink,
  useUserUsage,
} from "../../queries/hooks";
import { Tri } from "../../api/patch";
import { PRESETS, snapRange, type Preset, type SnappedRange } from "../../lib/range";
import { formatBytes, formatDateTime, parseBytes, quotaState, userState } from "../../lib/format";
import { zeroFill } from "../usage/series";
import type { Server, VlessUser } from "../../api/types";

// recharts is ~90 kB and only this drawer needs it, so login and the overview never pay
// for it.
const UsageChart = lazy(() =>
  import("../usage/UsageChart").then((m) => ({ default: m.UsageChart })),
);
const UsageTable = lazy(() =>
  import("../usage/UsageChart").then((m) => ({ default: m.UsageTable })),
);

export function UserDrawer({
  server,
  user,
  onClose,
}: {
  server: Server;
  user: VlessUser | null;
  onClose: () => void;
}) {
  if (!user) return null;
  return (
    <Dialog open side onClose={onClose} title={user.name}>
      <Boundary
        pending={<Skeleton className="h-72 w-full" />}
        resetKeys={[user.id]}
        fallback={({ error, retry, failure }) => (
          <ErrorState error={error} retry={retry} failure={failure} />
        )}
      >
        {/* Keyed so switching users resets the edit form, the QR toggle and the range
            picker. Without it those keep the previous user's state. */}
        <DrawerBody key={user.id} server={server} user={user} onClose={onClose} />
      </Boundary>
    </Dialog>
  );
}

function DrawerBody({
  server,
  user,
  onClose,
}: {
  server: Server;
  user: VlessUser;
  onClose: () => void;
}) {
  const state = userState(user);
  const quota = quotaState(user);

  const patch = usePatchUser(server);
  const remove = useDeleteUser(server);
  const reset = useResetUsage(server);
  const rotate = useRotateSubToken(server);

  const [confirming, setConfirming] = useState(false);

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={state.kind === "active" ? "ok" : state.kind === "expiring" ? "warn" : "danger"}>
          {state.label}
        </Badge>
        {user.note ? <span className="text-sm text-muted">{user.note}</span> : null}

        <div className="ml-auto flex flex-wrap gap-2">
          <Button
            onClick={() => patch.mutate({ id: user.id, patch: UserPatch.enabled(!user.enabled) })}
            disabled={patch.isPending}
          >
            {user.enabled ? "Disable" : "Enable"}
          </Button>
          {/* Only meaningful when a quota exists; resetting an unlimited user's window
              does nothing an operator would notice. */}
          {!quota.unlimited ? (
            <Button onClick={() => reset.mutate(user.id)} disabled={reset.isPending}>
              Reset usage
            </Button>
          ) : null}
          <Button variant="danger" onClick={() => setConfirming(true)}>
            Delete
          </Button>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatTile label="This window" value={formatBytes(user.usage?.window_total ?? 0)} />
        <StatTile label="Lifetime" value={formatBytes(user.usage?.total ?? 0)} />
        <StatTile
          label="Quota left"
          value={quota.unlimited ? "Unlimited" : formatBytes(quota.remaining)}
          hint={quota.unlimited ? undefined : `of ${formatBytes(quota.limit)}`}
        />
        <StatTile label="Expires" value={user.expires_at ? formatDateTime(user.expires_at) : "Never"} />
      </div>

      <Details server={server} user={user} />

      <Credentials
        server={server}
        user={user}
        onRotate={() => rotate.mutate(user.id)}
        rotating={rotate.isPending}
        rotated={rotate.isSuccess}
      />

      <Usage server={server} user={user} />

      <ExpirySection
        user={user}
        onChange={(tri) => patch.mutate({ id: user.id, patch: new UserPatch({ expiresAt: tri }) })}
        busy={patch.isPending}
      />

      <ConfirmDelete
        open={confirming}
        name={user.name}
        busy={remove.isPending}
        onCancel={() => setConfirming(false)}
        onConfirm={() =>
          remove.mutate(user.id, {
            onSuccess: () => {
              setConfirming(false);
              onClose();
            },
          })
        }
      />
    </div>
  );
}

/**
 * Name, note and quota, all editable after the fact.
 *
 * Renaming is safe: vlessvmore keys usage on the internal id and only ever tells sing-box
 * that id, so history survives — which is worth saying on screen, because renaming a VPN
 * user otherwise feels like the kind of thing that would lose their traffic totals.
 *
 * The UUID is deliberately not editable here. Changing it invalidates the user's existing
 * client configuration, which is a disconnect-everyone action and does not belong behind
 * the same Save button as fixing a typo in a note.
 */
function Details({ server, user }: { server: Server; user: VlessUser }) {
  const patch = usePatchUser(server);

  const initialQuota = user.quota_bytes > 0 ? formatBytes(user.quota_bytes, 0) : "";
  const [name, setName] = useState(user.name);
  const [note, setNote] = useState(user.note ?? "");
  const [quota, setQuota] = useState(initialQuota);

  const quotaBytes = parseBytes(quota);
  const quotaInvalid = quotaBytes === null;

  const trimmedName = name.trim();
  const trimmedNote = note.trim();
  const nameChanged = trimmedName !== user.name;
  const noteChanged = trimmedNote !== (user.note ?? "");
  const quotaChanged = !quotaInvalid && quotaBytes !== user.quota_bytes;
  const dirty = nameChanged || noteChanged || quotaChanged;

  function save() {
    // Only what actually moved. Sending the whole form every time would work, but an
    // unchanged `name` still counts as a change to the node, and every patch triggers a
    // reload — which drops every established connection on that server.
    patch.mutate({
      id: user.id,
      patch: new UserPatch({
        ...(nameChanged ? { name: trimmedName } : {}),
        ...(noteChanged ? { note: trimmedNote } : {}),
        ...(quotaChanged ? { quotaBytes: quotaBytes! } : {}),
      }),
    });
  }

  return (
    <section>
      <h3 className="mb-3 font-semibold">Details</h3>
      <div className="space-y-3">
        <Field
          label="Name"
          hint={
            nameChanged
              ? "Traffic history follows the user, not the name, so renaming keeps it."
              : undefined
          }
        >
          <Input value={name} onChange={(e) => setName(e.target.value)} />
        </Field>

        <Field label="Note" hint="Only you see this.">
          <Input
            value={note}
            onChange={(e) => setNote(e.target.value)}
            placeholder="phone, laptop, who it is for…"
          />
        </Field>

        <Field label="Quota" hint="e.g. 100GB. Empty for unlimited.">
          <Input
            value={quota}
            onChange={(e) => setQuota(e.target.value)}
            placeholder="Unlimited"
            aria-invalid={quotaInvalid}
          />
        </Field>

        {quotaInvalid ? (
          <p className="text-xs text-danger">
            Not a size. Write something like <code>100GB</code> or <code>500 MB</code>.
          </p>
        ) : null}

        {patch.error ? (
          <p role="alert" className="text-sm text-danger">
            {patch.error instanceof Error ? patch.error.message : String(patch.error)}
          </p>
        ) : null}

        <div className="flex items-center gap-2">
          <Button
            variant="primary"
            disabled={!dirty || quotaInvalid || !trimmedName || patch.isPending}
            onClick={save}
          >
            {patch.isPending ? "Saving…" : "Save"}
          </Button>
          {dirty ? (
            <Button
              onClick={() => {
                setName(user.name);
                setNote(user.note ?? "");
                setQuota(initialQuota);
              }}
            >
              Reset
            </Button>
          ) : null}
        </div>
      </div>
    </section>
  );
}

function Credentials({
  server,
  user,
  onRotate,
  rotating,
  rotated,
}: {
  server: Server;
  user: VlessUser;
  onRotate: () => void;
  rotating: boolean;
  rotated: boolean;
}) {
  const { data: link } = useUserLink(server, user.id);
  const [confirmRotate, setConfirmRotate] = useState(false);

  // The subscription is the better thing to scan: the client re-fetches it every 24
  // hours so config and key changes propagate, and its response headers are what make
  // the client show remaining traffic and an expiry date. The vless:// URI is the
  // fallback for clients that do not do subscriptions — and it is a much denser code,
  // ~250 characters against ~40.
  const codes = [
    link.subscription_qr && {
      key: "sub" as const,
      label: "Subscription",
      qr: link.subscription_qr,
      caption: "Auto-updates, and shows quota and expiry in the client. Scan this one.",
    },
    link.qr && {
      key: "vless" as const,
      label: "vless://",
      qr: link.qr,
      caption: "A one-off config. Does not update, and carries no quota information.",
    },
  ].filter((c) => c !== undefined);

  const [shown, setShown] = useState<"sub" | "vless">("sub");
  const active = codes.find((c) => c.key === shown) ?? codes[0];

  return (
    <section>
      <h3 className="mb-3 font-semibold">Connection</h3>
      <div className="flex flex-col gap-4 sm:flex-row">
        {active ? (
          <div className="shrink-0">
            <QrMatrix qr={active.qr} label={`${active.label} QR code for ${user.name}`} />
            {codes.length > 1 ? (
              <div className="mt-2 flex gap-1">
                {codes.map((c) => (
                  <button
                    key={c.key}
                    onClick={() => setShown(c.key)}
                    className={cx(
                      "rounded-lg px-2 py-1 text-xs",
                      c.key === active.key
                        ? "bg-accent text-accent-ink"
                        : "text-muted hover:bg-line",
                    )}
                  >
                    {c.label}
                  </button>
                ))}
              </div>
            ) : null}
            <p className="mt-2 max-w-[220px] text-xs text-muted">{active.caption}</p>
          </div>
        ) : null}

        <div className="min-w-0 flex-1 space-y-3">
          <SecretField label="vless:// link" value={link.link} />
          {link.subscription_url ? (
            <SecretField label="Subscription URL" value={link.subscription_url} />
          ) : null}
          {link.install_url ? (
            <SecretField label="Install page" value={link.install_url} />
          ) : null}

          <div className="flex items-center justify-between gap-3 rounded-lg border border-line p-3">
            <p className="text-xs text-muted">
              Rotating issues a new subscription URL and kills the old one immediately. The
              UUID is untouched, so nobody is disconnected.
            </p>
            <Button onClick={() => setConfirmRotate(true)} disabled={rotating}>
              {rotating ? "Rotating…" : "Rotate"}
            </Button>
          </div>

          {/*
            Stays up until the drawer closes, because the thing that goes wrong after a
            rotation is silent: the client keeps connecting on its stored config, so
            nobody notices that its subscription refresh has been 404ing until a config
            change fails to reach them weeks later.
          */}
          {rotated ? (
            <p className="rounded-lg bg-warn/15 p-3 text-xs text-warn">
              Rotated. Any link you sent before now returns 404 — send {user.name} the new
              subscription URL above.
            </p>
          ) : null}
        </div>
      </div>

      <Confirm
        open={confirmRotate}
        title={`Rotate ${user.name}'s subscription URL?`}
        confirmLabel="Rotate"
        busy={rotating}
        onCancel={() => setConfirmRotate(false)}
        onConfirm={() => {
          onRotate();
          setConfirmRotate(false);
        }}
      >
        <p>
          The current subscription and install links stop working the moment you do this,
          including any you have already sent. There is no way back to the old ones.
        </p>
        <p>
          Nobody is disconnected — the UUID does not change, so an already-configured
          client keeps connecting. But it will stop receiving config updates and stop
          showing quota and expiry until {user.name} has the new link.
        </p>
      </Confirm>
    </section>
  );
}

function Usage({ server, user }: { server: Server; user: VlessUser }) {
  const [preset, setPreset] = useState<Preset>(PRESETS[0]!);
  const [asTable, setAsTable] = useState(false);
  const [isPending, startTransition] = useTransition();

  // Snapped once per bucket rather than per render; see snapRange for why that matters
  // to both the cache and the proxy's coalescing.
  const range = useMemo(() => snapRange(preset), [preset]);

  return (
    <section>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <h3 className="font-semibold">Traffic</h3>
        <div className="flex flex-wrap items-center gap-1">
          {PRESETS.map((p) => (
            <button
              key={p.label}
              // In a transition, so React keeps the current chart on screen while the
              // new range loads instead of replacing it with a fallback. Without this,
              // every click on a range blanks the section and then repaints it.
              onClick={() => startTransition(() => setPreset(p))}
              className={cx(
                "rounded-lg px-2 py-1 text-xs",
                p.label === preset.label ? "bg-accent text-accent-ink" : "text-muted hover:bg-line",
              )}
            >
              {p.label}
            </button>
          ))}
          <button
            onClick={() => setAsTable((t) => !t)}
            className="ml-2 rounded-lg px-2 py-1 text-xs text-muted hover:bg-line"
          >
            {asTable ? "Chart" : "Table"}
          </button>
        </div>
      </div>

      {/*
        The query lives in UsageBody, below this Suspense, and that placement is the
        whole point: a suspending component hands control to its *nearest* boundary, so
        calling useUserUsage out here would suspend the drawer's boundary instead and
        blank the QR, the stat tiles and the actions along with the chart.
      */}
      <Suspense fallback={<Skeleton className="h-56 w-full" />}>
        <UsageBody
          server={server}
          user={user}
          range={range}
          asTable={asTable}
          pending={isPending}
        />
      </Suspense>

      <div className="mt-2 flex items-center gap-4 text-xs text-muted">
        <span className="flex items-center gap-1.5">
          <span className="size-2 rounded-sm bg-[var(--color-series-down)]" aria-hidden /> Down
        </span>
        <span className="flex items-center gap-1.5">
          <span className="size-2 rounded-sm bg-[var(--color-series-up)]" aria-hidden /> Up
        </span>
        <span className="ml-auto">
          The final bucket is still accumulating, and is shown faded.
        </span>
      </div>
    </section>
  );
}

function UsageBody({
  server,
  user,
  range,
  asTable,
  pending,
}: {
  server: Server;
  user: VlessUser;
  range: SnappedRange;
  asTable: boolean;
  pending: boolean;
}) {
  const { data, isFetching } = useUserUsage(server, user.id, range);
  const points = useMemo(() => zeroFill(data.series, range), [data.series, range]);

  // Dimmed while a new range is arriving or a poll is in flight — enough to say
  // "working" without throwing the previous answer away.
  return (
    <div className={cx((isFetching || pending) && "opacity-60 transition-opacity")}>
      {asTable ? (
        <UsageTable points={points} bucket={range.bucket} />
      ) : (
        <UsageChart points={points} bucket={range.bucket} />
      )}
    </div>
  );
}

function ExpirySection({
  user,
  onChange,
  busy,
}: {
  user: VlessUser;
  onChange: (tri: Tri<Date>) => void;
  busy: boolean;
}) {
  const current = user.expires_at ? user.expires_at.slice(0, 10) : "";
  const [value, setValue] = useState(current);

  return (
    <section>
      <h3 className="mb-3 font-semibold">Expiry</h3>
      <div className="flex flex-wrap items-end gap-2">
        <label className="flex-1">
          <span className="mb-1 block text-sm">Expires (UTC)</span>
          <input
            type="date"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            className="w-full rounded-lg border border-line bg-bg px-3 py-2 text-sm"
          />
        </label>
        {/* Tri.set and Tri.clear rather than a value-or-null: an explicit null clears the
            expiry, an absent key leaves it alone, and the two are one keystroke apart. */}
        <Button
          variant="primary"
          disabled={busy || !value || value === current}
          onClick={() => onChange(Tri.set(new Date(`${value}T00:00:00Z`)))}
        >
          Set
        </Button>
        <Button disabled={busy || !user.expires_at} onClick={() => onChange(Tri.clear())}>
          Never expire
        </Button>
      </div>
    </section>
  );
}
