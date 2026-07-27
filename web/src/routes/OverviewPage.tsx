import { Link } from "react-router";
import { Boundary } from "../components/Boundary";
import { ErrorState } from "../components/ErrorState";
import { ServerTitle } from "../components/ServerTitle";
import { Badge, Card, EmptyState, PageHeader, Skeleton, StatTile } from "../components/ui";
import { useServerInfo, useServerStatus, useServers, useUsers } from "../queries/hooks";
import { formatBytes, hasV2RayAPI, userState } from "../lib/format";
import type { Server } from "../api/types";

export function OverviewPage() {
  const { data: servers } = useServers();

  if (servers.length === 0) {
    return (
      <>
        <PageHeader title="Servers" />
        <EmptyState title="No servers configured">
          <p>
            This panel manages the vlessvmore nodes named in its environment. Set{" "}
            <code className="rounded bg-line px-1">VLESSVMORE_SERVERS</code> and restart:
          </p>
          <pre className="mt-3 overflow-x-auto rounded-lg border border-line bg-bg p-3 text-left text-xs">
            VLESSVMORE_SERVERS="https://vpn.example.com|&lt;token&gt;"
          </pre>
          <p className="mt-3">
            Mint the token on the node itself with{" "}
            <code className="rounded bg-line px-1">vlessvmore token create panel --raw</code>.
          </p>
        </EmptyState>
      </>
    );
  }

  return (
    <>
      <PageHeader title="Servers" subtitle={`${servers.length} node${servers.length === 1 ? "" : "s"}`} />
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        {servers.map((server) => (
          // One boundary per card, not one for the page. A node that is down, or whose
          // token was rejected, renders its own failure and the other cards never notice.
          <Boundary
            key={server.id}
            pending={<CardSkeleton />}
            resetKeys={[server.id]}
            fallback={({ error, retry, failure }) => (
              <ErrorState
                error={error}
                retry={retry}
                failure={failure}
                context={new URL(server.url).host}
              />
            )}
          >
            <ServerCard server={server} />
          </Boundary>
        ))}
      </div>
    </>
  );
}

function CardSkeleton() {
  return (
    <Card>
      <Skeleton className="h-5 w-40" />
      <Skeleton className="mt-3 h-4 w-24" />
      <Skeleton className="mt-4 h-12 w-full" />
    </Card>
  );
}

function ServerCard({ server }: { server: Server }) {
  const { data: info } = useServerInfo(server);
  const { data: status } = useServerStatus(server);
  const { data: users } = useUsers(server);

  const v2ray = hasV2RayAPI(status.sing_box_version);
  const traffic = users.reduce((sum, u) => sum + (u.usage?.total ?? 0), 0);
  const problems = users.filter((u) => userState(u).kind !== "active").length;

  return (
    <Card>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <Link
            to={`/servers/${server.id}`}
            className="block min-w-0 font-semibold hover:underline"
          >
            <ServerTitle info={info} />
          </Link>
          <p className="mt-0.5 truncate text-xs text-muted">
            :{info.port} · SNI {info.sni}
          </p>
        </div>
        {status.sing_box.running ? (
          <Badge tone="ok">Running</Badge>
        ) : (
          <Badge tone="danger">Stopped</Badge>
        )}
      </div>

      {v2ray === false ? (
        <p className="mt-3 rounded-lg bg-warn/15 p-2 text-xs text-warn">
          This node's sing-box was built without <code>with_v2ray_api</code>. There are no
          per-user counters, so usage stays at zero and quotas never fire.
        </p>
      ) : null}

      {status.sing_box.last_error ? (
        <p className="mt-3 rounded-lg bg-danger/15 p-2 text-xs text-danger">
          Last config generation failed: {status.sing_box.last_error}
        </p>
      ) : null}

      <div className="mt-4 grid grid-cols-3 gap-2 text-sm">
        <div>
          <div className="text-xs text-muted">Users</div>
          <div className="tnum font-medium">
            {status.active_users}
            <span className="text-muted"> / {status.users}</span>
          </div>
        </div>
        <div>
          <div className="text-xs text-muted">Traffic</div>
          <div className="tnum font-medium">{formatBytes(traffic)}</div>
        </div>
        <div>
          <div className="text-xs text-muted">Needs attention</div>
          <div className="tnum font-medium">{problems === 0 ? "—" : problems}</div>
        </div>
      </div>
    </Card>
  );
}

/** The KPI strip above the grid, summed across every node that answered. */
export function OverviewTotals({ servers }: { servers: Server[] }) {
  return (
    <div className="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-4">
      <StatTile label="Servers" value={servers.length} />
    </div>
  );
}
