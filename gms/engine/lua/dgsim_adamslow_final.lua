-- ADAMSLOW test probe ([host] test_lua=<s>:dgsim_adamslow_final.lua, last): a last scan and the totals line of every Slow instance seen (`adamslow scan final: ...`).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local ok, err = pcall(function()
  if dgsim_adamslow == nil then
    p('adamslow final: no dgsim_adamslow (run dgsim_adamslow_lib.lua first)')
    return
  end
  dgsim_adamslow.scan('final', p)
end)
if not ok then p('adamslow final: ERROR', err) end
return table.concat(out, '\n')
