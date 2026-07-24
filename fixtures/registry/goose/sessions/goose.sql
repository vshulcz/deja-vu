create table sessions (
  id text primary key,
  name text not null default '',
  description text not null default '',
  user_set_name boolean default false,
  session_type text not null default 'user',
  working_dir text not null,
  created_at timestamp default current_timestamp,
  updated_at timestamp default current_timestamp
);
create table messages (
  id integer primary key autoincrement,
  message_id text,
  session_id text not null references sessions(id),
  role text not null,
  content_json text not null,
  created_timestamp integer not null,
  timestamp timestamp default current_timestamp
);
insert into sessions values ('20250724_2', 'demo', 'sqlite fixture', 0, 'user', '/workspace/demo', '2026-07-24T11:00:00Z', '2026-07-24T11:00:02Z');
insert into messages values (1, 'msg-u1', '20250724_2', 'user', '[{"type":"text","text":"inspect the sqlite fixture"}]', 1784282401, '2026-07-24T11:00:01Z');
insert into messages values (2, 'msg-a1', '20250724_2', 'assistant', '[{"type":"text","text":"the sqlite rows are readable"}]', 1784282402, '2026-07-24T11:00:02Z');
