package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrPrivateTarget is returned for a send to a loopback, private,
// link-local or otherwise internal address when the server forbids them.
var ErrPrivateTarget = errors.New("the notification target is a private, loopback or link-local address, which this server does not allow")

// cgnat is the shared address space (RFC 6598), internal like RFC 1918.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// Private reports whether ip is not a public unicast address: loopback,
// RFC 1918 / ULA, link-local (cloud metadata lives there), CGNAT,
// multicast or unspecified.
func Private(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip)
}

// httpServices are the Shoutrrr schemes that send through the injected
// HTTP client, whose dialer refuses private addresses at connect time (no
// DNS-rebinding window). Every other scheme (smtp, mqtt, xmpp, …) dials
// on its own and is checked by resolving its host first.
var httpServices = map[string]bool{
	"bark": true, "discord": true, "generic": true, "googlechat": true, "gotify": true, "join": true,
	"lark": true, "matrix": true, "mattermost": true, "ntfy": true, "opsgenie": true, "pagerduty": true,
	"pushover": true, "rocketchat": true, "signal": true, "slack": true, "teams": true, "telegram": true,
	"twilio": true, "wecom": true, "zulip": true,
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
		return fmt.Errorf("cannot resolve %s", host)
	}
	for _, a := range addrs {
		if Private(a.IP) {
			return ErrPrivateTarget
		}
	}
	return nil
}
