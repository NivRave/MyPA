package conformance

import (
	"context"
	"testing"

	"github.com/nivik/mypa/internal/config"
	"github.com/stretchr/testify/require"
)

// V4-02: the same scenarios run against every provider, and every provider passes them against
// a fake that checks its requests the way the real API does.
func TestEveryProviderPassesTheConformanceSuite(t *testing.T) {
	results, err := RunFakes(context.Background())
	require.NoError(t, err)
	require.Len(t, results, len(config.Providers())*len(Scenarios()), "every scenario must run against every provider")
	for _, r := range results {
		t.Run(r.Provider+"/"+r.Scenario, func(t *testing.T) {
			require.NoError(t, r.Err)
		})
	}
}
