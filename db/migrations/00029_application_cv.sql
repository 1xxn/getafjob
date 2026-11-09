-- Application CV variant (#143): allows user to pick a specific CV variant for an application or job.

-- +goose Up
ALTER TABLE applications
  ADD COLUMN cv_document_id uuid REFERENCES documents (id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE applications
  DROP COLUMN IF EXISTS cv_document_id;
