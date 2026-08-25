package config

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSendsConfiguredHTTPHeadersWithoutLoggingCredentials(t *testing.T) {
	previousLogOutput := log.Writer()
	previousLogFlags := log.Flags()
	previousLogPrefix := log.Prefix()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousLogOutput)
		log.SetFlags(previousLogFlags)
		log.SetPrefix(previousLogPrefix)
	})

	var authorization, trace string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		trace = r.Header.Get("X-Trace")
		w.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprint(w, `{
			"mcpProxy": {"name":"test","version":"dev","type":"stdio","hierarchyPath":"testdata"},
			"mcpServers": {}
		}`)
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	_, err := Load(server.URL, false, false, "Authorization: Bearer test-token; X-Trace: trace-123", 1)
	require.NoError(t, err)
	assert.Equal(t, "Bearer test-token", authorization)
	assert.Equal(t, "trace-123", trace)
	assert.NotContains(t, logs.String(), "test-token")
	assert.NotContains(t, logs.String(), "trace-123")
}
