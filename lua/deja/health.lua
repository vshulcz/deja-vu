local M = {}

function M.check()
  vim.health.start("deja-vu")

  if vim.fn.has("nvim-0.9.0") == 1 then
    vim.health.ok("Neovim >= 0.9.0")
  else
    vim.health.error("Neovim >= 0.9.0 is required")
  end

  local deja = require("deja")
  local bin = deja.resolve_bin()
  if bin then
    local ver, err = deja.run({ "--version" })
    if ver then
      vim.health.ok(string.format("Found executable: %s (%s)", bin, vim.trim(ver)))
    else
      vim.health.warn(string.format("Found %s, but `--version` failed: %s", bin, err or "unknown"))
    end
  else
    vim.health.error("`deja` executable not found", {
      "Install via Homebrew: `brew install deja-vu`",
      "Or via curl: `curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh`",
    })
  end

  local has_snacks = package.loaded["snacks"] ~= nil or pcall(require, "snacks")
  local has_telescope = pcall(require, "telescope")
  local has_fzf = pcall(require, "fzf-lua")
  if has_snacks then
    vim.health.ok("Picker backend available: snacks.picker")
  elseif has_telescope then
    vim.health.ok("Picker backend available: telescope.nvim")
  elseif has_fzf then
    vim.health.ok("Picker backend available: fzf-lua")
  else
    vim.health.info("No optional fuzzy picker detected (snacks.nvim / telescope.nvim / fzf-lua); falling back to vim.ui.select")
  end
end

return M
