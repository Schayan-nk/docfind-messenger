//go:build windows

package secretbox

import (
	"fmt"
	"syscall"
	"unsafe"
)

// The key file, tied to this machine.
//
// DPAPI (CryptProtectData) encrypts with a key derived from Windows itself.
// The point is not extra strength -- AES-256-GCM is already the strong part --
// it is that the wrapped key is bound to THIS machine, so the two files
// travelling together to somebody's laptop still yield nothing. A backup or a
// copied folder is the realistic way a secret leaves a server, and this is the
// measure that answers it.
//
// CRYPTPROTECT_LOCAL_MACHINE, not the per-user default: the application runs
// as a scheduled task under whatever account the administrator chose, and a
// key sealed to one user profile would stop working the moment that changed.
// The trade is that any account on this machine can unwrap it -- which is the
// same set that can read the file at all, so it costs nothing real.

var (
	crypt32                = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	kernel32Local          = syscall.NewLazyDLL("kernel32.dll")
	procLocalFree          = kernel32Local.NewProc("LocalFree")
)

// dataBlob mirrors DATA_BLOB.
type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(b []byte) dataBlob {
	if len(b) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(b)), pbData: &b[0]}
}

func (b *dataBlob) bytes() []byte {
	if b.cbData == 0 || b.pbData == nil {
		return nil
	}
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}

const cryptprotectLocalMachine = 0x4

func protectKey(key []byte) ([]byte, error) {
	in := newBlob(key)
	var out dataBlob
	ok, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, // no description
		0, // no extra entropy: the key file is the only secret to keep
		0, 0,
		cryptprotectLocalMachine,
		uintptr(unsafe.Pointer(&out)),
	)
	if ok == 0 {
		return nil, fmt.Errorf("CryptProtectData: %v", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

func unprotectKey(wrapped []byte) ([]byte, error) {
	in := newBlob(wrapped)
	var out dataBlob
	ok, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0,
		cryptprotectLocalMachine,
		uintptr(unsafe.Pointer(&out)),
	)
	if ok == 0 {
		return nil, fmt.Errorf("CryptUnprotectData: %v", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}
