-- Boards without an applicant tracking system API: sitemaps of job pages and careers pages with JobPostings.

-- +goose Up
ALTER TABLE boards DROP CONSTRAINT boards_ats_check;
ALTER TABLE boards ADD CONSTRAINT boards_ats_check
  CHECK (ats IN ('greenhouse', 'ashby', 'lever', 'smartrecruiters', 'personio', 'recruitee', 'workable', 'workday',
                 'eightfold', 'sitemap', 'careers'));

-- +goose Down
DELETE FROM boards WHERE ats IN ('sitemap', 'careers');
ALTER TABLE boards DROP CONSTRAINT boards_ats_check;
ALTER TABLE boards ADD CONSTRAINT boards_ats_check
  CHECK (ats IN ('greenhouse', 'ashby', 'lever', 'smartrecruiters', 'personio', 'recruitee', 'workable', 'workday', 'eightfold'));
