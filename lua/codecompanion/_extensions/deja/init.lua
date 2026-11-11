---@class DejaCodeCompanionExtension
local Extension = {}

local defaults = {
  slash_command = "deja",
  editor_context = "deja",
  limit = 40,
  turn_limit = 60,
}

local ext_opts = vim.deepcopy(defaults)

---@param chat table
---@param id string
---@param content string
local function attach_chat_context(chat, id, content)
  if type(chat.add_context) == "function" then
    local ok = pcall(chat.add_context, chat, { role = "user", content = content }, {
      source = "deja",
      id = id,
      tag = "deja",
    })
    if ok then
      return
    end
    pcall(chat.add_context, chat, { role = "user", content = content }, "deja", id)
    return
  end
  if type(chat.add_buf_message) == "function" then
    chat:add_buf_message({ role = "user", content = content })
  end
end

---@param item table
---@return string|nil, string|nil
local function build_session_digest(item)
  local deja = require("deja")
  local handoff, _ = deja.run({ "handoff", item.id }, { cwd = vim.fn.getcwd() })
  if handoff and vim.trim(handoff) ~= "" then
    return string.format("<deja_session id=%q agent=%q project=%q>\n%s\n</deja_session>", item.id, item.harness or "agent", item.project or "", vim.trim(handoff)), nil
  end
  local args = { "show", item.id, "--json", "--limit", tostring(ext_opts.turn_limit or 60) }
  if item.harness and item.harness ~= "" then
    vim.list_extend(args, { "--harness", item.harness })
  end
  local decoded, err = deja.run_json(args)
  if not decoded then
    return nil, err
  end
  local md = table.concat(deja.format_session_markdown(decoded), "\n")
  return string.format("<deja_session id=%q agent=%q>\n%s\n</deja_session>", item.id, item.harness or "agent", md), nil
end

---@param ctx? table
---@return string
local function resolve_editor_context(ctx)
  local deja = require("deja")
  local target = ctx and ((ctx.target and ctx.target ~= "" and ctx.target) or (ctx.params and ctx.params ~= "" and ctx.params)) or nil
  if target then
    local out, err = deja.run({ "recall", target }, { cwd = vim.fn.getcwd() })
    if out and vim.trim(out) ~= "" then
      return "<deja_recall query=" .. vim.inspect(target) .. ">\n" .. vim.trim(out) .. "\n</deja_recall>"
    end
    return "No prior AI coding session matches found for " .. target .. (err and (" (" .. err .. ")") or "")
  end
  local out, err = deja.run({ "handoff" }, { cwd = vim.fn.getcwd() })
  if out and vim.trim(out) ~= "" then
    return "<deja_handoff>\n" .. vim.trim(out) .. "\n</deja_handoff>"
  end
  return "No recent AI coding session handoff available" .. (err and (" (" .. err .. ")") or "")
end

---@param opts? table
function Extension.setup(opts)
  ext_opts = vim.tbl_deep_extend("force", vim.deepcopy(defaults), opts or {})
  local ok_cc, cc_config = pcall(require, "codecompanion.config")
  if not ok_cc or type(cc_config) ~= "table" then
    return
  end

  local cfg = cc_config.config or cc_config
  local interactions = cfg.interactions or cfg.strategies
  if type(interactions) ~= "table" then
    return
  end

  if ext_opts.slash_command and ext_opts.slash_command ~= false and interactions.chat and interactions.chat.slash_commands then
    interactions.chat.slash_commands[ext_opts.slash_command] = {
      description = "Attach digest or transcript from a past coding agent session (deja-vu)",
      callback = function(chat)
        vim.ui.input({ prompt = "deja recall for chat (blank for recent): " }, function(input)
          if input == nil then
            return
          end
          require("deja").pick({
            query = input,
            limit = ext_opts.limit,
            title = "Attach session to CodeCompanion",
            on_choice = function(item)
              local content, err = build_session_digest(item)
              if not content then
                vim.notify("deja: " .. tostring(err), vim.log.levels.ERROR)
                return
              end
              local tag_id = string.format("<deja:%s:%s>", item.harness or "agent", item.short_id or item.id:sub(1, 8))
              attach_chat_context(chat, tag_id, content)
              vim.notify(string.format("deja: attached %s (%s) to chat", item.short_id or item.id:sub(1, 8), item.harness or "agent"), vim.log.levels.INFO)
            end,
          })
        end)
      end,
      opts = {
        contains_code = true,
      },
    }
  end

  local ec_table = (interactions.shared and interactions.shared.editor_context)
    or (interactions.chat and interactions.chat.editor_context)
    or (interactions.chat and interactions.chat.variables)
  if ext_opts.editor_context and ext_opts.editor_context ~= false and type(ec_table) == "table" then
    ec_table[ext_opts.editor_context] = {
      description = "Share recent cross-agent session handoff or recall digest (deja-vu)",
      callback = resolve_editor_context,
      opts = {
        contains_code = true,
        has_params = true,
      },
    }
  end
end

Extension.exports = {
  handoff = function(id_or_agent)
    local args = { "handoff" }
    if id_or_agent and vim.trim(id_or_agent) ~= "" then
      table.insert(args, vim.trim(id_or_agent))
    end
    local out, _ = require("deja").run(args, { cwd = vim.fn.getcwd() })
    return out or ""
  end,
  recall = function(query)
    if not query or vim.trim(query) == "" then
      return ""
    end
    local out, _ = require("deja").run({ "recall", vim.trim(query) }, { cwd = vim.fn.getcwd() })
    return out or ""
  end,
}

return Extension
