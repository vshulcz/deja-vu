create table session (id text primary key, directory text, time_created any, time_updated any);
create table message (id text primary key, session_id text, time_created any, data text);
create table part (id text primary key, message_id text, data text);
insert into session values ('registry-opencode', '/workspace/registry-demo', '2026-07-17T09:00:00Z', '2026-07-17T09:00:02Z');
insert into message values ('message-user', 'registry-opencode', 1784278801000, '{"role":"user","time":{"created":"2026-07-17T09:00:01Z"}}');
insert into message values ('message-assistant', 'registry-opencode', 1784278802000, '{"role":"assistant","time":{"created":"2026-07-17T09:00:02Z"}}');
insert into part values ('part-user', 'message-user', '{"type":"text","text":"inspect the sqlite fixture","time":{"start":"2026-07-17T09:00:01Z"}}');
insert into part values ('part-assistant', 'message-assistant', '{"type":"text","text":"the sqlite rows are readable","time":{"start":"2026-07-17T09:00:02Z"}}');
