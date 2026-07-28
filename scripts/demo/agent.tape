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
# Afterwards the raw recording is finished with ffmpeg and scripts/demo/frame.py:
#
#   python3 scripts/demo/frame.py            # window chrome + rounded corners
#   ffmpeg -i agent-raw.gif -i chrome.png -i corners.png -filter_complex "..."
#
# That step crops the terminal to its content, paints over the three regions
# carrying the recorder's identity rather than the tool (the greeting, the
# account line, the custom statusline), hard-cuts the model's thinking time
# instead of fast-forwarding it, and composites the result into a window.
# Nothing in the session output is edited.
