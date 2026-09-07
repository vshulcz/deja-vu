-- Crush v0.92.0, <project>/.crush/crush.db. The stamp columns are commented
-- "Unix timestamp in milliseconds" in Crush's own schema and hold whole
-- seconds in what it writes; both units are read.
create table sessions (
  id text primary key,
  parent_session_id text,
  title text not null,
  message_count integer not null default 0,
  prompt_tokens integer not null default 0,
  completion_tokens integer not null default 0,
  cost real not null default 0.0,
  updated_at integer not null,
  created_at integer not null,
  summary_message_id text,
  todos text
);
create table messages (
  id text primary key,
  session_id text not null references sessions(id),
  role text not null,
  parts text not null default '[]',
  model text,
  created_at integer not null,
  updated_at integer not null,
  finished_at integer,
  provider text,
  is_summary_message integer default 0 not null
);
insert into sessions values ('942cbc1e-78c7-41cb-aa8a-78c3baab018c', null, 'run the build', 3, 0, 0, 0.0, 1784282402, 1784282400, null, null);
insert into messages values ('58e9fc46-a49d-48f1-870f-93a9d29bae4f', '942cbc1e-78c7-41cb-aa8a-78c3baab018c', 'user', '[{"type":"text","data":{"text":"run the build"}},{"type":"finish","data":{"reason":"stop","time":0}}]', 'gpt-5', 1784282400, 1784282400, null, 'openai', 0);
-- The tool result carries a <cwd> tag Crush appends to every one; it is the
-- same directory on every line and is stripped rather than indexed.
insert into messages values ('7c1f0b52-2f77-4a71-9a2e-1b6b1e59b6a1', '942cbc1e-78c7-41cb-aa8a-78c3baab018c', 'tool', '[{"type":"tool_result","data":{"tool_call_id":"call_0","name":"bash","content":"build succeeded in 4.2s\n\n<cwd>/workspace/demo</cwd>","is_error":false}},{"type":"finish","data":{"reason":"stop","time":0}}]', null, 1784282401, 1784282401, null, null, 0);
insert into messages values ('a4d1c0e7-6c2b-4a1e-9f39-7a5f0c4b8d22', '942cbc1e-78c7-41cb-aa8a-78c3baab018c', 'assistant', '[{"type":"text","data":{"text":"the build is green"}},{"type":"finish","data":{"reason":"end_turn","time":1784282402}}]', 'gpt-5', 1784282402, 1784282402, 1784282402, 'openai', 0);
