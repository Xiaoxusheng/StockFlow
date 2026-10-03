package logger

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewInvalidLevelFails(t *testing.T) {
	_, err := New("verbose", "json")
	require.Error(t, err)
}

func TestNewBuildsAllChannels(t *testing.T) {
	logs, err := New("info", "json")
	require.NoError(t, err)
	require.NotNil(t, logs.Access)
	require.NotNil(t, logs.Error)
	require.NotNil(t, logs.Business)
	require.NotPanics(t, logs.Sync)
}

func TestNewConsoleFormat(t *testing.T) {
	_, err := New("debug", "console")
	require.NoError(t, err)
}
