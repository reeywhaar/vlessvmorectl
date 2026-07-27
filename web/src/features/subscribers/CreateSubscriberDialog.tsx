import { useState } from "react";
import { Button, Dialog, Field, Input } from "../../components/ui";
import { useCreateSubscriber } from "../../queries/hooks";

export function CreateSubscriberDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [note, setNote] = useState("");
  const create = useCreateSubscriber();

  function submit(e: React.FormEvent) {
    e.preventDefault();
    create.mutate(
      { name: name.trim(), note: note.trim() },
      {
        onSuccess: () => {
          setName("");
          setNote("");
          onClose();
        },
      },
    );
  }

  return (
    <Dialog open={open} onClose={onClose} title="Add subscriber">
      <form onSubmit={submit} className="space-y-4">
        <Field label="Name" hint="A person, not an account. Shown to them at the top of their page.">
          <Input value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
        </Field>

        <Field label="Note" hint="Optional, and yours only — “paid to August”. Never appears on their page.">
          <Input value={note} onChange={(e) => setNote(e.target.value)} />
        </Field>

        <p className="text-xs text-muted">
          They get a share link straight away. It is minted once and never changes, so
          attach their accounts next and send it when you are ready.
        </p>

        <div className="flex justify-end gap-2">
          <Button type="button" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={create.isPending || name.trim() === ""}>
            {create.isPending ? "Adding…" : "Add"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
