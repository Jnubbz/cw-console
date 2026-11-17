// Package qrz talks to the QRZ XML callbook and, as a body builder only, to
// the QRZ Logbook API.
//
// XML (https://www.qrz.com/docs/xml/current_spec.html, spec 1.34):
// POST https://xmldata.qrz.com/xml/current/ with username, password and agent.
// The answer carries a session Key. Later calls send that key as s, plus
// callsign. The spec also allows GET. This client uses POST so the password
// is not placed in a URL, where an access log would keep it. A response with
// no Key means the session is dead and the caller must log in again.
// Full name, address, state and grid require a QRZ subscription; a
// non-subscriber still gets a Session Message, which Card.Message carries.
//
// Logbook (https://www.qrz.com/docs/logbook/QRZLogbookAPI.html) is a different
// credential: an opaque logbook access key, not the XML password. INSERT,
// DELETE, STATUS and FETCH are documented as requiring a subscription.
// Endpoint https://logbook.qrz.com/api, HTTP POST, KEY and ACTION on every
// call, ADIF in the ADIF field. This file builds that body. It does not insert
// a contact; phase 5 does, from logbook.Format output.
package qrz

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Jnubbz/cw-console/lookup"
)

// Endpoint is the QRZ XML service. LogbookEndpoint is the logbook API.
const (
	Endpoint        = "https://xmldata.qrz.com/xml/current/"
	LogbookEndpoint = "https://logbook.qrz.com/api"
	Agent           = "CWConsole/1"
)

// ErrSession means the XML session key is missing or rejected. The caller
// logs in again. ErrNotFound is lookup.ErrNotFound.
var ErrSession = errors.New("qrz: session expired")

// Client is one operator's XML session. Password is held in memory for a
// re-login. Do not log the Client, and do not put it in a status JSON.
type Client struct {
	Endpoint string
	Agent    string
	HTTP     *http.Client
	User     string
	Password string
	Session  string
}

func (c *Client) endpoint() string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	return Endpoint
}

func (c *Client) agent() string {
	if c.Agent != "" {
		return c.Agent
	}
	return Agent
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Login exchanges the username and password for a session key.
func (c *Client) Login(ctx context.Context) error {
	if c.User == "" || c.Password == "" {
		return errors.New("qrz: username and password are required")
	}
	body := url.Values{}
	body.Set("username", c.User)
	body.Set("password", c.Password)
	body.Set("agent", c.agent())
	doc, err := c.post(ctx, body)
	if err != nil {
		return err
	}
	if doc.Session.Key == "" {
		if doc.Session.Error != "" {
			return fmt.Errorf("qrz: %s", doc.Session.Error)
		}
		return ErrSession
	}
	c.Session = doc.Session.Key
	return nil
}

// Lookup implements lookup.Source. It logs in when there is no session, and
// once more if QRZ drops the key. The password is sent only to Login.
func (c *Client) Lookup(ctx context.Context, call string) (lookup.Card, error) {
	call = strings.ToUpper(strings.TrimSpace(call))
	if call == "" {
		return lookup.Card{}, fmt.Errorf("qrz: empty callsign")
	}
	if c.Session == "" {
		if err := c.Login(ctx); err != nil {
			return lookup.Card{}, err
		}
	}
	card, err := c.fetch(ctx, call)
	if errors.Is(err, ErrSession) {
		c.Session = ""
		if err := c.Login(ctx); err != nil {
			return lookup.Card{}, err
		}
		return c.fetch(ctx, call)
	}
	return card, err
}

func (c *Client) fetch(ctx context.Context, call string) (lookup.Card, error) {
	body := url.Values{}
	body.Set("s", c.Session)
	body.Set("callsign", call)
	doc, err := c.post(ctx, body)
	if err != nil {
		return lookup.Card{}, err
	}
	return doc.card()
}

func (c *Client) post(ctx context.Context, body url.Values) (database, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), strings.NewReader(body.Encode()))
	if err != nil {
		return database{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.agent())
	resp, err := c.http().Do(req)
	if err != nil {
		return database{}, err
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return database{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return database{}, fmt.Errorf("qrz: HTTP %s", resp.Status)
	}
	var doc database
	if err := xml.Unmarshal(buf, &doc); err != nil {
		return database{}, fmt.Errorf("qrz: %v", err)
	}
	return doc, nil
}

type database struct {
	XMLName xml.Name `xml:"QRZDatabase"`
	Session struct {
		Key     string `xml:"Key"`
		Error   string `xml:"Error"`
		Message string `xml:"Message"`
	} `xml:"Session"`
	Callsign struct {
		Call    string `xml:"call"`
		Fname   string `xml:"fname"`
		Name    string `xml:"name"`
		State   string `xml:"state"`
		Country string `xml:"country"`
		DXCC    string `xml:"dxcc"`
		Grid    string `xml:"grid"`
		County  string `xml:"county"`
		CQZone  string `xml:"cqzone"`
		ITUZone string `xml:"ituzone"`
	} `xml:"Callsign"`
}

func (d database) card() (lookup.Card, error) {
	if d.Session.Key == "" {
		return lookup.Card{}, ErrSession
	}
	cs := d.Callsign
	if cs.Call == "" {
		errText := strings.ToLower(d.Session.Error)
		if strings.Contains(errText, "not found") {
			return lookup.Card{}, lookup.ErrNotFound
		}
		if d.Session.Error != "" {
			return lookup.Card{}, fmt.Errorf("qrz: %s", d.Session.Error)
		}
		return lookup.Card{}, lookup.ErrNotFound
	}
	dxcc, _ := strconv.Atoi(strings.TrimSpace(cs.DXCC))
	cq, _ := strconv.Atoi(strings.TrimSpace(cs.CQZone))
	itu, _ := strconv.Atoi(strings.TrimSpace(cs.ITUZone))
	name := strings.TrimSpace(strings.TrimSpace(cs.Fname) + " " + strings.TrimSpace(cs.Name))
	return lookup.Card{
		Call: strings.ToUpper(cs.Call), Name: name, Country: cs.Country,
		State: strings.ToUpper(cs.State), Grid: strings.ToUpper(cs.Grid),
		County: cs.County, DXCC: dxcc, CQZone: cq, ITUZone: itu,
		Source: "qrz", Message: d.Session.Message,
	}, nil
}

// InsertBody is the POST body for one QRZ Logbook INSERT.
// replace sets OPTION=REPLACE, which overwrites a duplicate, including a
// confirmed one. Leave it false for a normal log.
func InsertBody(apiKey, adif string, replace bool) (string, error) {
	if apiKey == "" {
		return "", errors.New("qrz: logbook key is required")
	}
	if strings.TrimSpace(adif) == "" {
		return "", errors.New("qrz: empty ADIF")
	}
	v := url.Values{}
	v.Set("KEY", apiKey)
	v.Set("ACTION", "INSERT")
	v.Set("ADIF", adif)
	if replace {
		v.Set("OPTION", "REPLACE")
	}
	return v.Encode(), nil
}

// FetchBody is a paged FETCH. QRZ asks clients to page large logs
// (MAX and AFTERLOGID) rather than download a book in one call.
func FetchBody(apiKey string, afterLogID, max int) (string, error) {
	if apiKey == "" {
		return "", errors.New("qrz: logbook key is required")
	}
	if max <= 0 {
		max = 250
	}
	v := url.Values{}
	v.Set("KEY", apiKey)
	v.Set("ACTION", "FETCH")
	v.Set("OPTION", fmt.Sprintf("TYPE:ADIF,MAX:%d,AFTERLOGID:%d", max, afterLogID))
	return v.Encode(), nil
}
