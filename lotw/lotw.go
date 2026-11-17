// Package lotw signs and uploads a log with the tqsl program, and downloads
// confirmations from LoTW's report service.
//
// Linking TrustedQSL would require cgo. This tree builds with CGO_ENABLED=0,
// so the upload path is the command line documented at
// https://lotw.arrl.org/lotw-help/cmdline/ :
//
//	tqsl -d -u -a compliant -x -l "<station location>" file.adi
//
//	-d  suppress the QSO date-range dialog
//	-u  upload the signed file instead of leaving a .tq8
//	-a compliant  sign every QSO that is not a duplicate and not outside
//	    the station location's date range
//	-x  batch mode: write status to stderr and exit (do not open the GUI)
//	-l  station location name, as TQSL shows it
//
// -n does not mean batch. It checks for a new TQSL and for expiring
// certificates, then exits without signing. The ARRL example also shows
// "-a new", which is not in the option list (ask, abort, compliant, all);
// this package uses compliant.
//
// -q is the other batch switch. -p passes the certificate passphrase on the
// command line, where ps can see it. SignUploadArgs does not use -p. Use a
// callsign certificate with no passphrase. The LoTW website password is a
// different secret, used only by the report download.
//
// Confirmations: HTTPS GET
// https://lotw.arrl.org/lotwuser/lotwreport.adi
// with login, password, qso_query=1. qso_qsl=yes (the default) returns QSL
// records; qso_qslsince limits them. qso_qsl=no and qso_qsorxsince return
// QSOs LoTW accepted. The password has to be a query parameter; that is the
// API. Callers must not log the request URL. RedactURL strips it.
//
// This package does not run tqsl and does not dial LoTW.
package lotw

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Report is the ADIF download LoTW serves.
const ReportURL = "https://lotw.arrl.org/lotwuser/lotwreport.adi"

// SignUploadArgs is the argv for one unattended upload, not including the
// tqsl program name. station is the TQSL station location. adi is the ADIF
// file. The returned slice never contains -p or -n.
func SignUploadArgs(station, adi string) ([]string, error) {
	if stringsTrim(station) == "" || stringsTrim(adi) == "" {
		return nil, errors.New("lotw: station location and ADIF path are required")
	}
	return []string{"-d", "-u", "-a", "compliant", "-x", "-l", station, adi}, nil
}

// Query selects the lotwreport.adi download.
// Confirmed true asks for QSL records (qso_qsl=yes) since Since.
// Confirmed false asks for uploaded QSOs (qso_qsl=no) received since Since.
type Query struct {
	Confirmed bool
	Since     time.Time
	OwnCall   string
	Call      string
	Mode      string
	Band      string
	DXCC      int
	WithOwn   bool
}

// ReportRequest builds the confirmation download. user and password are the
// LoTW website credentials, not the certificate passphrase.
func ReportRequest(user, password string, q Query) (*http.Request, error) {
	if stringsTrim(user) == "" || password == "" {
		return nil, errors.New("lotw: login and password are required")
	}
	u, err := url.Parse(ReportURL)
	if err != nil {
		return nil, err
	}
	v := url.Values{}
	v.Set("login", user)
	v.Set("password", password)
	v.Set("qso_query", "1")
	v.Set("qso_withown", boolYN(q.WithOwn))
	stamp := ""
	if !q.Since.IsZero() {
		stamp = q.Since.UTC().Format("2006-01-02 15:04:05")
	}
	if q.Confirmed {
		v.Set("qso_qsl", "yes")
		if stamp != "" {
			v.Set("qso_qslsince", stamp)
		}
	} else {
		v.Set("qso_qsl", "no")
		if stamp != "" {
			v.Set("qso_qsorxsince", stamp)
		}
	}
	if q.OwnCall != "" {
		v.Set("qso_owncall", q.OwnCall)
	}
	if q.Call != "" {
		v.Set("qso_callsign", q.Call)
	}
	if q.Mode != "" {
		v.Set("qso_mode", q.Mode)
	}
	if q.Band != "" {
		v.Set("qso_band", q.Band)
	}
	if q.DXCC > 0 {
		v.Set("qso_dxcc", strconv.Itoa(q.DXCC))
	}
	u.RawQuery = v.Encode()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "CWConsole/1")
	return req, nil
}

func boolYN(on bool) string {
	if on {
		return "yes"
	}
	return ""
}

// RedactURL hides the LoTW password so a log line can show the request.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "lotw:(unparsed url)"
	}
	q := u.Query()
	if q.Has("password") {
		q.Set("password", "REDACTED")
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func stringsTrim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
