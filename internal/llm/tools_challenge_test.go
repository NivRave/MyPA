package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func TestEmpiricalChallenge_ToolsSchemas(t *testing.T) {
	require.NotNil(t, CalendarEventTool, "CalendarEventTool must not be nil")
	require.NotEmpty(t, CalendarEventTool.FunctionDeclarations, "FunctionDeclarations must not be empty")

	var searchWebDecl *genai.FunctionDeclaration
	var rememberFactDecl *genai.FunctionDeclaration

	for _, decl := range CalendarEventTool.FunctionDeclarations {
		if decl.Name == "search_web" {
			searchWebDecl = decl
		}
		if decl.Name == "remember_fact" {
			rememberFactDecl = decl
		}
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

	// 2. Verify remember_fact and its scope parameter
	require.NotNil(t, rememberFactDecl, "remember_fact function declaration must be present in CalendarEventTool")
	assert.NotEmpty(t, rememberFactDecl.Description, "remember_fact must have a description")
	require.NotNil(t, rememberFactDecl.Parameters, "remember_fact must have Parameters defined")
	assert.Equal(t, genai.TypeObject, rememberFactDecl.Parameters.Type, "remember_fact parameters must be TypeObject")
	
	factProp, ok := rememberFactDecl.Parameters.Properties["fact"]
	require.True(t, ok, "remember_fact must have 'fact' property")
	assert.Equal(t, genai.TypeString, factProp.Type, "'fact' must be TypeString")
	assert.Contains(t, rememberFactDecl.Parameters.Required, "fact", "'fact' must be required")

	scopeProp, ok := rememberFactDecl.Parameters.Properties["scope"]
	require.True(t, ok, "remember_fact must have 'scope' property in its schema")
	assert.Equal(t, genai.TypeString, scopeProp.Type, "'scope' must be TypeString")
	assert.NotEmpty(t, scopeProp.Description, "'scope' must have description")

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
	foundRememberFactWithScope := false
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
		if fMap["name"] == "remember_fact" {
			params, ok := fMap["parameters"].(map[string]interface{})
			require.True(t, ok)
			props, ok := params["properties"].(map[string]interface{})
			require.True(t, ok)
			_, hasScope := props["scope"]
			if hasScope {
				foundRememberFactWithScope = true
			}
		}
	}
	assert.True(t, foundSearchWeb, "serialized JSON must contain search_web")
	assert.True(t, foundRememberFactWithScope, "serialized JSON must contain remember_fact with scope")
}
