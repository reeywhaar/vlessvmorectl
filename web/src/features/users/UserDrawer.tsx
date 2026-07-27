import { Suspense, lazy, useMemo, useState } from "react";
import { Boundary } from "../../components/Boundary";
import { ErrorState } from "../../components/ErrorState";
import { QrMatrix } from "../../components/QrMatrix";
import {
  Badge,
  Button,
  ConfirmDelete,
  Dialog,
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
import { PRESETS, snapRange, type Preset } from "../../lib/range";
import { formatBytes, formatDateTime, quotaState, userState } from "../../lib/format";
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
        <DrawerBody server={server} user={user} onClose={onClose} />
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

      <Credentials server={server} user={user} onRotate={() => rotate.mutate(user.id)} rotating={rotate.isPending} />

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

function Credentials({
  server,
  user,
  onRotate,
  rotating,
}: {
  server: Server;
  user: VlessUser;
  onRotate: () => void;
  rotating: boolean;
}) {
  const { data: link } = useUserLink(server, user.id);

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
            <Button onClick={onRotate} disabled={rotating}>
              {rotating ? "Rotating…" : "Rotate"}
            </Button>
          </div>
        </div>
      </div>
    </section>
  );
}

function Usage({ server, user }: { server: Server; user: VlessUser }) {
  const [preset, setPreset] = useState<Preset>(PRESETS[0]!);
  const [asTable, setAsTable] = useState(false);

  // Snapped once per bucket rather than per render; see snapRange for why that matters
  // to both the cache and the proxy's coalescing.
  const range = useMemo(() => snapRange(preset), [preset]);
  const { data, isFetching } = useUserUsage(server, user.id, range);
  const points = useMemo(() => zeroFill(data.series, range), [data.series, range]);

  return (
    <section>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <h3 className="font-semibold">Traffic</h3>
        <div className="flex flex-wrap items-center gap-1">
          {PRESETS.map((p) => (
            <button
              key={p.label}
              onClick={() => setPreset(p)}
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

      {/* keepPreviousData holds the previous render during a refetch; dimming says so
          without a skeleton flash. */}
      <div className={cx(isFetching && "opacity-60 transition-opacity")}>
        <Suspense fallback={<Skeleton className="h-56 w-full" />}>
          {asTable ? (
            <UsageTable points={points} bucket={range.bucket} />
          ) : (
            <UsageChart points={points} bucket={range.bucket} />
          )}
        </Suspense>
      </div>

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
