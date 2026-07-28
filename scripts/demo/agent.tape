# The hero demo: a real coding agent recalling a real prior session, inside the
# TUI people actually look at.
#
# The point of deja is not that you can search your history — it is that the
# agent does it for you, unprompted. A demo of a person typing queries sells a
# search tool; this shows the thing being sold.
#
# The session is genuine — a real model, a real MCP tool call — against the
# synthetic corpus from ./scripts/demo, so nobody's actual history is published.
#
#   go run ./scripts/demo -out /tmp/demo-home
#   HOME=/tmp/demo-home deja index --rebuild
#   vhs scripts/demo/agent.tape
#
# Afterwards, ffmpeg cuts the dead time and paints over three regions that carry
# the recorder's identity rather than the tool: the welcome greeting, the
# account line in the header, and the custom statusline. Nothing else is edited.
#
#   ffmpeg -i agent-raw.gif -filter_complex \\
#     "drawbox=x=185:y=66:w=230:h=34:color=0x171717:t=fill, \\
#      drawbox=x=30:y=161:w=535:h=41:color=0x171717:t=fill, \\
#      drawbox=x=0:y=912:w=1200:h=68:color=0x171717:t=fill" out.gif

Output agent-raw.gif

Set Shell zsh
Set FontSize 15
Set Width 1200
Set Height 980
Set Padding 20
Set TypingSpeed 45ms
Set Framerate 24

# The recording is made from inside a Claude Code session, so the child-session
# markers have to go or the nested run writes its transcript somewhere else.
Hide
Type "cd /tmp/work-demo && unset CLAUDE_CODE_CHILD_SESSION CLAUDECODE CLAUDE_CODE_ENTRYPOINT && export PS1='$ ' && clear"
Enter
Sleep 500ms
Type "claude --mcp-config /tmp/demo-mcp.json --allowedTools 'mcp__deja__recall,mcp__deja__recall_context'"
Enter
# First run in a directory asks whether the folder is trusted; accept it while
# the recording is still hidden, or the question below lands in that prompt.
Sleep 4s
Enter
Sleep 8s
Show

Type "prepared statement errors behind pgbouncer again after upgrading the driver. did we hit this before?"
Sleep 600ms
Enter
Sleep 75s
