-- D5 test probe (SERVER_LUA_API.md tier 1, MATCH_FLOW.md *D5*; [host] test_lua=<s>:dgsim_d5_assault.lua, every few
-- seconds while the Mortal waves assault the Spirit base after dgsim_d5_clear_lane.lua): reports every Mortal lane
-- minion (HP, position, nAgent.GetBestTarget, the aggro list by name) and every Spirit base structure (the nubs and the
-- Guardian: HP, alive, how many minions have it as their best target). To keep the session short it then shortens the
-- fight, never the kill: a structure that a Mortal minion within dgsim_d5_range (25) units has as its best target, with
-- more than dgsim_d5_keep (150) HP left, takes one true-damage hit from that minion's snapshot down to 150 HP, so the
-- minions' own next hits kill it (the death by a lethal hit through the host's damage core and death processing).
-- Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f)', v.x, v.y) end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end

local keep = dgsim_d5_keep or 150
local range = dgsim_d5_range or 25

local ok, err = pcall(function()
  local live, seen = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if not seen[tostring(id)] then
      seen[tostring(id)] = true
      live[#live + 1] = id
    end
  end
  local names = {}
  local minions, structures = {}, {}
  for _, id in ipairs(live) do
    local name = nGameObject.GetAssetName(id) or '?'
    names[tostring(id)] = name
    if string.find(name, '^Minion_Mortal_') and nGameObject.IsAlive(id) then minions[#minions + 1] = id end
    if string.find(name, '^nub_spirit_') or name == 'GuardianSpirit_v5' then structures[#structures + 1] = id end
  end
  local function nameOf(id)
    if id == nil or id == kObjIDNone then return 'none' end
    return (names[tostring(id)] or '?') .. ':' .. tostring(id)
  end
  p('assault at match time', f1(nGameSimulator.GetMatchTime()) .. ':', #minions, 'Mortal minions alive')
  for _, m in ipairs(minions) do
    local list = {}
    local okList, targets = pcall(nAgent.GetTargetsOnAggroList, m)
    if okList and type(targets) == 'table' then
      for _, t in ipairs(targets) do list[#list + 1] = nameOf(t) end
    end
    p('  minion', nameOf(m), 'hp', f1(nGameObject.GetHitPoints(m)), 'at', vec(nGameObject.GetPosition(m)), 'best',
      nameOf(nAgent.GetBestTarget(m)), 'list [' .. table.concat(list, ' ') .. ']')
  end
  for _, s in ipairs(structures) do
    local hp = nGameObject.GetHitPoints(s)
    local alive = nGameObject.IsAlive(s)
    local spos = nGameObject.GetPosition(s)
    local attackers, near = 0, nil
    for _, m in ipairs(minions) do
      if nAgent.GetBestTarget(m) == s then
        attackers = attackers + 1
        local mpos = nGameObject.GetPosition(m)
        local dx, dy = mpos.x - spos.x, mpos.y - spos.y
        if near == nil and dx * dx + dy * dy <= range * range then near = m end
      end
    end
    local line = '  structure ' .. nameOf(s) .. ' hp ' .. f1(hp) .. ' alive ' .. tostring(alive) .. ' best target of ' ..
      attackers .. ' minion(s)'
    if alive and near and hp > keep + 1 then
      local snap = nAttribute.CreateAttributeSnapshot_FromObject(near, kObjIDNone)
      local hit, dealt = nGameObject.TakeDamage(snap, s, hp - keep, 0, nDescriptors.IsTrueDamage, kZeroVector)
      nAttribute.RemoveAttributeSnapshot(snap)
      line = line .. '; shortened: ' .. tostring(hit) .. ' ' .. f1(dealt) .. ' true damage from ' .. nameOf(near) ..
        '\'s snapshot -> hp ' .. f1(nGameObject.GetHitPoints(s))
    end
    p(line)
  end
end)
if not ok then p('assault: ERROR', err) end
return table.concat(out, '\n')
