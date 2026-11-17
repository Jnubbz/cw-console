// Package wrl logs contacts to World Radio League.
//
// WRL has a public HTTP API. Version 1.1.0 was read from
// https://api.worldradioleague.com/v1/llms.txt on 2026-11-17. The human docs
// are https://worldradioleague.com/developer/ and the OpenAPI document is
// https://api.worldradioleague.com/v1/openapi.json. A key is created under
// Integrations, Developer API. It is shown once. Send it as
// Authorization: Bearer wrl_live_... (or X-API-Key). The responses carry no
// CORS headers, so the browser must not call WRL; this process does.
//
// POST /v1/contacts accepts one contact. An array is rejected with 413.
// programId is required (use ProgramID). Field names are ADIF names in
// camelCase: call, freq (MHz), band ("20m"), mode, rstSent, rstRcvd,
// gridsquare, state, stationCallsign, dxcc. mode is the specific mode: "FT8",
// not "MFSK" plus a submode. timestamp is RFC3339 UTC. Unknown fields are
// rejected. logbookId is optional and selects a logbook; omit it to use the
// account default. GET /v1/me returns that default and the quota.
//
// Rate limits published in that document: 60 writes/min, 120 reads/min,
// 5000 writes/day, 20000 reads/day. 429 carries Retry-After in seconds.
//
// Bulk upload is ADIF, three steps: POST /v1/imports returns an upload URL,
// PUT the .adi file to that URL with no API key, then poll GET /v1/imports/{id}.
// One import at a time per account. The file limit is 10 MB.
//
// Activities (POTA and the others) go in myActivities and theirActivities as
// {type, ref, isPrimary}, not in the ADIF MY_SIG tags. A reference WRL does
// not know is a 422 and the contact is not stored.
//
// This package builds those requests. It does not log a contact on its own.
package wrl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BaseURL is the API origin. ProgramID is the ADIF-style program name sent
// on every contact and import.
const (
	BaseURL   = "https://api.worldradioleague.com"
	ProgramID = "CWConsole"
)

// Activity is one park, summit or island. Type is "POTA", "SOTA", "IOTA" or
// "WWFF". Ref is the designator ("US-0662"). Primary marks the one WRL pins.
type Activity struct {
	Type    string `json:"type,omitempty"`
	Ref     string `json:"ref"`
	Primary bool   `json:"isPrimary,omitempty"`
}

// Contact is one QSO. FreqMHz is the ADIF frequency in megahertz (14.074).
// LogbookID empty means the account's default logbook.
type Contact struct {
	LogbookID       string
	Call            string
	At              time.Time
	FreqMHz         float64
	Band            string
	Mode            string
	RSTSent         string
	RSTRcvd         string
	Grid            string
	State           string
	StationCall     string
	DXCC            int
	MyActivities    []Activity
	TheirActivities []Activity
}

type wire struct {
	LogbookID       string     `json:"logbookId,omitempty"`
	ProgramID       string     `json:"programId"`
	Call            string     `json:"call"`
	Timestamp       string     `json:"timestamp"`
	Freq            *float64   `json:"freq,omitempty"`
	Band            string     `json:"band,omitempty"`
	Mode            string     `json:"mode,omitempty"`
	RSTSent         string     `json:"rstSent,omitempty"`
	RSTRcvd         string     `json:"rstRcvd,omitempty"`
	Grid            string     `json:"gridsquare,omitempty"`
	State           string     `json:"state,omitempty"`
	StationCall     string     `json:"stationCallsign,omitempty"`
	DXCC            int        `json:"dxcc,omitempty"`
	MyActivities    []Activity `json:"myActivities,omitempty"`
	TheirActivities []Activity `json:"theirActivities,omitempty"`
}

// Body encodes one contact. Mode is sent as given: pass "FT8" or "CW" or
// "SSTV", not an ADIF mode/submode pair.
func Body(c Contact) ([]byte, error) {
	call := strings.ToUpper(strings.TrimSpace(c.Call))
	if call == "" {
		return nil, errors.New("wrl: call is required")
	}
	if c.At.IsZero() {
		return nil, errors.New("wrl: timestamp is required")
	}
	w := wire{
		LogbookID: c.LogbookID, ProgramID: ProgramID, Call: call,
		Timestamp: c.At.UTC().Format(time.RFC3339),
		Band:      c.Band, Mode: c.Mode, RSTSent: c.RSTSent, RSTRcvd: c.RSTRcvd,
		Grid: strings.ToUpper(c.Grid), State: strings.ToUpper(c.State),
		StationCall: strings.ToUpper(c.StationCall), DXCC: c.DXCC,
		MyActivities: c.MyActivities, TheirActivities: c.TheirActivities,
	}
	if c.FreqMHz > 0 {
		f := c.FreqMHz
		w.Freq = &f
	}
	return json.Marshal(w)
}

// Client calls the API. Key is the bearer token. It is not written into URLs.
type Client struct {
	Base string
	Key  string
	HTTP *http.Client
}

func (c *Client) base() string {
	if c.Base != "" {
		return strings.TrimRight(c.Base, "/")
	}
	return BaseURL
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (c *Client) auth(req *http.Request) error {
	if c.Key == "" {
		return errors.New("wrl: API key is required")
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Accept", "application/json")
	return nil
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Create stores one contact and returns its id.
func (c *Client) Create(ctx context.Context, contact Contact) (string, error) {
	buf, err := Body(contact)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/v1/contacts", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.auth(req); err != nil {
		return "", err
	}
	var data struct {
		ID string `json:"id"`
	}
	if err := c.do(req, &data); err != nil {
		return "", err
	}
	if data.ID == "" {
		return "", errors.New("wrl: contact response had no id")
	}
	return data.ID, nil
}

// Import is a bulk ADIF upload that has been started. PUT the file bytes to
// UploadURL with UploadADIF. That request must not carry the API key. Then
// poll Status until it is terminal.
type Import struct {
	ID        string
	UploadURL string
}

// StartImport begins a bulk upload. logbookID empty uses the default logbook.
func (c *Client) StartImport(ctx context.Context, logbookID string) (Import, error) {
	payload := map[string]string{"programId": ProgramID}
	if logbookID != "" {
		payload["logbookId"] = logbookID
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return Import{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/v1/imports", bytes.NewReader(buf))
	if err != nil {
		return Import{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.auth(req); err != nil {
		return Import{}, err
	}
	var data struct {
		ID     string `json:"id"`
		Upload struct {
			URL string `json:"url"`
		} `json:"upload"`
	}
	if err := c.do(req, &data); err != nil {
		return Import{}, err
	}
	if data.ID == "" || data.Upload.URL == "" {
		return Import{}, errors.New("wrl: import response had no upload url")
	}
	return Import{ID: data.ID, UploadURL: data.Upload.URL}, nil
}

// UploadADIF PUTs the ADIF file to the URL StartImport returned.
// The API key is not sent. httpClient may be nil.
func UploadADIF(ctx context.Context, httpClient *http.Client, uploadURL string, adi []byte) error {
	if uploadURL == "" {
		return errors.New("wrl: empty upload url")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(adi))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wrl: upload HTTP %s", resp.Status)
	}
	return nil
}

func (c *Client) do(req *http.Request, data any) error {
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(buf, &env); err != nil {
		return fmt.Errorf("wrl: HTTP %s", resp.Status)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("wrl: rate limited, retry after %s", resp.Header.Get("Retry-After"))
	}
	if env.Error != nil && env.Error.Code != "" {
		return fmt.Errorf("wrl: %s", env.Error.Code)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wrl: HTTP %s", resp.Status)
	}
	if data == nil || len(env.Data) == 0 {
		return nil
	}
	return json.Unmarshal(env.Data, data)
}
