package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type repeatingReader struct {
	data      []byte
	bytesLeft int64
}

func newRepeatingReader(pattern string, totalBytes int64) *repeatingReader {
	return &repeatingReader{
		data:      []byte(pattern),
		bytesLeft: totalBytes,
	}
}

func (r *repeatingReader) Read(p []byte) (n int, err error) {
	if r.bytesLeft <= 0 {
		return 0, io.EOF
	}
	toRead := int64(len(p))
	if toRead > r.bytesLeft {
		toRead = r.bytesLeft
	}
	patternLen := int64(len(r.data))
	for i := int64(0); i < toRead; i++ {
		p[i] = r.data[i%patternLen]
	}
	r.bytesLeft -= toRead
	return int(toRead), nil
}

// TestAdversarial_Proxy_MaxBytesReader verifies that oversized requests forwarded through
// the proxy (e.g. >1MB to /graphql, >10MB to /webhook/telegram) are constrained by MaxBytesReader
// and do not crash the proxy server.
func TestAdversarial_Proxy_MaxBytesReader(t *testing.T) {
	gatewayCalled := false
	gatewaySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gatewayCalled = true
		// Read body from proxy
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "gateway error reading body: "+err.Error(), http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "gateway-ok")
	}))
	defer gatewaySrv.Close()

	orchCalled := false
	orchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orchCalled = true
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "orch error reading body: "+err.Error(), http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "orch-ok")
	}))
	defer orchSrv.Close()

	mux, err := setupProxy(gatewaySrv.URL, orchSrv.URL)
	require.NoError(t, err)

	proxySrv := httptest.NewServer(mux)
	defer proxySrv.Close()

	// Case 1: Send 2MB to /graphql (exceeds 1MB proxy limit)
	streamGraphQL := newRepeatingReader(`{"query":"{ test }"}`, 2<<20)
	req1, err := http.NewRequest("POST", proxySrv.URL+"/graphql", streamGraphQL)
	require.NoError(t, err)

	resp1, err := http.DefaultClient.Do(req1)
	require.NoError(t, err)
	defer resp1.Body.Close()

	// ReverseProxy will get error when copying from MaxBytesReader and abort or return bad gateway / bad request
	assert.True(t, resp1.StatusCode >= 400, "Oversized request to /graphql must return client or server error (>= 400), got %d", resp1.StatusCode)

	// Case 2: Send 12MB to /webhook/telegram (exceeds 10MB proxy limit)
	streamTelegram := newRepeatingReader(`{"update_id":123}`, 12<<20)
	req2, err := http.NewRequest("POST", proxySrv.URL+"/webhook/telegram", streamTelegram)
	require.NoError(t, err)

	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()

	assert.True(t, resp2.StatusCode >= 400, "Oversized request to /webhook/telegram must return client or server error (>= 400), got %d", resp2.StatusCode)

	// Case 3: Verify subsequent normal request still succeeds without proxy degradation
	respNormal, err := http.Get(proxySrv.URL + "/auth/google/login")
	require.NoError(t, err)
	defer respNormal.Body.Close()
	assert.Equal(t, http.StatusOK, respNormal.StatusCode, "Proxy must remain fully functional")

	_ = gatewayCalled
	_ = orchCalled
}
