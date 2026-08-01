import { useEffect, useState } from "react";
import { Button, Dialog, Field, Input, cx } from "../../components/ui";
import { UserCreate } from "../../api/patch";
import { useCreateUserOnServers, useServerNames, useUsersByServer } from "../../queries/hooks";
import { parseBytes } from "../../lib/format";
import { isVlessError } from "../../api/errors";
import type { CreatedOnNode } from "../../queries/hooks";
import type { NodeUsers } from "../../queries/hooks";
import type { Server } from "../../api/types";

export function CreateUserDialog({
  servers,
  open,
  onClose,
}: {
  /** The nodes on offer. One means no choice to make, and the picker is not drawn. */
  servers: Server[];
  open: boolean;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [quota, setQuota] = useState("");
  const [expires, setExpires] = useState("");
  const [note, setNote] = useState("");
  // Nothing ticked to begin with: adding a user to every node is a choice, not a default worth
  // making on somebody's behalf. Select all is one click when it is what they wanted.
  const [picked, setPicked] = useState<string[]>([]);
  const [failures, setFailures] = useState<CreatedOnNode[]>([]);

  // Shares the query keys the pages behind this already poll, so watching for collisions
  // costs no extra requests.
  const nodes = useUsersByServer(servers);
  const names = useServerNames(servers);
  const create = useCreateUserOnServers(servers);

  const quotaBytes = parseBytes(quota);
  const quotaInvalid = quota.trim() !== "" && quotaBytes === null;

  const taken = (node: NodeUsers) =>
    (node.users ?? []).some((u) => u.name === name.trim()) && name.trim() !== "";

  // The user's intent minus what the nodes will not accept, so a name that collides removes a
  // node from the submission without forgetting that it had been ticked.
  const targets = nodes.filter((n) => picked.includes(n.server.id) && !taken(n));
  const allPicked = servers.length > 0 && servers.every((s) => picked.includes(s.id));

  // A fresh dialog, rather than the last attempt's name and errors.
  useEffect(() => {
    if (!open) return;
    setName("");
    setQuota("");
    setExpires("");
    setNote("");
    setPicked([]);
    setFailures([]);
    create.reset();
    // create.reset is stable, and servers comes from configuration.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (quotaBytes === null || targets.length === 0) return;

    create.mutate(
      {
        ids: targets.map((n) => n.server.id),
        input: new UserCreate({
          name: name.trim(),
          // 0 is the node's own spelling of "unlimited", so an empty field maps to it
          // rather than to an omitted key.
          quotaBytes,
          ...(expires ? { expiresAt: new Date(`${expires}T00:00:00Z`) } : {}),
          ...(note.trim() ? { note: note.trim() } : {}),
        }),
      },
      {
        onSuccess: (results) => {
          const failed = results.filter((r) => r.error !== null);
          setFailures(failed);
          if (failed.length === 0) {
            onClose();
            return;
          }
          // Only the nodes that refused stay ticked, so pressing Create again retries those
          // and cannot enrol the same user twice on a node that already took them.
          setPicked(failed.map((r) => r.server.id));
        },
      },
    );
  }

  return (
    <Dialog open={open} onClose={onClose} title="Add user">
      <form onSubmit={submit} className="space-y-4">
        <Field label="Name" hint="Shown in the client, and usable in place of the id in URLs.">
          <Input value={name} onChange={(e) => setName(e.target.value)} required data-autofocus />
        </Field>

        {servers.length > 1 ? (
          // role=group rather than fieldset: a legend has to be the fieldset's first child, and
          // the Select all control belongs on that same line.
          <div role="group" aria-labelledby="create-user-servers">
            <div className="mb-1 flex items-center justify-between gap-2">
              <span id="create-user-servers" className="text-sm font-medium">
                Servers
              </span>
              <button
                type="button"
                className="text-xs text-muted underline hover:text-ink"
                onClick={() => setPicked(allPicked ? [] : servers.map((s) => s.id))}
              >
                {allPicked ? "Clear" : "Select all"}
              </button>
            </div>
            <ul className="space-y-1 rounded-lg border border-line p-1">
              {nodes.map((node) => (
                <NodeChoice
                  key={node.server.id}
                  node={node}
                  name={names[node.server.id] ?? host(node.server)}
                  checked={picked.includes(node.server.id) && !taken(node)}
                  taken={taken(node)}
                  onToggle={() =>
                    setPicked((ids) =>
                      ids.includes(node.server.id)
                        ? ids.filter((id) => id !== node.server.id)
                        : [...ids, node.server.id],
                    )
                  }
                />
              ))}
            </ul>
            {/* Only once they have picked something. Nothing picked is the starting state, not a
                mistake to point at. */}
            {picked.length > 0 && targets.length === 0 ? (
              <p className="mt-1 text-xs text-muted">
                Every node you picked already has this name.
              </p>
            ) : null}
          </div>
        ) : null}

        <Field label="Quota" hint="e.g. 100GB. Leave empty for unlimited.">
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

        <Field label="Expires" hint="UTC. Leave empty for never.">
          <Input type="date" value={expires} onChange={(e) => setExpires(e.target.value)} />
        </Field>

        <Field label="Note" hint="Only you see this.">
          <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder="phone" />
        </Field>

        {/* Per node, because a fan-out fails per node: "one of three refused" is the answer,
            and which one it was is the actionable part. */}
        {failures.length > 0 ? (
          <div role="alert" className="space-y-1 text-sm text-danger">
            {failures.map((f) => (
              <p key={f.server.id}>
                {host(f.server)}: {describe(f.error)}
              </p>
            ))}
          </div>
        ) : null}

        {create.error ? (
          <p role="alert" className="text-sm text-danger">
            {describe(create.error)}
          </p>
        ) : null}

        <div className="flex justify-end gap-2 pt-2">
          <Button type="button" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="submit"
            variant="primary"
            disabled={
              create.isPending || !name.trim() || quotaInvalid || targets.length === 0
            }
          >
            {create.isPending ? "Creating…" : "Create"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

function NodeChoice({
  node,
  name,
  checked,
  taken,
  onToggle,
}: {
  node: NodeUsers;
  name: string;
  checked: boolean;
  taken: boolean;
  onToggle: () => void;
}) {
  return (
    <li>
      {/* A real checkbox in a label, as in AttachEntryDialog: space toggles it without
          submitting the form the way a bare button inside one would. */}
      <label
        className={cx(
          "flex cursor-pointer items-center gap-2 rounded-lg px-2 py-1.5 text-sm",
          taken && "cursor-not-allowed opacity-50",
          // No fill for a ticked row, unlike AttachEntryDialog: there picking is the exception,
          // here every node starts ticked, so a fill on each marks nothing the box does not.
          !taken && "hover:bg-line",
        )}
      >
        <input
          type="checkbox"
          className="size-4 shrink-0 accent-[var(--color-accent,currentColor)]"
          checked={checked}
          disabled={taken}
          onChange={onToggle}
        />
        <span className="min-w-0 flex-1 truncate">{name}</span>
        {taken ? (
          <span className="shrink-0 text-xs">already has this name</span>
        ) : node.failure ? (
          <span className="shrink-0 text-xs text-warn">did not answer</span>
        ) : null}
      </label>
    </li>
  );
}

function host(server: Server): string {
  return new URL(server.url).host;
}

function describe(error: unknown): string {
  if (isVlessError(error)) return error.message;
  return error instanceof Error ? error.message : String(error);
}
