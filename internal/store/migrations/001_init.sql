-- One row per piece of evidence. PRs are evidence too: they have the source
-- "pr" and one more row in pull_requests.
CREATE TABLE evidence (
  id          TEXT PRIMARY KEY,         -- SOURCE:HASH, or pr:GITHUB_ID
  source      TEXT NOT NULL,            -- where it came from, for example slack
  external_id TEXT,                     -- ID in the source, for re-imports
  title       TEXT NOT NULL,
  body        TEXT,
  url         TEXT,
  occurred_at TEXT NOT NULL,            -- YYYY-MM-DD or RFC 3339 in UTC
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  UNIQUE (source, external_id)
);
CREATE INDEX evidence_occurred_at ON evidence (occurred_at);

CREATE TABLE pull_requests (
  evidence_id TEXT PRIMARY KEY REFERENCES evidence (id) ON DELETE CASCADE,
  github_id   INTEGER NOT NULL UNIQUE,
  repo        TEXT NOT NULL,            -- owner/name
  number      INTEGER NOT NULL,
  author      TEXT NOT NULL,            -- login that matched, can be a mannequin
  UNIQUE (repo, number)
);

CREATE TABLE notes (
  id          INTEGER PRIMARY KEY,
  evidence_id TEXT NOT NULL REFERENCES evidence (id) ON DELETE CASCADE,
  body        TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
CREATE INDEX notes_evidence_id ON notes (evidence_id);

CREATE TABLE evidence_tags (
  evidence_id TEXT NOT NULL REFERENCES evidence (id) ON DELETE CASCADE,
  tag         TEXT NOT NULL,
  PRIMARY KEY (evidence_id, tag)
);

CREATE TABLE note_tags (
  note_id INTEGER NOT NULL REFERENCES notes (id) ON DELETE CASCADE,
  tag     TEXT NOT NULL,
  PRIMARY KEY (note_id, tag)
);
