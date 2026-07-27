import { useEffect, useRef, useState, type ReactNode } from "react";

/** Six lines instead of clsx. */
export function cx(...parts: (string | false | null | undefined)[]): string {
  return parts.filter(Boolean).join(" ");
}

// ---- surfaces ----

export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <div
      className={cx(
        "rounded-[14px] border border-line bg-card p-5 shadow-sm",
        className,
      )}
    >
      {children}
    </div>
  );
}

export function PageHeader({ title, subtitle, actions }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <header className="mb-6 flex flex-wrap items-start justify-between gap-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {subtitle ? <p className="mt-1 text-sm text-muted">{subtitle}</p> : null}
      </div>
      {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
    </header>
  );
}

// ---- controls ----

type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";

const buttonStyles: Record<ButtonVariant, string> = {
  primary: "bg-accent text-accent-ink hover:opacity-90",
  secondary: "border border-line bg-card hover:border-muted",
  ghost: "hover:bg-line/60",
  danger: "bg-danger text-white hover:opacity-90",
};

export function Button({
  variant = "secondary",
  className,
  ...rest
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: ButtonVariant }) {
  return (
    <button
      {...rest}
      className={cx(
        "inline-flex items-center justify-center gap-2 rounded-lg px-3 py-1.5 text-sm font-medium",
        "transition-opacity disabled:cursor-not-allowed disabled:opacity-50",
        buttonStyles[variant],
        className,
      )}
    />
  );
}

export function Input({ className, ...rest }: React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...rest}
      className={cx(
        "w-full rounded-lg border border-line bg-bg px-3 py-2 text-sm text-ink",
        "placeholder:text-muted disabled:opacity-50",
        className,
      )}
    />
  );
}

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-sm font-medium">{label}</span>
      {children}
      {hint ? <span className="mt-1 block text-xs text-muted">{hint}</span> : null}
    </label>
  );
}

// ---- status ----

export type Tone = "ok" | "warn" | "danger" | "muted" | "accent";

const toneStyles: Record<Tone, string> = {
  ok: "bg-ok/15 text-ok",
  warn: "bg-warn/15 text-warn",
  danger: "bg-danger/15 text-danger",
  muted: "bg-line text-muted",
  accent: "bg-accent/15 text-accent",
};

/**
 * Colour is never the only signal — every badge carries its own words, and the dot is
 * decoration. An operator with a colour-vision deficiency, or one looking at a
 * screenshot in a ticket, reads the same thing.
 */
export function Badge({ tone = "muted", children }: { tone?: Tone; children: ReactNode }) {
  return (
    <span
      className={cx(
        "inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium",
        toneStyles[tone],
      )}
    >
      <span aria-hidden className="size-1.5 rounded-full bg-current" />
      {children}
    </span>
  );
}

export function StatTile({ label, value, hint }: { label: string; value: ReactNode; hint?: ReactNode }) {
  return (
    <div className="rounded-[14px] border border-line bg-card px-4 py-3">
      <div className="text-xs font-medium uppercase tracking-wide text-muted">{label}</div>
      <div className="mt-1 text-xl font-semibold">{value}</div>
      {hint ? <div className="mt-0.5 text-xs text-muted">{hint}</div> : null}
    </div>
  );
}

export function Banner({ tone = "warn", title, children, action }: { tone?: Tone; title: ReactNode; children?: ReactNode; action?: ReactNode }) {
  return (
    <div
      role="alert"
      className={cx("mb-4 rounded-[14px] border border-line p-4 text-sm", toneStyles[tone])}
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="font-semibold">{title}</div>
          {children ? <div className="mt-1 opacity-90">{children}</div> : null}
        </div>
        {action}
      </div>
    </div>
  );
}

/**
 * A quota bar. Free from data the caller already has; no extra request per row.
 *
 * Lives here rather than beside its first caller in routes/ServerPage.tsx because the
 * public share page draws one too, and that page must not import from routes/ — see the
 * note left behind in ServerPage.
 */
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

export function Skeleton({ className }: { className?: string }) {
  return <div className={cx("animate-pulse rounded-lg bg-line", className)} aria-hidden />;
}

export function EmptyState({ title, children }: { title: ReactNode; children?: ReactNode }) {
  return (
    <div className="rounded-[14px] border border-dashed border-line px-6 py-10 text-center">
      <p className="font-medium">{title}</p>
      {children ? <div className="mx-auto mt-2 max-w-md text-sm text-muted">{children}</div> : null}
    </div>
  );
}

// ---- copy ----

/**
 * Copy to the clipboard, or say so when the browser will not let us.
 *
 * The guard around navigator.clipboard is load-bearing, not defensive habit. That API is
 * undefined outside a secure context — plain HTTP on anything but localhost — and an
 * error thrown in a React event handler is *not* caught by an error boundary. Unguarded,
 * the whole failure presents as a button labelled "Copy" that does nothing, for ever,
 * with only a console message to explain it.
 *
 * In the panel that is an annoyance. On the public share page it lands on people reaching
 * a plain-HTTP deployment from whatever browser their phone came with, and copying the
 * link is the only thing they came to do — so the fallback selects the text instead and
 * tells them to copy it themselves.
 */
export function CopyButton({ value, label = "Copy" }: { value: string; label?: string }) {
  const [state, setState] = useState<"idle" | "copied" | "unavailable">("idle");

  useEffect(() => {
    if (state === "idle") return;
    const t = setTimeout(() => setState("idle"), 2500);
    return () => clearTimeout(t);
  }, [state]);

  const copy = () => {
    if (!navigator.clipboard?.writeText) {
      setState("unavailable");
      selectNearbyValue(value);
      return;
    }
    navigator.clipboard.writeText(value).then(
      () => setState("copied"),
      // A rejection here is a permissions policy or a document that was not focused.
      // Same remedy as having no API at all.
      () => {
        setState("unavailable");
        selectNearbyValue(value);
      },
    );
  };

  return (
    <Button
      variant="secondary"
      onClick={copy}
      // Announced rather than only shown, since the visual change is subtle.
      aria-live="polite"
    >
      {state === "copied" ? "Copied" : state === "unavailable" ? "Select it" : label}
    </Button>
  );
}

/**
 * Selects the on-screen text so the reader can copy it by hand.
 *
 * Finds the element by its content rather than by a ref, because CopyButton is used
 * standalone as well as inside SecretField and has no reliable handle on the node showing
 * the value. Best-effort by design: if it finds nothing, the button has still changed its
 * label to say the automatic path did not work.
 */
function selectNearbyValue(value: string) {
  if (typeof document === "undefined") return;
  for (const el of document.querySelectorAll("code, input")) {
    const text = el instanceof HTMLInputElement ? el.value : el.textContent;
    if (text !== value) continue;
    if (el instanceof HTMLInputElement) {
      el.select();
      return;
    }
    const range = document.createRange();
    range.selectNodeContents(el);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    return;
  }
}

/**
 * A credential the operator may need to read, hidden until they ask.
 *
 * Subscription and install URLs are capabilities: anyone holding one can connect as
 * that user. Not something to leave on screen during a screen-share.
 */
export function SecretField({ label, value }: { label: string; value: string }) {
  const [shown, setShown] = useState(false);
  return (
    <div>
      <div className="mb-1 flex items-center justify-between">
        <span className="text-sm font-medium">{label}</span>
        <button
          className="text-xs text-muted underline underline-offset-2 hover:text-ink"
          onClick={() => setShown((s) => !s)}
        >
          {shown ? "Hide" : "Reveal"}
        </button>
      </div>
      <div className="flex items-center gap-2">
        <code
          className={cx(
            "min-w-0 flex-1 truncate rounded-lg border border-line bg-bg px-3 py-2 text-xs",
            !shown && "select-none blur-[5px]",
          )}
        >
          {value}
        </code>
        <CopyButton value={value} />
      </div>
    </div>
  );
}

// ---- dialog ----

/**
 * Native <dialog> with showModal(): focus trap, inert background and ::backdrop for
 * free, which is why there is no Radix dependency here. Escape is the one part the
 * browser gets wrong for nested dialogs; see onKeyDown.
 */
export function Dialog({
  open,
  onClose,
  title,
  children,
  side = false,
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  children: ReactNode;
  /** Render as a right-hand sheet rather than a centred modal. */
  side?: boolean;
}) {
  const ref = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (open && !el.open) el.showModal();
    if (!open && el.open) el.close();
  }, [open]);

  return (
    <dialog
      ref={ref}
      // target === currentTarget means "this dialog's own event, not a nested one's".
      onClose={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      // Chromium closes *every* open dialog on one Escape rather than only the topmost,
      // so dismissing a confirm took the drawer behind it too. Handled at keydown, while
      // both are still open — by `cancel` the inner has already gone and there is nothing
      // left to tell them apart. Nested dialogs come later in document order, so the last
      // open one is the innermost.
      onKeyDown={(e) => {
        if (e.key !== "Escape") return;
        e.preventDefault();
        const open = document.querySelectorAll("dialog[open]");
        if (open[open.length - 1] === e.currentTarget) onClose();
      }}
      // A backdrop click lands on the dialog element itself, never on a child.
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      className={cx(
        "bg-card text-ink backdrop:bg-black/55",
        side
          ? "ml-auto mr-0 h-full max-h-none w-full max-w-2xl rounded-none border-l border-line"
          : "m-auto w-full max-w-lg rounded-[14px] border border-line",
      )}
    >
      <div className={cx("flex h-full flex-col", side && "max-h-screen")}>
        <div className="flex items-center justify-between gap-4 border-b border-line px-5 py-4">
          <h2 className="text-lg font-semibold">{title}</h2>
          <Button variant="ghost" onClick={onClose} aria-label="Close">
            ✕
          </Button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-5">{children}</div>
      </div>
    </dialog>
  );
}

/**
 * A plain are-you-sure. Lighter than ConfirmDelete on purpose: rotating a subscription
 * is recoverable by sending the new link, so it does not warrant a typing exercise on an
 * action people reach for mid-incident.
 */
export function Confirm({
  open,
  title,
  confirmLabel,
  variant = "primary",
  busy,
  onCancel,
  onConfirm,
  children,
}: {
  open: boolean;
  title: ReactNode;
  confirmLabel: string;
  variant?: ButtonVariant;
  busy?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
  children: ReactNode;
}) {
  return (
    <Dialog open={open} onClose={onCancel} title={title}>
      <div className="space-y-2 text-sm text-muted">{children}</div>
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={onCancel}>Cancel</Button>
        <Button variant={variant} disabled={busy} onClick={onConfirm}>
          {busy ? "Working…" : confirmLabel}
        </Button>
      </div>
    </Dialog>
  );
}

/**
 * Deleting a user takes their usage history with it and cannot be undone, so this asks
 * for the name to be typed rather than for a click on a button labelled "OK".
 */
export function ConfirmDelete({
  open,
  name,
  busy,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  name: string;
  busy?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const [typed, setTyped] = useState("");
  useEffect(() => {
    if (open) setTyped("");
  }, [open]);

  return (
    <Dialog open={open} onClose={onCancel} title={`Delete ${name}?`}>
      <p className="text-sm text-muted">
        This removes the user and their entire usage history from the node. It cannot be
        undone, and their client will stop connecting on the next reload.
      </p>
      <div className="mt-4">
        <Field label={`Type "${name}" to confirm`}>
          <Input value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
        </Field>
      </div>
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={onCancel}>Cancel</Button>
        <Button variant="danger" disabled={typed !== name || busy} onClick={onConfirm}>
          {busy ? "Deleting…" : "Delete"}
        </Button>
      </div>
    </Dialog>
  );
}
