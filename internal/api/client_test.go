package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"vlessvmorectl/internal/config"
)

func TestTheCallerIsWhoTheProxySaw(t *testing.T) {
	for _, c := range []struct {
		name, proxies, peer, xff, xri, want string
	}{
		{"caddy on the docker network", "private", "172.18.0.5:41000", "203.0.113.7", "", "203.0.113.7"},
		{"only the proxy's own hop counts", "private", "172.18.0.5:41000", "198.51.100.1, 203.0.113.7", "", "203.0.113.7"},
		{"ipv6 through a proxy", "private", "172.18.0.5:41000", "2001:db8::7", "", "2001:db8::7"},
		{"loopback is a proxy too", "private", "127.0.0.1:41000", "203.0.113.7", "", "203.0.113.7"},
		{"no header, the peer", "private", "172.18.0.5:41000", "", "", "172.18.0.5"},
		{"a header that is not an address, the peer", "private", "172.18.0.5:41000", "unknown", "", "172.18.0.5"},
		{"a stranger cannot name somebody else", "private", "203.0.113.9:41000", "198.51.100.1", "", "203.0.113.9"},
		{"a stranger over ipv6", "private", "[2001:db8::9]:41000", "198.51.100.1", "", "2001:db8::9"},
		{"ipv4 mapped into ipv6", "private", "[::ffff:203.0.113.9]:41000", "", "", "203.0.113.9"},
		{"X-Real-IP is the caller's own", "private", "172.18.0.5:41000", "203.0.113.7", "198.51.100.1", "203.0.113.7"},
		{"X-Real-IP from a stranger", "private", "203.0.113.9:41000", "", "198.51.100.1", "203.0.113.9"},
		{"a proxy on another host, named", "198.51.100.3", "198.51.100.3:41000", "203.0.113.7", "", "203.0.113.7"},
		{"naming one stops trusting the rest", "198.51.100.3", "172.18.0.5:41000", "203.0.113.7", "", "172.18.0.5"},
		{"a chain of named proxies", "private, 198.51.100.0/24", "172.18.0.5:41000", "192.0.2.1, 203.0.113.7, 198.51.100.3", "", "203.0.113.7"},
		{"a chain whose first hop is not named", "private", "172.18.0.5:41000", "203.0.113.7, 198.51.100.3", "", "198.51.100.3"},
		{"nothing but proxies, the furthest", "private", "172.18.0.5:41000", "10.0.0.2, 10.0.0.3", "", "10.0.0.2"},
		{"none believes nobody", "none", "172.18.0.5:41000", "203.0.113.7", "", "172.18.0.5"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := serverTrusting(t, c.proxies)
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = c.peer
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if c.xri != "" {
				r.Header.Set("X-Real-IP", c.xri)
			}
			if got := s.clientIP(r); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

// A proxy that sends the header twice means one list, in order.
func TestTwoForwardedHeadersAreOneList(t *testing.T) {
	s := serverTrusting(t, "private")
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.5:41000"
	r.Header.Add("X-Forwarded-For", "198.51.100.1")
	r.Header.Add("X-Forwarded-For", "203.0.113.7")
	if got := s.clientIP(r); got != "203.0.113.7" {
		t.Errorf("got %s", got)
	}
}

func serverTrusting(t *testing.T, proxies string) *Server {
	t.Helper()
	p, err := config.ParseProxies(proxies)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{cfg: &config.Config{TrustedProxies: p}}
}

// failregex is the filter in the README with <ADDR> spelled out: the line it matches is a
// contract with a jail on the host. `\S*` because fail2ban cuts the timestamp out first.
var failregex = regexp.MustCompile(`^time=\S* level=WARN msg="password refused" ip=([0-9A-Fa-f.:]+) via=(\S+)`)

// fail2banHits is what the filter would ban on in logs, as "ip via" pairs.
func fail2banHits(logs string) []string {
	var hits []string
	for line := range strings.Lines(logs) {
		if m := failregex.FindStringSubmatch(line); m != nil {
			hits = append(hits, m[1]+" "+m[2])
		}
	}
	return hits
}

func TestEveryRefusalIsALineFail2banBansOn(t *testing.T) {
	h := newHarness(t, "")
	cookie := h.login()
	h.subscriber("Ivan")

	// httptest's peer is 192.0.2.1, which is nobody's proxy.
	h.do(http.MethodPost, "/api/login", nil, `{"username":"alice","password":"guess one"}`)
	h.do(http.MethodPost, "/api/login", nil, `{"username":"nobody","password":"guess two"}`)
	h.do(http.MethodPost, "/api/account/password", cookie,
		`{"current_password":"guess three","new_password":"`+newPassword+`"}`)
	h.do(http.MethodGet, "/api/access/QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ", nil, "")

	want := []string{"192.0.2.1 sign-in", "192.0.2.1 sign-in", "192.0.2.1 current-password", "192.0.2.1 share-link"}
	got := fail2banHits(h.logs.String())
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("fail2ban would ban\n%v\nwant\n%v\nfrom:\n%s", got, want, h.logs.String())
	}
	if strings.Contains(h.logs.String(), "guess") {
		t.Errorf("a password is in the log:\n%s", h.logs.String())
	}
}

func TestARefusalThroughCaddyNamesTheCaller(t *testing.T) {
	h := newHarness(t, "")
	req := httptest.NewRequest(http.MethodPost, "/api/login",
		strings.NewReader(`{"username":"alice","password":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "172.18.0.5:41000"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	h.handler.ServeHTTP(httptest.NewRecorder(), req)

	if got := fail2banHits(h.logs.String()); len(got) != 1 || got[0] != "203.0.113.7 sign-in" {
		t.Errorf("fail2ban would ban %v, from:\n%s", got, h.logs.String())
	}
}

func TestASuccessfulSignInIsNoFail2banLine(t *testing.T) {
	h := newHarness(t, "")
	h.login()
	if got := fail2banHits(h.logs.String()); len(got) != 0 {
		t.Errorf("fail2ban would ban %v, from:\n%s", got, h.logs.String())
	}
}
