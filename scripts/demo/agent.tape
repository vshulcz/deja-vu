# The hero demo: a real coding agent recalling a real prior session.
#
# The point of deja is not that you can search your history — it is that the
# agent does it for you, unprompted. The earlier demo showed a person typing
# queries, which sells a search tool. This shows the thing being sold: you
# describe a symptom, and the agent opens with what you decided months ago.
#
# The session is genuine — a real model, a real MCP tool call — against the
# synthetic corpus from ./scripts/demo, so nobody's actual history is published.
#
#   go run ./scripts/demo -out /tmp/demo-home
#   HOME=/tmp/demo-home deja index --rebuild
#   vhs scripts/demo/agent.tape
#
# Dead time is cut afterwards with ffmpeg; nothing in the output is edited.

Output agent-raw.gif

Set Shell zsh
Set FontSize 17
Set Width 1200
Set Height 620
Set Padding 24
Set TypingSpeed 40ms
Set Framerate 24

Hide
Type "cd /tmp/work-demo && export PATH=/tmp/demobin:$PATH PS1='$ ' && clear"
Enter
Show

Sleep 500ms

Type "codex 'prepared statement errors behind pgbouncer again after upgrading the driver. did we hit this before?'"
Sleep 400ms
Enter
Sleep 60s
