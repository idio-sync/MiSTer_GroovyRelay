//go:build windows

package spotify

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// hookProgram returns exe in a form librespot's --onevent can run.
// librespot splits the value on whitespace with no shell, so a path like
// C:\Program Files\... is rewritten to its 8.3 short form.
func hookProgram(exe string) (string, error) {
	if !strings.ContainsAny(exe, " \t") {
		return exe, nil
	}
	long, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		return "", fmt.Errorf("bridge binary path %q contains spaces and has no short (8.3) name for librespot's --onevent: %v", exe, err)
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.ContainsAny(short, " \t") {
		return "", fmt.Errorf("bridge binary path %q contains spaces, which librespot's --onevent cannot run; move the binary", exe)
	}
	return short, nil
}
