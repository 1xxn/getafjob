-- Boards on Eightfold career sites (Netflix, Microsoft and others).

-- +goose Up
ALTER TABLE boards DROP CONSTRAINT boards_ats_check;
ALTER TABLE boards ADD CONSTRAINT boards_ats_check
  CHECK (ats IN ('greenhouse', 'ashby', 'lever', 'smartrecruiters', 'personio', 'recruitee', 'workable', 'workday', 'eightfold'));

-- +goose Down
DELETE FROM boards WHERE ats = 'eightfold';
ALTER TABLE boards DROP CONSTRAINT boards_ats_check;
ALTER TABLE boards ADD CONSTRAINT boards_ats_check
  CHECK (ats IN ('greenhouse', 'ashby', 'lever', 'smartrecruiters', 'personio', 'recruitee', 'workable', 'workday'));
