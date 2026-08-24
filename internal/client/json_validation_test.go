package client

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRejectDuplicateJSONMembersEnforcesDepthLimit(t *testing.T) {
	atLimit := strings.Repeat("[", maxProviderJSONDepth) + "0" + strings.Repeat("]", maxProviderJSONDepth)
	require.NoError(t, RejectDuplicateJSONMembers([]byte(atLimit)))

	overLimit := "[" + atLimit + "]"
	err := RejectDuplicateJSONMembers([]byte(overLimit))
	require.ErrorIs(t, err, ErrJSONDepthExceeded)
}

func TestRejectJSONMembersEnforcesNodeLimit(t *testing.T) {
	// Object, first member name, and first scalar consume the three admitted
	// nodes; the second member name must be rejected before ordinary decoding.
	err := rejectJSONMembersWithLimits([]byte(`{"first":1,"second":2}`), 8, 3)
	require.ErrorIs(t, err, ErrJSONNodesExceeded)
}
