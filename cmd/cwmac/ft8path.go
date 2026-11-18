package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// The FT8 engine shells out to WSJT-X's jt9 decoder and to the ft8tx helper the
// Pi image builds from WSJT-X's encoder. Neither binary is shipped here: WSJT-X
// is GPL-3.0, this program is AGPL-3.0, and a desktop install already has jt9
// inside WSJT-X. Running that separate program is not a copy of it.
//
// CWBOX_JT9 and CWBOX_FT8TX still win, so the Pi paths stay put.

func init() {
	if os.Getenv("CWBOX_JT9") == "" {
		if p := firstRegular(jt9Candidates()); p != "" {
			jt9Path = p
		}
	}
	if os.Getenv("CWBOX_FT8TX") == "" {
		if p := firstRegular(ft8txCandidates()); p != "" {
			ft8txPath = p
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

func ft8txCandidates() []string {
	name := "ft8tx"
	if runtime.GOOS == "windows" {
		name = "ft8tx.exe"
	}
	out := lookPaths([]string{name})
	if dir := filepath.Dir(jt9Path); dir != "" && dir != "." {
		out = append(out, filepath.Join(dir, name))
	}
	if exe, err := os.Executable(); err == nil {
		out = append(out, filepath.Join(filepath.Dir(exe), name))
	}
	out = append(out, "/usr/bin/"+name, "/usr/local/bin/"+name)
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
			return "FT8 will decode (jt9 was found). Transmit audio needs the ft8tx helper next to jt9, or CWBOX_FT8TX pointing at it. The POTA Pi image already has it. WSJT-X itself does not include ft8tx."
		}
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return "Install WSJT-X from https://wsjt.sourceforge.io/wsjtx.html so /Applications/wsjtx.app is present, then restart CW Console. This program uses that app's jt9 decoder and does not ship a copy."
	case "windows":
		return `Install WSJT-X so jt9.exe is at C:\WSJT\wsjtx\bin\jt9.exe (or on the PATH), then restart. This program does not ship a copy of jt9.`
	default:
		return "Install WSJT-X so jt9 is on the PATH (the wsjtx package, or the binary from https://wsjt.sourceforge.io/wsjtx.html), then restart CW Console. This program does not ship a copy of jt9."
	}
}

// alsaDev is a name aplay -D understands. Browser device ids are not.
func alsaDev(dev string) bool {
	if dev == "" || strings.Contains(dev, " ") {
		return false
	}
	return strings.Contains(dev, ":") || dev == "default" || strings.HasPrefix(dev, "sysdefault") || strings.HasPrefix(dev, "plughw") || strings.HasPrefix(dev, "hw")
}
