package ffmpeg

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// findFFBinary locates ffmpeg/ffprobe across platforms. Returns "" if the
// binary can't be found. On Windows the Go runtime only sees the Windows
// PATH, which often omits MSYS/Git-Bash wrapper directories, so we probe a
// handful of known install locations.
func findFFBinary(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		exe := name
		if filepath.Ext(exe) == "" {
			exe += ".exe"
		}
		candidates := []string{
			`C:\Users\Jake\sdk\ffmpeg-8.1-essentials_build\bin\` + exe,
			`C:\ffmpeg\bin\` + exe,
			`C:\Program Files\ffmpeg\bin\` + exe,
		}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates,
				filepath.Join(home, "sdk", "ffmpeg-8.1-essentials_build", "bin", exe),
				filepath.Join(home, "scoop", "apps", "ffmpeg", "current", "bin", exe),
			)
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	return ""
}
