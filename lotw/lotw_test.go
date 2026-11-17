package lotw

import (
	"strings"
	"testing"
	"time"
)

func TestSignUploadArgsAreBatchAndHaveNoPassword(t *testing.T) {
	args, err := SignUploadArgs("Home QTH", "/tmp/log.adi")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	for _, flag := range []string{"-d", "-u", "-a compliant", "-x", "-l Home QTH"} {
		if !strings.Contains(got, flag) {
			t.Fatalf("missing %s in %s", flag, got)
		}
	}
	for _, banned := range []string{"-p", "-n"} {
		for _, a := range args {
			if a == banned {
				t.Fatalf("argv contains %s: %s", banned, got)
			}
		}
	}
	if args[len(args)-1] != "/tmp/log.adi" {
		t.Fatalf("adi path not last: %v", args)
	}
	if _, err := SignUploadArgs("", "x"); err == nil {
		t.Fatal("empty station accepted")
	}
}

func TestReportRequestAndRedaction(t *testing.T) {
	since := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	req, err := ReportRequest("KN0BLE", "website-secret", Query{
		Confirmed: true, Since: since, WithOwn: true, OwnCall: "KN0BLE", Band: "20m",
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.Host != "lotw.arrl.org" || !strings.HasPrefix(req.URL.Path, "/lotwuser/lotwreport.adi") {
		t.Fatalf("url %s", req.URL)
	}
	q := req.URL.Query()
	if q.Get("qso_query") != "1" || q.Get("qso_qsl") != "yes" || q.Get("password") != "website-secret" {
		t.Fatalf("%v", q)
	}
	if q.Get("qso_qslsince") != "2026-01-02 03:04:05" || q.Get("qso_withown") != "yes" || q.Get("qso_band") != "20m" {
		t.Fatalf("%v", q)
	}
	shown := RedactURL(req.URL.String())
	if strings.Contains(shown, "website-secret") || !strings.Contains(shown, "REDACTED") {
		t.Fatalf("redaction failed: %s", shown)
	}
}

func TestUploadQueryUsesTheOtherSinceParameter(t *testing.T) {
	req, err := ReportRequest("KN0BLE", "pw", Query{Confirmed: false, Since: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	q := req.URL.Query()
	if q.Get("qso_qsl") != "no" || q.Get("qso_qsorxsince") == "" || q.Get("qso_qslsince") != "" {
		t.Fatalf("%v", q)
	}
}
