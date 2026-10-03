package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKeySpec(t *testing.T) {
	// 键规范 sf:{module}:{key}（backend-m1-plan §2）
	require.Equal(t, "sf:session:abc123", Key("session", "abc123"))
	require.Equal(t, "sf:loginfail:alice", Key("loginfail", "alice"))
}

func TestHelpersDisabledWhenClientNil(t *testing.T) {
	ctx := context.Background()
	_, err := GetJSON(ctx, nil, "k", new(any))
	require.ErrorIs(t, err, ErrDisabled)

	err = SetJSON(ctx, nil, "k", 1, time.Minute)
	require.ErrorIs(t, err, ErrDisabled)

	_, err = Incr(ctx, nil, "k")
	require.ErrorIs(t, err, ErrDisabled)
}
