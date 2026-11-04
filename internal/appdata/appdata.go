// Package appdata: where CW Console keeps settings, macros and logs.
//
// CW Console grew out of CW Decoder, which may still be installed. The two are separate
// apps with separate folders, so both can be used. The first time CW Console runs, it
// starts from a copy of CW Decoder's folder (settings, macros, logs); CW Decoder's own
// folder is left as it is.
package appdata

import (
	"io"
	"os"
	"path/filepath"
)

const (
	Name    = "CW Console"
	OldName = "CW Decoder"
)

// Dir: base/CW Console (base is os.UserConfigDir()), seeded from base/CW Decoder the first
// time. Never fails: a failed copy just starts fresh.
func Dir(base string) string {
	dir := filepath.Join(base, Name)
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	old := filepath.Join(base, OldName)
	if st, err := os.Stat(old); err == nil && st.IsDir() {
		tmp := dir + ".copying"
		os.RemoveAll(tmp)
		if copyTree(old, tmp) == nil && os.Rename(tmp, dir) == nil {
			return dir
		}
		os.RemoveAll(tmp)
	}
	os.MkdirAll(dir, 0o755)
	return dir
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		t := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(t, 0o755)
		case !info.Mode().IsRegular():
			return nil // links and the like: skip
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(t, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
