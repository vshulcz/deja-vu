create table session_v2 (id text primary key, project_id text, parent_id text, directory text, title text, time_created integer not null, time_updated integer not null);
create table session_message (id text primary key, session_id text not null, type text not null, seq integer not null, time_created integer not null, time_updated integer not null, data text not null);
insert into session_v2 values ('registry-opencode-v2', 'project-registry', null, '/workspace/registry-demo', 'inspect the sqlite fixture', 1784278800000, 1784278802000);
insert into session_message values ('message-user', 'registry-opencode-v2', 'user', 1, 1784278801000, 1784278801000, '{"metadata":{},"time":{"created":1784278801000},"text":"inspect the sqlite fixture"}');
insert into session_message values ('message-assistant', 'registry-opencode-v2', 'assistant', 2, 1784278802000, 1784278802000, '{"time":{"created":1784278802000},"content":[{"type":"text","text":"the sqlite rows are readable"}]}');
