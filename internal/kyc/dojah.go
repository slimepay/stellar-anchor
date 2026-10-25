// Package kyc verifies Nigerian identities (BVN or NIN) and screens customers
// against sanctions and PEP lists through Dojah.
package kyc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	StatusAccepted   = "ACCEPTED"
	StatusRejected   = "REJECTED"
	StatusProcessing = "PROCESSING"

	IDTypeBVN = "bvn"
	IDTypeNIN = "nin"
)

type Identity struct {
	FirstName string
	LastName  string
	BirthDate string // YYYY-MM-DD
	IDType    string
	IDNumber  string
}

// Decision is the outcome of a check. PROCESSING means a person has to review
// the customer before they can transact.
type Decision struct {
	Status string
	Reason string
}

type Verifier interface {
	// Verify returns an error only when Dojah could not give an answer, so the
	// check can be retried later.
	Verify(ctx context.Context, id Identity) (Decision, error)
}

type Testnet struct{}

func (Testnet) Verify(context.Context, Identity) (Decision, error) {
	return Decision{Status: StatusAccepted}, nil
}

type Dojah struct {
	baseURL   string
	appID     string
	secretKey string
	http      *http.Client
}

func NewDojah(baseURL, appID, secretKey string) *Dojah {
	return &Dojah{
		baseURL:   strings.TrimRight(baseURL, "/"),
		appID:     appID,
		secretKey: secretKey,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

var errNotFound = errors.New("identity not found")

func (d *Dojah) Verify(ctx context.Context, id Identity) (Decision, error) {
	var (
		dec Decision
		err error
	)
	switch id.IDType {
	case IDTypeBVN:
		dec, err = d.verifyBVN(ctx, id)
	case IDTypeNIN:
		dec, err = d.verifyNIN(ctx, id)
	default:
		return Decision{StatusRejected, "id_type must be bvn or nin"}, nil
	}
	if errors.Is(err, errNotFound) {
		return Decision{StatusRejected, "identity number not found"}, nil
	}
	if err != nil || dec.Status != StatusAccepted {
		return dec, err
	}
	return d.screen(ctx, id)
}

func (d *Dojah) verifyBVN(ctx context.Context, id Identity) (Decision, error) {
	q := url.Values{
		"bvn":        {id.IDNumber},
		"first_name": {id.FirstName},
		"last_name":  {id.LastName},
		"dob":        {id.BirthDate},
	}
	var out struct {
		Entity struct {
			FirstName *matchField `json:"first_name"`
			LastName  *matchField `json:"last_name"`
			DOB       *matchField `json:"dob"`
		} `json:"entity"`
	}
	if err := d.do(ctx, http.MethodGet, "/api/v1/kyc/bvn?"+q.Encode(), nil, &out); err != nil {
		return Decision{}, err
	}
	e := out.Entity
	if !e.FirstName.ok() || !e.LastName.ok() {
		return Decision{StatusRejected, "name does not match the BVN record"}, nil
	}
	if e.DOB != nil && !e.DOB.Status {
		return Decision{StatusRejected, "date of birth does not match the BVN record"}, nil
	}
	return Decision{Status: StatusAccepted}, nil
}

type matchField struct {
	Status bool `json:"status"`
}

func (m *matchField) ok() bool { return m != nil && m.Status }

func (d *Dojah) verifyNIN(ctx context.Context, id Identity) (Decision, error) {
	var out struct {
		Entity struct {
			FirstName   string `json:"first_name"`
			LastName    string `json:"last_name"`
			DateOfBirth string `json:"date_of_birth"`
		} `json:"entity"`
	}
	if err := d.do(ctx, http.MethodGet, "/api/v1/kyc/nin?"+url.Values{"nin": {id.IDNumber}}.Encode(), nil, &out); err != nil {
		return Decision{}, err
	}
	e := out.Entity
	if !sameName(e.FirstName, id.FirstName) || !sameName(e.LastName, id.LastName) {
		return Decision{StatusRejected, "name does not match the NIN record"}, nil
	}
	if recorded, ok := parseDate(e.DateOfBirth); ok {
		if given, ok := parseDate(id.BirthDate); !ok || !given.Equal(recorded) {
			return Decision{StatusRejected, "date of birth does not match the NIN record"}, nil
		}
	}
	return Decision{Status: StatusAccepted}, nil
}

func (d *Dojah) screen(ctx context.Context, id Identity) (Decision, error) {
	dob := ""
	if t, ok := parseDate(id.BirthDate); ok {
		dob = t.Format("02-01-2006")
	}
	body := map[string]any{
		"schema": "individual",
		"properties": map[string]string{
			"names":         strings.TrimSpace(id.FirstName + " " + id.LastName),
			"date_of_birth": dob,
			"nationality":   "Nigerian",
			"id_number":     "",
		},
		"screening_options": map[string]any{
			"pep_check":           true,
			"sanction":            true,
			"adverse_media_check": false,
			"watchlists":          []string{},
		},
	}
	var out struct {
		Entity struct {
			MatchStatus string `json:"match_status"`
		} `json:"entity"`
	}
	if err := d.do(ctx, http.MethodPost, "/api/v2/aml/screening", body, &out); err != nil {
		if errors.Is(err, errNotFound) {
			return Decision{}, errors.New("aml screening: unexpected not found")
		}
		return Decision{}, err
	}
	switch strings.ToLower(strings.TrimSpace(out.Entity.MatchStatus)) {
	case "no match":
		return Decision{Status: StatusAccepted}, nil
	case "confirmed match":
		return Decision{StatusRejected, "sanctions or PEP screening match"}, nil
	default:
		return Decision{StatusProcessing, "screening result needs manual review"}, nil
	}
}

func (d *Dojah) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.baseURL+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("AppId", d.appID)
	req.Header.Set("Authorization", d.secretKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := d.http.Do(req)
	if err != nil {
		return fmt.Errorf("dojah: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("dojah: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		return json.Unmarshal(raw, out)
	case resp.StatusCode == http.StatusNotFound,
		resp.StatusCode == http.StatusBadRequest && bytes.Contains(bytes.ToLower(raw), []byte("not found")):
		return errNotFound
	default:
		return fmt.Errorf("dojah: http %d", resp.StatusCode)
	}
}

func sameName(a, b string) bool {
	norm := func(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }
	return norm(a) != "" && norm(a) == norm(b)
}

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "02-01-2006", "02/01/2006", "2006/01/02", "02-Jan-2006", "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), true
		}
	}
	return time.Time{}, false
}
