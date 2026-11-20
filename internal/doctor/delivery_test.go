package doctor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeldMessages(t *testing.T) {
	admin := &models.User{HouseholdID: "h1"}
	counts := func(waiting, expired int64, err error) func(context.Context, string, time.Time) (int64, int64, error) {
		return func(_ context.Context, household string, _ time.Time) (int64, int64, error) {
			assert.Equal(t, "h1", household, "only the admin's own household is counted")
			return waiting, expired, err
		}
	}
	cases := []struct {
		name   string
		check  HeldMessages
		scope  Scope
		status Status
	}{
		{"nothing waits", HeldMessages{Counts: counts(0, 0, nil)}, Scope{Admin: admin}, StatusOK},
		{"some wait", HeldMessages{Counts: counts(2, 0, nil)}, Scope{Admin: admin}, StatusInfo},
		{"some expired", HeldMessages{Counts: counts(1, 3, nil)}, Scope{Admin: admin}, StatusWarn},
		{"database down", HeldMessages{Counts: counts(0, 0, errors.New("connection refused"))}, Scope{Admin: admin}, StatusFail},
		{"no household", HeldMessages{Counts: counts(0, 0, nil)}, Scope{}, StatusInfo},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := c.check.Run(context.Background(), c.scope)
			require.Len(t, res, 1)
			assert.Equal(t, c.status, res[0].Status)
			assert.NotEmpty(t, res[0].Detail)
			if c.status == StatusWarn || c.status == StatusFail {
				assert.NotEmpty(t, res[0].NextStep, "every problem says what to do next")
			}
		})
	}
	res := HeldMessages{Counts: counts(2, 3, nil)}.Run(context.Background(), Scope{Admin: admin})
	assert.Contains(t, res[0].Detail, "2 message(s) wait")
	assert.Contains(t, res[0].Detail, "3 expired")
}
