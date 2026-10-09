-- D1b-live test probe (AGENT_AI.md T3; run with [host] test_lua=<s>:dgsim_agents_kill.lua): kills a nub and a Binding
-- with nGameObject.KillObject (GameObject_Kill 0x008E0210: HP 0, OnTookDamage/OnDealtDamage), so the behavior tree's
-- leaf 1 (DeathAbility, 0x008A9560) can start their AIDefinition.deathAbility (ability_BaseNub_Death,
-- ability_Tower_Death) on the next agent step. The Binding is the bottom lane's Binding_Spirit_Outer (x > 0), not the
-- top lane's that the test wave walks to. Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

-- every slot of the server pool (nObjectManager.GetObjectIdByIndex iterates pool slots, GMS_PROTOCOL.md D2g-live),
-- up to the first slot after the last live object (beyond the pool's count it asserts, SPObjectPool.cpp 161)
local function find(name, wantPositiveX)
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if nGameObject.GetAssetName(id) == name then
      local pos = nGameObject.GetPosition(id)
      if wantPositiveX == nil or (pos and (pos.x > 0) == wantPositiveX) then return id, pos end
    end
  end
end

local ok, err = pcall(function()
  local targets = {
    {name = dgsim_kill_nub or 'nub_spirit_v5_A', positiveX = nil},
    {name = dgsim_kill_binding or 'Binding_Spirit_Outer', positiveX = true},
  }
  for _, t in ipairs(targets) do
    local id, pos = find(t.name, t.positiveX)
    if id then
      local hp = nGameObject.GetHitPoints(id)
      nGameObject.KillObject(id)
      p('kill:', t.name, tostring(id), 'at', pos and string.format('(%.1f %.1f)', pos.x, pos.y) or '?', 'hp', hp, '->',
        nGameObject.GetHitPoints(id), 'alive', tostring(nGameObject.IsAlive(id)))
    else
      p('kill:', t.name, 'not found')
    end
  end
end)
if not ok then p('kill: ERROR', err) end
return table.concat(out, '\n')
