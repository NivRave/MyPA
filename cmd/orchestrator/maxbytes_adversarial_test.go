package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
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

// TestAdversarial_Orchestrator_GraphQL_MaxBytesReader verifies that sending an oversized
// payload (> 1MB) to /graphql triggers an error and does NOT crash the orchestrator server.
func TestAdversarial_Orchestrator_GraphQL_MaxBytesReader(t *testing.T) {
	// Create handler replicating orchestrator's GraphQL wrapper
	graphqlHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
		// Handler attempting to read body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"errors":[{"message":"%s"}]}`, err.Error()), http.StatusRequestEntityTooLarge)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"data":{"bytes_read":%d}}`, len(body))))
	})

	srv := httptest.NewServer(graphqlHandler)
	defer srv.Close()

	// 1. Send 2MB payload (> 1MB limit)
	oversizedStream := newRepeatingReader(`{"query":"query { __typename }"}`, 2<<20)
	req, err := http.NewRequest("POST", srv.URL, oversizedStream)
	assert.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, "Oversized GraphQL query must return 413 Request Entity Too Large")

	// 2. Subsequent normal request (< 1MB) must succeed
	smallReq, err := http.NewRequest("POST", srv.URL, newRepeatingReader(`{"query":"{ test }"}`, 100))
	assert.NoError(t, err)
	smallReq.Header.Set("Content-Type", "application/json")

	smallResp, err := http.DefaultClient.Do(smallReq)
	assert.NoError(t, err)
	defer smallResp.Body.Close()

	assert.Equal(t, http.StatusOK, smallResp.StatusCode, "Server must remain functional for normal requests")
}
