-- ADAMSLOW test probe ([host] test_lua=<from>-<to>/<step>:dgsim_adamslow_scan.lua, after dgsim_adamslow_lib.lua):
-- every live object holding Lockdown's Slow
-- (modifier_Item_Legendary_OnTookPhysical_AoESlow_Slow, 3 s), each instance logged once with its team, its caster and
-- the relation to the caster (enemy / NEUTRAL / ALLY / SELF), and a totals line when something new turned up. Repeat it
-- every second or so: a Slow lasts 3 s. Returns a report (empty when nothing is new).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local ok, err = pcall(function()
  if dgsim_adamslow == nil then
    p('adamslow scan: no dgsim_adamslow (run dgsim_adamslow_lib.lua first)')
    return
  end
  dgsim_adamslow.scan('tick', p)
end)
if not ok then p('adamslow scan: ERROR', err) end
return table.concat(out, '\n')
