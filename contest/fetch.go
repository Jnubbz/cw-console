package contest

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
)

// CTYURL is Jim Reisert AD1C's current country file.
const CTYURL = "https://www.country-files.com/cty/cty.dat"

// ErrNotNewer means the downloaded file parsed and is not newer than the
// version already in use. The file on disk is left alone.
var ErrNotNewer = errors.New("cty.dat is not newer than the one already in use")

// InstallCTY writes body into dir/cty.dat when it is a country file and its
// version is newer than current (YYYYMMDD, "" if nothing is installed yet).
// The previous cty.dat is kept as cty.dat.bak. A file that does not parse, or
// that is not newer, does not replace anything.
func InstallCTY(dir string, body []byte, current string) (version string, err error) {
	c, err := ParseCTY(bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	if !newerVersion(c.Version, current) {
		return c.Version, ErrNotNewer
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, "cty.dat")
	tmp := dest + ".tmp"
	bak := dest + ".bak"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return "", err
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		_ = os.Remove(bak)
		if err := os.Rename(dest, bak); err != nil {
			_ = os.Remove(tmp)
			return "", err
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Rename(bak, dest)
		return "", err
	}
	return c.Version, nil
}

func newerVersion(next, cur string) bool {
	if next == "" || next == cur {
		return false
	}
	if cur == "" {
		return true
	}
	return next > cur
}
