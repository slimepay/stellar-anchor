package customer

import (
	"context"
	"errors"
	"testing"

	"aidmedium/stellar-anchor/internal/audit"
	"aidmedium/stellar-anchor/internal/kyc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memStore struct {
	byID map[string]*Customer
}

func newMemStore() *memStore { return &memStore{byID: map[string]*Customer{}} }

func clone(c *Customer) *Customer {
	cp := *c
	cp.Fields = map[string]string{}
	for k, v := range c.Fields {
		cp.Fields[k] = v
	}
	if c.Verified != nil {
		cp.Verified = map[string]string{}
		for k, v := range c.Verified {
			cp.Verified[k] = v
		}
	}
	return &cp
}

func (m *memStore) Get(_ context.Context, id string) (*Customer, error) {
	if c, ok := m.byID[id]; ok {
		return clone(c), nil
	}
	return nil, ErrNotFound
}

func (m *memStore) FindByAccount(_ context.Context, account, memo string) (*Customer, error) {
	for _, c := range m.byID {
		if c.Account == account && c.Memo == memo {
			return clone(c), nil
		}
	}
	return nil, ErrNotFound
}

func (m *memStore) Save(_ context.Context, c *Customer) error {
	m.byID[c.ID] = clone(c)
	return nil
}

func (m *memStore) Delete(_ context.Context, id string) error {
	if _, ok := m.byID[id]; !ok {
		return ErrNotFound
	}
	delete(m.byID, id)
	return nil
}

type fakeVerifier struct {
	decision kyc.Decision
	err      error
	calls    int
}

func (f *fakeVerifier) Verify(context.Context, kyc.Identity) (kyc.Decision, error) {
	f.calls++
	return f.decision, f.err
}

func fullFields() map[string]string {
	return map[string]string{
		"first_name":          "Ada",
		"last_name":           "Obi",
		"email_address":       "ada@example.com",
		"birth_date":          "1990-05-17",
		"id_type":             "bvn",
		"id_number":           "22222222222",
		"bank_account_number": "0123456789",
		"bank_number":         "000013",
	}
}

func setup() (*Service, *memStore, *fakeVerifier) {
	store := newMemStore()
	v := &fakeVerifier{decision: kyc.Decision{Status: kyc.StatusAccepted}}
	return NewService(store, v, audit.Nop{}), store, v
}

func TestGetUnknownAccountNeedsInfo(t *testing.T) {
	svc, _, _ := setup()
	resp, err := svc.Get(context.Background(), Query{Account: "GABC"})
	require.NoError(t, err)
	assert.Equal(t, StatusNeedsInfo, resp.Status)
	assert.Empty(t, resp.ID)
	assert.Contains(t, resp.Fields, "id_number")
	assert.Contains(t, resp.Fields, "bank_account_number")
	assert.NotContains(t, resp.Fields, "mobile_number", "optional fields are not demanded")
}

func TestGetUnknownIDNotFound(t *testing.T) {
	svc, _, _ := setup()
	_, err := svc.Get(context.Background(), Query{ID: "nope"})
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestPutPartialThenComplete(t *testing.T) {
	svc, _, v := setup()
	ctx := context.Background()

	resp, err := svc.Put(ctx, PutRequest{Account: "GABC", Fields: map[string]string{"first_name": "Ada"}})
	require.NoError(t, err)
	assert.Equal(t, StatusNeedsInfo, resp.Status)
	assert.Zero(t, v.calls, "KYC waits for all fields")
	id := resp.ID

	got, err := svc.Get(ctx, Query{ID: id})
	require.NoError(t, err)
	assert.Contains(t, got.ProvidedFields, "first_name")
	assert.Contains(t, got.Fields, "last_name")

	resp, err = svc.Put(ctx, PutRequest{ID: id, Account: "GABC", Fields: fullFields()})
	require.NoError(t, err)
	assert.Equal(t, id, resp.ID)
	assert.Equal(t, StatusAccepted, resp.Status)
	assert.Equal(t, 1, v.calls)

	got, err = svc.Get(ctx, Query{Account: "GABC"})
	require.NoError(t, err)
	assert.Equal(t, StatusAccepted, got.Status)
	assert.Equal(t, StatusAccepted, got.ProvidedFields["id_number"].Status)
	for name, spec := range got.Fields {
		assert.True(t, spec.Optional, "only optional fields are still listed, got %s", name)
	}
}

func TestIdentityVerifiedBeforeBankDetails(t *testing.T) {
	svc, store, v := setup()
	ctx := context.Background()
	identity := fullFields()
	delete(identity, "bank_account_number")
	delete(identity, "bank_number")

	resp, err := svc.Put(ctx, PutRequest{Account: "GABC", Fields: identity})
	require.NoError(t, err)
	assert.Equal(t, StatusNeedsInfo, resp.Status, "bank details are still missing")
	assert.Equal(t, 1, v.calls, "identity is checked on its own")
	assert.True(t, store.byID[resp.ID].IdentityVerified())

	resp, err = svc.Put(ctx, PutRequest{ID: resp.ID, Fields: map[string]string{"bank_number": "000013", "bank_account_number": "0123456789"}})
	require.NoError(t, err)
	assert.Equal(t, StatusAccepted, resp.Status)
	assert.Equal(t, 1, v.calls)

	v.decision = kyc.Decision{Status: kyc.StatusRejected}
	resp, err = svc.Put(ctx, PutRequest{ID: resp.ID, Fields: map[string]string{"last_name": "Obiora"}})
	require.NoError(t, err)
	assert.Equal(t, StatusRejected, resp.Status)
	assert.False(t, store.byID[resp.ID].IdentityVerified())
}

func TestKYCOnlyRerunsWhenIdentityChanges(t *testing.T) {
	svc, _, v := setup()
	ctx := context.Background()
	resp, err := svc.Put(ctx, PutRequest{Account: "GABC", Fields: fullFields()})
	require.NoError(t, err)

	_, err = svc.Put(ctx, PutRequest{ID: resp.ID, Fields: map[string]string{"bank_account_number": "9876543210"}})
	require.NoError(t, err)
	assert.Equal(t, 1, v.calls, "bank details do not need a new KYC check")

	_, err = svc.Put(ctx, PutRequest{ID: resp.ID, Fields: map[string]string{"last_name": "Obiora"}})
	require.NoError(t, err)
	assert.Equal(t, 2, v.calls)
}

func TestRejectedAndManualReview(t *testing.T) {
	svc, _, v := setup()
	ctx := context.Background()

	v.decision = kyc.Decision{Status: kyc.StatusRejected, Reason: "name does not match the BVN record"}
	resp, err := svc.Put(ctx, PutRequest{Account: "GA", Fields: fullFields()})
	require.NoError(t, err)
	assert.Equal(t, StatusRejected, resp.Status)
	got, _ := svc.Get(ctx, Query{ID: resp.ID})
	assert.Equal(t, "name does not match the BVN record", got.Message)

	v.decision = kyc.Decision{Status: kyc.StatusProcessing, Reason: "screening result needs manual review"}
	resp, err = svc.Put(ctx, PutRequest{Account: "GB", Fields: fullFields()})
	require.NoError(t, err)
	assert.Equal(t, StatusProcessing, resp.Status)
}

func TestDojahOutageRetriedOnGet(t *testing.T) {
	svc, _, v := setup()
	ctx := context.Background()

	v.err = errors.New("dojah: http 503")
	resp, err := svc.Put(ctx, PutRequest{Account: "GABC", Fields: fullFields()})
	require.NoError(t, err)
	assert.Equal(t, StatusProcessing, resp.Status)

	v.err = nil
	got, err := svc.Get(ctx, Query{ID: resp.ID})
	require.NoError(t, err)
	assert.Equal(t, StatusAccepted, got.Status)
	assert.Equal(t, 2, v.calls)

	_, err = svc.Get(ctx, Query{ID: resp.ID})
	require.NoError(t, err)
	assert.Equal(t, 2, v.calls, "no more checks once Dojah has answered")
}

func TestPutValidation(t *testing.T) {
	svc, _, _ := setup()
	bad := []map[string]string{
		{"email_address": "not-an-email"},
		{"birth_date": "17/05/1990"},
		{"birth_date": "2020-01-01"},
		{"id_type": "passport"},
		{"id_number": "123"},
		{"bank_account_number": "12345"},
		{"bank_number": "abc"},
		{"mobile_number": "call me"},
	}
	for _, f := range bad {
		_, err := svc.Put(context.Background(), PutRequest{Account: "GABC", Fields: f})
		var verr *ValidationError
		assert.ErrorAs(t, err, &verr, "%v", f)
	}
}

func TestPutWithForeignIDRejected(t *testing.T) {
	svc, _, _ := setup()
	ctx := context.Background()
	resp, err := svc.Put(ctx, PutRequest{Account: "GOWNER", Fields: fullFields()})
	require.NoError(t, err)

	_, err = svc.Put(ctx, PutRequest{ID: resp.ID, Account: "GATTACKER", Fields: map[string]string{"bank_account_number": "1111111111"}})
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMemoSeparatesCustomers(t *testing.T) {
	svc, _, _ := setup()
	ctx := context.Background()
	a, err := svc.Put(ctx, PutRequest{Account: "GSHARED", Memo: "1", Fields: fullFields()})
	require.NoError(t, err)
	b, err := svc.Put(ctx, PutRequest{Account: "GSHARED", Memo: "2", Fields: fullFields()})
	require.NoError(t, err)
	assert.NotEqual(t, a.ID, b.ID)
}

func TestDelete(t *testing.T) {
	svc, _, _ := setup()
	ctx := context.Background()
	resp, err := svc.Put(ctx, PutRequest{Account: "GABC", Fields: fullFields()})
	require.NoError(t, err)

	require.NoError(t, svc.Delete(ctx, resp.ID))
	assert.ErrorIs(t, svc.Delete(ctx, resp.ID), ErrNotFound)
	_, err = svc.Lookup(ctx, "GABC", "")
	assert.ErrorIs(t, err, ErrNotFound)
}
