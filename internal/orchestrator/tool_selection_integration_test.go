package orchestrator_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/domains/baby"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/domains/finance"
	"github.com/nivik/mypa/internal/domains/house"
	"github.com/nivik/mypa/internal/domains/memory"
	"github.com/nivik/mypa/internal/domains/schedule"
	"github.com/nivik/mypa/internal/domains/shared"
	"github.com/nivik/mypa/internal/domains/web_research"
	"github.com/nivik/mypa/internal/domains/workspace_calendar"
	"github.com/nivik/mypa/internal/domains/workspace_contacts"
	"github.com/nivik/mypa/internal/domains/workspace_gmail"
	"github.com/nivik/mypa/internal/domains/workspace_tasks"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

// TestFull13Modules_TokenReduction verifies SCALE-01:
// 1. All 13 production domain modules register >= 67 total tool declarations.
// 2. CoreDeclarations() strictly returns the 7 Tier 1 core tools (8 before V4-06 removed remember_fact).
// 3. SelectDeclarations for DomainBaby injects exactly 13 tools (8 core + 5 baby).
// 4. Achieves >= 80% token reduction (13 / 67 tools).
// 5. Ambient default selection (no matched domain) injects only Core + Calendar + Tasks tools.
func TestFull13Modules_TokenReduction(t *testing.T) {
	reg := orchestrator.NewRegistry()

	// Register all 13 production modules
	reg.Register(workspace_calendar.NewModule(nil))
	reg.Register(workspace_gmail.NewModule())
	reg.Register(workspace_tasks.NewModule())
	reg.Register(workspace_contacts.NewModule())
	reg.Register(web_research.NewModule())
	reg.Register(durability.NewModule(nil, nil))
	reg.Register(memory.NewModule(nil))
	reg.Register(shared.NewModule(nil, nil, nil))
	reg.Register(schedule.NewModule(nil))
	reg.Register(baby.NewModule(nil))
	reg.Register(house.NewModule(nil))
	reg.Register(finance.NewModule(nil))
	reg.Register(automation.NewModule(nil))

	// 1. Verify total declarations across all 13 modules
	allDecls := reg.AllDeclarations()
	totalTools := len(allDecls)
	t.Logf("Total registered tool declarations across all 13 modules: %d", totalTools)
	// 66 since V4-06 removed remember_fact (67 before).
	assert.GreaterOrEqual(t, totalTools, 66, "Expected at least 66 total declarations across 13 modules")

	// 2. Verify Tier 1 Core Tools: must be exactly 7 tools
	coreDecls := reg.CoreDeclarations()
	require.Len(t, coreDecls, 7, "CoreDeclarations must contain exactly 7 Tier 1 tools")

	expectedCoreTools := map[string]bool{
		"save_topic_memory":     true,
		"search_topic_memories": true,
		"schedule_reminder":     true,
		"search_web":            true,
		"fetch_webpage":         true,
		"undo_last_action":      true,
		"get_current_time":      true,
	}

	for _, d := range coreDecls {
		assert.True(t, expectedCoreTools[d.Name], "Unexpected tool %s in CoreDeclarations", d.Name)
	}

	// 3. Verify Two-Tier Selection for DomainBaby: exactly 13 tools (8 core + 5 baby tools)
	babySelected := reg.SelectDeclarations([]orchestrator.DomainGroup{orchestrator.DomainBaby}, false)
	require.Len(t, babySelected, 12, "Baby domain selection must contain exactly 12 tools (7 core + 5 baby)")

	expectedBabyTools := map[string]bool{
		"upsert_baby_profile":            true,
		"log_baby_care":                  true,
		"query_baby_logs_and_trends":     true,
		"get_baby_milestone_suggestions": true,
		"manage_baby_inventory":          true,
	}

	babyToolCount := 0
	coreToolCount := 0
	for _, d := range babySelected {
		if expectedBabyTools[d.Name] {
			babyToolCount++
		}
		if expectedCoreTools[d.Name] {
			coreToolCount++
		}
	}
	assert.Equal(t, 5, babyToolCount, "Must contain all 5 baby domain tools")
	assert.Equal(t, 7, coreToolCount, "Must contain all 7 core tools")

	// 4. Token reduction math:
	// 13 tools injected instead of >= 67 tools
	reductionPercent := (1.0 - (float64(len(babySelected)) / float64(totalTools))) * 100.0
	t.Logf("Empirical token reduction: %.2f%% (13 tools injected out of %d)", reductionPercent, totalTools)
	assert.GreaterOrEqual(t, reductionPercent, 80.0, "Token reduction must be >= 80%%")

	// 5. Verify Ambient Default (unmatched domain): Core + Calendar + Tasks
	ambientSelected := reg.SelectDeclarations(nil, false)
	ambientNames := make(map[string]bool)
	for _, d := range ambientSelected {
		ambientNames[d.Name] = true
	}

	// Must contain core tools
	assert.True(t, ambientNames["save_topic_memory"])
	assert.False(t, ambientNames["remember_fact"], "remember_fact was removed (V4-06)")
	assert.True(t, ambientNames["get_current_time"])
	// Must contain ambient domains (calendar & tasks)
	assert.True(t, ambientNames["create_calendar_event"])
	assert.True(t, ambientNames["create_task"])
	// Must NOT contain baby, finance, house, contacts, or automation tools
	assert.False(t, ambientNames["upsert_baby_profile"], "Baby tools must NOT be in ambient set")
	assert.False(t, ambientNames["log_financial_transaction"], "Finance tools must NOT be in ambient set")
	assert.False(t, ambientNames["save_house_contract_or_receipt"], "House tools must NOT be in ambient set")
	assert.False(t, ambientNames["register_external_api"], "Automation tools must NOT be in ambient set")
	assert.False(t, ambientNames["upsert_personal_crm"], "Contacts tools must NOT be in ambient set")
}

type capturedRequest struct {
	Tools []struct {
		FunctionDeclarations []struct {
			Name string `json:"name"`
		} `json:"functionDeclarations"`
	} `json:"tools"`
}

type testRewriteTransport struct {
	URL string
}

func (t *testRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.URL[7:] // strip "http://"
	return http.DefaultTransport.RoundTrip(req)
}

// TestLLMChat_WithActiveTools_BypassesStaticTools verifies that:
// 1. When WithActiveTools is set in context, Client.Chat passes ONLY the scoped tools to Gemini,
//    bypassing the static 28-tool CalendarEventTool.
// 2. When WithNoTools is set in context, Client.Chat passes tools: nil.
// 3. When neither is set, Client.Chat falls back to the static tools.
func TestLLMChat_WithActiveTools_BypassesStaticTools(t *testing.T) {
	var lastCapturedReq capturedRequest

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req capturedRequest
		_ = json.Unmarshal(bodyBytes, &req)
		lastCapturedReq = req

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [
				{
					"content": {
						"parts": [{"text": "chat response"}],
						"role": "model"
					}
				}
			]
		}`))
	}))
	defer ts.Close()

	withHTTPClient := func(cfg *genai.ClientConfig) {
		cfg.HTTPClient = &http.Client{
			Transport: &testRewriteTransport{URL: ts.URL},
		}
	}

	client, err := llm.NewClient(context.Background(), "mock-key", "gemini-test", withHTTPClient)
	require.NoError(t, err)

	// Subtest 1: WithActiveTools (bypasses CalendarEventTool)
	activeTools := []*genai.FunctionDeclaration{
		{Name: "custom_scoped_tool_1"},
		{Name: "custom_scoped_tool_2"},
	}
	ctxActive := llm.WithActiveTools(context.Background(), activeTools)

	resp, err := client.Chat(ctxActive, "system", nil, "hello", nil, "")
	require.NoError(t, err)
	assert.Equal(t, "chat response", resp.Text)

	require.Len(t, lastCapturedReq.Tools, 1, "Must contain exactly 1 tool object")
	require.Len(t, lastCapturedReq.Tools[0].FunctionDeclarations, 2, "Must contain exactly the 2 scoped tools")
	assert.Equal(t, "custom_scoped_tool_1", lastCapturedReq.Tools[0].FunctionDeclarations[0].Name)
	assert.Equal(t, "custom_scoped_tool_2", lastCapturedReq.Tools[0].FunctionDeclarations[1].Name)

	// Subtest 2: WithNoTools (omits all tools)
	ctxNoTools := llm.WithNoTools(context.Background())
	resp, err = client.Chat(ctxNoTools, "system", nil, "terminal summary", nil, "")
	require.NoError(t, err)
	assert.Equal(t, "chat response", resp.Text)

	assert.Empty(t, lastCapturedReq.Tools, "Tools must be empty/nil when WithNoTools is set")

	// Subtest 3: Default fallback (contains CalendarEventTool)
	resp, err = client.Chat(context.Background(), "system", nil, "default turn", nil, "")
	require.NoError(t, err)
	assert.Equal(t, "chat response", resp.Text)

	require.NotEmpty(t, lastCapturedReq.Tools, "Default turn must contain fallback tools")
	assert.GreaterOrEqual(t, len(lastCapturedReq.Tools[0].FunctionDeclarations), 20,
		"Default fallback must contain the static CalendarEventTool declarations")
}
