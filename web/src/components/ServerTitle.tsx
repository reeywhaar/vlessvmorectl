import { hasServerName, serverLabel } from "../lib/format";
import type { ServerInfo } from "../api/types";

/**
 * A node's name with its hostname alongside, dimmed.
 *
 * When the node has no configured name the hostname *is* the title, so it is not repeated
 * — printing "vpn-nl.example.com vpn-nl.example.com" would be the obvious way to write
 * this and reads as a bug.
 *
 * The address stays visible either way. A name like "Amsterdam" is what an operator
 * thinks in, but the host is what they need when comparing against a DNS record or an
 * entry in VLESSVMORE_SERVERS.
 */
export function ServerTitle({ info }: { info: ServerInfo }) {
  return (
    <span className="inline-flex min-w-0 flex-wrap items-baseline gap-x-2">
      <span className="truncate">{serverLabel(info)}</span>
      {hasServerName(info) ? (
        <span className="truncate text-sm font-normal text-muted">{info.host}</span>
      ) : null}
    </span>
  );
}
