package notify

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
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

// httpServices are the Shoutrrr schemes that send through the injected
// HTTP client, whose dialer refuses private addresses at connect time (no
// DNS-rebinding window). Every other scheme (smtp, mqtt, xmpp, …) dials
// on its own and is checked by resolving its host first.
var httpServices = map[string]bool{
	"bark": true, "discord": true, "generic": true, "googlechat": true, "gotify": true, "join": true,
	"lark": true, "matrix": true, "mattermost": true, "ntfy": true, "opsgenie": true, "pagerduty": true,
	"pushover": true, "rocketchat": true, "signal": true, "slack": true, "teams": true, "telegram": true,
	"twilio": true, "wecom": true, "zulip": true, "ifttt": true, "pushbullet": true, "notifiarr": true,
	"signalgrid": true,
}

// guardedClient is an HTTP client whose connections to private addresses
// fail. It ignores proxy settings: a proxy would hide the destination.
func guardedClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || Private(ip) {
				return ErrPrivateTarget
			}
			return nil
		},
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialer.DialContext
	return &http.Client{Transport: transport, Timeout: timeout}
}

// checkHost resolves the host of a non-HTTP service URL and refuses
// private addresses. HTTP services are guarded at dial time instead.
func checkHost(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("the notification URL is not a valid URL")
	}
	scheme := strings.SplitN(strings.ToLower(u.Scheme), "+", 2)[0]
	host := u.Hostname()
	if httpServices[scheme] || host == "" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if Private(ip) {
			return ErrPrivateTarget
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		// Not the host itself: for some services it is a secret.
		return errors.New("cannot resolve the notification host")
	}
	for _, a := range addrs {
		if Private(a.IP) {
			return ErrPrivateTarget
		}
	}
	return nil
}
