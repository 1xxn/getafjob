-- Applications (#26): state per user and job, every transition as an event, and the submit register that makes
-- a second submission impossible (ADR 0014).

-- +goose Up
CREATE TABLE applications (
  id         uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  job_id     bigint NOT NULL REFERENCES jobs (id) ON DELETE RESTRICT,
  state      text NOT NULL CHECK (state IN ('discovered', 'matched', 'queued', 'running', 'needs_input', 'submitting',
    'submitted', 'confirmed', 'interview', 'rejected', 'offer', 'withdrawn', 'skipped', 'parked', 'failed',
    'submit_unknown')),
  version    bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (user_id, job_id)
);
CREATE INDEX applications_user_state ON applications (user_id, state);
CREATE INDEX applications_submitting ON applications (updated_at) WHERE state = 'submitting';

CREATE TABLE application_events (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  application_id uuid NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
  user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  event          text NOT NULL CHECK (length(event) <= 40),
  from_state     text NOT NULL,
  to_state       text NOT NULL,
  actor          text NOT NULL CHECK (actor IN ('user', 'worker', 'runner', 'system')),
  detail         jsonb NOT NULL DEFAULT '{}' CHECK (length(detail::text) <= 16384),
  at             timestamptz NOT NULL
);
CREATE INDEX application_events_app ON application_events (application_id, id);

-- One row per application, written in the same transaction as the state submitting, before the click.
CREATE TABLE submit_attempts (
  application_id uuid PRIMARY KEY REFERENCES applications (id) ON DELETE CASCADE,
  user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  started_at     timestamptz NOT NULL,
  finished_at    timestamptz,
  outcome        text CHECK (outcome IN ('submitted', 'unknown', 'not_sent'))
);

ALTER TABLE applications ENABLE ROW LEVEL SECURITY;
ALTER TABLE applications FORCE ROW LEVEL SECURITY;
CREATE POLICY applications_tenant ON applications
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());
ALTER TABLE application_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE application_events FORCE ROW LEVEL SECURITY;
CREATE POLICY application_events_tenant ON application_events
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());
ALTER TABLE submit_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE submit_attempts FORCE ROW LEVEL SECURITY;
-- Rows are written and finished, never removed by the application (deleting a user cascades).
CREATE POLICY submit_attempts_read ON submit_attempts FOR SELECT USING (user_id = app_user_id() OR app_is_system());
CREATE POLICY submit_attempts_insert ON submit_attempts FOR INSERT WITH CHECK (user_id = app_user_id() OR app_is_system());
CREATE POLICY submit_attempts_finish ON submit_attempts FOR UPDATE
  USING (user_id = app_user_id() OR app_is_system()) WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE submit_attempts;
DROP TABLE application_events;
DROP TABLE applications;
