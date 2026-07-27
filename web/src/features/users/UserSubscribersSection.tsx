import { useMemo, useState } from "react";
import { Boundary } from "../../components/Boundary";
import { Button, Skeleton, cx } from "../../components/ui";
import {
  useAttachEntry,
  useDetachEntry,
  useSubscribers,
} from "../../queries/hooks";
import type { Server, VlessUser } from "../../api/types";

/**
 * Who this account belongs to, edited from where the operator already is.
 *
 * The flow this exists for: an account is created *for somebody*, and without this the
 * operator has to close the drawer, go to Subscribers, find the person, open Attach and
 * find the account again — four navigations for the thing they were already doing.
 *
 * Wrapped in its own Boundary by the caller, deliberately. This is a panel-origin query
 * living inside a drawer whose every other query goes to a node; without a boundary of
 * its own, a blip on the panel's own store would take out the QR code and the rotate
 * button beneath it.
 */
export function UserSubscribersSection({ server, user }: { server: Server; user: VlessUser }) {
  return (
    <section>
      <h3 className="mb-3 font-semibold">Assigned to</h3>
      <Boundary
        pending={<Skeleton className="h-10 w-full" />}
        fallback={() => (
          <p className="text-xs text-muted">
            Couldn't load the subscriber list just now.
          </p>
        )}
      >
        <Body server={server} user={user} />
      </Boundary>
    </section>
  );
}

function Body({ server, user }: { server: Server; user: VlessUser }) {
  const { data: subscribers } = useSubscribers();
  const attach = useAttachEntry();
  const detach = useDetachEntry();
  const [picking, setPicking] = useState(false);

  // Which subscribers hold this exact (server, account) pair, and the entry id that
  // detaching would need.
  const holders = useMemo(
    () =>
      subscribers
        .map((s) => {
          const entry = s.entries.find(
            (e) => e.server_id === server.id && e.vless_user_id === user.id,
          );
          return entry ? { subscriber: s, entryId: entry.id } : null;
        })
        .filter((h) => h !== null),
    [subscribers, server.id, user.id],
  );

  const available = subscribers.filter(
    (s) => !holders.some((h) => h.subscriber.id === s.id),
  );

  return (
    <div className="space-y-2">
      {holders.length === 0 ? (
        <p className="text-xs text-muted">
          Nobody yet. Attaching puts this account on that person's share link.
        </p>
      ) : (
        <ul className="flex flex-wrap gap-2">
          {holders.map(({ subscriber, entryId }) => (
            <li key={subscriber.id}>
              <span
                className={cx(
                  "inline-flex items-center gap-1.5 rounded-full bg-line px-2 py-0.5 text-xs",
                  subscriber.disabled && "opacity-60",
                )}
              >
                {subscriber.name}
                {subscriber.disabled ? " (off)" : ""}
                <button
                  aria-label={`Detach from ${subscriber.name}`}
                  className="text-muted hover:text-ink"
                  disabled={detach.isPending}
                  onClick={() => detach.mutate({ id: subscriber.id, entryId })}
                >
                  ✕
                </button>
              </span>
            </li>
          ))}
        </ul>
      )}

      {picking ? (
        <div className="rounded-lg border border-line p-2">
          {available.length === 0 ? (
            <p className="text-xs text-muted">
              Every subscriber already has this account.
            </p>
          ) : (
            <ul className="max-h-40 space-y-1 overflow-y-auto">
              {available.map((s) => (
                <li key={s.id}>
                  <button
                    className="w-full rounded-lg px-2 py-1 text-left text-sm hover:bg-line"
                    disabled={attach.isPending}
                    onClick={() =>
                      attach.mutate(
                        {
                          id: s.id,
                          entry: { server_id: server.id, vless_user_id: user.id, label: "" },
                        },
                        { onSuccess: () => setPicking(false) },
                      )
                    }
                  >
                    {s.name}
                  </button>
                </li>
              ))}
            </ul>
          )}
          <Button className="mt-2" onClick={() => setPicking(false)}>
            Cancel
          </Button>
        </div>
      ) : (
        <Button onClick={() => setPicking(true)}>Assign to someone</Button>
      )}
    </div>
  );
}
