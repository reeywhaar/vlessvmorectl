import { useState } from "react";
import { Button, Dialog, Field, Input } from "../../components/ui";
import { UserCreate } from "../../api/patch";
import { useCreateUser } from "../../queries/hooks";
import { parseBytes } from "../../lib/format";
import { isVlessError } from "../../api/errors";
import type { Server } from "../../api/types";

export function CreateUserDialog({
  server,
  open,
  onClose,
}: {
  server: Server;
  open: boolean;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [quota, setQuota] = useState("");
  const [expires, setExpires] = useState("");
  const [note, setNote] = useState("");
  const create = useCreateUser(server);

  const quotaBytes = parseBytes(quota);
  const quotaInvalid = quota.trim() !== "" && quotaBytes === null;

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (quotaBytes === null) return;

    create.mutate(
      new UserCreate({
        name: name.trim(),
        // 0 is the node's own spelling of "unlimited", so an empty field maps to it
        // rather than to an omitted key.
        quotaBytes,
        ...(expires ? { expiresAt: new Date(`${expires}T00:00:00Z`) } : {}),
        ...(note.trim() ? { note: note.trim() } : {}),
      }),
      {
        onSuccess: () => {
          setName("");
          setQuota("");
          setExpires("");
          setNote("");
          onClose();
        },
      },
    );
  }

  return (
    <Dialog open={open} onClose={onClose} title="Add user">
      <form onSubmit={submit} className="space-y-4">
        <Field label="Name" hint="Shown in the client, and usable in place of the id in URLs.">
          <Input value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
        </Field>

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

        {create.error ? (
          <p role="alert" className="text-sm text-danger">
            {isVlessError(create.error) ? create.error.message : String(create.error)}
          </p>
        ) : null}

        <div className="flex justify-end gap-2 pt-2">
          <Button type="button" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="submit"
            variant="primary"
            disabled={create.isPending || !name.trim() || quotaInvalid}
          >
            {create.isPending ? "Creating…" : "Create"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
