// The plugin's decision, kept out of index.js because a host is free to treat
// anything a plugin module exports as part of the plugin — opencode does
// exactly that with its own — and this is a helper, not a second plugin.

// argv builds the call for a query somebody typed, rather than handing the text
// to deja as it stands.
//
// The command is always named. deja's bare-query path dispatches a first word
// that happens to be a command, so a one-word query went to the command of that
// name: `version` printed a version number, `index` rebuilt the index, and
// neither searched anything.
//
// A query that starts with a dash is read as a flag: `deja search --no-verify`
// exits on an unknown flag, the caller turns that into "", and the model is
// told the history holds nothing about --no-verify while eleven sessions
// discuss it — a confident wrong answer, which is worse than an error. `--`
// ends the flags. It is sent only when the query needs it, so a deja too old to
// know the terminator on this subcommand still answers every ordinary query.
export function argv(cmd, flags, text) {
  const arg = String(text);
  return arg.startsWith("-") ? [cmd, ...flags, "--", arg] : [cmd, ...flags, arg];
}

// guarded runs one registration, and swallows the host's refusal of a name it
// already holds.
//
// dsh answers a duplicate with "prompt context deja:recall is already
// registered" or "command deja is already registered", and that failure is not
// local: the whole plugin tree fails to load, so a second copy of this plugin —
// the npm package next to the installer's files, a --patch overlay naming it
// twice — costs the user their agent over an optional memory plugin. One
// registration is all any of these need, so the loser stands down.
export function guarded(register) {
  try {
    register();
    return true;
  } catch {
    return false;
  }
}

// contributions says what this package adds, given what `deja install` already
// wrote into DSH_HOME and what the profile turned off. The rule is the same
// everywhere: fill the gaps, never repeat the installer.
//
// command.js and the mcp-deja profile row are written by the same install, so
// the command file standing there means the model can already reach deja —
// registering these tools would list every answer twice. auto.js is separate:
// only `deja install dsh-auto` writes it, so a profile can take the command
// from the installer and still want recall from here.
export function contributions(wiring, config) {
  const wired = wiring || {};
  const settings = config || {};
  return {
    tools: !wired.command,
    command: !wired.command,
    recall: settings.autoRecall !== false && !wired.auto,
  };
}
