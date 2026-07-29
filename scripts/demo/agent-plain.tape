# Act one of the README demo: the same question, asked of the same agent, with
# no memory wired in.
#
# This is the half that makes the other half mean anything. Without it a viewer
# sees a good answer and assumes the model is clever; with it they see that the
# model had no idea, and that the difference is the tool.
#
# Identical to agent.tape except that every MCP server is switched off.
#
#   vhs scripts/demo/agent-plain.tape

Output plain-raw.gif

Set Shell zsh
Set FontSize 19
Set Width 1320
Set Height 1140
Set Padding 28
Set TypingSpeed 45ms
Set Framerate 50

Hide
Type "cd /tmp/work-plain && unset CLAUDE_CODE_CHILD_SESSION CLAUDECODE CLAUDE_CODE_ENTRYPOINT && export PS1='$ ' && clear"
Enter
Sleep 500ms
# --strict-mcp-config with an empty set is what makes this act honest: the
# recording machine has deja wired into Claude Code globally, so a bare
# `claude` would still call it and there would be no contrast to show. The
# same goes for skills: deja installs one, so slash commands are off too.
Type "claude --strict-mcp-config --mcp-config /tmp/empty-mcp.json --disable-slash-commands"
Enter
Sleep 4s
Enter
Sleep 8s
Show

Type "prepared statement errors behind pgbouncer again after upgrading the driver. did we hit this before?"
Sleep 600ms
Enter
Sleep 70s
