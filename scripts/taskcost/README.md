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

Run again on deja main at 54f6f6ab, eleven runs an arm, same model, the two
arms alternating run by run, all solved:

    arm                      tool calls   median tokens   wall
    nothing wired                  17.8         103,443    62s
    deja                            7.1          52,815    43s

The numbers move with the model and the harness; what the stand is for is the
gap between arms on one machine in one window, not the absolute figures. The
window is shorter than it looks: the same arm's median moved between 24k and
44k from one hour to the next on the same afternoon, and a sequential pair
(all of one arm, then all of the other) came out at 80% off where the
alternating one says 49%. Alternate the arms.

Two things that make an arm measure nothing, both silent: a home created fresh
by `deja install opencode-auto` has no opencode `auth.json`, so opencode exits
in seconds with zero tokens; and `run.sh` copies the history, builds the index
and pins the MCP env only for the arm named `deja`, so a second deja build under
another name needs its own fixture directory.
