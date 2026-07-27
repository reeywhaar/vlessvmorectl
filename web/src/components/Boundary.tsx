import { Component, Suspense, type ErrorInfo, type ReactNode } from "react";
import { QueryErrorResetBoundary } from "@tanstack/react-query";
import { isVlessError, type VlessFailure } from "../api/errors";

export interface FallbackProps {
  error: unknown;
  /** Clears the query cache's error state and re-renders, so this really does retry. */
  retry: () => void;
  /** The typed failure, when the error came from the API layer. */
  failure: VlessFailure | null;
}

interface Props {
  children: ReactNode;
  /** Shown while suspended. */
  pending?: ReactNode;
  /** Shown when a child throws. */
  fallback: (props: FallbackProps) => ReactNode;
  /** Changing any of these resets the boundary, e.g. on navigation. */
  resetKeys?: unknown[];
}

/**
 * One generic error boundary, composed with Suspense.
 *
 * The pairing is the point: children throw promises for loading and errors for failure,
 * so no data component ever has to branch on `isLoading` or `isError`, and none of them
 * can return nothing. A component that silently rendered an empty card would leave an
 * operator unable to tell "this node has no users" from "this broke".
 *
 * Use it *granularly* — one per server card, one per route, one per drawer — rather than
 * once at the top. That is what keeps a single dead node from blanking the whole
 * overview: its own card renders the failure and the other cards never notice.
 */
export function Boundary({ children, pending, fallback, resetKeys }: Props) {
  return (
    <QueryErrorResetBoundary>
      {({ reset }) => (
        <ErrorBoundary onReset={reset} fallback={fallback} resetKeys={resetKeys ?? []}>
          <Suspense fallback={pending ?? null}>{children}</Suspense>
        </ErrorBoundary>
      )}
    </QueryErrorResetBoundary>
  );
}

interface EBProps {
  children: ReactNode;
  fallback: (props: FallbackProps) => ReactNode;
  onReset: () => void;
  resetKeys: unknown[];
}

interface EBState {
  error: unknown | null;
}

/**
 * Hand-written rather than pulled from react-error-boundary: this is the whole of that
 * package's surface that we use, error boundaries still require a class component, and
 * the reset-key comparison is four lines.
 */
class ErrorBoundary extends Component<EBProps, EBState> {
  override state: EBState = { error: null };
  private prevKeys: unknown[] = this.props.resetKeys;

  static getDerivedStateFromError(error: unknown): EBState {
    return { error };
  }

  override componentDidCatch(error: unknown, info: ErrorInfo): void {
    // Left in for production. This panel is operated by people who can read a console,
    // and a swallowed stack is worse than a noisy one.
    console.error("boundary caught", error, info.componentStack);
  }

  override componentDidUpdate(): void {
    const { resetKeys } = this.props;
    if (this.state.error !== null && changed(this.prevKeys, resetKeys)) {
      this.prevKeys = resetKeys;
      this.reset();
    } else {
      this.prevKeys = resetKeys;
    }
  }

  private reset = (): void => {
    this.props.onReset();
    this.setState({ error: null });
  };

  override render(): ReactNode {
    if (this.state.error !== null) {
      const error = this.state.error;
      return this.props.fallback({
        error,
        retry: this.reset,
        failure: isVlessError(error) ? error.failure : null,
      });
    }
    return this.props.children;
  }
}

function changed(a: unknown[], b: unknown[]): boolean {
  return a.length !== b.length || a.some((v, i) => !Object.is(v, b[i]));
}
