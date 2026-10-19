package notify

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// ErrPrivateTarget is returned for a send to a loopback, private,
// link-local or otherwise internal address when the server forbids them.
var ErrPrivateTarget = errors.New("the notification target is a private, loopback or link-local address, which this server does not allow")

// internalV4 are IPv4 ranges that are not public unicast but that the
// net.IP predicates below do not cover.
var internalV4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // "this network" (RFC 1122)
	netip.MustParsePrefix("100.64.0.0/10"), // shared address space, CGNAT (RFC 6598)
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments (RFC 6890)
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking (RFC 2544)
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved, incl. broadcast
}

// embeddedV4 are IPv6 ranges that carry an IPv4 address: NAT64 (RFC 6052,
// RFC 8215; the address is the last 32 bits) and 6to4 (RFC 3056; bits
// 16-47). Such an address is as internal as the IPv4 address it embeds.
var (
	nat64     = []netip.Prefix{netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48")}
	sixToFour = netip.MustParsePrefix("2002::/16")
)

// Private reports whether ip is not a public unicast address: loopback,
// RFC 1918 / ULA, link-local (cloud metadata lives there), CGNAT,
// multicast, unspecified, the other reserved IPv4 ranges, or an IPv6
// address embedding any of those (NAT64, 6to4).
func Private(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	a = a.Unmap()
	if a.Is4() {
		for _, p := range internalV4 {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	b := a.As16()
	for _, p := range nat64 {
		if p.Contains(a) {
			return Private(net.IP(b[12:16]))
		}
	}
	if sixToFour.Contains(a) {
		return Private(net.IP(b[2:6]))
	}
	return false
}

// reviewedSchemes is every Shoutrrr scheme, with how its connections are
// guarded. A Shoutrrr upgrade that adds a scheme fails
// TestEveryShoutrrrSchemeIsReviewed until someone checks whether the new
// service connects while it is being created (before Shoutrrr injects our
// client and dialer) and adds it here.
var reviewedSchemes = map[string]string{
	"matrix": "logs in from Initialize: built directly, our client set first",
	"logger": "no network",
	"smtp":   "dialer", "mqtt": "dialer", "mqtts": "dialer", "xmpp": "dialer", "xmpps": "dialer",
	"bark": "client", "discord": "client", "generic": "client", "googlechat": "client", "gotify": "client",
	"hangouts": "client", "homeassistant": "client", "ifttt": "client", "join": "client", "lark": "client",
	"mattermost": "client", "notifiarr": "client", "ntfy": "client", "opsgenie": "client", "pagerduty": "client",
	"pushbullet": "client", "pushover": "client", "rocketchat": "client", "signal": "client", "signalgrid": "client",
	"slack": "client", "teams": "client", "telegram": "client", "twilio": "client", "wecom": "client", "zulip": "client",
}

// refuseHook, when set, replaces refusedAddress, so tests can refuse a
// specific listener. It is atomic because a sender's background dial (the
// MQTT client's reconnect loop) can outlive the send that started it.
var refuseHook atomic.Pointer[func(address string) bool]

// refused reports whether a connection to address (host:port, the
// resolved IP) must fail.
func refused(address string) bool {
	if h := refuseHook.Load(); h != nil {
		return (*h)(address)
	}
	return refusedAddress(address)
}

func refusedAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return true
	}
	ip := net.ParseIP(host)
	return ip == nil || Private(ip)
}

// guardedDialer is a dialer whose connections to private addresses fail.
// Control sees the resolved address, so a name that resolves (or later
// re-resolves) to a private address is refused too, and so is every hop
// of a redirect.
func guardedDialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			if refused(address) {
				return ErrPrivateTarget
			}
			return nil
		},
	}
}

// guardedClient is an HTTP client that dials through d. It ignores proxy
// settings: a proxy would hide the destination.
func guardedClient(d *net.Dialer, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = d.DialContext
	return &http.Client{Transport: transport, Timeout: timeout}
}

// scheme is a URL's service name: the scheme without a "+transport"
// suffix ("generic+https" -> "generic").
func scheme(u *url.URL) string {
	return strings.SplitN(strings.ToLower(u.Scheme), "+", 2)[0]
}
