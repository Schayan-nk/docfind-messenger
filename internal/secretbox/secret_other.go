//go:build !windows

package secretbox

// Everywhere that is not Windows, the key file is the key.
//
// This build exists so the package compiles and its tests run on a developer
// machine and in CI; the product ships on Windows, where secret_windows.go
// wraps the key with DPAPI and binds it to the machine.
//
// No pretend encryption here. Wrapping the key with a second key that also
// has to live on the same disk moves the problem one file sideways and buys
// nothing, and writing it would leave the next reader believing in protection
// that is not there. The 0600 permission and the directory around it are what
// guards this file; on a machine where that is not enough, neither would a
// scrambled copy be.
func protectKey(key []byte) ([]byte, error) { return key, nil }

func unprotectKey(wrapped []byte) ([]byte, error) { return wrapped, nil }
