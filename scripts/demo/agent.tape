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
# Afterwards the raw recording is finished:
#
#   python3 scripts/demo/frame.py --height 1058   # window, corners, header patch
#   python3 scripts/demo/annotate.py              # callouts and spotlight
#   ffmpeg -i agent-raw.gif -i patch.png -i chrome.png -i corners.png \\
#          -i callout-asked.png -i spotlight.png -i callout-recall.png ...
#   gifsicle -O3 --lossy=20 hero-raw.gif -o assets/demo.gif
#
# That step crops the terminal to its content, replaces the two header lines
# carrying the recorder's account with neutral text, drops the custom
# statusline, hard-cuts the model's thinking time rather than fast-forwarding
# it, composites the result into a window, and fades in two callouts and a
# spotlight over the line that matters. Nothing in the session output is edited.
#
# Two things worth knowing before re-cutting it. A static PNG has one frame at
# t=0, so `fade` never fires on it — each overlay needs loop/fps first or it
# stays invisible. And `--lossy=60` looked fine on the bright frames and turned
# the dimmed ones to mud; 20 is the ceiling here.

Output agent-raw.gif

Set Shell zsh
Set FontSize 19
Set Width 1320
Set Height 1140
Set Padding 28
Set TypingSpeed 45ms
Set Framerate 50

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
Sleep 115s
