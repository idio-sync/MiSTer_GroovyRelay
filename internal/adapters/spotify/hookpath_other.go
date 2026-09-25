//go:build !windows

package spotify

import (
	"fmt"
	"strings"
)

// hookProgram returns exe in a form librespot's --onevent can run.
// librespot splits the value on whitespace with no shell, so the path
// itself must not contain any.
func hookProgram(exe string) (string, error) {
	if strings.ContainsAny(exe, " \t") {
		return "", fmt.Errorf("bridge binary path %q contains whitespace, which librespot's --onevent cannot run; move the binary", exe)
	}
	return exe, nil
}
