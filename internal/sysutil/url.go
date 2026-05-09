package sysutil

import "net/url"

// MaskURL replaces the password component of a URL's user-info with **** for
// safe display. Returns the input unchanged if it doesn't parse or has no
// credentials. Use this for URLs shown in pickers, status lines, or logs;
// for redaction of sensitive output going off-process, use
// internal/security.RedactSensitive instead.
func MaskURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if u.User != nil {
		u.User = url.UserPassword(u.User.Username(), "****")
	}
	return u.String()
}
