package doctor

import (
	"context"
	"errors"
	"testing"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-63: /doctor counts the household's automation rules whose action does nothing when they
// fire, so the owner can fix them; it does not change them.
func TestIdleRules(t *testing.T) {
	admin := &models.User{HouseholdID: "h1"}
	count := func(n int64, err error) func(context.Context, string) (int64, error) {
		return func(_ context.Context, household string) (int64, error) {
			assert.Equal(t, "h1", household, "only the admin's own household is counted")
			return n, err
		}
	}
	cases := []struct {
		name   string
		check  IdleRules
		scope  Scope
		status Status
		detail string
	}{
		{"none", IdleRules{Count: count(0, nil)}, Scope{Admin: admin}, StatusOK, "Every automation rule"},
		{"some", IdleRules{Count: count(2, nil)}, Scope{Admin: admin}, StatusWarn, "2 automation rule(s)"},
		{"error", IdleRules{Count: count(0, errors.New("connection refused"))}, Scope{Admin: admin}, StatusFail, "connection refused"},
		{"no household", IdleRules{Count: count(0, nil)}, Scope{}, StatusInfo, "Not checked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.check.Run(context.Background(), tc.scope)
			require.Len(t, res, 1)
			assert.Equal(t, tc.status, res[0].Status)
			assert.Contains(t, res[0].Detail, tc.detail)
			if tc.status == StatusWarn {
				assert.Contains(t, res[0].NextStep, "prompt:")
			}
		})
	}
}
