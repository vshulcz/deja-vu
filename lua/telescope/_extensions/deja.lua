local ok, pickers = pcall(require, "telescope.pickers")
if not ok then
  error("deja telescope extension requires nvim-telescope/telescope.nvim")
end
local telescope = require("telescope")
local deja = require("deja")

return telescope.register_extension({
  setup = function(ext_config)
    if ext_config and next(ext_config) ~= nil then
      deja.setup(ext_config)
    end
  end,
  exports = {
    deja = function(opts)
      opts = opts or {}
      deja.recall(opts.query)
    end,
    recall = function(opts)
      opts = opts or {}
      deja.recall(opts.query)
    end,
    last = function(opts)
      opts = opts or {}
      deja.last(opts.limit)
    end,
    blame = function(opts)
      deja.blame(opts)
    end,
  },
})
