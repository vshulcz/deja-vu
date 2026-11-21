//go:build windows

package main

import (
	"debug/pe"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
)

// zcodeDesktopVersion is the installed ZCode desktop version, indirected so a
// test can say which build is there, the way claudeVersion does. The desktop
// build is not on PATH by default and its CLI config carries no version, so the
// executable's own PE version resource is the only version deja can ask
// without starting the app. Where it cannot be read, the caller falls back to
// probing the config files themselves.
var zcodeDesktopVersion = zcodeDesktopVersionReal
var zcodeLookPath = exec.LookPath

// zcodeRegistryInstallLocations is indirected so a test can say the registry
// is empty (or that an uninstall key is present) without touching the real
// registry. It returns the InstallLocation value of every HKCU/HKLM
// Uninstall subkey whose DisplayName names ZCode — the one place Windows
// records where a per-user app was actually installed, which covers an
// install under a directory deja's fixed candidate list does not know
// (e.g. `S:\apps\llm\app\zcode`, #6016's D:\ZCode, or a portable layout).
var zcodeRegistryInstallLocations = zcodeRegistryInstallLocationsReal

// zcodeUninstallRoots are the registry roots deja reads. Each entry names a
// view (HKCU or HKLM, the latter including the WOW6432Node redirection on
// 32-bit processes) and the key under it that holds the uninstall entries.
var zcodeUninstallRoots = []struct {
	root syscall.Handle
	sub  string
}{
	{syscall.HKEY_CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
	{syscall.HKEY_LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
	{syscall.HKEY_LOCAL_MACHINE, `Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
}

// regValueString reads one REG_SZ / REG_EXPAND_SZ value from an open key.
// It returns ok=false when the value is absent or not a string, so a
// partial uninstall entry (DisplayName but no InstallLocation) is skipped
// rather than trusted.
func regValueString(key syscall.Handle, name string) (string, bool) {
	namePtr, _ := syscall.UTF16PtrFromString(name)
	var typ uint32
	var size uint32
	// First call sizes the buffer. ERROR_MORE_DATA is expected.
	err := syscall.RegQueryValueEx(key, namePtr, nil, &typ, nil, &size)
	if err != nil && err != syscall.ERROR_MORE_DATA {
		return "", false
	}
	if typ != syscall.REG_SZ && typ != syscall.REG_EXPAND_SZ {
		return "", false
	}
	buf := make([]byte, size)
	err = syscall.RegQueryValueEx(key, namePtr, nil, &typ, &buf[0], &size)
	if err != nil {
		return "", false
	}
	// The value is UTF-16 with a trailing NUL; strip it and any padding.
	u16 := utf16.Decode(unsafeSlice(buf))
	for len(u16) > 0 && u16[len(u16)-1] == 0 {
		u16 = u16[:len(u16)-1]
	}
	return string(u16), true
}

// unsafeSlice reinterprets a byte slice as uint16 without importing unsafe:
// the registry returns REG_SZ as a UTF-16LE buffer, and utf16.Decode takes
// []uint16. The bytes are copied on the way in, so aliasing the caller's
// buffer is safe.
func unsafeSlice(b []byte) []uint16 {
	if len(b) == 0 {
		return nil
	}
	// Pad to an even length so the final odd byte is never read past.
	if len(b)%2 != 0 {
		b = append(b, 0)
	}
	u := make([]uint16, len(b)/2)
	for i := 0; i < len(u); i++ {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return u
}

// zcodeRegistryInstallLocationsReal walks the Windows uninstall keys and
// collects the InstallLocation of every entry that names ZCode. It uses the
// stdlib syscall registry API only, so deja keeps its zero-dependency build.
func zcodeRegistryInstallLocationsReal() []string {
	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		for _, existing := range out {
			if strings.EqualFold(filepath.Clean(existing), filepath.Clean(p)) {
				return
			}
		}
		out = append(out, p)
	}
	for _, root := range zcodeUninstallRoots {
		var key syscall.Handle
		sub, _ := syscall.UTF16PtrFromString(root.sub)
		if err := syscall.RegOpenKeyEx(root.root, sub, 0, syscall.KEY_READ, &key); err != nil {
			continue
		}
		func() {
			defer syscall.RegCloseKey(key)
			var subkeys, maxSubkeyLen uint32
			if err := syscall.RegQueryInfoKey(key, nil, nil, nil, &subkeys, &maxSubkeyLen, nil, nil, nil, nil, nil, nil); err != nil {
				return
			}
			for i := uint32(0); i < subkeys; i++ {
				nameBuf := make([]uint16, maxSubkeyLen+1)
				nameLen := uint32(len(nameBuf))
				if err := syscall.RegEnumKeyEx(key, i, &nameBuf[0], &nameLen, nil, nil, nil, nil); err != nil {
					continue
				}
				var subKey syscall.Handle
				subName := string(utf16.Decode(nameBuf[:nameLen]))
				subNamePtr, _ := syscall.UTF16PtrFromString(subName)
				if err := syscall.RegOpenKeyEx(key, subNamePtr, 0, syscall.KEY_READ, &subKey); err != nil {
					continue
				}
				func() {
					defer syscall.RegCloseKey(subKey)
					display, ok := regValueString(subKey, "DisplayName")
					if !ok || !strings.Contains(strings.ToLower(display), "zcode") {
						return
					}
					loc, ok := regValueString(subKey, "InstallLocation")
					if !ok {
						return
					}
					// InstallLocation may be the install root or already name
					// the executable; either way a clean join works.
					if strings.EqualFold(filepath.Base(loc), "ZCode.exe") {
						add(loc)
					} else {
						add(filepath.Join(loc, "ZCode.exe"))
					}
				}()
			}
		}()
	}
	return out
}

func zcodeDesktopVersionReal() (v [3]int, ok bool) {
	for _, p := range zcodeCandidatePaths() {
		if v, ok := zcodeExeVersion(p); ok {
			return v, true
		}
	}
	return v, false
}

// zcodeCandidatePaths returns candidate locations for ZCode.exe on Windows.
// Per-user install under LOCALAPPDATA is probed first, followed by PATH and
// system-wide ProgramFiles locations (#6016), and finally any install the
// Windows uninstall registry records under an InstallLocation the fixed
// paths do not cover.
func zcodeCandidatePaths() []string {
	var paths []string
	add := func(p string) {
		if p == "" {
			return
		}
		for _, existing := range paths {
			if strings.EqualFold(filepath.Clean(existing), filepath.Clean(p)) {
				return
			}
		}
		paths = append(paths, p)
	}

	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		add(filepath.Join(local, "Programs", "ZCode", "ZCode.exe"))
	}
	for _, name := range []string{"ZCode.exe", "zcode.exe"} {
		if p, err := zcodeLookPath(name); err == nil {
			add(p)
		}
	}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		if root := os.Getenv(env); root != "" {
			add(filepath.Join(root, "ZCode", "ZCode.exe"))
		}
	}
	for _, loc := range zcodeRegistryInstallLocations() {
		add(loc)
	}
	return paths
}

// zcodeExeVersion reads one PE file's fixed file version.
func zcodeExeVersion(path string) (v [3]int, ok bool) {
	f, err := pe.Open(path)
	if err != nil {
		return v, false
	}
	defer f.Close()
	sec := f.Section(".rsrc")
	if sec == nil {
		return v, false
	}
	data, err := sec.Data()
	if err != nil {
		return v, false
	}
	return zcodeVersionFromRSRC(data)
}

// zcodeVersionFromRSRC finds VS_VERSION_INFO in a .rsrc section and reads the
// four 16-bit numbers of VS_FIXEDFILEINFO.dwFileVersionMS/LS. The ProductVersion
// string can carry a suffix, so the fixed numbers are the ones to compare.
func zcodeVersionFromRSRC(data []byte) (v [3]int, ok bool) {
	// wLength, wValueLength, wType, then the UTF-16 key "VS_VERSION_INFO\0".
	// Everything after the header is aligned to 4 bytes.
	var key []byte
	for _, r := range "VS_VERSION_INFO" {
		key = append(key, byte(r), 0x00)
	}
	key = append(key, 0x00, 0x00)
	for i := 6; i+len(key) <= len(data); i += 2 {
		hit := true
		for j := range key {
			if data[i+j] != key[j] {
				hit = false
				break
			}
		}
		if !hit {
			continue
		}
		header := i - 6
		wLength := int(binary.LittleEndian.Uint16(data[header:]))
		wValueLength := int(binary.LittleEndian.Uint16(data[header+2:]))
		if wValueLength < 52 || header+wLength > len(data) {
			return v, false
		}
		val := header + 6 + len(key)
		for (val-header)%4 != 0 {
			val++
		}
		if val+16 > len(data) || binary.LittleEndian.Uint32(data[val:]) != 0xFEEF04BD {
			return v, false
		}
		ms := binary.LittleEndian.Uint32(data[val+8:])
		ls := binary.LittleEndian.Uint32(data[val+12:])
		return [3]int{int(ms >> 16), int(ms & 0xffff), int(ls >> 16)}, true
	}
	return v, false
}
