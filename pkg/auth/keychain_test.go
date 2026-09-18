package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountForURL(t *testing.T) {
	account, err := accountForURL("https://pastila.example.com:8443/path")

	require.NoError(t, err)
	assert.Equal(t, "pastila.example.com:8443", account)
}

func TestAccountForURLRejectsMissingHost(t *testing.T) {
	_, err := accountForURL("not-a-url")

	assert.Error(t, err)
}
