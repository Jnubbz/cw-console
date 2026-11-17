package qrz

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jnubbz/cw-console/lookup"
)

const loginXML = `<?xml version="1.0" ?>
<QRZDatabase version="1.34">
  <Session>
    <Key>session-key</Key>
    <Count>1</Count>
    <SubExp>non-subscriber</SubExp>
  </Session>
</QRZDatabase>`

const callXML = `<?xml version="1.0" ?>
<QRZDatabase version="1.34">
  <Session>
    <Key>session-key</Key>
    <Message>A subscription is required to obtain the complete data</Message>
  </Session>
  <Callsign>
    <call>W1AW</call>
    <fname>ARRL</fname>
    <name>HQ</name>
    <state>CT</state>
    <country>United States</country>
    <dxcc>291</dxcc>
    <grid>FN31pr</grid>
  </Callsign>
</QRZDatabase>`

func TestLookupPostsThePasswordAndNotInTheURL(t *testing.T) {
	var sawLogin, sawCall bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("password leaked into the URL: %s", r.URL.RawQuery)
		}
		buf, _ := io.ReadAll(r.Body)
		form := string(buf)
		if strings.Contains(form, "username=") {
			sawLogin = true
			if !strings.Contains(form, "password=s3cret") || !strings.Contains(form, "agent=") {
				t.Errorf("login body %s", form)
			}
			_, _ = io.WriteString(w, loginXML)
			return
		}
		sawCall = true
		if strings.Contains(form, "s3cret") {
			t.Errorf("password was sent again on lookup: %s", form)
		}
		if !strings.Contains(form, "s=session-key") || !strings.Contains(form, "callsign=W1AW") {
			t.Errorf("lookup body %s", form)
		}
		_, _ = io.WriteString(w, callXML)
	}))
	defer srv.Close()

	c := &Client{Endpoint: srv.URL, HTTP: srv.Client(), User: "KN0BLE", Password: "s3cret"}
	var src lookup.Source = c
	card, err := src.Lookup(context.Background(), "w1aw")
	if err != nil {
		t.Fatal(err)
	}
	if !sawLogin || !sawCall {
		t.Fatalf("login %v lookup %v", sawLogin, sawCall)
	}
	if card.Call != "W1AW" || card.State != "CT" || card.DXCC != 291 || card.Country != "United States" {
		t.Fatalf("%+v", card)
	}
	if card.Name != "ARRL HQ" || !strings.Contains(card.Message, "subscription") {
		t.Fatalf("%+v", card)
	}
	if c.Session != "session-key" {
		t.Fatalf("session %q", c.Session)
	}
}

func TestMissingKeyIsADeadSession(t *testing.T) {
	doc := database{}
	doc.Session.Error = "session timeout"
	if _, err := doc.card(); !errorsIsSession(err) {
		t.Fatalf("got %v", err)
	}
}

func errorsIsSession(err error) bool {
	return err != nil && strings.Contains(err.Error(), "session")
}

func TestInsertBodyOmitsReplaceUnlessAsked(t *testing.T) {
	s, err := InsertBody("ABCD-KEY", "<CALL:4>W1AW <EOR>", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s, "REPLACE") {
		t.Fatalf("replace was set: %s", s)
	}
	if !strings.Contains(s, "ACTION=INSERT") || !strings.Contains(s, "KEY=ABCD-KEY") {
		t.Fatalf("%s", s)
	}
	if _, err := InsertBody("", "<EOR>", false); err == nil {
		t.Fatal("empty key accepted")
	}
}

func TestFetchBodyPages(t *testing.T) {
	s, err := FetchBody("ABCD-KEY", 100, 250)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "ACTION=FETCH") || !strings.Contains(s, "AFTERLOGID%3A100") && !strings.Contains(s, "AFTERLOGID:100") {
		t.Fatalf("%s", s)
	}
}
