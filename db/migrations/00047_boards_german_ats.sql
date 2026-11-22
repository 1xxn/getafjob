-- Boards on German and European applicant tracking systems: softgarden, JOIN and SAP SuccessFactors.

-- +goose Up
ALTER TABLE boards DROP CONSTRAINT boards_ats_check;
ALTER TABLE boards ADD CONSTRAINT boards_ats_check
  CHECK (ats IN ('greenhouse', 'ashby', 'lever', 'smartrecruiters', 'personio', 'recruitee', 'workable', 'workday',
                 'eightfold', 'sitemap', 'careers', 'softgarden', 'join', 'successfactors'));

-- +goose Down
DELETE FROM boards WHERE ats IN ('softgarden', 'join', 'successfactors');
ALTER TABLE boards DROP CONSTRAINT boards_ats_check;
ALTER TABLE boards ADD CONSTRAINT boards_ats_check
  CHECK (ats IN ('greenhouse', 'ashby', 'lever', 'smartrecruiters', 'personio', 'recruitee', 'workable', 'workday',
                 'eightfold', 'sitemap', 'careers'));
