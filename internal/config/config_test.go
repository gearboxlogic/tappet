package config

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSendsConfiguredHTTPHeaders(t *testing.T) {
	t.Parallel()

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
}
