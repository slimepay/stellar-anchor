package kyc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeDojah struct {
	bvnStatus  int
	bvnBody    string
	ninStatus  int
	ninBody    string
	amlStatus  int
	amlBody    string
	amlRequest map[string]any
	amlCalled  bool
}

func (f *fakeDojah) start(t *testing.T) *Dojah {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "app-id", r.Header.Get("AppId"))
		assert.Equal(t, "secret-key", r.Header.Get("Authorization"), "Dojah takes the key without Bearer")
		switch r.URL.Path {
		case "/api/v1/kyc/bvn":
			assert.Equal(t, "22222222222", r.URL.Query().Get("bvn"))
			assert.Equal(t, "Ada", r.URL.Query().Get("first_name"))
			assert.Equal(t, "1990-05-17", r.URL.Query().Get("dob"))
			w.WriteHeader(f.bvnStatus)
			_, _ = w.Write([]byte(f.bvnBody))
		case "/api/v1/kyc/nin":
			w.WriteHeader(f.ninStatus)
			_, _ = w.Write([]byte(f.ninBody))
		case "/api/v2/aml/screening":
			assert.Equal(t, http.MethodPost, r.Method)
			f.amlCalled = true
			_ = json.NewDecoder(r.Body).Decode(&f.amlRequest)
			w.WriteHeader(f.amlStatus)
			_, _ = w.Write([]byte(f.amlBody))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return NewDojah(srv.URL, "app-id", "secret-key")
}

const (
	bvnMatch = `{"entity":{"bvn":{"value":"2*****22222","status":true},"first_name":{"confidence_value":100,"status":true},"last_name":{"confidence_value":100,"status":true},"dob":{"status":true}}}`
	noMatch  = `{"entity":{"match_status":"No Match","risk_level":"Low"}}`
)

func bvnIdentity() Identity {
	return Identity{FirstName: "Ada", LastName: "Obi", BirthDate: "1990-05-17", IDType: IDTypeBVN, IDNumber: "22222222222"}
}

func TestBVNAccepted(t *testing.T) {
	f := &fakeDojah{bvnStatus: 200, bvnBody: bvnMatch, amlStatus: 200, amlBody: noMatch}
	dec, err := f.start(t).Verify(context.Background(), bvnIdentity())
	require.NoError(t, err)
	assert.Equal(t, StatusAccepted, dec.Status)
	require.True(t, f.amlCalled)
	assert.Equal(t, "individual", f.amlRequest["schema"])
	props := f.amlRequest["properties"].(map[string]any)
	assert.Equal(t, "Ada Obi", props["names"])
	assert.Equal(t, "17-05-1990", props["date_of_birth"])
}

func TestBVNNameMismatch(t *testing.T) {
	f := &fakeDojah{bvnStatus: 200, bvnBody: `{"entity":{"first_name":{"confidence_value":20,"status":false},"last_name":{"status":true}}}`}
	dec, err := f.start(t).Verify(context.Background(), bvnIdentity())
	require.NoError(t, err)
	assert.Equal(t, StatusRejected, dec.Status)
	assert.False(t, f.amlCalled, "no screening after a failed identity check")
}

func TestBVNDOBMismatch(t *testing.T) {
	f := &fakeDojah{bvnStatus: 200, bvnBody: `{"entity":{"first_name":{"status":true},"last_name":{"status":true},"dob":{"status":false}}}`}
	dec, err := f.start(t).Verify(context.Background(), bvnIdentity())
	require.NoError(t, err)
	assert.Equal(t, StatusRejected, dec.Status)
}

func TestBVNNotFound(t *testing.T) {
	f := &fakeDojah{bvnStatus: 400, bvnBody: `{"error":"BVN not found"}`}
	dec, err := f.start(t).Verify(context.Background(), bvnIdentity())
	require.NoError(t, err)
	assert.Equal(t, StatusRejected, dec.Status)
}

func TestDojahUnavailableIsRetryable(t *testing.T) {
	for _, status := range []int{401, 402, 429, 500, 503} {
		f := &fakeDojah{bvnStatus: status, bvnBody: `{}`}
		_, err := f.start(t).Verify(context.Background(), bvnIdentity())
		assert.Error(t, err, "status %d", status)
	}
}

func TestNIN(t *testing.T) {
	id := Identity{FirstName: "ADA", LastName: "obi", BirthDate: "1990-05-17", IDType: IDTypeNIN, IDNumber: "70123456789"}

	f := &fakeDojah{ninStatus: 200, ninBody: `{"entity":{"first_name":"Ada","last_name":"Obi","date_of_birth":"1990-05-17"}}`, amlStatus: 200, amlBody: noMatch}
	dec, err := f.start(t).Verify(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, StatusAccepted, dec.Status, "names compare case-insensitively")

	f = &fakeDojah{ninStatus: 200, ninBody: `{"entity":{"first_name":"Ada","last_name":"Obi","date_of_birth":"17-05-1991"}}`}
	dec, err = f.start(t).Verify(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, StatusRejected, dec.Status)

	f = &fakeDojah{ninStatus: 200, ninBody: `{"entity":{"first_name":"Bola","last_name":"Obi"}}`}
	dec, err = f.start(t).Verify(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, StatusRejected, dec.Status)
}

func TestScreeningOutcomes(t *testing.T) {
	cases := map[string]string{
		"No Match":          StatusAccepted,
		"Confirmed Match":   StatusRejected,
		"Potential Match":   StatusProcessing,
		"Partially Matched": StatusProcessing,
		"":                  StatusProcessing,
	}
	for match, want := range cases {
		f := &fakeDojah{bvnStatus: 200, bvnBody: bvnMatch, amlStatus: 200, amlBody: `{"entity":{"match_status":"` + match + `"}}`}
		dec, err := f.start(t).Verify(context.Background(), bvnIdentity())
		require.NoError(t, err)
		assert.Equal(t, want, dec.Status, match)
	}
}

func TestUnknownIDType(t *testing.T) {
	dec, err := NewDojah("http://unused", "a", "b").Verify(context.Background(), Identity{IDType: "passport"})
	require.NoError(t, err)
	assert.Equal(t, StatusRejected, dec.Status)
}
