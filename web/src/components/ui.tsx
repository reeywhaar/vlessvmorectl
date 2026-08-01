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
 * navigator.clipboard is undefined outside a secure context, and an error thrown in a
 * React event handler is not caught by an error boundary — unguarded, the failure is a
 * button that does nothing for ever with only a console message. The fallback selects the
 * text instead.
 */
export function CopyButton({
  value,
  label = "Copy",
  icon = false,
}: {
  value: string;
  label?: string;
  /**
   * Render as a square glyph rather than a labelled button.
   *
   * For rows that carry a credential and need two actions side by side, where two text
   * buttons would be wider than the value they act on. The label becomes the accessible
   * name and the tooltip, so nothing is lost to a screen reader.
   */
  icon?: boolean;
}) {
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

  const text = state === "copied" ? "Copied" : state === "unavailable" ? "Select it" : label;

  if (icon) {
    return (
      <IconButton onClick={copy} label={text} active={state === "copied"}>
        {state === "copied" ? <CheckIcon /> : <CopyIcon />}
      </IconButton>
    );
  }

  return (
    <Button
      variant="secondary"
      onClick={copy}
      // Announced rather than only shown, since the visual change is subtle.
      aria-live="polite"
    >
      {text}
    </Button>
  );
}

/**
 * A square glyph button.
 *
 * `label` is the accessible name and the tooltip rather than visible text, so these are
 * only ever right where the glyph is unambiguous and the context supplies the noun — the
 * copy and QR actions sitting against the value they act on.
 */
export function IconButton({
  label,
  onClick,
  active,
  children,
}: {
  label: string;
  onClick: () => void;
  active?: boolean;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      title={label}
      // Announced rather than only shown: the glyph swap on success is subtle.
      aria-live="polite"
      className={cx(
        "inline-flex size-9 shrink-0 items-center justify-center rounded-lg",
        "transition-colors hover:bg-line",
        active ? "text-ok" : "text-muted hover:text-ink",
      )}
    >
      {children}
    </button>
  );
}

/**
 * Inline SVG rather than an icon package.
 *
 * Three glyphs do not justify a dependency, and these ship in a bundle a stranger on
 * mobile data downloads. `currentColor` throughout so they inherit the button's state.
 */
const iconProps = {
  viewBox: "0 0 24 24",
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 1.75,
  strokeLinecap: "round" as const,
  strokeLinejoin: "round" as const,
  className: "size-5",
  "aria-hidden": true,
};

export function CopyIcon() {
  return (
    <svg {...iconProps}>
      <rect x="9" y="9" width="11" height="11" rx="2.5" />
      <path d="M5 15V6.5A2.5 2.5 0 0 1 7.5 4H15" />
    </svg>
  );
}

export function CheckIcon() {
  return (
    <svg {...iconProps}>
      <path d="m5 13 4.5 4.5L19 7" />
    </svg>
  );
}

export function KeyIcon() {
  return (
    <svg {...iconProps}>
      <circle cx="8" cy="16" r="4" />
      <path d="M11 13l9-9M15 9l2 2M17.5 6.5l2 2" />
    </svg>
  );
}

export function TrashIcon() {
  return (
    <svg {...iconProps}>
      <path d="M5 7h14M10 4h4M6.5 7l.8 12A1.5 1.5 0 0 0 8.8 20h6.4a1.5 1.5 0 0 0 1.5-1L17.5 7" />
    </svg>
  );
}

export function PencilIcon() {
  return (
    <svg {...iconProps}>
      <path d="M4 20h4L19 9a2.1 2.1 0 0 0-3-3L5 17v3Z" />
    </svg>
  );
}

/**
 * Sun and moon as paths: ☀ and ☾ have no emoji presentation in most fonts, so browsers
 * fall back to the text form — on Firefox the sun is a small asterisk.
 */
export function SunIcon() {
  return (
    <svg {...iconProps}>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M18.7 5.3l-1.4 1.4M6.7 17.3l-1.4 1.4" />
    </svg>
  );
}

export function MoonIcon() {
  return (
    <svg {...iconProps}>
      <path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5Z" />
    </svg>
  );
}

export function MenuIcon() {
  return (
    <svg {...iconProps}>
      <path d="M4 7h16M4 12h16M4 17h16" />
    </svg>
  );
}

export function CloseIcon() {
  return (
    <svg {...iconProps}>
      <path d="m6 6 12 12M18 6 6 18" />
    </svg>
  );
}

export function QrIcon() {
  return (
    <svg {...iconProps}>
      <rect x="3.5" y="3.5" width="6.5" height="6.5" rx="1.5" />
      <rect x="14" y="3.5" width="6.5" height="6.5" rx="1.5" />
      <rect x="3.5" y="14" width="6.5" height="6.5" rx="1.5" />
      <path d="M14 14h2.5v2.5H14zM18 18h2.5v2.5H18zM14 20.5h2.5M20.5 14v2.5" />
    </svg>
  );
}

/**
 * Selects the on-screen text so the reader can copy it by hand.
 *
 * By content rather than by ref: CopyButton is also used standalone and has no handle on
 * the node showing the value. Best-effort — the label has already changed either way.
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
 * An anchor wearing Button's clothes.
 *
 * A real <a> so long-press, middle-click, open-in-new-tab and copy-link-address work —
 * the affordances for moving a link between two devices.
 */
export function ButtonLink({
  variant = "secondary",
  className,
  ...rest
}: React.AnchorHTMLAttributes<HTMLAnchorElement> & { variant?: ButtonVariant }) {
  return (
    <a
      {...rest}
      className={cx(
        "inline-flex items-center justify-center gap-2 rounded-lg px-3 py-1.5 text-sm font-medium",
        "transition-opacity",
        buttonStyles[variant],
        className,
      )}
    />
  );
}

/**
 * A credential the operator may need to read, hidden until they ask.
 *
 * Subscription and install URLs are capabilities: anyone holding one can connect as
 * that user. Not something to leave on screen during a screen-share.
 *
 * `masked` defaults to true, so every existing call site keeps the blur. The public share
 * page passes false, and the reason is that the blur's threat model does not apply there:
 * it exists for an operator's screen-share, and somebody looking at their own credential
 * on their own phone is not a threat. Making them tap "Reveal" before they can copy their
 * own link would be friction bought for nothing.
 */
export function SecretField({
  label,
  value,
  masked = true,
  hint,
  action,
}: {
  label: string;
  value: string;
  masked?: boolean;
  /** What this value is for, under the row. */
  hint?: ReactNode;
  /**
   * An extra control beside Copy — in practice the button that puts this value on screen
   * as a QR code.
   *
   * A slot rather than a `qr` prop, because this component has no business knowing what a
   * QR is. It holds a string and hides it; what else can be done with that string is the
   * caller's concern.
   */
  action?: ReactNode;
}) {
  const [revealed, setRevealed] = useState(false);
  const shown = !masked || revealed;
  return (
    <div>
      <div className="mb-1 flex items-center justify-between">
        <span className="text-sm font-medium">{label}</span>
        {masked ? (
          <button
            className="text-xs text-muted underline underline-offset-2 hover:text-ink"
            onClick={() => setRevealed((s) => !s)}
          >
            {revealed ? "Hide" : "Reveal"}
          </button>
        ) : null}
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
        {/* Icons, not a labelled button. A credential row already carries a label, a
            Reveal control and often a QR button; a text "Copy" beside all that is wider
            than the value it acts on, and on a narrow drawer it pushes the value to its
            own line. The accessible name is unchanged. */}
        <CopyButton value={value} icon />
        {action}
      </div>
      {hint ? <p className="mt-1 text-xs text-muted">{hint}</p> : null}
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
        /*
          dvh, not h-full and not vh. On iOS Safari `h-full` does not resolve to a definite
          height in the top layer, so the flex child's overflow-y never engages and content
          spills past the dialog. `vh` is the large viewport there, putting the last
          screenful under the toolbar. Only dvh is both definite and visible-area.
        */
        side
          ? "ml-auto mr-0 h-dvh max-h-dvh w-full max-w-2xl rounded-none border-l border-line"
          : // Centred dialogs size to their content, up to a bound — the share page's
            // connection details are taller than a small phone. Width leaves a gutter
            // rather than being `w-full`: at exactly 100% the rounded corners and border
            // sit off-screen on a phone and it reads as a page, not a dialog.
            "m-auto max-h-[85dvh] w-[calc(100%-2rem)] max-w-lg rounded-[14px] border border-line",
      )}
    >
      {/* max-h-dvh rather than max-h-screen, for the reason above: on iOS `screen` is
          100vh, which is taller than what is actually visible. */}
      <div className={cx("flex flex-col", side ? "h-full max-h-dvh" : "max-h-[85dvh]")}>
        <div className="flex items-center justify-between gap-4 border-b border-line px-5 py-4">
          <h2 className="text-lg font-semibold">{title}</h2>
          <IconButton label="Close" onClick={onClose}>
            <CloseIcon />
          </IconButton>
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
