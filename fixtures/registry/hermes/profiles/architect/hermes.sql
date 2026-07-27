create table messages (
  id integer primary key autoincrement,
  session_id text not null,
  role text not null,
  content text,
  tool_call_id text,
  tool_calls text,
  tool_name text,
  timestamp real not null,
  token_count integer,
  finish_reason text
);
insert into messages (session_id, role, content, timestamp, token_count, finish_reason) values
  ('01JCH1', 'user', 'the retry loop in the fetcher never backs off', 1785015600.25, 11, null),
  ('01JCH1', 'assistant', 'It retries immediately because the sleep is inside the success branch. Moving it above the return fixes the hot loop.', 1785015661.5, 27, 'stop'),
  ('01JCH1', 'tool', null, 1785015662.0, null, null),
  ('01JCH2', 'user', 'why did we pick msgpack over json for the wire format', 1785102000.0, 12, null),
  ('01JCH2', 'assistant', 'Payloads were dominated by float arrays; msgpack cut them roughly in half and the decoder was already a dependency.', 1785102045.75, 25, 'stop');
