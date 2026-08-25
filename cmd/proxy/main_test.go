package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyRouting(t *testing.T) {
	// Create mock upstream servers
	gatewaySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "gateway-response")
	}))
	defer gatewaySrv.Close()

	orchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "orchestrator-response")
	}))
	defer orchSrv.Close()

	mux, err := setupProxy(gatewaySrv.URL, orchSrv.URL)
	require.NoError(t, err)

	proxySrv := httptest.NewServer(mux)
	defer proxySrv.Close()

	tests := []struct {
		name         string
		path         string
		expectedBody string
		expectedCode int
	}{
		{
			name:         "Route Telegram to Gateway",
			path:         "/webhook/telegram",
			expectedBody: "gateway-response",
			expectedCode: 200,
		},
		{
			name:         "Route Twilio to Gateway",
			path:         "/webhook/twilio",
			expectedBody: "gateway-response",
			expectedCode: 200,
		},
		{
			name:         "Route Auth to Orchestrator",
			path:         "/auth/google/login",
			expectedBody: "orchestrator-response",
			expectedCode: 200,
		},
		{
			name:         "Route Not Found",
			path:         "/unknown/route",
			expectedBody: "Not found\n",
			expectedCode: 404,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(proxySrv.URL + tt.path)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.expectedCode, resp.StatusCode)

			buf := make([]byte, 1024)
			n, _ := resp.Body.Read(buf)
			assert.Equal(t, tt.expectedBody, string(buf[:n]))
		})
	}
}

func TestSetupProxy_InvalidURL(t *testing.T) {
	// url.Parse won't fail for simple invalid schemes, but we can test with a control character
	_, err := setupProxy("http://\x00invalid", "http://ok")
	assert.Error(t, err)
	
	_, err = setupProxy("http://ok", "http://\x00invalid")
	assert.Error(t, err)
}
