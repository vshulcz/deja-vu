-- OpenClaw per-agent store after the SQLite flip (openclaw/openclaw#98236,
-- shipped in 2026.8.x): ~/.openclaw/agents/<agent>/agent/openclaw-agent.sqlite.
-- transcript_events.event_json holds the same lines the JSONL transcripts held;
-- session_windows.reason records reset/rollover boundaries, whose earlier
-- sessions stay in the store. Produced by `openclaw doctor --session-sqlite
-- import` over fixtures/registry/openclaw/agents, trimmed to the two tables
-- deja reads, plus one reset window added by hand.
/* WARNING: Script requires that SQLITE_DBCONFIG_DEFENSIVE be disabled */
CREATE TABLE session_windows (
  session_id TEXT NOT NULL PRIMARY KEY,
  session_key TEXT NOT NULL,
  previous_session_id TEXT,
  reason TEXT CHECK (reason IS NULL OR reason IN ('initial', 'reset', 'rollover', 'fork', 'rewind', 'switch', 'recovery', 'compaction')),
  session_scope TEXT NOT NULL DEFAULT 'conversation' CHECK (session_scope IN ('conversation', 'shared-main', 'group', 'channel')),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  transcript_updated_at INTEGER DEFAULT NULL,
  transcript_observed_at INTEGER DEFAULT NULL,
  session_entry_provenance INTEGER NOT NULL DEFAULT 0 CHECK (session_entry_provenance IN (0, 1)),
  acp_owned INTEGER NOT NULL DEFAULT 0 CHECK (acp_owned IN (0, 1)),
  plugin_owner_id TEXT,
  hook_external_content_source TEXT CHECK (hook_external_content_source IS NULL OR hook_external_content_source IN ('gmail', 'webhook')),
  started_at INTEGER,
  ended_at INTEGER,
  status TEXT CHECK (status IS NULL OR status IN ('running', 'done', 'failed', 'killed', 'timeout')),
  chat_type TEXT CHECK (chat_type IS NULL OR chat_type IN ('direct', 'group', 'channel')),
  channel TEXT,
  account_id TEXT,
  primary_conversation_id TEXT,
  model_provider TEXT,
  model TEXT,
  agent_harness_id TEXT,
  parent_session_key TEXT,
  spawned_by TEXT,
  display_name TEXT
) STRICT;
INSERT INTO session_windows VALUES('a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d','agent:main:main',NULL,NULL,'conversation',1784883610000,1784538010000,1788374855090,1784883610000,1,0,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL);
CREATE TABLE transcript_events (
  session_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  event_json TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (session_id, seq),
  FOREIGN KEY (session_id) REFERENCES "session_windows"(session_id) ON DELETE CASCADE
) STRICT;
INSERT INTO transcript_events VALUES('a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d',0,'{"type":"session","version":3,"id":"a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d","timestamp":"2026-07-20T09:00:00Z","cwd":"/workspace/registry-demo"}',1784538000000);
INSERT INTO transcript_events VALUES('a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d',1,'{"type":"model_change","id":"mc1","parentId":null,"timestamp":"2026-07-20T09:00:01Z","provider":"anthropic","modelId":"claude-sonnet-4"}',1784538001000);
INSERT INTO transcript_events VALUES('a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d',2,'{"type":"message","id":"u1","parentId":"mc1","timestamp":"2026-07-20T09:00:05Z","message":{"role":"user","content":[{"type":"text","text":"openclaw registry conformance question"}],"timestamp":1784883605000}}',1784538005000);
INSERT INTO transcript_events VALUES('a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d',3,'{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-07-20T09:00:10Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"reasoning"},{"type":"text","text":"openclaw registry conformance answer"}],"api":"anthropic","model":"claude-sonnet-4","usage":{"input":5,"output":10},"stopReason":"endTurn","timestamp":1784883610000}}',1784538010000);

INSERT INTO session_windows VALUES('b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e','agent:main:main','a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d','reset','conversation',1784813600000,1784813700000,1784813700000,NULL,1,0,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,'claude-cli','claude-sonnet-5',NULL,NULL,NULL,NULL);
INSERT INTO transcript_events VALUES('b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e',0,'{"type":"session","version":3,"id":"b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e","timestamp":"2026-07-23T13:33:20.000Z","cwd":"/Users/me/work/api"}',1784813600000);
INSERT INTO transcript_events VALUES('b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e',1,'{"type":"message","id":"m1","parentId":null,"timestamp":"2026-07-23T13:33:21.000Z","message":{"role":"user","content":"after the reset: the pool timeout was the pgbouncer default","timestamp":1784813601000}}',1784813601000);
INSERT INTO transcript_events VALUES('b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e',2,'{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-07-23T13:33:22.000Z","message":{"role":"assistant","content":[{"type":"text","text":"raising server_idle_timeout fixed it"}],"timestamp":1784813602000}}',1784813602000);
