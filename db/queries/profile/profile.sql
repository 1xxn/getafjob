-- name: LoadProfile :one
SELECT sealed, preferences, version, updated_at FROM profiles WHERE user_id = $1;

-- name: SaveProfile :execrows
-- Optimistic locking: the row must still have the version the editor loaded (0: no row yet).
INSERT INTO profiles (user_id, sealed, version, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(sealed), 1, sqlc.arg(updated_at))
ON CONFLICT (user_id) DO UPDATE SET sealed = EXCLUDED.sealed, version = profiles.version + 1, updated_at = EXCLUDED.updated_at
WHERE profiles.version = sqlc.arg(version)::bigint;

-- name: SavePreferences :exec
INSERT INTO profiles (user_id, preferences, updated_at) VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO UPDATE SET preferences = EXCLUDED.preferences, updated_at = EXCLUDED.updated_at;

-- name: InsertDocument :exec
INSERT INTO documents (id, user_id, kind, sealed_name, mime, size, label, language, focus, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: ListDocuments :many
SELECT id, kind, sealed_name, mime, size, label, language, focus, created_at FROM documents
WHERE user_id = $1 ORDER BY created_at, id;

-- name: GetDocument :one
SELECT id, kind, sealed_name, mime, size, label, language, focus, created_at FROM documents WHERE id = $1 AND user_id = $2;

-- name: CountDocuments :one
SELECT count(*) FROM documents WHERE user_id = $1;

-- name: UpdateVariant :execrows
UPDATE documents SET label = $3, language = $4, focus = $5 WHERE id = $1 AND user_id = $2 AND kind = 'cv';

-- name: DeleteDocument :execrows
DELETE FROM documents WHERE id = $1 AND user_id = $2;

-- name: ListItems :many
SELECT id, sealed, updated_at FROM profile_items WHERE user_id = $1 AND kind = $2 ORDER BY id;

-- name: CountItems :one
SELECT count(*) FROM profile_items WHERE user_id = $1 AND kind = $2;

-- name: InsertItem :exec
INSERT INTO profile_items (id, user_id, kind, sealed, updated_at) VALUES ($1, $2, $3, $4, $5);

-- name: UpdateItem :execrows
UPDATE profile_items SET sealed = $4, updated_at = $5 WHERE id = $1 AND user_id = $2 AND kind = $3;

-- name: DeleteItem :execrows
DELETE FROM profile_items WHERE id = $1 AND user_id = $2 AND kind = $3;

-- name: ActiveProfileUsers :many
-- System role: users with a profile or preferences whose account is enabled.
SELECT p.user_id FROM profiles p JOIN users u ON u.id = p.user_id
WHERE u.disabled_at IS NULL AND (p.sealed IS NOT NULL OR p.preferences IS NOT NULL) ORDER BY p.user_id;

-- name: ActivePreferences :many
-- System role: the search preferences of enabled users.
SELECT p.preferences FROM profiles p JOIN users u ON u.id = p.user_id
WHERE u.disabled_at IS NULL AND p.preferences IS NOT NULL ORDER BY p.user_id;

-- name: ListWorkExperiences :many
SELECT id, user_id, company, title, location, workplace_type, start_date, end_date, is_current, sealed_bullets, sort_order, created_at, updated_at
FROM work_experiences
WHERE user_id = $1
ORDER BY sort_order ASC, created_at ASC;

-- name: GetWorkExperience :one
SELECT id, user_id, company, title, location, workplace_type, start_date, end_date, is_current, sealed_bullets, sort_order, created_at, updated_at
FROM work_experiences
WHERE id = $1 AND user_id = $2;

-- name: InsertWorkExperience :exec
INSERT INTO work_experiences (id, user_id, company, title, location, workplace_type, start_date, end_date, is_current, sealed_bullets, sort_order, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: UpdateWorkExperience :execrows
UPDATE work_experiences
SET company = $3, title = $4, location = $5, workplace_type = $6, start_date = $7, end_date = $8, is_current = $9, sealed_bullets = $10, sort_order = $11, updated_at = $12
WHERE id = $1 AND user_id = $2;

-- name: UpdateWorkExperienceOrder :execrows
UPDATE work_experiences
SET sort_order = $3, updated_at = $4
WHERE id = $1 AND user_id = $2;

-- name: DeleteWorkExperience :execrows
DELETE FROM work_experiences
WHERE id = $1 AND user_id = $2;

-- name: CountWorkExperiences :one
SELECT count(*) FROM work_experiences WHERE user_id = $1;

-- name: ListEducations :many
SELECT id, user_id, institution, degree, field_of_study, start_date, end_date, is_current, grade, sort_order, created_at, updated_at
FROM educations
WHERE user_id = $1
ORDER BY sort_order ASC, created_at ASC;

-- name: GetEducation :one
SELECT id, user_id, institution, degree, field_of_study, start_date, end_date, is_current, grade, sort_order, created_at, updated_at
FROM educations
WHERE id = $1 AND user_id = $2;

-- name: InsertEducation :exec
INSERT INTO educations (id, user_id, institution, degree, field_of_study, start_date, end_date, is_current, grade, sort_order, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: UpdateEducation :execrows
UPDATE educations
SET institution = $3, degree = $4, field_of_study = $5, start_date = $6, end_date = $7, is_current = $8, grade = $9, sort_order = $10, updated_at = $11
WHERE id = $1 AND user_id = $2;

-- name: UpdateEducationOrder :execrows
UPDATE educations
SET sort_order = $3, updated_at = $4
WHERE id = $1 AND user_id = $2;

-- name: DeleteEducation :execrows
DELETE FROM educations
WHERE id = $1 AND user_id = $2;

-- name: CountEducations :one
SELECT count(*) FROM educations WHERE user_id = $1;

