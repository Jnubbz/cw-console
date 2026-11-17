// Package secrets stores operator credentials outside the repository.
//
// The file is JSON in the app data directory (internal/appdata), mode 0600
// on Unix. The keys are the constants below. The web UI may set them through
// a POST and must not receive them back; a later settings handler reports
// only whether a key is present. Environment variables are not read here.
// The plan describes which variable overrides which key on the Pi, where the
// SD card is easier to copy than a desktop home directory.
//
// A missing file is an empty store. A file this process did not write, or a
// file with a wider mode on Unix, is still read after the mode is tightened,
// so an operator who created it by hand is not locked out. The values are
// never included in an error string.
package secrets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Key names stored in the file. QRZ XML (username and password) is a different
// credential from the QRZ Logbook access key. The LoTW website password is a
// different credential from the TQSL certificate, which is not stored here.
const (
	QRZUsername   = "qrz.username"
	QRZPassword   = "qrz.password"
	QRZLogbookKey = "qrz.logbook_key"
	QRZSession    = "qrz.session_key"
	LoTWUsername  = "lotw.username"
	LoTWPassword  = "lotw.password"
	LoTWStation   = "lotw.station"
	WRLAPIKey     = "wrl.api_key"
)

// File is a 0600 JSON map. Get and Set are safe for concurrent use.
type File struct {
	path string
	mu   sync.Mutex
}

type document struct {
	Version int               `json:"version"`
	Values  map[string]string `json:"values"`
}

// Open reads path, creating it mode 0600 if it does not exist.
func Open(path string) (*File, error) {
	if path == "" {
		return nil, errors.New("secrets: empty path")
	}
	f := &File{path: path}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		if err := f.write(document{Version: 1, Values: map[string]string{}}); err != nil {
			return nil, err
		}
		return f, nil
	} else if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	if _, err := f.read(); err != nil {
		return nil, err
	}
	return f, nil
}

// Set stores key. An empty value deletes it.
func (f *File) Set(key, value string) error {
	if key == "" {
		return errors.New("secrets: empty key")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.read()
	if err != nil {
		return err
	}
	if doc.Values == nil {
		doc.Values = map[string]string{}
	}
	if value == "" {
		delete(doc.Values, key)
	} else {
		doc.Values[key] = value
	}
	doc.Version = 1
	return f.write(doc)
}

// Get returns the value. The second result is false when it is unset.
func (f *File) Get(key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.read()
	if err != nil {
		return "", false, err
	}
	v, ok := doc.Values[key]
	if !ok || v == "" {
		return "", false, nil
	}
	return v, true, nil
}

// Has reports whether key is set, without returning the value. Status JSON
// uses this.
func (f *File) Has(key string) (bool, error) {
	_, ok, err := f.Get(key)
	return ok, err
}

func (f *File) read() (document, error) {
	b, err := os.ReadFile(f.path)
	if err != nil {
		return document{}, err
	}
	if len(b) == 0 {
		return document{Version: 1, Values: map[string]string{}}, nil
	}
	var doc document
	if err := json.Unmarshal(b, &doc); err != nil {
		return document{}, err
	}
	if doc.Values == nil {
		doc.Values = map[string]string{}
	}
	return doc, nil
}

func (f *File) write(doc document) error {
	if doc.Values == nil {
		doc.Values = map[string]string{}
	}
	buf, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, f.path)
}
