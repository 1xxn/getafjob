// Package netguard keeps links from foreign sources away from the own machine and network (SSRF).
//
// PublicURL checks the spelling only (no DNS, cheap enough for every crawled link) and reads the URL the way
// Chromium does (WHATWG URL, hosts mapped with UTS 46). Client checks every connection again after DNS, so
// redirects and DNS rebinding cannot reach private addresses either.
package netguard

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/idna"
	"golang.org/x/text/unicode/norm"
)

// ErrBlocked reports a target in the own machine or network, or one that is no web address.
var ErrBlocked = errors.New("target is not a public web address")

// nonPublic lists special-purpose ranges (IANA registries) that are never public targets.
var nonPublic = mustPrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
	"192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
	"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
	"::/128", "::1/128", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "fc00::/7", "fe80::/10",
	"fec0::/10", "ff00::/8",
)

func mustPrefixes(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(s))
	for i, p := range s {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}

// PublicIP reports whether ip is a public unicast address. IPv4 embedded in IPv6 (mapped, NAT64, 6to4) is
// checked as IPv4.
func PublicIP(ip netip.Addr) bool {
	ip = ip.Unmap().WithZone("")
	if ip.Is6() {
		b := ip.As16()
		switch {
		case netip.MustParsePrefix("64:ff9b::/96").Contains(ip):
			return PublicIP(netip.AddrFrom4([4]byte(b[12:16])))
		case netip.MustParsePrefix("2002::/16").Contains(ip):
			return PublicIP(netip.AddrFrom4([4]byte(b[2:6])))
		}
	}
	if !ip.IsValid() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

var localSuffixes = []string{".localhost", ".local", ".internal", ".home.arpa", ".lan", ".intranet"}

// uts46 maps hosts like browsers do (full-width and enclosed characters, ideographic dots …).
var uts46 = idna.New(idna.MapForLookup(), idna.Transitional(false), idna.StrictDomainName(false))

// browserHost returns the host as a browser reads it, or false if no browser would open it.
func browserHost(host string) (string, bool) {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return strings.ToLower(host[1 : len(host)-1]), true // IPv6 literal: no decoding, no mapping
	}
	h, err := url.PathUnescape(host)
	if err != nil {
		return "", false
	}
	h = strings.Map(func(r rune) rune {
		if r == '­' || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Variation_Selector, r) {
			return -1 // invisible characters are dropped by UTS 46
		}
		return r
	}, h)
	// NFKC first: x/net/idna keeps STD3-disallowed characters such as "⒈" that browsers map to "1.".
	mapped, err := uts46.ToASCII(norm.NFKC.String(h))
	if err != nil {
		mapped = strings.ToLower(h)
	}
	for _, r := range mapped {
		if r <= 0x20 || r == 0x7f || strings.ContainsRune("#%/:<>?@[\\]^|", r) {
			return "", false // forbidden domain code point
		}
	}
	return mapped, true
}

// PublicHost reports whether a host (name or IP literal, without DNS) can be a public server.
func PublicHost(host string) bool {
	h, ok := browserHost(host)
	if !ok {
		return false
	}
	h = strings.TrimSuffix(h, ".")
	if h == "" || h == "localhost" {
		return false
	}
	for _, s := range localSuffixes {
		if strings.HasSuffix(h, s) {
			return false
		}
	}
	if strings.Contains(h, ":") {
		ip, err := netip.ParseAddr(h)
		return err == nil && ip.Zone() == "" && PublicIP(ip)
	}
	switch ip, kind := browserIPv4(h); kind {
	case ipInvalid:
		return false
	case ipV4:
		return PublicIP(ip)
	case ipName:
	}
	return strings.Contains(h, ".") // "http://router" is resolved by the local network
}

type ipKind int

const (
	ipName ipKind = iota
	ipV4
	ipInvalid
)

// browserIPv4 parses IPv4 the way browsers do (WHATWG URL): "127.1", "0x7f.0.0.1", "0177.0.0.1", "2130706433".
func browserIPv4(host string) (netip.Addr, ipKind) {
	parts := strings.Split(host, ".")
	if len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if last == "" || !isDigits(last, 10) && !strings.HasPrefix(strings.ToLower(last), "0x") {
		return netip.Addr{}, ipName // does not end in a number: a name
	}
	if len(parts) > 4 {
		return netip.Addr{}, ipInvalid
	}
	nums := make([]uint32, len(parts))
	for i, p := range parts {
		n, ok := ipv4Number(p)
		if !ok {
			return netip.Addr{}, ipInvalid
		}
		nums[i] = n
	}
	for _, n := range nums[:len(nums)-1] {
		if n > 255 {
			return netip.Addr{}, ipInvalid
		}
	}
	if uint64(nums[len(nums)-1]) >= 1<<(8*(5-len(nums))) {
		return netip.Addr{}, ipInvalid
	}
	addr := nums[len(nums)-1]
	for i, n := range nums[:len(nums)-1] {
		addr |= n << (8 * (3 - i))
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], addr)
	return netip.AddrFrom4(b), ipV4
}

func ipv4Number(p string) (uint32, bool) {
	base := 10
	switch {
	case len(p) >= 2 && (p[:2] == "0x" || p[:2] == "0X"):
		base, p = 16, p[2:]
		if p == "" {
			return 0, true
		}
	case len(p) > 1 && p[0] == '0':
		base, p = 8, p[1:]
	}
	if !isDigits(p, base) {
		return 0, false
	}
	n, err := strconv.ParseUint(p, base, 32)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}

func isDigits(s string, base int) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		ok := false
		switch base {
		case 8:
			ok = r >= '0' && r <= '7'
		case 10:
			ok = r >= '0' && r <= '9'
		case 16:
			ok = r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
		}
		if !ok {
			return false
		}
	}
	return true
}

// browserURL normalises a URL like browsers before they look for the host: surrounding control characters and
// spaces go, tabs and newlines inside go, and in http(s) URLs "\" is "/".
func browserURL(raw string) string {
	raw = strings.TrimFunc(raw, func(r rune) bool { return r <= 0x20 })
	raw = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(raw)
	return strings.ReplaceAll(raw, "\\", "/")
}

// PublicURL reports whether raw is an http(s) URL whose host can be public – without DNS, read like a browser.
func PublicURL(raw string) bool {
	_, ok := Host(raw)
	return ok
}

// Host returns the host a browser would connect to for raw, if raw is a public http(s) URL.
func Host(raw string) (string, bool) {
	u, err := url.Parse(browserURL(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" {
		return "", false
	}
	host := u.Host
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	if !strings.HasPrefix(host, "[") {
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
	} else if i := strings.LastIndex(host, "]"); i >= 0 {
		host = host[:i+1]
	}
	if !PublicHost(host) {
		return "", false
	}
	h, _ := browserHost(host)
	return strings.TrimSuffix(h, "."), true
}
