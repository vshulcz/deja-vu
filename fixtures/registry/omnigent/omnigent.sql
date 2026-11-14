create table conversations (
    workspace_id text not null default '',
    id blob not null,
    created_at integer not null,
    updated_at integer not null,
    title text,
    parent_conversation_id blob,
    fork_from_item_id blob,
    primary key (workspace_id, id)
);
create table omnigent_conversation_metadata (
    workspace_id text not null default '',
    id blob not null,
    workspace text not null default '',
    kind integer not null default 1,
    task_summary text,
    project_id blob,
    primary key (workspace_id, id)
);
create table conversation_items (
    workspace_id text not null default '',
    conversation_id blob not null,
    position integer not null,
    id blob not null,
    created_at integer not null,
    type integer not null,
    data text not null,
    primary key (workspace_id, conversation_id, position)
);
insert into conversations (workspace_id, id, created_at, updated_at, title, parent_conversation_id)
values ('', x'0195a8c07b2070008000112233445566', 1752915600, 1752915610, 'Pin queue consumer prefetch count', null);
insert into omnigent_conversation_metadata (workspace_id, id, workspace, kind, task_summary)
values ('', x'0195a8c07b2070008000112233445566', '/workspace/omnigent-worker', 1, 'Pin queue consumer prefetch count');
insert into conversation_items (workspace_id, conversation_id, position, id, created_at, type, data)
values ('', x'0195a8c07b2070008000112233445566', 0, x'0195a8c07b2170008000112233445566', 1752915601, 1, '{"role":"user","content":[{"type":"input_text","text":"How should we bound worker memory spikes on large RabbitMQ batches?"}],"is_meta":false}');
insert into conversation_items (workspace_id, conversation_id, position, id, created_at, type, data)
values ('', x'0195a8c07b2070008000112233445566', 1, x'0195a8c07b2270008000112233445566', 1752915605, 2, '{"name":"bash","arguments":"{\"command\":\"pytest tests/test_consumer.py -k prefetch\"}","call_id":"call_omnigent_1"}');
insert into conversation_items (workspace_id, conversation_id, position, id, created_at, type, data)
values ('', x'0195a8c07b2070008000112233445566', 2, x'0195a8c07b2370008000112233445566', 1752915608, 3, '{"call_id":"call_omnigent_1","output":"1 passed in 0.42s"}');
insert into conversation_items (workspace_id, conversation_id, position, id, created_at, type, data)
values ('', x'0195a8c07b2070008000112233445566', 3, x'0195a8c07b2470008000112233445566', 1752915610, 1, '{"role":"assistant","content":[{"type":"output_text","text":"Set channel.basic_qos(prefetch_count=16) before registering the consumer."}],"is_meta":false}');
