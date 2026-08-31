CREATE TABLE IF NOT EXISTS visits (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  browser_id      TEXT,
  session_id      TEXT NOT NULL,
  first_seen      INTEGER NOT NULL,
  last_seen       INTEGER NOT NULL,
  ua              TEXT,
  ip              TEXT,
  country         TEXT,
  pct             REAL,
  bits            REAL,
  band            TEXT,
  rfp_detected    INTEGER,
  canvas_masked   INTEGER,
  gl_masked       INTEGER,
  tz_spoofed      INTEGER,
  vec             BLOB,
  raw             TEXT
);

CREATE TABLE IF NOT EXISTS browsers (
  id              TEXT PRIMARY KEY,
  first_seen      INTEGER NOT NULL,
  last_seen       INTEGER NOT NULL,
  visit_count     INTEGER NOT NULL DEFAULT 1,
  ua              TEXT,
  pct             REAL,
  bits            REAL,
  band            TEXT,
  vec             BLOB
);

CREATE TABLE IF NOT EXISTS signals (
  visit_id        INTEGER NOT NULL REFERENCES visits(id) ON DELETE CASCADE,
  tier            TEXT NOT NULL,
  key             TEXT NOT NULL,
  value           TEXT NOT NULL,
  PRIMARY KEY (visit_id, key)
);

CREATE INDEX IF NOT EXISTS idx_signals_kv          ON signals(key, value);
CREATE INDEX IF NOT EXISTS idx_signals_visit       ON signals(visit_id);
CREATE INDEX IF NOT EXISTS idx_visits_session      ON visits(session_id);
CREATE INDEX IF NOT EXISTS idx_visits_pct          ON visits(pct DESC);
CREATE INDEX IF NOT EXISTS idx_visits_bits         ON visits(bits DESC);
CREATE INDEX IF NOT EXISTS idx_visits_browser      ON visits(browser_id);
CREATE INDEX IF NOT EXISTS idx_browsers_last_seen  ON browsers(last_seen DESC);
