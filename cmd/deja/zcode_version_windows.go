//go:build windows

package main

import (
	"debug/pe"
	"encoding/binary"
	"os"
	"path/filepath"
)

// zcodeDesktopVersion is the installed ZCode desktop version, indirected so a
// test can say which build is there, the way claudeVersion does. The desktop
// build is not on PATH and its CLI config carries no version, so the
// executable's own PE version resource is the only version deja can ask
// without starting the app. Where it cannot be read, the caller falls back to
// probing the config files themselves.
var zcodeDesktopVersion = zcodeDesktopVersionReal

func zcodeDesktopVersionReal() (v [3]int, ok bool) {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		return v, false
	}
	return zcodeExeVersion(filepath.Join(root, "Programs", "ZCode", "ZCode.exe"))
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
