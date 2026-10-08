-- ZCode CLI 3.14.4, ~/.zcode/cli/db/db.sqlite: OpenCode's schema. A
-- compaction adds a user message whose semantics.kind is compact_summary, the
-- raw summary under summary.body, with a synthetic text part that wraps it and
-- a compaction part (persistCompactSummary); the turns before it stay.
create table session (id text primary key, directory text, time_created any, time_updated any);
create table message (id text primary key, session_id text, time_created any, data text);
create table part (id text primary key, message_id text, data text);
insert into session values ('reg-zcode-db-001', '/workspace/zcode-demo', 1789650000000, 1789650004000);
insert into message values ('m-user', 'reg-zcode-db-001', 1789650001000, '{"role":"user","time":{"created":1789650001000}}');
insert into message values ('m-assistant', 'reg-zcode-db-001', 1789650002000, '{"role":"assistant","time":{"created":1789650002000}}');
insert into message values ('m-compact', 'reg-zcode-db-001', 1789650003000, '{"role":"user","time":{"created":1789650003000},"summary":{"title":"Compact summary","body":"ZCODE-SUMMARY the stream was cut short by the proxy idle timeout","diffs":[]},"agent":"zcode-agent","semantics":{"origin":"agent_runtime","kind":"compact_summary","uiVisibility":"hidden","providerVisibility":"visible","transcriptVisibility":"hidden"}}');
insert into message values ('m-next', 'reg-zcode-db-001', 1789650004000, '{"role":"user","time":{"created":1789650004000}}');
insert into part values ('p-user', 'm-user', '{"type":"text","text":"why is the stream cut short","time":{"start":1789650001000}}');
insert into part values ('p-assistant', 'm-assistant', '{"type":"text","text":"the proxy closed on an idle timeout","time":{"start":1789650002000}}');
insert into part values ('p-compact-text', 'm-compact', '{"type":"text","text":"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.\n\nSummary:\nZCODE-SUMMARY the stream was cut short by the proxy idle timeout\nContinue the conversation from where it left off without asking the user any further questions.","synthetic":true,"time":{"start":1789650003000,"end":1789650003000}}');
insert into part values ('p-compact', 'm-compact', '{"type":"compaction","auto":true,"trigger":"auto","tail_start_id":"m-assistant"}');
insert into part values ('p-next', 'm-next', '{"type":"text","text":"raise the idle timeout","time":{"start":1789650004000}}');
