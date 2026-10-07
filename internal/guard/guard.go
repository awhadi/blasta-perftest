// Package guard validates load-test targets. A load generator is a request
// amplifier, so the server must not let a stray job point at localhost,
// link-local metadata endpoints, or private networks by accident.
package guard

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// blockedHostnames are always refused: cloud metadata and loopback names.
var blockedHostnames = map[string]bool{
	"localhost":                true,
	"metadata":                 true,
	"metadata.google.internal": true,
	"instance-data":            true,
}

// CheckURL validates an http/https target URL against allowlist and
// private-range policy.
//
//   - allowlist, when non-empty, restricts targets to those exact hosts.
//   - blockPrivate refuses loopback, link-local, and RFC1918 destinations.
func CheckURL(raw string, allowlist []string, blockPrivate bool) error {
	_, err := CheckTarget(raw, allowlist, blockPrivate)
	return err
}

// CheckDialTarget applies the guard to a non-http target, given the schemes its
// executor accepts. The hostname blocklist and private-range policy still apply;
// the allowlist is skipped because it only applies to http/https jobs.
func CheckDialTarget(raw string, blockPrivate bool, schemes ...string) error {
	return checkDialTarget(raw, false, nil, blockPrivate, schemes...)
}

// CheckHostPort guards a bare "host:port" target. The tcp executor accepts this
// form as well as a tcp:// URL, so both paths need the same policy.
func CheckHostPort(raw string, allowlist []string, blockPrivate bool) error {
	return checkDialTarget(raw, true, allowlist, blockPrivate, "tcp")
}

func checkDialTarget(raw string, hostPortForm bool, allowlist []string, blockPrivate bool, schemes ...string) error {
	if hostPortForm {
		host, _, err := net.SplitHostPort(raw)
		if err != nil {
			return fmt.Errorf("invalid host:port %q: %w", raw, err)
		}
		return checkHost(host, allowlist, blockPrivate)
	}
	return checkDialTargetURL(raw, blockPrivate, schemes...)
}

func checkDialTargetURL(raw string, blockPrivate bool, schemes ...string) error {
	if raw == "" {
		return errors.New("target is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid target: %w", err)
	}
	allowed := false
	for _, s := range schemes {
		if strings.EqualFold(u.Scheme, s) {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("unsupported scheme %q (want %s)", u.Scheme, strings.Join(schemes, " or "))
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("target has no host")
	}
	return checkHost(host, nil, blockPrivate)
}

// CheckTarget validates an http/https target URL and returns its host.
func CheckTarget(raw string, allowlist []string, blockPrivate bool) (string, error) {
	if raw == "" {
		return "", errors.New("target url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return "", errors.New("url has no host")
	}
	if err := checkHost(host, allowlist, blockPrivate); err != nil {
		return "", err
	}
	return host, nil
}

// checkHost enforces the allowlist, hostname blocklist, and private-range
// policy for an already-extracted host.
func checkHost(host string, allowlist []string, blockPrivate bool) error {
	if len(allowlist) > 0 && !hostAllowed(host, allowlist) {
		return fmt.Errorf("host %q is not in the allowlist", host)
	}

	lh := strings.ToLower(host)
	if blockedHostnames[lh] {
		return fmt.Errorf("host %q is blocked", host)
	}

	if blockPrivate {
		if ip := net.ParseIP(host); ip != nil {
			if isBlockedIP(ip) {
				return fmt.Errorf("target %s is a private/loopback address (set blockPrivate=false to allow)", ip)
			}
		} else {
			// Resolve and check every address, guarding against DNS rebinding.
			addrs, err := net.LookupIP(host)
			if err != nil {
				return fmt.Errorf("cannot resolve %q: %w", host, err)
			}
			for _, ip := range addrs {
				if isBlockedIP(ip) {
					return fmt.Errorf("host %q resolves to blocked address %s", host, ip)
				}
			}
		}
	}
	return nil
}

func hostAllowed(host string, allowlist []string) bool {
	for _, a := range allowlist {
		if strings.EqualFold(strings.TrimSpace(a), host) {
			return true
		}
	}
	return false
}

// BlockedIP reports whether an address is on a private or otherwise internal network
// (loopback, link-local, private ranges, carrier-grade NAT, unique-local).
func BlockedIP(ip net.IP) bool { return isBlockedIP(ip) }

func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() || ip.IsPrivate() {
		return true
	}
	// Carrier-grade NAT (100.64.0.0/10) and IPv6 unique local (fc00::/7).
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
	}
	return len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc
}
