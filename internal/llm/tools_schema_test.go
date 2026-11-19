package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func TestToolsSchemas(t *testing.T) {
	require.NotNil(t, CalendarEventTool, "CalendarEventTool must not be nil")
	require.NotEmpty(t, CalendarEventTool.FunctionDeclarations, "FunctionDeclarations must not be empty")

	var searchWebDecl *genai.FunctionDeclaration

	for _, decl := range CalendarEventTool.FunctionDeclarations {
		if decl.Name == "search_web" {
			searchWebDecl = decl
		}
		// V4-06: remember_fact was removed; save_topic_memory (declared by the memory module, where
		// its schema is tested) is the one tool that saves a fact.
		assert.NotEqual(t, "remember_fact", decl.Name, "remember_fact must not be declared")
	}

	// 1. Verify search_web
	require.NotNil(t, searchWebDecl, "search_web function declaration must be present in CalendarEventTool")
	assert.NotEmpty(t, searchWebDecl.Description, "search_web must have a non-empty description")
	require.NotNil(t, searchWebDecl.Parameters, "search_web must have Parameters defined")
	assert.Equal(t, genai.TypeObject, searchWebDecl.Parameters.Type, "search_web parameters must be TypeObject")
	require.NotNil(t, searchWebDecl.Parameters.Properties, "search_web parameters must have Properties")
	
	queryProp, ok := searchWebDecl.Parameters.Properties["query"]
	require.True(t, ok, "search_web parameters must include 'query'")
	assert.Equal(t, genai.TypeString, queryProp.Type, "'query' parameter must be TypeString")
	assert.NotEmpty(t, queryProp.Description, "'query' parameter must have description")
	assert.Contains(t, searchWebDecl.Parameters.Required, "query", "'query' must be in Required list")

	// 3. Empirically verify JSON serialization adhering to Google GenAI schema
	jsonBytes, err := json.Marshal(CalendarEventTool)
	require.NoError(t, err, "CalendarEventTool must serialize to JSON without error")
	require.NotEmpty(t, jsonBytes, "Serialized JSON must not be empty")

	var rawMap map[string]interface{}
	err = json.Unmarshal(jsonBytes, &rawMap)
	require.NoError(t, err, "Serialized JSON must be valid JSON")

	// Check unmarshalled content contains functionDeclarations
	funcs, ok := rawMap["functionDeclarations"].([]interface{})
	require.True(t, ok, "functionDeclarations must be present in serialized JSON")

	foundSearchWeb := false
	for _, f := range funcs {
		fMap, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		if fMap["name"] == "search_web" {
			foundSearchWeb = true
			params, ok := fMap["parameters"].(map[string]interface{})
			require.True(t, ok)
			props, ok := params["properties"].(map[string]interface{})
			require.True(t, ok)
			_, hasQuery := props["query"]
			assert.True(t, hasQuery, "serialized search_web must have 'query' in properties")
		}
		assert.NotEqual(t, "remember_fact", fMap["name"], "serialized JSON must not contain remember_fact")
	}
	assert.True(t, foundSearchWeb, "serialized JSON must contain search_web")
}
