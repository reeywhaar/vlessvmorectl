import { useMemo, useState } from "react";
import { Badge, Button, EmptyState, PageHeader, cx } from "../components/ui";
import { userState } from "../lib/format";
import { useServers, useSubscribers, useUsersByServer } from "../queries/hooks";
import { CreateSubscriberDialog } from "../features/subscribers/CreateSubscriberDialog";
import { SubscriberDrawer } from "../features/subscribers/SubscriberDrawer";
import { resolveEntries } from "../features/subscribers/resolve";
import type { Subscriber } from "../api/types";

/**
 * The people this panel hands VPN accounts to.
 *
 * The "needs attention" column is a client-side join over data the panel already has:
 * useUsersByServer shares its cache keys with the overview's ten-second poll, so arriving
 * here from the servers page costs no requests at all and the numbers are as fresh as
 * everything else on screen.
 */
export function SubscribersPage() {
  const { data: subscribers } = useSubscribers();
  const { data: servers } = useServers();
  const nodes = useUsersByServer(servers);

  const [creating, setCreating] = useState(false);
  const [openId, setOpenId] = useState<string | null>(null);

  const rows = useMemo(() => {
    const scored = subscribers.map((s) => ({
      subscriber: s,
      ...summarise(s, servers, nodes),
    }));
    // Anything needing attention first, then by name — the same ordering rule the server
    // page uses, so the two lists behave the same way under a refresh.
    return scored.sort((a, b) => {
      if (a.problems !== b.problems) return b.problems - a.problems;
      return a.subscriber.name.localeCompare(b.subscriber.name);
    });
  }, [subscribers, servers, nodes]);

  const open = subscribers.find((s) => s.id === openId) ?? null;

  return (
    <>
      <PageHeader
        title="Subscribers"
        subtitle={subscribers.length === 1 ? "1 person" : `${subscribers.length} people`}
        actions={
          <Button variant="primary" onClick={() => setCreating(true)}>
            Add subscriber
          </Button>
        }
      />

      {subscribers.length === 0 ? (
        // The empty state teaches the concept, because nothing else on screen does.
        <EmptyState title="No subscribers yet">
          A subscriber is a person. Attach the VPN accounts they hold — on any number of
          servers — and they get one link showing all of them, with QR codes and their
          remaining data.
        </EmptyState>
      ) : (
        <div className="overflow-x-auto rounded-[14px] border border-line bg-card">
          <table className="w-full text-sm">
            <thead className="border-b border-line text-left text-xs uppercase tracking-wide text-muted">
              <tr>
                <th className="px-4 py-3 font-medium">Name</th>
                <th className="px-4 py-3 font-medium">Connections</th>
                <th className="px-4 py-3 font-medium">Needs attention</th>
                <th className="px-4 py-3" />
              </tr>
            </thead>
            <tbody>
              {rows.map(({ subscriber, servers: serverCount, problems, pending }) => (
                <tr
                  key={subscriber.id}
                  tabIndex={0}
                  onClick={() => setOpenId(subscriber.id)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      setOpenId(subscriber.id);
                    }
                  }}
                  className={cx(
                    "cursor-pointer border-b border-line last:border-0",
                    "hover:bg-line/40 focus-visible:bg-line/40 focus-visible:outline-none",
                  )}
                >
                  <td className="px-4 py-3">
                    <span className="font-medium">{subscriber.name}</span>
                    {subscriber.disabled ? (
                      <span className="ml-2">
                        <Badge tone="muted">Switched off</Badge>
                      </span>
                    ) : null}
                    {subscriber.note ? (
                      <div className="text-xs text-muted">{subscriber.note}</div>
                    ) : null}
                  </td>
                  <td className="px-4 py-3 text-muted">
                    {subscriber.entries.length === 0
                      ? "None"
                      : `${subscriber.entries.length} on ${serverCount} ${
                          serverCount === 1 ? "server" : "servers"
                        }`}
                  </td>
                  <td className="px-4 py-3">
                    {/* A dash while the nodes are still answering, and a question mark
                        when one of them did not: "we don't know" and "nothing wrong" are
                        different answers and must not look the same. */}
                    {problems > 0 ? (
                      <Badge tone="danger">{problems}</Badge>
                    ) : pending ? (
                      <span className="text-muted">—</span>
                    ) : (
                      <span className="text-muted">None</span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-right text-muted">›</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <CreateSubscriberDialog open={creating} onClose={() => setCreating(false)} />
      {open ? (
        <SubscriberDrawer subscriber={open} servers={servers} onClose={() => setOpenId(null)} />
      ) : null}
    </>
  );
}

/** Counts what an operator would want to act on, without asking a node anything extra. */
function summarise(
  subscriber: Subscriber,
  servers: Parameters<typeof resolveEntries>[1],
  nodes: Parameters<typeof resolveEntries>[2],
) {
  const resolved = resolveEntries(subscriber.entries, servers, nodes);
  let problems = 0;
  let pending = false;

  for (const r of resolved) {
    if (r.nodeState === "unconfigured") problems++;
    else if (r.nodeState === "loading") pending = true;
    else if (r.nodeState === "failed") pending = true;
    else if (!r.user) problems++;
    else if (userState(r.user).kind !== "active") problems++;
  }

  return {
    problems,
    pending,
    servers: new Set(subscriber.entries.map((e) => e.server_id)).size,
  };
}
