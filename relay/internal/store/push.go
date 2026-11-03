package store

import (
	"context"
	"time"

	"isoform-relay/internal/push"
)

// RegisterPush stores a device's APNs token for the environment this relay serves.
func (s *Store) RegisterPush(ctx context.Context, deviceID, token, environment string, now time.Time) (time.Time, error) {
	return s.RegisterPushWithGatewayGrant(ctx, deviceID, token, environment, "", now)
}

// RegisterPushWithGatewayGrant stores a device's APNs token and optional gateway grant.
func (s *Store) RegisterPushWithGatewayGrant(ctx context.Context, deviceID, token, environment, gatewayGrant string, now time.Time) (time.Time, error) {
	hubID, err := deviceHub(ctx, s.db, deviceID)
	if err != nil {
		return time.Time{}, err
	}
	return push.RegisterWithGatewayGrant(ctx, s.db, hubID, deviceID, token, environment, gatewayGrant, now)
}

// RemovePush deletes a device's APNs registration. It is idempotent.
func (s *Store) RemovePush(ctx context.Context, deviceID string) error {
	return push.Remove(ctx, s.db, deviceID)
}

// ClaimPush leases the next due push notification, or returns nil.
func (s *Store) ClaimPush(ctx context.Context, now time.Time, lease time.Duration) (*push.Claim, error) {
	return push.ClaimNext(ctx, s.db, now, lease)
}

// ValidatePush confirms a lease immediately before a provider send.
func (s *Store) ValidatePush(ctx context.Context, claim push.Claim, now time.Time) error {
	return push.ValidateClaim(ctx, s.db, claim, now)
}

// FinishPush records a provider outcome for a current lease.
func (s *Store) FinishPush(ctx context.Context, claim push.Claim, outcome push.Outcome, now time.Time) error {
	return push.FinishClaim(ctx, s.db, claim, outcome, now)
}
