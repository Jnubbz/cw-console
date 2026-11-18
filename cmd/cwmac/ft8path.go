package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// The FT8 engine shells out to WSJT-X's jt9 decoder. Transmit audio is generated
// in this program. Neither WSJT-X nor the Pi's ft8tx helper is shipped here:
// WSJT-X is GPL-3.0, this program is AGPL-3.0, and a desktop install already has
// jt9 inside WSJT-X. Running that separate program is not a copy of it.
//
// CWBOX_JT9 still wins for the decoder. CWBOX_FT8TX, when set, uses that helper
// instead of the built-in encoder (the Pi can keep its image's ft8tx that way).

func init() {
	if os.Getenv("CWBOX_JT9") == "" {
		if p := firstRegular(jt9Candidates()); p != "" {
			jt9Path = p
		}
	}
	if os.Getenv("CWBOX_FT8DIR") == "" && runtime.GOOS == "windows" {
		ft8Dir = filepath.Join(os.TempDir(), "cw-console-ft8")
	}
}

func firstRegular(paths []string) string {
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

func jt9Candidates() []string {
	names := []string{"jt9"}
	if runtime.GOOS == "windows" {
		names = []string{"jt9.exe"}
	}
	out := lookPaths(names)
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		out = append(out,
			"/Applications/wsjtx.app/Contents/MacOS/jt9",
			filepath.Join(home, "Applications/wsjtx.app/Contents/MacOS/jt9"),
			"/opt/homebrew/bin/jt9",
			"/usr/local/bin/jt9",
		)
	case "windows":
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), `C:\WSJT`} {
			if root == "" {
				continue
			}
			out = append(out,
				filepath.Join(root, "WSJT-X", "bin", "jt9.exe"),
				filepath.Join(root, "wsjtx", "bin", "jt9.exe"),
			)
		}
	default:
		out = append(out, "/usr/bin/jt9", "/usr/local/bin/jt9")
	}
	return out
}

func lookPaths(names []string) []string {
	var out []string
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// ft8InstallHint is what the page shows when the FT8 button cannot switch.
// An empty string means the decoder was found.
func ft8InstallHint() string {
	if ft8Available() {
		if !ft8TxAvailable() {
			return "FT8 will decode (jt9 was found). CWBOX_FT8TX is set but that encoder was not found, so transmit is off. Unset it to use the built-in encoder."
		}
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return "Install WSJT-X from https://wsjt.sourceforge.io/wsjtx.html so /Applications/wsjtx.app is present, then restart CW Console. Decoding uses that app's jt9. Transmit is built in. This program does not ship a copy of jt9."
	case "windows":
		return `Install WSJT-X so jt9.exe is at C:\WSJT\wsjtx\bin\jt9.exe (or on the PATH), then restart. Decoding uses that jt9. Transmit is built in. This program does not ship a copy of jt9.`
	default:
		return "Install WSJT-X so jt9 is on the PATH (the wsjtx package, or the binary from https://wsjt.sourceforge.io/wsjtx.html), then restart CW Console. Decoding uses that jt9. Transmit is built in. This program does not ship a copy of jt9."
	}
}

// alsaDev is a name aplay -D understands. Browser device ids are not.
func alsaDev(dev string) bool {
	if dev == "" || strings.Contains(dev, " ") {
		return false
	}
	return strings.Contains(dev, ":") || dev == "default" || strings.HasPrefix(dev, "sysdefault") || strings.HasPrefix(dev, "plughw") || strings.HasPrefix(dev, "hw")
}
