import { useMemo, useState } from "react";
import { Navigate, useParams } from "react-router";
import { Boundary } from "../components/Boundary";
import { ErrorState } from "../components/ErrorState";
import {
  Badge,
  Banner,
  Button,
  EmptyState,
  Input,
  PageHeader,
  Skeleton,
  StatTile,
  cx,
} from "../components/ui";
import { UserDrawer } from "../features/users/UserDrawer";
import { CreateUserDialog } from "../features/users/CreateUserDialog";
import {
  useReloadNode,
  useServerInfo,
  useServerStatus,
  useServers,
  useUsers,
} from "../queries/hooks";
import { useReloadProblem } from "../queries/reloadWatch";
import { formatBytes, formatRelative, hasV2RayAPI, quotaState, userState } from "../lib/format";
import type { Server, VlessUser } from "../api/types";

export function ServerPage() {
  const { serverId = "" } = useParams();
  const { data: servers } = useServers();
  const server = servers.find((s) => s.id === serverId);

  if (!server) return <Navigate to="/" replace />;

  return (
    <Boundary
      key={server.id}
      pending={<Skeleton className="h-64 w-full" />}
      resetKeys={[server.id]}
      fallback={({ error, retry, failure }) => (
        <ErrorState error={error} retry={retry} failure={failure} context={new URL(server.url).host} />
      )}
    >
      <ServerDetail server={server} />
    </Boundary>
  );
}

function ServerDetail({ server }: { server: Server }) {
  const { data: info } = useServerInfo(server);
  const { data: status } = useServerStatus(server);
  const { data: users } = useUsers(server);
  const reload = useReloadNode(server);
  const problem = useReloadProblem(server.id, status.sing_box.last_reload);

  const [filter, setFilter] = useState("");
  const [creating, setCreating] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);

  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const matched = q
      ? users.filter(
          (u) => u.name.toLowerCase().includes(q) || (u.note ?? "").toLowerCase().includes(q),
        )
      : users;
    // Anything needing attention first — that is the reason to open this page.
    return [...matched].sort((a, b) => {
      const rank = (u: VlessUser) => (userState(u).kind === "active" ? 1 : 0);
      return rank(a) - rank(b) || a.name.localeCompare(b.name);
    });
  }, [users, filter]);

  const v2ray = hasV2RayAPI(status.sing_box_version);
  const totalTraffic = users.reduce((sum, u) => sum + (u.usage?.total ?? 0), 0);

  return (
    <>
      <PageHeader
        title={info.host}
        subtitle={`:${info.port} · SNI ${info.sni} · ${info.flow || "no flow"}`}
        actions={<Button variant="primary" onClick={() => setCreating(true)}>Add user</Button>}
      />

      {/* Sticky, and it outlives the toast on purpose: an operator who disabled someone
          believes they are disconnected, and until this clears they are not. */}
      {problem ? (
        <Banner
          tone="warn"
          title="Saved, but this node did not reload"
          action={
            <Button onClick={() => reload.mutate()} disabled={reload.isPending}>
              {reload.isPending ? "Reloading…" : "Retry reload"}
            </Button>
          }
        >
          {problem.error}. The change is stored and will apply on the next successful
          reload — it is not live yet.
        </Banner>
      ) : null}

      {v2ray === false ? (
        <Banner tone="warn" title="No per-user traffic counters on this node">
          Its sing-box was built without <code>with_v2ray_api</code>, so usage stays at zero
          and quotas never fire. Rebuild the image with that tag.
        </Banner>
      ) : null}

      {status.sing_box.last_error ? (
        <Banner tone="danger" title="The last config generation failed">
          {status.sing_box.last_error}
        </Banner>
      ) : null}

      <div className="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatTile
          label="sing-box"
          value={status.sing_box.running ? "Running" : "Stopped"}
          hint={status.sing_box.started_at ? `since ${formatRelative(status.sing_box.started_at)}` : undefined}
        />
        <StatTile label="Users" value={`${status.active_users} / ${status.users}`} hint="active / total" />
        <StatTile label="Lifetime traffic" value={formatBytes(totalTraffic)} />
        <StatTile
          label="Last reload"
          value={status.sing_box.last_reload ? formatRelative(status.sing_box.last_reload) : "—"}
        />
      </div>

      <div className="mb-3 flex items-center justify-between gap-3">
        <Input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter by name or note"
          className="max-w-xs"
          type="search"
        />
        <span className="text-sm text-muted">
          {shown.length} of {users.length}
        </span>
      </div>

      {users.length === 0 ? (
        <EmptyState title="No users on this node">
          Add one to generate a VLESS link, a QR code and a subscription URL.
        </EmptyState>
      ) : (
        <UserTable users={shown} onSelect={setSelected} />
      )}

      <CreateUserDialog server={server} open={creating} onClose={() => setCreating(false)} />

      {selected ? (
        <UserDrawer
          server={server}
          user={users.find((u) => u.id === selected) ?? null}
          onClose={() => setSelected(null)}
        />
      ) : null}
    </>
  );
}

function UserTable({ users, onSelect }: { users: VlessUser[]; onSelect: (id: string) => void }) {
  return (
    <div className="overflow-x-auto rounded-[14px] border border-line">
      <table className="w-full min-w-[42rem] text-left text-sm">
        <thead className="bg-card text-xs uppercase tracking-wide text-muted">
          <tr>
            <th className="px-4 py-3 font-medium">Name</th>
            <th className="px-4 py-3 font-medium">Status</th>
            <th className="px-4 py-3 font-medium">Quota</th>
            <th className="px-4 py-3 text-right font-medium">Used</th>
            <th className="px-4 py-3 text-right font-medium">Lifetime</th>
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <UserRow key={u.id} user={u} onSelect={onSelect} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function UserRow({ user, onSelect }: { user: VlessUser; onSelect: (id: string) => void }) {
  const state = userState(user);
  const quota = quotaState(user);

  const tone =
    state.kind === "active" ? "ok" : state.kind === "expiring" ? "warn" : state.kind === "disabled" ? "muted" : "danger";

  return (
    <tr
      className="cursor-pointer border-t border-line hover:bg-card"
      onClick={() => onSelect(user.id)}
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onSelect(user.id);
        }
      }}
    >
      <td className="px-4 py-3">
        <div className="font-medium">{user.name}</div>
        {user.note ? <div className="text-xs text-muted">{user.note}</div> : null}
      </td>
      <td className="px-4 py-3">
        <Badge tone={tone}>{state.label}</Badge>
      </td>
      <td className="px-4 py-3">
        {quota.unlimited ? (
          <span className="text-muted">Unlimited</span>
        ) : (
          <div className="w-32">
            <QuotaMeter fraction={quota.fraction} />
            <div className="tnum mt-1 text-xs text-muted">
              {formatBytes(quota.remaining)} left
            </div>
          </div>
        )}
      </td>
      <td className="tnum px-4 py-3 text-right">{formatBytes(user.usage?.window_total ?? 0)}</td>
      <td className="tnum px-4 py-3 text-right text-muted">{formatBytes(user.usage?.total ?? 0)}</td>
    </tr>
  );
}

/** Free from data the 10-second poll already carries; no extra request per row. */
export function QuotaMeter({ fraction }: { fraction: number }) {
  const pct = Math.round(fraction * 100);
  return (
    <div
      className="h-1.5 w-full overflow-hidden rounded-full bg-line"
      role="meter"
      aria-valuenow={pct}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-label="Quota used"
    >
      <div
        className={cx(
          "h-full rounded-full",
          fraction >= 1 ? "bg-danger" : fraction > 0.85 ? "bg-warn" : "bg-accent",
        )}
        style={{ width: `${Math.max(2, pct)}%` }}
      />
    </div>
  );
}
