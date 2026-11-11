local M = {}

M.config = {
  bin = nil,
  limit = 40,
  picker = "auto",
  project_scoped = false,
  keymaps = false,
}

local cached_bin = nil

local function fallback_bins()
  local home = vim.env.HOME or vim.fn.expand("~")
  return {
    "/opt/homebrew/bin/deja",
    "/usr/local/bin/deja",
    home .. "/.local/bin/deja",
    home .. "/go/bin/deja",
  }
end

function M.resolve_bin()
  if M.config.bin and M.config.bin ~= "" then
    return M.config.bin
  end
  if cached_bin and vim.fn.executable(cached_bin) == 1 then
    return cached_bin
  end
  local exepath = vim.fn.exepath("deja")
  if exepath and exepath ~= "" then
    cached_bin = exepath
    return cached_bin
  end
  for _, p in ipairs(fallback_bins()) do
    if vim.fn.executable(p) == 1 then
      cached_bin = p
      return cached_bin
    end
  end
  return nil
end

function M.run(args, opts)
  opts = opts or {}
  local bin = M.resolve_bin()
  if not bin then
    return nil, "deja binary not found in PATH (install via `brew install deja-vu` or set `opts.bin`)"
  end
  local cmd = { bin }
  vim.list_extend(cmd, args)
  if vim.system then
    local res = vim.system(cmd, { text = true, cwd = opts.cwd }):wait()
    if res.code ~= 0 then
      local err = vim.trim((res.stderr ~= "" and res.stderr) or res.stdout or ("exit " .. tostring(res.code)))
      return nil, err
    end
    return res.stdout or "", nil
  end
  local out = vim.fn.system(cmd)
  if vim.v.shell_error ~= 0 then
    return nil, vim.trim(out or "command failed")
  end
  return out or "", nil
end

function M.run_json(args, opts)
  local out, err = M.run(args, opts)
  if not out then
    return nil, err
  end
  if vim.trim(out) == "" then
    return {}, nil
  end
  local ok, decoded = pcall(vim.json.decode, out)
  if not ok then
    return nil, "invalid JSON from deja: " .. tostring(decoded)
  end
  return decoded, nil
end

local function first_snippet(messages)
  if type(messages) ~= "table" then
    return ""
  end
  for _, m in ipairs(messages) do
    if type(m) == "table" and type(m.text) == "string" and vim.trim(m.text) ~= "" then
      return (m.text:gsub("%s+", " ")):sub(1, 180)
    end
  end
  return ""
end

local function normalize_session(raw, strict_flag)
  if type(raw) ~= "table" then
    return nil
  end
  local s = type(raw.session) == "table" and raw.session or raw
  if type(s.id) ~= "string" or s.id == "" then
    return nil
  end
  local snippet = s.snippet
  if (not snippet or snippet == "") and type(s.messages) == "table" then
    snippet = first_snippet(s.messages)
  end
  local updated = type(s.updated) == "string" and s.updated:sub(1, 16):gsub("T", " ") or ""
  return {
    id = s.id,
    short_id = s.id:sub(1, 8),
    harness = s.harness or "agent",
    project = s.project or "",
    path = s.path or "",
    title = (type(s.title) == "string" and vim.trim(s.title) ~= "") and vim.trim(s.title) or s.id:sub(1, 12),
    started = s.started or "",
    updated = updated,
    snippet = snippet or "",
    messages = type(s.messages) == "table" and s.messages or {},
    strict = strict_flag == true or raw.strict == true,
  }
end

function M.parse_search(decoded)
  local items = {}
  if type(decoded) ~= "table" then
    return items
  end
  local hits = vim.islist(decoded) and decoded or decoded.hits or decoded.sessions or {}
  for _, h in ipairs(hits) do
    local item = normalize_session(h, h.strict)
    if item then
      table.insert(items, item)
    end
  end
  return items
end

function M.parse_last(decoded)
  local items = {}
  if type(decoded) ~= "table" then
    return items
  end
  local list = vim.islist(decoded) and decoded or decoded.sessions or decoded.hits or {}
  for _, s in ipairs(list) do
    local item = normalize_session(s, false)
    if item then
      table.insert(items, item)
    end
  end
  return items
end

function M.parse_blame(decoded)
  local items = {}
  if type(decoded) ~= "table" then
    return items
  end
  local list = vim.islist(decoded) and decoded or decoded.hits or decoded.sessions or {}
  for _, entry in ipairs(list) do
    local item = normalize_session(entry, true)
    if item then
      table.insert(items, item)
    end
  end
  return items
end

function M.format_item(item)
  local proj = item.project ~= "" and (" · " .. item.project) or ""
  local ts = item.updated ~= "" and (" [" .. item.updated .. "]") or ""
  local summary = item.snippet ~= "" and item.snippet or item.title
  return string.format("[%s] %s%s%s — %s", item.harness, item.short_id, proj, ts, summary)
end

function M.format_session_markdown(envelope)
  local s = type(envelope) == "table" and (envelope.session or envelope) or {}
  local lines = {
    string.format("# %s", (type(s.title) == "string" and s.title ~= "") and s.title or (s.id or "Session")),
    "",
    string.format("- **Agent:** `%s`", s.harness or "unknown"),
    string.format("- **Session ID:** `%s`", s.id or "unknown"),
  }
  if s.project and s.project ~= "" then
    table.insert(lines, string.format("- **Project:** `%s`", s.project))
  end
  if s.updated and s.updated ~= "" then
    table.insert(lines, string.format("- **Updated:** `%s`", s.updated))
  end
  if type(envelope.touched) == "table" and #envelope.touched > 0 then
    table.insert(lines, string.format("- **Files touched (%d):** `%s`", #envelope.touched, table.concat(envelope.touched, "`, `")))
  end
  table.insert(lines, "")
  table.insert(lines, "---")
  local msgs = type(s.messages) == "table" and s.messages or {}
  for _, m in ipairs(msgs) do
    table.insert(lines, "")
    local hdr = string.format("### %s", (m.role or "message"):upper())
    if m.time and m.time ~= "" then
      hdr = hdr .. string.format(" (%s)", m.time:sub(1, 19):gsub("T", " "))
    end
    table.insert(lines, hdr)
    table.insert(lines, "")
    for _, l in ipairs(vim.split(tostring(m.text or ""), "\n", { plain = true })) do
      table.insert(lines, l)
    end
  end
  return lines
end

function M.open_float(lines, opts)
  opts = opts or {}
  local buf = vim.api.nvim_create_buf(false, true)
  vim.bo[buf].buftype = "nofile"
  vim.bo[buf].bufhidden = "wipe"
  vim.bo[buf].swapfile = false
  vim.bo[buf].filetype = opts.filetype or "markdown"
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, lines)
  vim.bo[buf].modifiable = false

  local width = math.min(120, math.max(60, math.floor(vim.o.columns * 0.82)))
  local height = math.min(40, math.max(14, math.floor(vim.o.lines * 0.78)))
  local win = vim.api.nvim_open_win(buf, true, {
    relative = "editor",
    width = width,
    height = height,
    row = math.floor((vim.o.lines - height) / 2),
    col = math.floor((vim.o.columns - width) / 2),
    style = "minimal",
    border = "rounded",
    title = opts.title and (" " .. opts.title .. " ") or " deja-vu ",
    title_pos = "center",
  })
  vim.wo[win].wrap = true
  vim.wo[win].linebreak = true
  vim.wo[win].cursorline = true

  local close = function()
    if vim.api.nvim_win_is_valid(win) then
      vim.api.nvim_win_close(win, true)
    end
  end
  vim.keymap.set("n", "q", close, { buffer = buf, nowait = true, silent = true })
  vim.keymap.set("n", "<Esc>", close, { buffer = buf, nowait = true, silent = true })
  if opts.session_id then
    vim.keymap.set("n", "y", function()
      vim.fn.setreg("+", opts.session_id)
      vim.notify("deja: copied session ID " .. opts.session_id, vim.log.levels.INFO)
    end, { buffer = buf, nowait = true, silent = true, desc = "Copy session ID" })
    vim.keymap.set("n", "H", function()
      close()
      M.handoff(opts.session_id)
    end, { buffer = buf, nowait = true, silent = true, desc = "Open handoff digest" })
    vim.keymap.set("n", "R", function()
      close()
      M.resume(opts.session_id, opts.harness)
    end, { buffer = buf, nowait = true, silent = true, desc = "Resume session in terminal" })
  end
  return buf, win
end

function M.show(id, harness)
  if not id or vim.trim(id) == "" then
    vim.notify("deja: session ID required", vim.log.levels.WARN)
    return
  end
  local args = { "show", vim.trim(id), "--json", "--limit", "200" }
  if harness and harness ~= "" then
    vim.list_extend(args, { "--harness", harness })
  end
  local decoded, err = M.run_json(args)
  if not decoded then
    vim.notify("deja show: " .. err, vim.log.levels.ERROR)
    return
  end
  local lines = M.format_session_markdown(decoded)
  local s = decoded.session or decoded
  M.open_float(lines, {
    title = string.format("deja · %s · %s (%s: quit q · yank id y · handoff H · resume R)", s.harness or "session", tostring(s.id or id):sub(1, 8), s.project or "local"),
    session_id = s.id or id,
    harness = s.harness or harness,
  })
end

function M.resume(id, harness)
  local args = { "resume", id }
  if harness and harness ~= "" then
    vim.list_extend(args, { "--harness", harness })
  end
  local out, err = M.run(args)
  if not out then
    vim.notify("deja resume: " .. err, vim.log.levels.ERROR)
    return
  end
  local cmd_line = nil
  local cwd = nil
  for _, line in ipairs(vim.split(out, "\n", { plain = true })) do
    local dir_part, rest = line:match("^cd%s+['\"]?(.-)['\"]?%s*&&%s*(.+)$")
    if dir_part and rest then
      cwd = dir_part
      cmd_line = rest
      break
    elseif vim.trim(line) ~= "" and not line:match("^#") then
      cmd_line = vim.trim(line)
    end
  end
  if not cmd_line then
    vim.notify("deja resume: nothing to run", vim.log.levels.WARN)
    return
  end
  vim.cmd("botright split")
  vim.fn.termopen(cmd_line, { cwd = cwd })
  vim.cmd("startinsert")
end

local function pick_items(items, title)
  if #items == 0 then
    vim.notify("deja: no matching sessions", vim.log.levels.INFO)
    return
  end
  local backend = M.config.picker or "auto"

  if (backend == "auto" or backend == "snacks") and package.loaded["snacks"] and _G.Snacks and _G.Snacks.picker then
    local snack_items = {}
    for idx, it in ipairs(items) do
      local preview_lines = {
        string.format("# [%s] %s", it.harness, it.title),
        "",
        string.format("- **ID:** `%s`", it.id),
        string.format("- **Project:** `%s`", it.project),
        string.format("- **Updated:** `%s`", it.updated),
        "",
      }
      if #it.messages > 0 then
        for _, m in ipairs(it.messages) do
          table.insert(preview_lines, string.format("### %s", (m.role or "turn"):upper()))
          table.insert(preview_lines, tostring(m.text or ""))
          table.insert(preview_lines, "")
        end
      elseif it.snippet ~= "" then
        table.insert(preview_lines, it.snippet)
      end
      table.insert(snack_items, {
        idx = idx,
        text = M.format_item(it),
        item = it,
        preview = { text = table.concat(preview_lines, "\n"), ft = "markdown" },
      })
    end
    _G.Snacks.picker.pick({
      title = title,
      items = snack_items,
      format = "text",
      preview = "preview",
      confirm = function(picker, selected)
        picker:close()
        if selected and selected.item then
          M.show(selected.item.id, selected.item.harness)
        end
      end,
    })
    return
  end

  if backend == "auto" or backend == "telescope" then
    local ok_pickers, pickers = pcall(require, "telescope.pickers")
    local ok_finders, finders = pcall(require, "telescope.finders")
    local ok_conf, conf_mod = pcall(require, "telescope.config")
    local ok_actions, actions = pcall(require, "telescope.actions")
    local ok_state, action_state = pcall(require, "telescope.actions.state")
    local ok_prev, previewers = pcall(require, "telescope.previewers")
    if ok_pickers and ok_finders and ok_conf and ok_actions and ok_state and ok_prev then
      pickers.new({}, {
        prompt_title = title,
        finder = finders.new_table({
          results = items,
          entry_maker = function(it)
            local display = M.format_item(it)
            return {
              value = it,
              display = display,
              ordinal = display .. " " .. it.id,
            }
          end,
        }),
        sorter = conf_mod.values.generic_sorter({}),
        previewer = previewers.new_buffer_previewer({
          title = "Session Preview",
          define_preview = function(self, entry)
            local it = entry.value
            vim.bo[self.state.bufnr].filetype = "markdown"
            local out, _ = M.run_json({ "show", it.id, "--json", "--limit", "40", "--harness", it.harness })
            local lines = out and M.format_session_markdown(out) or { it.title, "", it.snippet }
            vim.api.nvim_buf_set_lines(self.state.bufnr, 0, -1, false, lines)
          end,
        }),
        attach_mappings = function(prompt_bufnr)
          actions.select_default:replace(function()
            actions.close(prompt_bufnr)
            local sel = action_state.get_selected_entry()
            if sel and sel.value then
              M.show(sel.value.id, sel.value.harness)
            end
          end)
          return true
        end,
      }):find()
      return
    end
  end

  if backend == "auto" or backend == "fzf" then
    local ok_fzf, fzf = pcall(require, "fzf-lua")
    if ok_fzf then
      local map = {}
      local labels = {}
      for _, it in ipairs(items) do
        local lbl = M.format_item(it)
        map[lbl] = it
        table.insert(labels, lbl)
      end
      fzf.fzf_exec(labels, {
        prompt = title .. "> ",
        actions = {
          ["default"] = function(selected)
            if selected and selected[1] and map[selected[1]] then
              local it = map[selected[1]]
              M.show(it.id, it.harness)
            end
          end,
        },
      })
      return
    end
  end

  vim.ui.select(items, {
    prompt = title,
    format_item = M.format_item,
  }, function(choice)
    if choice then
      M.show(choice.id, choice.harness)
    end
  end)
end

function M.recall(query)
  local run_search = function(q)
    if not q or vim.trim(q) == "" then
      M.last()
      return
    end
    local args = { "search", "--json", "--limit", tostring(M.config.limit or 40) }
    if M.config.project_scoped then
      local cwd_name = vim.fn.fnamemodify(vim.fn.getcwd(), ":t")
      if cwd_name ~= "" then
        vim.list_extend(args, { "--project", cwd_name })
      end
    end
    vim.list_extend(args, { "--", vim.trim(q) })
    local decoded, err = M.run_json(args)
    if not decoded then
      vim.notify("deja search: " .. err, vim.log.levels.ERROR)
      return
    end
    pick_items(M.parse_search(decoded), "deja recall: " .. vim.trim(q))
  end

  if query and vim.trim(query) ~= "" then
    run_search(query)
    return
  end
  vim.ui.input({ prompt = "deja recall (blank for recent): " }, function(input)
    if input == nil then
      return
    end
    run_search(input)
  end)
end

function M.last(n)
  local count = tostring(tonumber(n) or M.config.limit or 40)
  local decoded, err = M.run_json({ "last", count, "--json" })
  if not decoded then
    vim.notify("deja last: " .. err, vim.log.levels.ERROR)
    return
  end
  pick_items(M.parse_last(decoded), "deja recent sessions")
end

function M.blame(opts)
  opts = opts or {}
  local file = vim.api.nvim_buf_get_name(0)
  if file == "" then
    vim.notify("deja blame: current buffer has no file path", vim.log.levels.WARN)
    return
  end
  local rel = vim.fn.fnamemodify(file, ":.")
  local target = opts.file_only and rel or string.format("%s:%d", rel, vim.api.nvim_win_get_cursor(0)[1])
  local decoded, err = M.run_json({ "blame", target, "--json" }, { cwd = vim.fn.getcwd() })
  if not decoded then
    vim.notify("deja blame: " .. err, vim.log.levels.ERROR)
    return
  end
  local items = M.parse_blame(decoded)
  if #items == 0 and not opts.file_only then
    decoded, err = M.run_json({ "blame", rel, "--json" }, { cwd = vim.fn.getcwd() })
    if decoded then
      items = M.parse_blame(decoded)
      target = rel
    end
  end
  if #items == 1 then
    M.show(items[1].id, items[1].harness)
    return
  end
  pick_items(items, "deja blame " .. target)
end

function M.handoff(id_or_agent)
  local args = { "handoff" }
  if id_or_agent and vim.trim(id_or_agent) ~= "" then
    table.insert(args, vim.trim(id_or_agent))
  end
  local out, err = M.run(args, { cwd = vim.fn.getcwd() })
  if not out then
    vim.notify("deja handoff: " .. err, vim.log.levels.ERROR)
    return
  end
  vim.fn.setreg("+", out)
  local lines = vim.split(out, "\n", { plain = true })
  M.open_float(lines, {
    title = "deja handoff (copied to + clipboard · press q to close)",
  })
end

function M.fix(err_query)
  local run_fix = function(q)
    if not q or vim.trim(q) == "" then
      return
    end
    local out, err = M.run({ "fix", vim.trim(q) }, { cwd = vim.fn.getcwd() })
    if not out then
      vim.notify("deja fix: " .. err, vim.log.levels.ERROR)
      return
    end
    M.open_float(vim.split(out, "\n", { plain = true }), {
      title = "deja fix: " .. vim.trim(q),
    })
  end

  if err_query and vim.trim(err_query) ~= "" then
    run_fix(err_query)
    return
  end
  local diags = vim.diagnostic.get(0, { lnum = vim.api.nvim_win_get_cursor(0)[1] - 1 })
  local default_msg = (#diags > 0 and diags[1].message) and diags[1].message:gsub("%s+", " ") or ""
  vim.ui.input({ prompt = "deja fix error: ", default = default_msg }, function(input)
    if input then
      run_fix(input)
    end
  end)
end

function M.statusline()
  local bin = M.resolve_bin()
  if not bin then
    return ""
  end
  local out, _ = M.run({ "statusline" })
  if not out then
    return ""
  end
  return vim.trim(out:gsub("\n.*", ""))
end

function M.setup(opts)
  M.config = vim.tbl_deep_extend("force", M.config, opts or {})
  if M.config.keymaps then
    local map = type(M.config.keymaps) == "table" and M.config.keymaps or {}
    vim.keymap.set("n", map.recall or "<leader>dr", function() M.recall() end, { desc = "Deja: Recall agent sessions" })
    vim.keymap.set("n", map.last or "<leader>dl", function() M.last() end, { desc = "Deja: Recent agent sessions" })
    vim.keymap.set("n", map.blame or "<leader>db", function() M.blame() end, { desc = "Deja: Blame line across AI sessions" })
    vim.keymap.set("n", map.handoff or "<leader>dh", function() M.handoff() end, { desc = "Deja: Copy & view handoff digest" })
    vim.keymap.set("n", map.fix or "<leader>df", function() M.fix() end, { desc = "Deja: Recall past fix for diagnostic" })
  end
  return M
end

return M
