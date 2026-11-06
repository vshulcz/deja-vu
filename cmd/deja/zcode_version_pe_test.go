//go:build windows

package main

import (
	"encoding/binary"
	"testing"
)

// zcodeVersionResource builds the VS_VERSION_INFO a real ZCode.exe carries:
// the fixed 52-byte VS_FIXEDFILEINFO, whose dwFileVersionMS/LS hold 3.17.2.9661.
func zcodeVersionResource(major, minor, patch, build uint16) []byte {
	var key []byte
	for _, r := range "VS_VERSION_INFO" {
		key = append(key, byte(r), 0x00)
	}
	key = append(key, 0x00, 0x00)
	value := make([]byte, 52)
	binary.LittleEndian.PutUint32(value[0:], 0xFEEF04BD)
	binary.LittleEndian.PutUint32(value[8:], uint32(major)<<16|uint32(minor))
	binary.LittleEndian.PutUint32(value[12:], uint32(patch)<<16|uint32(build))
	valOffset := 6 + len(key)
	for (valOffset)%4 != 0 {
		valOffset++
	}
	total := valOffset + len(value)
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[0:], uint16(total))
	binary.LittleEndian.PutUint16(buf[2:], uint16(len(value)))
	copy(buf[6:], key)
	copy(buf[valOffset:], value)
	return buf
}

func TestZCodeVersionFromRSRC(t *testing.T) {
	orig := zcodeDesktopVersion
	defer func() { zcodeDesktopVersion = orig }()

	for _, tc := range []struct {
		name                string
		major, minor, patch uint16
		want                [3]int
	}{
		{"3.17.2.9661", 3, 17, 2, [3]int{3, 17, 2}},
		{"3.16.9.1", 3, 16, 9, [3]int{3, 16, 9}},
		{"3.14.4.0", 3, 14, 4, [3]int{3, 14, 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := zcodeVersionFromRSRC(zcodeVersionResource(tc.major, tc.minor, tc.patch, 9661))
			if !ok || got != tc.want {
				t.Errorf("version = %v, %v; want %v, true", got, ok, tc.want)
			}
		})
	}
	if _, ok := zcodeVersionFromRSRC([]byte(`{"not":"a resource"}`)); ok {
		t.Error("random bytes were accepted as a version resource")
	}

	// The threshold itself: 3.17+ is a live config, 3.14.4 is not.
	if versionBefore([3]int{3, 17, 0}, zcodeConfigJSONSince) {
		t.Error("3.17.0 should not be before the threshold")
	}
	if !versionBefore([3]int{3, 16, 9}, zcodeConfigJSONSince) {
		t.Error("3.16.9 should be before the threshold")
	}
}
