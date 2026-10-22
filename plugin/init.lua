local ttt = require("ttt")
local rpc = require("ttt.rpc")
local json = require("ttt.json")
local sys = require("ttt.system")
local events = require("ttt.events")
local fs = require("ttt.fs")
local automation = require("ttt.automation")

local conn, connecting, stopped = nil, false, false
local pending = {}
local generation = 0
local drawer_spec, drawer_panel, drawer_open
local callbacks = { "on_click", "on_change", "on_submit", "on_select", "on_expand", "on_command", "on_menu" }
local containers = { vstack = true, hstack = true, scrollview = true, box = true }
local widgets = {
  label = true, title = true, keyvalue = true, tree = true, list = true, button = true, checkbox = true,
  input = true, vstack = true, hstack = true, scrollview = true, box = true, divider = true, dropdown = true,
  progress = true, table = true, markdown = true,
}
local raw = { text = true, cell = true }

local function emit_widget(node, cb, ...)
  if not conn then return end
  conn:notify("editor/event", { name = "drawer." .. cb, data = { id = node.id, type = node.type, args = { ... } } })
end

local function wants(node, cb)
  if node.events == nil then return not (node.type == "input" and cb == "on_change") end
  for _, name in ipairs(node.events) do
    if name == cb then return true end
  end
  return false
end

local function validate(nodes, path, top)
  if nodes == nil then return end
  if type(nodes) ~= "table" then error(path .. " must be an array") end
  for i, node in ipairs(nodes) do
    local at = path .. "[" .. i .. "]"
    if type(node) ~= "table" then error(at .. " must be a table") end
    local kind = node.type
    if raw[kind] then
      if not top then error(at .. ": " .. kind .. " is only allowed at the top level") end
      if type(node.x) ~= "number" or type(node.y) ~= "number" then error(at .. ": x and y must be numbers") end
    elseif not widgets[kind] then
      error(at .. ": unknown widget type " .. tostring(kind))
    end
    if node.events ~= nil then
      if type(node.events) ~= "table" then error(at .. ".events must be an array") end
      for _, name in ipairs(node.events) do
        local known = false
        for _, cb in ipairs(callbacks) do known = known or cb == name end
        if not known then error(at .. ": unknown event " .. tostring(name)) end
      end
    end
    if containers[kind] then validate(node.children, at .. ".children", false) end
  end
end

local function build(panel, nodes)
  for _, node in ipairs(nodes or {}) do
    local kind = node.type
    if raw[kind] then
      panel[kind](panel, node.x, node.y, node.text or node.char, node.style)
    elseif kind == "divider" then
      panel:divider()
    elseif type(node.value) == "string" and (kind == "label" or kind == "title" or kind == "markdown") then
      panel[kind](panel, node.value)
    else
      local cfg = {}
      for k, v in pairs(kind == "keyvalue" and (node.items or {}) or node) do cfg[k] = v end
      if kind == "keyvalue" then
        for k, v in pairs(node) do
          if k:match("^margin_") or k:match("^padding_") then cfg[k] = v end
        end
      else
        cfg.type, cfg.children, cfg.events = nil, nil, nil
      end
      for _, cb in ipairs(callbacks) do
        if wants(node, cb) then cfg[cb] = function(...) emit_widget(node, cb, ...) end end
      end
      if kind == "checkbox" then
        cfg.on_change = function(checked)
          node.checked = checked
          if wants(node, "on_change") then emit_widget(node, "on_change", checked) end
        end
      end
      if containers[kind] then
        cfg.render = function(child) build(child, node.children) end
      end
      panel[kind](panel, cfg)
    end
  end
end

local function check_spec(spec)
  if type(spec) ~= "table" then error("spec must be a table") end
  validate(spec.children, "children", true)
end

local drawer = {
  open = function(spec)
    check_spec(spec)
    drawer_spec, drawer_panel, drawer_open = spec, nil, true
    ttt.open_drawer({
      width = spec.width, min_width = spec.min_width, side = spec.side,
      render = function(panel)
        drawer_panel = panel
        if drawer_spec then build(panel, drawer_spec.children) end
      end,
    })
  end,
  update = function(spec)
    if not drawer_open then error("no drawer open") end
    check_spec(spec)
    drawer_spec = spec
    if drawer_panel then drawer_panel:redraw() end
  end,
  close = function()
    drawer_spec, drawer_panel, drawer_open = nil, nil, false
    ttt.close_drawer()
  end,
}

local modules = {
  ttt = ttt, ["ttt.editor"] = require("ttt.editor"), ["ttt.terminal"] = require("ttt.terminal"),
  ["ttt.decorations"] = require("ttt.decorations"), ["mcp.drawer"] = drawer,
}
local signatures = {
  ["ttt.editor"] = {
    buffer_text = "() -> string", buffer_lines = "() -> string[]", current_line = "() -> string",
    get_line = "(line) -> string", line_count = "() -> number", viewport = "() -> table",
    cursor = "() -> {line, col}", selection = "() -> table", selection_text = "() -> string",
    file_path = "() -> string", file_name = "() -> string", language = "() -> string",
    byte_to_col = "(text, byte_offset) -> col", col_to_byte = "(text, col) -> byte_offset",
    get_cursors = "() -> table[]", scroll_to = "(line)", scroll_by = "(delta)",
    insert = "(line, col, text)", set_line = "(line, text)", replace = "(start_line, start_col, end_line, end_col, text)",
    set_cursor = "(line, col)", set_selection = "(start_line, start_col, end_line, end_col)", clear_selection = "()",
    begin_undo_group = "()", end_undo_group = "()", add_cursor = "(line, col)", clear_cursors = "()",
    set_search = "(pattern, use_regex?)", clear_search = "()",
  },
  ["ttt.terminal"] = {
    spawn = "({shell, args?, cwd?, label?, env?}) -> id, err", send = "(id, text) -> ok, err",
    focus = "(id) -> ok, err", close = "(id) -> ok, err", read = "(id, max_bytes?) -> text, err",
    screen = "(id) -> table, err", list = "() -> table[]",
  },
  ["mcp.drawer"] = {
    open = "({width?, min_width?, side?, children: node[]}); node = {type, id?, events?, children?, ...widget fields}; "
      .. "type: label, title, markdown (value=string or text=, style=...), keyvalue{items}, tree, list, button, "
      .. "checkbox, input, dropdown, progress, table, divider, containers vstack/hstack/scrollview/box (children), "
      .. "top-level only: text{x,y,text,style}, cell{x,y,char,style} (x,y relative to the drawer); "
      .. "events limits callbacks (on_click, on_change, on_submit, on_select, on_expand, on_command, on_menu), "
      .. "default all except input on_change; callbacks arrive as drawer.<callback> events with {id, type, args}; "
      .. "checkbox state is kept until the next open/update; the user may dismiss the drawer without an event",
    update = "(spec) re-render the open drawer with a new spec; widget state is kept by position",
    close = "()",
  },
  ["ttt.decorations"] = {
    set = "(path, {line, end_line?, col?, end_col?, style?, note?}[])", clear = "(path?)",
  },
  ttt = {
    version ="() -> string", platform = "() -> string", arch = "() -> string", graphics = "() -> table",
    plugin_dir = "() -> string", open_file = "(path, line?, readonly?)", exec_command = "(id) -> bool",
    list_commands = "() -> {id, title}[]", clipboard_read = "() -> string", clipboard_write = "(text)",
    screenshot = "(path?) -> text", debug = "(path?) -> JSON string", notify = "(message, level?)",
    key = "(combo)", type = "(text)", click = "(x, y)", drag = "(x1, y1, x2, y2)", quit = "()",
  },
}

local status_icons = { connected = "◉", connecting = "◌", offline = "◌", failed = "⚠" }
local status_state

local function status(state)
  status_state = state
  ttt.set_status_item("right", "connection", "MCP " .. status_icons[state], { priority = 1100 })
end

local function api_list()
  local result = {}
  for name, mod in pairs(modules) do
    local functions = {}
    for key, value in pairs(mod) do
      if type(value) == "function" then
        functions[#functions + 1] = { name = key, signature = (signatures[name] or {})[key] }
      end
    end
    table.sort(functions, function(a, b) return a.name < b.name end)
    result[name] = functions
  end
  return result
end

local function run(request)
  local channel = conn
  local action = request.action
  if action.action == "api.call" and (action.params or {}).module == "ttt" then
    local params = action.params
    local name, args = params["function"], params.args or {}
    if name == "key" or name == "type" then
      action = { action = name, args = args[1] }
    elseif name == "click" or name == "drag" then
      local coords = {}
      local count = name == "drag" and 4 or 2
      for i = 1, count do coords[i] = tostring(args[i]) end
      action = { action = name, args = table.concat(coords, " ") }
    elseif name == "quit" then
      action = { action = "quit" }
    end
  end
  local function complete(result, err)
    pending[request.id] = nil
    if channel then
      channel:notify("operation/complete", { id = request.id, result = result, error = err })
    end
  end
  if action.action == "api.list" then complete(api_list()); return end
  if action.action == "api.call" then
    local params = action.params or {}
    local mod = modules[params.module]
    local fn = mod and mod[params["function"]]
    if type(fn) ~= "function" then error("unknown API function") end
    local values = { pcall(fn, unpack(params.args or {})) }
    if not values[1] then complete(nil, tostring(values[2])); return end
    if (values[2] == nil or values[2] == false) and type(values[3]) == "string" then
      complete(nil, values[3]); return
    end
    table.remove(values, 1)
    ttt.set_timeout(0, function() complete({ values = values }) end)
    return
  end
  if action.action == "quit" or action.action == "shutdown" then
    complete({ shutdown_requested = true })
    ttt.set_timeout(100, function() ttt.quit() end)
    return
  end
  local args = action.args or ""
  if action.action == "open" then args = assert(json.encode(action.params or {})) end
  local path
  if (action.action == "screenshot" or action.action == "debug") and args ~= "" then
    path, args = args, ""
  end
  pending[request.id] = automation.run(action.action, args, function(result, err)
    if not err and path then
      local text = result
      if action.action == "debug" then text = assert(json.encode(result)) end
      local ok, write_err = fs.write(path, text)
      if not ok then err = write_err end
    end
    complete(result, err)
  end)
end

local function connect()
  if conn or connecting or stopped then return end
  connecting = true
  -- Retries run every 2s; keep a failure visible instead of flickering.
  if status_state ~= "failed" then status("connecting") end
  generation = generation + 1
  local attempt = generation
  sys.exec_async("ttt-mcp", { "service", "ensure" }, function(result)
    if attempt ~= generation then return end
    if result.exit_code ~= 0 then
      connecting = false
      status("failed")
      ttt.log("warn", "ttt-mcp: " .. (result.stderr or "service unavailable"))
      return
    end
    local service, decode_err = json.decode(result.stdout)
    if not service then connecting = false; ttt.log("error", tostring(decode_err)); return end
    if stopped then connecting = false; return end
    local endpoint = service.endpoint
    if endpoint:sub(1, 5) == "unix:" and sys.env("TTT_MCP_DIR") == "" then
      endpoint = "unix:~/.cache/ttt-mcp/hub.sock"
    end
    local ok, connect_error = pcall(rpc.connect, endpoint, {
      on_notification = function(method, params)
        if attempt ~= generation then return end
        if method == "operation/cancel" and pending[params.id] then pending[params.id]() end
      end,
      on_request = function(method, params)
        if attempt ~= generation then error("connection superseded") end
        if method ~= "operation/run" then error("unknown method " .. method) end
        local ok, err = pcall(run, params)
        if not ok and conn then conn:notify("operation/complete", { id = params.id, error = tostring(err) }) end
        return { queued = true }
      end,
      on_close = function()
        if attempt ~= generation then return end
        for _, cancel in pairs(pending) do cancel() end
        pending = {}
        conn, connecting = nil, false
        status("offline")
      end,
    }, function(channel, err)
      if attempt ~= generation then if channel then channel:close() end; return end
      connecting = false
      if not channel then status("failed"); ttt.log("warn", "ttt-mcp: " .. tostring(err)); return end
      if stopped then channel:close(); return end
      conn = channel
      automation.run("session", "", function(session, session_err)
        if session_err then channel:close(); return end
        session.name = sys.env("TTT_MCP_NAME")
        if session.name == "" then session.name = session.workspaces[1] or ("ttt " .. tostring(session.pid)) end
        channel:request("hello", { role = "editor", token = service.token, session = session }, function(_, hello_err)
          if hello_err then ttt.log("error", "ttt-mcp: " .. hello_err); channel:close(); return end
          status("connected")
        end)
      end)
    end)
    if not ok then
      connecting = false
      status("failed")
      ttt.log("error", "ttt-mcp: " .. tostring(connect_error))
    end
  end)
end

ttt.register({ commands = {
  { id = "mcp.connect", title = "MCP: Connect", handler = function() stopped = false; connect() end },
  { id = "mcp.disconnect", title = "MCP: Disconnect", handler = function()
    stopped = true
    generation = generation + 1
    connecting = false
    for _, cancel in pairs(pending) do cancel() end
    pending = {}
    local channel = conn
    conn = nil
    if channel then channel:close() end
    status("offline")
  end },
} })

for _, name in ipairs({ "file.open", "file.close", "file.save", "editor.change", "cursor.change", "tab.change" }) do
  events.on(name, function(data)
    if not conn then return end
    conn:notify("editor/event", { name = name, data = data })
    conn:notify("session/update", { active_file = modules["ttt.editor"].file_path() })
  end)
end

ttt.set_timeout(0, connect)
ttt.set_interval(2000, function()
  if not conn then connect(); return end
  local channel = conn
  automation.run("session", "", function(session, err)
    if not err and conn == channel then channel:notify("session/update", session) end
  end)
end)
