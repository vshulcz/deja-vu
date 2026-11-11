if vim.g.loaded_deja_nvim == 1 then
  return
end
vim.g.loaded_deja_nvim = 1

vim.api.nvim_create_user_command("DejaRecall", function(cmd)
  local q = vim.trim(cmd.args or "")
  if q == "" and cmd.range > 0 then
    local lines = vim.api.nvim_buf_get_lines(0, cmd.line1 - 1, cmd.line2, false)
    q = vim.trim(table.concat(lines, " "))
  end
  require("deja").recall(q ~= "" and q or nil)
end, {
  nargs = "*",
  range = true,
  desc = "Search local coding agent sessions via deja-vu",
})

vim.api.nvim_create_user_command("DejaLast", function(cmd)
  require("deja").last(cmd.args ~= "" and tonumber(cmd.args) or nil)
end, {
  nargs = "?",
  desc = "Browse recent coding agent sessions via deja-vu",
})

vim.api.nvim_create_user_command("DejaBlame", function(cmd)
  require("deja").blame({ file_only = cmd.bang })
end, {
  bang = true,
  desc = "Find which AI coding session wrote the current line (:DejaBlame! for whole file)",
})

vim.api.nvim_create_user_command("DejaShow", function(cmd)
  require("deja").show(cmd.args)
end, {
  nargs = 1,
  desc = "Inspect a coding agent session by ID prefix",
})

vim.api.nvim_create_user_command("DejaHandoff", function(cmd)
  require("deja").handoff(cmd.args)
end, {
  nargs = "?",
  desc = "Generate and copy a cross-agent handoff digest",
})

vim.api.nvim_create_user_command("DejaFix", function(cmd)
  require("deja").fix(cmd.args)
end, {
  nargs = "*",
  desc = "Recall what fixed the current diagnostic or error string",
})
