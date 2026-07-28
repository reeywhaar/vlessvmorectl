import { useEffect, useState } from "react";
import { Link } from "react-router";
import { Boundary } from "../../components/Boundary";
import { ErrorState } from "../../components/ErrorState";
import {
  Badge,
  Button,
  ButtonLink,
  Confirm,
  Dialog,
  Field,
  Input,
  SecretField,
  Skeleton,
  cx,
} from "../../components/ui";
import { serverLabel, userState } from "../../lib/format";
import {
  useDeleteSubscriber,
  useDetachEntry,
  usePatchSubscriber,
  useServerInfo,
  useUsersByServer,
} from "../../queries/hooks";
import type { Server, Subscriber } from "../../api/types";
import { AttachEntryDialog } from "./AttachEntryDialog";
import { resolveEntries, type ResolvedEntry } from "./resolve";

/**
 * Where a subscriber is edited.
 *
 * The share link is minted once and never rotates, so there is no Rotate button here.
 * Revoking a leaked link means the Disabled toggle — instant, reversible, and it keeps
 * the attachments — or deleting the record. That makes the toggle load-bearing rather
 * than a nicety, which is why it sits at the top rather than in a settings corner.
 */
export function SubscriberDrawer({
  subscriber,
  servers,
  onClose,
}: {
  subscriber: Subscriber;
  servers: Server[];
  onClose: () => void;
}) {
  return (
    <Dialog open side onClose={onClose} title={subscriber.name}>
      <Boundary
        resetKeys={[subscriber.id]}
        pending={<Skeleton className="h-64 w-full" />}
        fallback={({ error, retry, failure }) => (
          <ErrorState error={error} retry={retry} failure={failure} />
        )}
      >
        {/* key so switching subscribers resets the rename form and every open confirm,
            rather than carrying one person's half-typed edit onto another's record. */}
        <DrawerBody
          key={subscriber.id}
          subscriber={subscriber}
          servers={servers}
          onClose={onClose}
        />
      </Boundary>
    </Dialog>
  );
}

function DrawerBody({
  subscriber,
  servers,
  onClose,
}: {
  subscriber: Subscriber;
  servers: Server[];
  onClose: () => void;
}) {
  const patch = usePatchSubscriber();
  const remove = useDeleteSubscriber();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [attaching, setAttaching] = useState(false);

  return (
    <div className="space-y-6">
      <ShareLink subscriber={subscriber} />

      <section>
        <div className="flex items-center justify-between gap-3 rounded-lg border border-line p-3">
          <div>
            <p className="text-sm font-medium">
              {subscriber.disabled ? "Link is switched off" : "Link is live"}
            </p>
            <p className="text-xs text-muted">
              {subscriber.disabled
                ? "The page returns “not valid”. Nobody has been disconnected — this only affects the page."
                : "Switching it off stops the page working without touching any account."}
            </p>
          </div>
          <Button
            variant={subscriber.disabled ? "primary" : "secondary"}
            disabled={patch.isPending}
            onClick={() =>
              patch.mutate({ id: subscriber.id, patch: { disabled: !subscriber.disabled } })
            }
          >
            {subscriber.disabled ? "Switch on" : "Switch off"}
          </Button>
        </div>
      </section>

      <Details subscriber={subscriber} />

      <Connections
        subscriber={subscriber}
        servers={servers}
        onAttach={() => setAttaching(true)}
      />

      <section className="border-t border-line pt-4">
        <div className="flex items-center justify-between gap-3">
          <p className="text-xs text-muted">
            Deleting removes the link and the list of accounts. The VPN accounts
            themselves are untouched.
          </p>
          <Button variant="danger" onClick={() => setConfirmDelete(true)}>
            Delete
          </Button>
        </div>
      </section>

      {/*
        Confirm, not ConfirmDelete. ConfirmDelete's body is hardcoded to "removes the user
        and their entire usage history from the node", which is flatly wrong here —
        deleting a subscriber destroys nothing on any node — and the typing exercise it
        demands is reserved for things that are genuinely unrecoverable.
      */}
      <Confirm
        open={confirmDelete}
        title={`Delete ${subscriber.name}?`}
        confirmLabel="Delete"
        variant="danger"
        busy={remove.isPending}
        onCancel={() => setConfirmDelete(false)}
        onConfirm={() =>
          remove.mutate(subscriber.id, {
            onSuccess: () => {
              setConfirmDelete(false);
              onClose();
            },
          })
        }
      >
        <p>
          Their share link stops working immediately, and it cannot be brought back — a
          new subscriber gets a new link, and the accounts have to be attached again.
        </p>
        <p>Nobody is disconnected. Every VPN account stays exactly as it is.</p>
      </Confirm>

      {/*
        A child of the drawer's tree, not a sibling elsewhere. Dialog resolves "innermost"
        by document order when Escape is pressed, so a nested dialog rendered outside this
        subtree would take the drawer down with it.
      */}
      <AttachEntryDialog
        subscriber={subscriber}
        servers={servers}
        open={attaching}
        onClose={() => setAttaching(false)}
      />
    </div>
  );
}

function ShareLink({ subscriber }: { subscriber: Subscriber }) {
  // The backend sends a relative path on purpose: Host and X-Forwarded-Host are both
  // client-supplied, so a server-built absolute URL is a link to whatever host the caller
  // claimed. The browser is the one party that knows this panel's real origin.
  const url = new URL(subscriber.access_path, window.location.origin).toString();

  return (
    <section>
      <SecretField label="Share link" value={url} />
      <div className="mt-2 flex items-center justify-between gap-3">
        <p className="text-xs text-muted">
          Anyone holding this link sees every account attached below. It is minted once
          and never changes.
        </p>
        <ButtonLink href={url} target="_blank" rel="noopener noreferrer">
          Preview
        </ButtonLink>
      </div>
    </section>
  );
}

function Details({ subscriber }: { subscriber: Subscriber }) {
  const patch = usePatchSubscriber();
  const [name, setName] = useState(subscriber.name);
  const [note, setNote] = useState(subscriber.note ?? "");

  useEffect(() => {
    setName(subscriber.name);
    setNote(subscriber.note ?? "");
  }, [subscriber.name, subscriber.note]);

  const dirty = name !== subscriber.name || note !== (subscriber.note ?? "");

  return (
    <section className="space-y-3">
      <h3 className="font-semibold">Details</h3>
      <Field label="Name" hint="Shown to them at the top of their page.">
        <Input value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label="Note" hint="Yours only. Never appears on their page.">
        <Input value={note} onChange={(e) => setNote(e.target.value)} />
      </Field>
      {dirty ? (
        <div className="flex gap-2">
          <Button
            variant="primary"
            disabled={patch.isPending || name.trim() === ""}
            onClick={() =>
              patch.mutate({ id: subscriber.id, patch: { name: name.trim(), note } })
            }
          >
            {patch.isPending ? "Saving…" : "Save"}
          </Button>
          <Button
            onClick={() => {
              setName(subscriber.name);
              setNote(subscriber.note ?? "");
            }}
          >
            Reset
          </Button>
        </div>
      ) : null}
    </section>
  );
}

function Connections({
  subscriber,
  servers,
  onAttach,
}: {
  subscriber: Subscriber;
  servers: Server[];
  onAttach: () => void;
}) {
  const nodes = useUsersByServer(servers);
  const resolved = resolveEntries(subscriber.entries, servers, nodes);

  return (
    <section>
      <div className="mb-3 flex items-center justify-between">
        <h3 className="font-semibold">Connections</h3>
        <Button onClick={onAttach}>Attach an account</Button>
      </div>

      {resolved.length === 0 ? (
        <p className="rounded-lg border border-dashed border-line p-4 text-sm text-muted">
          Nothing attached yet, so their link shows an empty page. Attach an account to
          give them something to connect with.
        </p>
      ) : (
        <ul className="space-y-2">
          {resolved.map((r) => (
            <EntryRow key={r.entry.id} subscriberId={subscriber.id} resolved={r} />
          ))}
        </ul>
      )}
    </section>
  );
}

function EntryRow({ subscriberId, resolved }: { subscriberId: string; resolved: ResolvedEntry }) {
  const detach = useDetachEntry();
  const { entry, server, user, nodeState } = resolved;

  return (
    <li className="rounded-lg border border-line p-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">
            {/*
              Straight to the account's own drawer, which is where every question this row
              raises gets answered — the quota, the traffic history, the link to resend.
              Only when the server is still configured: an orphaned entry has nowhere to go,
              and a link that navigates to a redirect is worse than plain text.
            */}
            {server ? (
              <Link
                to={`/servers/${server.id}?user=${encodeURIComponent(entry.vless_user_id)}`}
                className="underline decoration-line underline-offset-4 hover:decoration-ink"
              >
                <NodeName server={server} />
              </Link>
            ) : (
              "Unknown server"
            )}
          </p>
          <p className="truncate text-xs text-muted">
            {user ? user.name : entry.vless_user_id}
            {entry.label ? ` · ${entry.label}` : ""}
          </p>
        </div>
        <div className="flex items-center gap-2">
          {user ? <Badge tone={badgeTone(user)}>{userState(user).label}</Badge> : null}
          {/*
            No confirm. The consequence is stated inline instead, and it is one click from
            being undone — the typing exercise is reserved for irreversible node-side
            destruction.
          */}
          <Button
            disabled={detach.isPending}
            onClick={() => detach.mutate({ id: subscriberId, entryId: entry.id })}
          >
            Detach
          </Button>
        </div>
      </div>

      <Problem nodeState={nodeState} missing={nodeState === "ok" && !user} />
    </li>
  );
}

function Problem({ nodeState, missing }: { nodeState: ResolvedEntry["nodeState"]; missing: boolean }) {
  const strip = (tone: "warn" | "danger", text: string) => (
    <p
      className={cx(
        "mt-2 rounded-lg p-2 text-xs",
        tone === "warn" ? "bg-warn/15 text-warn" : "bg-danger/15 text-danger",
      )}
    >
      {text}
    </p>
  );

  if (nodeState === "unconfigured") {
    return strip(
      "danger",
      "This server is no longer in this panel's configuration, so it shows as unavailable " +
        "on their page. Its id is derived from its URL, so changing that URL orphans the entry — " +
        "detach and re-attach to fix it.",
    );
  }
  if (nodeState === "failed") {
    return strip("warn", "Couldn't reach this server, so this row can't show its status.");
  }
  if (missing) {
    return strip("danger", "This account no longer exists on that server.");
  }
  return null;
}

function NodeName({ server }: { server: Server }) {
  const info = useServerInfo(server);
  return <>{serverLabel(info.data)}</>;
}

function badgeTone(user: Parameters<typeof userState>[0]) {
  const state = userState(user);
  if (state.kind === "active") return "ok" as const;
  if (state.kind === "expiring") return "warn" as const;
  if (state.kind === "disabled") return "muted" as const;
  return "danger" as const;
}
