//go:build !windows

package main

import "syscall"

// setPrivateUmask makes every file the bridge creates (databases, SQLite
// journals, media) readable by the owner only.
func setPrivateUmask() {
	syscall.Umask(0o077)
}
