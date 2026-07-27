package api

import (
	"encoding/json"
	"time"
)

// Partial Go models of vlessvmore's wire format.
//
// Until the access page existed, this service never parsed a node's JSON at all — the
// proxy passes bytes through verbatim and the SPA does the reading. The access endpoint
// has to assemble a projection server-side, so it needs types. These are them, and they
// come with two rules that are the opposite of how this repo reads its own files.
//
// # Unknown fields are ignored
//
// store.readJSON sets DisallowUnknownFields because those files are ours and a
// misspelled key in one is a bug we want to hear about at startup. This is somebody
// else's API, living in another repository on its own release schedule. A node that
// gains a field in its next version must not turn every subscriber's page into a 500, so
// decodeNode uses a plain json.Unmarshal.
//
// # Only what is rendered is modelled
//
// Keeping the subset small is the other half of not drifting: a field that is not here
// cannot be quietly depended upon, and cannot leak into a projection by accident. Note
// in particular that nodeUser has no sub_token field even though the node sends one —
// this process has no use for it, so it does not decode it.
func decodeNode(b []byte, dst any) error { return json.Unmarshal(b, dst) }

// nodeUser is GET /api/users/{id}.
type nodeUser struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// QuotaBytes is 0 for unlimited.
	QuotaBytes int64 `json:"quota_bytes"`
	// ExpiresAt is absent for never.
	ExpiresAt *time.Time `json:"expires_at"`
	// DisabledReason is set only when enforcement turned the user off: "quota" or
	// "expired".
	DisabledReason string     `json:"disabled_reason"`
	Usage          *nodeUsage `json:"usage"`
}

// nodeUsage is the usage summary embedded in a user.
//
// Only the window figures and the quota are modelled. The lifetime totals the node also
// sends are deliberately left out: the quota is measured against the window, and putting
// two different "total bytes" numbers in front of somebody trying to work out why they
// were cut off produces a support message rather than understanding.
type nodeUsage struct {
	WindowUp    int64 `json:"window_up"`
	WindowDown  int64 `json:"window_down"`
	WindowTotal int64 `json:"window_total"`
	QuotaBytes  int64 `json:"quota_bytes"`
	// QuotaRemaining is 0 when unlimited, which is not "none left".
	QuotaRemaining int64 `json:"quota_remaining"`
}

// nodeLink is GET /api/users/{id}/link.
type nodeLink struct {
	Link            string    `json:"link"`
	SubscriptionURL string    `json:"subscription_url"`
	InstallURL      string    `json:"install_url"`
	QR              *qrMatrix `json:"qr"`
	SubscriptionQR  *qrMatrix `json:"subscription_qr"`
}

// nodeServerInfo is GET /api/server, of which only the label matters here.
//
// The rest of that response is the node's Reality configuration — public key, short id,
// SNI, handshake target. None of it is decoded, because none of it belongs anywhere near
// a projection an anonymous caller receives.
type nodeServerInfo struct {
	Name string `json:"name"`
	Host string `json:"host"`
}

// qrMatrix is a QR code as a bitmap, exactly as the node sends it and exactly as the
// SPA's QrMatrix component expects it. Passed through rather than re-encoded: there is no
// QR library in this module and adding one to re-derive a value we already have would be
// a dependency bought for nothing.
type qrMatrix struct {
	Size int `json:"size"`
	// Rows is Size strings of Size characters, '0' light and '1' dark, top row first.
	Rows []string `json:"rows"`
	// QuietZone is the light margin, in modules. Without it, scanners fail.
	QuietZone int `json:"quiet_zone"`
}
