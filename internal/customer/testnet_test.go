package customer

import (
	"context"
	"testing"

	"aidmedium/stellar-anchor/internal/audit"
	"aidmedium/stellar-anchor/internal/kyc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The testnet fixture IDs go through the real service, so a rejected or
// reviewed customer is never identity verified and cannot withdraw.
func TestTestnetFixturesControlVerification(t *testing.T) {
	cases := map[string]struct {
		idNumber string
		status   string
		verified bool
	}{
		"accepted": {"22222222222", StatusAccepted, true},
		"rejected": {kyc.TestnetRejectedID, StatusRejected, false},
		"review":   {kyc.TestnetReviewID, StatusProcessing, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc := NewService(newMemStore(), kyc.Testnet{}, audit.Nop{})
			fields := fullFields()
			fields["id_number"] = tc.idNumber

			resp, err := svc.Put(context.Background(), PutRequest{Account: "GA", Fields: fields})
			require.NoError(t, err)
			assert.Equal(t, tc.status, resp.Status)

			c, err := svc.Lookup(context.Background(), "GA", "")
			require.NoError(t, err)
			assert.Equal(t, tc.verified, c.IdentityVerified())
		})
	}
}
