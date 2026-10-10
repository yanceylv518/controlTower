package billing

import (
	"errors"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

var ErrUpstreamURLConflict = errors.New("upstream_url_conflict")
var ErrUpstreamChannelConflict = errors.New("upstream_channel_conflict")

// NormalizeUpstreamURL compares complete endpoints, never domain substrings.
// Scheme, non-default port and path remain meaningful; credentials are not URLs
// that can be stored in the public upstream configuration.
func NormalizeUpstreamURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, " \t\r\n\\") {
		return "", errors.New("invalid_upstream_url")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", errors.New("invalid_upstream_url")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("invalid_upstream_url")
	}
	host := strings.ToLower(u.Hostname())
	if strings.ContainsAny(host, " \\%") {
		return "", errors.New("invalid_upstream_url")
	}
	port := u.Port()
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid_upstream_url")
		}
		port = strconv.Itoa(n)
	}
	if u.Scheme == "http" && port == "80" || u.Scheme == "https" && port == "443" {
		port = ""
	}
	u.Host = host
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	u.Path, err = url.PathUnescape(path)
	if err != nil {
		return "", errors.New("invalid_upstream_url")
	}
	u.RawPath = path
	return u.String(), nil
}

func MatchUpstreamChannels(endpoint string, channels []ConfiguredChannel) []UpstreamChannel {
	out := []UpstreamChannel{}
	seen := map[int64]bool{}
	for _, c := range channels {
		normalized, err := NormalizeUpstreamURL(c.BaseURL)
		if err != nil || normalized != endpoint || seen[c.ChannelID] || c.ChannelID <= 0 {
			continue
		}
		seen[c.ChannelID] = true
		// New URL associations include the whole channel. Nil models means all
		// models, including models added later; old manual selections stay intact.
		out = append(out, UpstreamChannel{ChannelID: c.ChannelID, ChannelName: c.ChannelName})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChannelID < out[j].ChannelID })
	return out
}
