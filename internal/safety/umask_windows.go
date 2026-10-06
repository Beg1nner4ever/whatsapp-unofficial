//go:build windows

package safety

// SetPrivateUmask is a no-op on Windows, which has no umask; files inherit
// the ACL of the user's profile directory.
func SetPrivateUmask() {}
