create table agent_session_records (
    session_id text primary key,
    record_json text not null
);
create table journal_sessions (
    session_id text primary key,
    workspace_id text not null,
    epoch text not null
);
create table journal_rows (
    session_id text not null,
    epoch text not null,
    seq integer not null,
    ts integer not null,
    row_json text not null,
    primary key (session_id, epoch, seq)
);
insert into agent_session_records (session_id, record_json)
values ('orca-sess-01', '{"id":"orca-sess-01","workspaceId":"ws-1","cwd":"/workspace/orca-payments","title":"Fix webhook signature verification","createdAt":1752915600000,"updatedAt":1752915610000,"provider":"claude"}');
insert into journal_sessions (session_id, workspace_id, epoch)
values ('orca-sess-01', 'ws-1', 'ep-1');
insert into journal_rows (session_id, epoch, seq, ts, row_json)
values ('orca-sess-01', 'ep-1', 1, 1752915601000, '{"itemId":"msg-1","revision":1,"observedAt":1752915601000,"sequence":1,"body":{"kind":"message","role":"user","blocks":[{"kind":"text","text":"Why is Stripe webhook verification rejecting valid signatures?"}]}}');
insert into journal_rows (session_id, epoch, seq, ts, row_json)
values ('orca-sess-01', 'ep-1', 2, 1752915605000, '{"itemId":"msg-2","revision":1,"observedAt":1752915605000,"sequence":2,"body":{"kind":"message","role":"assistant","blocks":[{"kind":"text","text":"Checking the raw request body parser..."}]}}');
insert into journal_rows (session_id, epoch, seq, ts, row_json)
values ('orca-sess-01', 'ep-1', 3, 1752915610000, '{"itemId":"msg-2","revision":2,"observedAt":1752915610000,"sequence":2,"body":{"kind":"message","role":"assistant","blocks":[{"kind":"text","text":"Verify against the raw unparsed request buffer before JSON middleware mutates whitespace."},{"kind":"tool-call","toolCallId":"tc-1","name":"bash","input":{"command":"go test ./internal/webhook/..."}}]}}');
