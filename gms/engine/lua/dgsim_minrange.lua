-- MINRANGE probe ([host] test_lua=<s>:dgsim_minrange.lua, at 3 s and then every few seconds).
-- First run wraps the Tick of the lane minions' basic attacks (ability_Minion_Ranged, ability_minion_melee) so that every
-- attack records: match time, the attacker and target (asset names), centre distance, both footprint radii, the edge
-- distance (centre - radii, GameObject_IsWithinRangeOf's rule), the ability's range, and the attacker's position change
-- since its previous attack (is it still walking?). Every run returns and clears the records (logged by the host).
dgsim_minrange_log = dgsim_minrange_log or {}
local function f(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end
local function name(id) if id == nil or id == kObjIDNone then return 'none' end return (nGameObject.GetAssetName(id) or '?') .. ':' .. tostring(id) end
local function dist(a, b) local dx, dy = a.x - b.x, a.y - b.y return math.sqrt(dx * dx + dy * dy) end
dgsim_minrange_last = dgsim_minrange_last or {}
local function wrap(abName)
  local ab = _G[abName]
  if ab == nil or ab.__minrange then return end
  local orig = ab[nAbilityFns.Tick]
  ab.__minrange = true
  ab[nAbilityFns.Tick] = function(p1)
    local me, tg = p1.myObjectID, p1.myTargetID
    local ok, err = pcall(function()
      local mp, tp = nGameObject.GetPosition(me), nGameObject.GetPosition(tg)
      local c = dist(mp, tp)
      local rm, rt = nGameObject.GetFootprintRadius(me), nGameObject.GetFootprintRadius(tg)
      local key = tostring(me)
      local last = dgsim_minrange_last[key]
      local moved = last and dist(mp, last.pos) or -1
      dgsim_minrange_last[key] = { pos = mp, t = nGameSimulator.GetMatchTime() }
      dgsim_minrange_log[#dgsim_minrange_log + 1] = string.format('%s t=%.2f %s -> %s centre %s radii %s+%s edge %s range %s (moved %s since its last attack)',
        abName, nGameSimulator.GetMatchTime(), name(me), name(tg), f(c), f(rm), f(rt), f(c - rm - rt), f(nAbility.GetRankedValue(ab.range, p1.rank)), f(moved))
    end)
    if not ok then dgsim_minrange_log[#dgsim_minrange_log + 1] = 'ERR ' .. tostring(err) end
    return orig(p1)
  end
end
wrap('ability_Minion_Ranged')
wrap('ability_minion_melee')
local out = table.concat(dgsim_minrange_log, '\n')
dgsim_minrange_log = {}
return out
