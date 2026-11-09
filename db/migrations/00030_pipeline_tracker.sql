-- Application notes, salary discussions, and interview tracking (#144).

-- +goose Up
CREATE TABLE application_notes (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id          uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    application_id   uuid NOT NULL UNIQUE REFERENCES applications (id) ON DELETE CASCADE,
    notes            text NOT NULL DEFAULT '',
    salary_discussed text NOT NULL DEFAULT '',
    interview_at     timestamptz,
    follow_up_at     timestamptz,
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL
);

CREATE INDEX application_notes_user_app ON application_notes (user_id, application_id);

ALTER TABLE application_notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE application_notes FORCE ROW LEVEL SECURITY;

CREATE POLICY application_notes_tenant ON application_notes
    USING (user_id = app_user_id() OR app_is_system())
    WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE application_notes;
