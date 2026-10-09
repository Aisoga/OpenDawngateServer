-- D1c-live test probe ([host] test_lua=<s>:dgsim_match_report.lua, any number of times): what the server Lua sees of
-- the match flow (docs/MATCH_FLOW.md §5): the match clock (nGameSimulator.GetMatchTime, T1), the Guardians
-- (nBattlegrounds.GetGuardianOnTeam, T2), the parasite (nDirector.GetParasiteID and its Untargetable attribute, T9),
-- nBattlegrounds.GetNubRespawnTuning (R5), and the NPC levels (nGameObject.GetNPCLevel, T4/T11) of the Guardians, the
-- Bindings, the creeps and the lane minions (counted per level). Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f)', v.x, v.y) end

local function describe(tag, id)
  if id == nil or id == kObjIDNone then
    p(tag, 'none')
    return
  end
  local untargetable = nAttribute.GetAttributeValue(id, nAttributeType.Untargetable)
  p(tag, tostring(id), nGameObject.GetAssetName(id), 'team', nGameObject.GetTeam(id), 'at', vec(nGameObject.GetPosition(id)),
    'hp', nGameObject.GetHitPoints(id), 'level', nGameObject.GetNPCLevel(id), 'untargetable', tostring(untargetable))
end

local ok, err = pcall(function()
  p('match time', nGameSimulator.GetMatchTime(), 'nub respawn tuning', nBattlegrounds.GetNubRespawnTuning())
  describe('guardian 1:', nBattlegrounds.GetGuardianOnTeam(nTeamID.Mortal))
  describe('guardian 2:', nBattlegrounds.GetGuardianOnTeam(nTeamID.Spirit))
  describe('parasite:', nDirector.GetParasiteID())
  -- levels per noun group, through the live objects (pool slots up to the first empty answer)
  local groups = {}
  local seen = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or '?'
      local group = nil
      if string.find(name, '^Binding_') then group = 'Binding'
      elseif string.find(name, '^Minion_') then group = 'minion'
      elseif string.find(name, '^Creep_Jungle_') then group = 'creep'
      elseif string.find(name, '^Guardian') then group = 'Guardian' end
      if group and nGameObject.IsAlive(id) then
        local lvl = nGameObject.GetNPCLevel(id)
        groups[group] = groups[group] or {}
        groups[group][lvl] = (groups[group][lvl] or 0) + 1
      end
    end
  end
  for _, g in ipairs({'Guardian', 'Binding', 'creep', 'minion'}) do
    local parts = {}
    for lvl, n in pairs(groups[g] or {}) do parts[#parts + 1] = 'level ' .. tostring(lvl) .. ' x' .. n end
    table.sort(parts)
    p('levels', g .. ':', #parts > 0 and table.concat(parts, ', ') or 'none alive')
  end
end)
if not ok then p('report: ERROR', err) end
return table.concat(out, '\n')
