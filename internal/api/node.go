package api

import (
	"encoding/json"
	"time"
)

// Partial Go models of vlessvmore's wire format, with two rules that are the opposite of
// how this repo reads its own files.
//
// Unknown fields are ignored: this is another project's API on its own release schedule,
// and a field it adds must not turn every subscriber's page into a 500.
//
// Only what is rendered is modelled, so a field that is not here cannot be depended upon
// or leak into a projection. nodeUser has no sub_token even though the node sends one.
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
