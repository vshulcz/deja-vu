# taskcost — what finishing one piece of work costs

`scripts/day0compare` asks what a memory tool can answer the minute it is
installed. This asks the other half: on a machine whose history already holds
the answer, what does it cost an agent to finish a piece of work — in tool
calls, in tokens, in wall clock.

The repository is the expensive part on purpose: 40 packages of 8 files, and the
answer in two places an agent will not guess (a build tag and an environment
variable) with a stale document naming the wrong variable. Two prior sessions on
the machine did this work and recorded the command that passed.

    python3 scripts/taskcost/fixture.py /tmp/taskcost
    for n in 1 2 3 4 5 6; do scripts/taskcost/run.sh /tmp/taskcost none $n; done
    for n in 1 2 3 4 5 6; do scripts/taskcost/run.sh /tmp/taskcost deja $n; done

Each run prints one CSV row: arm, run, seconds, tool calls, input, output,
reasoning, cache read, total tokens, whether the tree was modified, whether the
suite was proved green. The task forbids editing files, so a row with `1` in the
modified column is a run that did something else and should be dropped.

Another memory tool is an arm too: wire it into an opencode home the way its own
install writes it, leave that home at `<fixture>/home-<arm>`, and pass `<arm>`.
Pin whatever env its MCP server needs inside that config — opencode does not
pass the parent environment to an MCP child, so a tool whose store lives under
`$HOME` will otherwise read the run's empty home and answer nothing.

Measured with opencode 1.18.32 and `openai/gpt-5.6-luna`, six to twelve runs an
arm, all solved:

    arm                      tool calls   median tokens   wall
    nothing wired                  17.9         126,222    67s
    agentmemory 0.9.29             15.9         104,974    70s
    deja                            9.8          53,558    45s

The numbers move with the model and the harness; what the stand is for is the
gap between arms on one machine in one window, not the absolute figures.
