-- Global job index (ADR 0006): crawled once for all users, no personal data, therefore no row-level security.

-- +goose Up
CREATE TABLE boards (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  ats             text NOT NULL CHECK (ats IN ('greenhouse', 'ashby', 'lever', 'smartrecruiters', 'personio', 'recruitee', 'workable', 'workday')),
  slug            text NOT NULL CHECK (length(slug) BETWEEN 1 AND 200),
  company         text NOT NULL DEFAULT '' CHECK (length(company) <= 300),
  config          jsonb NOT NULL DEFAULT '{}',
  origin          text NOT NULL DEFAULT 'admin' CHECK (origin IN ('admin', 'discovered')),
  enabled         boolean NOT NULL DEFAULT true,
  etag            text,
  last_modified   text,
  last_crawled_at timestamptz,
  last_error      text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (ats, slug)
);

CREATE TABLE crawl_runs (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  started_at  timestamptz NOT NULL,
  finished_at timestamptz,
  sources     text[] NOT NULL DEFAULT '{}',
  stats       jsonb NOT NULL DEFAULT '{}',
  errors      jsonb NOT NULL DEFAULT '{}'
);

CREATE TABLE jobs (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  fingerprint  text NOT NULL UNIQUE,
  match_key    text NOT NULL,
  source       text NOT NULL,
  external_id  text NOT NULL,
  company      text NOT NULL,
  title        text NOT NULL,
  location     text NOT NULL DEFAULT '',
  workplace    text NOT NULL DEFAULT '' CHECK (workplace IN ('', 'remote', 'hybrid', 'onsite')),
  url          text NOT NULL,
  apply_url    text NOT NULL DEFAULT '',
  alt_urls     text[] NOT NULL DEFAULT '{}',
  description  text NOT NULL DEFAULT '',
  posted       date,
  content_hash text NOT NULL,
  status       text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
  first_seen   timestamptz NOT NULL,
  last_seen    timestamptz NOT NULL,
  closed_at    timestamptz,
  search       tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
    setweight(to_tsvector('simple', coalesce(company, '')), 'B') ||
    setweight(to_tsvector('english', left(coalesce(description, ''), 100000)), 'C') ||
    setweight(to_tsvector('german', left(coalesce(description, ''), 100000)), 'C')
  ) STORED
);
CREATE INDEX jobs_match_key ON jobs (match_key);
CREATE INDEX jobs_open_source ON jobs (source) WHERE status = 'open';
CREATE INDEX jobs_search ON jobs USING gin (search);
CREATE INDEX jobs_title_trgm ON jobs USING gin (title gin_trgm_ops);
CREATE INDEX jobs_first_seen ON jobs (first_seen DESC);

CREATE TABLE job_snapshots (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  job_id       bigint NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
  crawl_run    bigint REFERENCES crawl_runs (id) ON DELETE SET NULL,
  change       text NOT NULL CHECK (change IN ('new', 'updated', 'reopened', 'merged', 'closed')),
  title        text NOT NULL,
  location     text NOT NULL,
  content_hash text NOT NULL,
  diff         jsonb NOT NULL DEFAULT '{}',
  created_at   timestamptz NOT NULL
);
CREATE INDEX job_snapshots_job ON job_snapshots (job_id, created_at DESC);

-- +goose Down
DROP TABLE job_snapshots;
DROP TABLE jobs;
DROP TABLE crawl_runs;
DROP TABLE boards;
