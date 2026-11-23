package config

import (
	"fmt"
	"net/netip"
	"strings"
)

// Proxies are the peers whose X-Forwarded-For says who the caller is.
type Proxies []netip.Prefix

// Trusts reports whether addr is one of them.
func (p Proxies) Trusts(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range p {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// private is what "private" stands for: where a proxy on the same host or Docker network
// connects from.
var private = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7", "127.0.0.0/8", "::1/128"}

// ParseProxies reads a list of addresses, ranges, "private" and "none", split by commas or
// spaces. Empty is "private".
func ParseProxies(raw string) (Proxies, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	if len(fields) == 0 {
		fields = []string{"private"}
	}
	var out Proxies
	for _, f := range fields {
		switch {
		case f == "none":
			if len(fields) > 1 {
				return nil, fmt.Errorf("%s: \"none\" goes alone, not with %q", TrustedProxiesEnv, raw)
			}
			return Proxies{}, nil
		case f == "private":
			for _, r := range private {
				out = append(out, netip.MustParsePrefix(r))
			}
		case strings.Contains(f, "/"):
			prefix, err := netip.ParsePrefix(f)
			if err != nil {
				return nil, fmt.Errorf("%s: %q is not a range", TrustedProxiesEnv, f)
			}
			out = append(out, prefix.Masked())
		default:
			addr, err := netip.ParseAddr(f)
			if err != nil {
				return nil, fmt.Errorf("%s: %q is not an address, a range, \"private\" or \"none\"", TrustedProxiesEnv, f)
			}
			addr = addr.Unmap()
			out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
		}
	}
	return out, nil
}
