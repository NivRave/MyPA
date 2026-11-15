package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/nivik/mypa/internal/db"
	"github.com/stretchr/testify/assert"
)

func TestAdversarial_SEC02_PlaygroundDisabledInProduction(t *testing.T) {
	origEnv := os.Getenv("ENV")
	defer os.Setenv("ENV", origEnv)

	// Set ENV=production
	os.Setenv("ENV", "production")
	os.Setenv("DASHBOARD_API_SECRET", "adv-dashboard-secret")

	dbClient := &db.Client{}
	mux := setupRouter(nil, nil, nil, dbClient, nil)

	// 1. In production, GET /playground must return HTTP 404 Not Found
	reqPlayground := httptest.NewRequest("GET", "/playground", nil)
	rrPlayground := httptest.NewRecorder()
	mux.ServeHTTP(rrPlayground, reqPlayground)

	assert.Equal(t, http.StatusNotFound, rrPlayground.Code, "Expected HTTP 404 for /playground in production")

	// 2. In production, unauthenticated POST /graphql must return HTTP 401 Unauthorized
	reqGraphQL := httptest.NewRequest("POST", "/graphql", nil)
	rrGraphQL := httptest.NewRecorder()
	mux.ServeHTTP(rrGraphQL, reqGraphQL)

	assert.Equal(t, http.StatusUnauthorized, rrGraphQL.Code, "Expected HTTP 401 for unauthenticated /graphql")
}

func TestAdversarial_SEC02_PlaygroundAllowedInDevelopment(t *testing.T) {
	origEnv := os.Getenv("ENV")
	defer os.Setenv("ENV", origEnv)

	// Set ENV=development
	os.Setenv("ENV", "development")
	os.Setenv("DASHBOARD_API_SECRET", "adv-dashboard-secret")

	dbClient := &db.Client{}
	mux := setupRouter(nil, nil, nil, dbClient, nil)

	// In development, GET /playground returns 200 OK (HTML UI)
	reqPlayground := httptest.NewRequest("GET", "/playground", nil)
	rrPlayground := httptest.NewRecorder()
	mux.ServeHTTP(rrPlayground, reqPlayground)

	assert.Equal(t, http.StatusOK, rrPlayground.Code, "Expected HTTP 200 for /playground in development")
	assert.Contains(t, rrPlayground.Body.String(), "GraphQL playground")
}
