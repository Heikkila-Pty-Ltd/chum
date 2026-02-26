package store

// schemaEvolution contains tables for the evolutionary/learning system:
// UBS findings, proteins, protein folds, and paleontology runs.
// Genome tables are managed separately by ensureGenomesTable().
const schemaEvolution = `
CREATE TABLE IF NOT EXISTS ubs_findings (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	dispatch_id   INTEGER NOT NULL DEFAULT 0,
	morsel_id     TEXT    NOT NULL DEFAULT '',
	project       TEXT    NOT NULL DEFAULT '',
	provider      TEXT    NOT NULL DEFAULT '',
	species       TEXT    NOT NULL DEFAULT '',
	rule_id       TEXT    NOT NULL DEFAULT '',
	severity      TEXT    NOT NULL DEFAULT '',
	file_path     TEXT    NOT NULL DEFAULT '',
	line_number   INTEGER NOT NULL DEFAULT 0,
	message       TEXT    NOT NULL DEFAULT '',
	language      TEXT    NOT NULL DEFAULT '',
	attempt       INTEGER NOT NULL DEFAULT 0,
	fixed         BOOLEAN NOT NULL DEFAULT 0,
	created_at    DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS proteins (
	id          TEXT PRIMARY KEY,
	category    TEXT NOT NULL DEFAULT '',
	name        TEXT NOT NULL DEFAULT '',
	molecules   TEXT NOT NULL DEFAULT '[]',
	generation  INTEGER NOT NULL DEFAULT 0,
	successes   INTEGER NOT NULL DEFAULT 0,
	failures    INTEGER NOT NULL DEFAULT 0,
	avg_tokens  REAL NOT NULL DEFAULT 0,
	fitness     REAL NOT NULL DEFAULT 0,
	parent_id   TEXT NOT NULL DEFAULT '',
	created_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS protein_folds (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	protein_id   TEXT NOT NULL DEFAULT '',
	project      TEXT NOT NULL DEFAULT '',
	morsel_id    TEXT NOT NULL DEFAULT '',
	provider     TEXT NOT NULL DEFAULT '',
	total_tokens INTEGER NOT NULL DEFAULT 0,
	duration_s   REAL NOT NULL DEFAULT 0,
	success      BOOLEAN NOT NULL DEFAULT 0,
	retro        TEXT NOT NULL DEFAULT '{}',
	created_at   DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS paleontology_runs (
	id                     INTEGER PRIMARY KEY AUTOINCREMENT,
	run_at                 DATETIME NOT NULL DEFAULT (datetime('now')),
	antibodies_discovered  INTEGER NOT NULL DEFAULT 0,
	genes_mutated          INTEGER NOT NULL DEFAULT 0,
	proteins_nominated     INTEGER NOT NULL DEFAULT 0,
	species_audited        INTEGER NOT NULL DEFAULT 0,
	cost_alerts            INTEGER NOT NULL DEFAULT 0,
	summary                TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_ubs_provider ON ubs_findings(provider);
CREATE INDEX IF NOT EXISTS idx_ubs_species  ON ubs_findings(species, rule_id);
CREATE INDEX IF NOT EXISTS idx_ubs_project  ON ubs_findings(project, created_at);
CREATE INDEX IF NOT EXISTS idx_proteins_category ON proteins(category);
CREATE INDEX IF NOT EXISTS idx_folds_protein ON protein_folds(protein_id);
CREATE INDEX IF NOT EXISTS idx_folds_project ON protein_folds(project);
`
