package fakeagentmail

import (
	"context"
	"time"
)

type apiKey struct {
	ID          string
	Secret      string
	Prefix      string
	Name        string
	PodID       string
	InboxID     string
	Permissions map[string]bool
	CreatedAt   time.Time
	ExpiresAt   *time.Time
}

type keyContext struct{}

func withKey(ctx context.Context, k *apiKey) context.Context {
	return context.WithValue(ctx, keyContext{}, k)
}

func callerKey(ctx context.Context) *apiKey {
	k, _ := ctx.Value(keyContext{}).(*apiKey)
	return k
}
