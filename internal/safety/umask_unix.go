//go:build !windows

package safety

import "syscall"

// SetPrivateUmask makes every file the process creates (databases, SQLite
// journals, sockets, media, logs) accessible to the owner only.
func SetPrivateUmask() {
	syscall.Umask(0o077)
}
