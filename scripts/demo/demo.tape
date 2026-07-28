# The README demo. Render with:
#
#   go run ./scripts/demo -out /tmp/demo-home
#   rm -rf /tmp/demo-home/.cache/deja
#   vhs scripts/demo/demo.tape
#
# Three beats, in the order someone discovers the tool:
#
#   1. One command, no setup, and the answer comes from eight months before
#      deja was installed. This is the whole pitch — every other memory tool
#      starts empty and records forward.
#   2. A second question answered instantly, because the index is already built.
#   3. Proof it spans agents and time, rather than a claim that it does.
#
# The corpus is synthetic on purpose: a demo recorded against real history
# would publish someone's work.

Output assets/demo.gif

Set Shell zsh
Set FontSize 17
Set Width 1200
Set Height 560
Set Padding 24
Set TypingSpeed 40ms
Set Framerate 24

Hide
Type "export HOME=/tmp/demo-home PATH=/tmp/demobin:$PATH PS1='$ '"
Enter
Type "clear"
Enter
Show

Sleep 800ms

# Beat 1 — nothing has been set up, and the answer is from last November.
Type "deja 'pgbouncer prepared statements'"
Sleep 400ms
Enter
Sleep 5s

# Beat 2 — the index is warm now; the second question is instant.
Type "deja 'move the queue off redis'"
Sleep 400ms
Enter
Sleep 4s

# Beat 3 — across agents, across months. Cleared first so the numbers land on
# an empty screen instead of scrolling in under the earlier answers.
Hide
Type "clear"
Enter
Show
Type "deja stats | head -14"
Sleep 400ms
Enter
Sleep 5s
