// Package store keeps koan's rows in SQLite: the schema and its format, the
// pragmas every connection sets, the write lock, and the steps of a write.
package store

// Format is the one database format this binary reads, kept in
// PRAGMA user_version.
const Format = 2

// schema is PRD Storage's, statement for statement.
const schema = `
CREATE TABLE issued (
  id      INTEGER PRIMARY KEY CHECK (id BETWEEN 1 AND 999999999999999),
  kind    TEXT NOT NULL CHECK (kind IN ('task', 'folder')),
  state   TEXT NOT NULL CHECK (state IN ('live', 'deleted', 'archived')),
  archive TEXT,
  CHECK ((state = 'archived') = (archive IS NOT NULL))
);

CREATE TABLE folder (
  id     INTEGER PRIMARY KEY REFERENCES issued(id),
  parent INTEGER REFERENCES folder(id),
  name   TEXT NOT NULL,
  title  TEXT,
  extra  TEXT NOT NULL CHECK (json_type(extra) = 'object')
);
CREATE UNIQUE INDEX folder_sibling ON folder(parent, name COLLATE NOCASE);
CREATE UNIQUE INDEX folder_top     ON folder(name COLLATE NOCASE) WHERE parent IS NULL;

CREATE TABLE task (
  id           INTEGER PRIMARY KEY REFERENCES issued(id),
  folder       INTEGER REFERENCES folder(id),
  gate         TEXT CHECK (gate IN ('start', 'done')),
  title        TEXT NOT NULL,
  priority     INTEGER,
  created_at   TEXT NOT NULL,
  completed_at TEXT,
  updated_at   TEXT NOT NULL,
  extra        TEXT NOT NULL CHECK (json_type(extra) = 'object'),
  CHECK (gate IS NULL OR folder IS NOT NULL)
);
CREATE UNIQUE INDEX task_gate ON task(folder, gate) WHERE gate IS NOT NULL;
CREATE INDEX task_folder ON task(folder);

CREATE TABLE edge (
  task    INTEGER NOT NULL REFERENCES task(id) ON DELETE CASCADE,
  blocker INTEGER NOT NULL REFERENCES task(id) ON DELETE CASCADE,
  PRIMARY KEY (task, blocker),
  CHECK (task <> blocker)
) WITHOUT ROWID;
CREATE INDEX edge_blocker ON edge(blocker);

CREATE TABLE tag   (task INTEGER NOT NULL REFERENCES task(id) ON DELETE CASCADE, tag TEXT NOT NULL,
                    PRIMARY KEY (task, tag)) WITHOUT ROWID;
CREATE TABLE claim (task INTEGER PRIMARY KEY REFERENCES task(id) ON DELETE CASCADE,
                    claimed_at TEXT NOT NULL, extra TEXT NOT NULL CHECK (json_type(extra) = 'object'));

CREATE TABLE root (first_id INTEGER NOT NULL CHECK (first_id >= 1));

CREATE TABLE pending (seq INTEGER PRIMARY KEY, change INTEGER NOT NULL,
                      step TEXT NOT NULL CHECK (json_valid(step)),
                      stuck INTEGER NOT NULL DEFAULT 0 CHECK (stuck IN (0, 1)));

CREATE TABLE event (
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,
  change      INTEGER NOT NULL,
  at          TEXT NOT NULL,
  command     TEXT NOT NULL,
  kind        TEXT NOT NULL,
  id          INTEGER,
  folder_id   INTEGER,
  folder_path TEXT NOT NULL,
  data        TEXT NOT NULL CHECK (json_type(data) = 'object')
);
CREATE INDEX event_at     ON event(at);
CREATE INDEX event_id     ON event(id);
CREATE INDEX event_folder ON event(folder_id);
CREATE INDEX event_path   ON event(folder_path);
CREATE TRIGGER event_fixed BEFORE UPDATE ON event
  BEGIN SELECT RAISE(ABORT, 'events are never changed'); END;
`
