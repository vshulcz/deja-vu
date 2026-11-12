create table session_v2 (
    id text primary key,
    project_id text not null,
    parent_id text,
    directory text not null,
    title text not null,
    version text not null,
    time_created integer not null,
    time_updated integer not null
);
create table session_message (
    id text primary key,
    session_id text not null,
    type text not null,
    seq integer not null,
    time_created integer not null,
    time_updated integer not null,
    data text not null
);
insert into session_v2 (id, project_id, parent_id, directory, title, version, time_created, time_updated)
values ('ses_mimo_01', 'proj_demo', null, '/workspace/mimo-service', 'Fix token refresh race in auth middleware', '0.1.0', 1752915600000, 1752915605000);
insert into session_message (id, session_id, type, seq, time_created, time_updated, data)
values ('msg_mimo_01', 'ses_mimo_01', 'user', 1, 1752915600000, 1752915600000, '{"time":{"created":1752915600000},"text":"Why do concurrent requests fail during token refresh in auth.go?"}');
insert into session_message (id, session_id, type, seq, time_created, time_updated, data)
values ('msg_mimo_02', 'ses_mimo_01', 'assistant', 2, 1752915605000, 1752915605000, '{"time":{"created":1752915605000},"content":[{"type":"text","text":"Guard the refresh rotation with a singleflight group so concurrent callers share the in-flight token.","time":{"created":1752915605000}}]}');
