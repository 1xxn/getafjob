-- name: UpsertSubscription :one
INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth)
VALUES ($1, $2, $3, $4)
ON CONFLICT (endpoint) DO UPDATE
  SET p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth, created_at = clock_timestamp()
RETURNING id, user_id, endpoint, p256dh, auth, created_at;

-- name: DeleteSubscription :exec
DELETE FROM push_subscriptions
WHERE user_id = $1 AND endpoint = $2;

-- name: DeleteSubscriptionByEndpoint :exec
DELETE FROM push_subscriptions
WHERE endpoint = $1;

-- name: ListSubscriptionsByUser :many
SELECT id, user_id, endpoint, p256dh, auth, created_at
FROM push_subscriptions
WHERE user_id = $1
ORDER BY created_at DESC;
