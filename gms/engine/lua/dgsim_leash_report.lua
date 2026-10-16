-- LEASH live probe: a report of the jungle creatures and well workers near the human, for
-- the kiting sessions. [host] test_lua=<s>:dgsim_leash_report.lua. Each line: the NPC, its distance from its home (a
-- creep's initial position, a worker's well centre), HP, Untargetable, in combat, the best target and the aggro list;
-- a worker also its return-to-work point. The host's own "leash:" lines in dgsim.log say how far each fight went.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end
local function xy(v) if not v then return 'nil' end return string.format('(%.1f, %.1f)', v.x, v.y) end
local function dist(a, b) local dx, dy = a.x - b.x, a.y - b.y return math.sqrt(dx * dx + dy * dy) end

local WELLS = {
  nVector.create(182.5, -27.9, 0), nVector.create(-27.6, 182.7, 0),
  nVector.create(-182.3, 27.9, 0), nVector.create(28.2, -182.6, 0),
}

local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c, pid end
  end
end

local function aggro(id)
  local ok, targets = pcall(nAgent.GetTargetsOnAggroList, id)
  local s = {}
  if ok and type(targets) == 'table' then for _, t in ipairs(targets) do s[#s + 1] = tostring(t) end end
  return '[' .. table.concat(s, ' ') .. ']'
end

local ok, err = pcall(function()
  local me = human()
  if not me then
    p('leash report: no human player creature')
    return
  end
  local here = nGameObject.GetPosition(me)
  p('leash report: match time', f1(nGameSimulator.GetMatchTime()), 'human', tostring(me), 'at', xy(here), 'hp',
    f1(nGameObject.GetHitPoints(me)))
  local seen, n = {}, 0
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      local creep = string.find(name, '^Creep_Jungle_') ~= nil
      local worker = string.find(name, '^Titan_Node_Follower') ~= nil
      if (creep or worker) and nGameObject.IsAlive(id) and n < 24 then
        local pos = nGameObject.GetPosition(id)
        if dist(pos, here) <= 45 then
          n = n + 1
          local home, extra = nGameObject.GetInitialPosition(id), ''
          if worker then
            for _, w in ipairs(WELLS) do if dist(pos, w) <= 40 then home = w end end
            local okR, hasR, ret = pcall(nLocomotion.GetFollowerMiningReturnToWorkPoint, id)
            extra = ' return-to-work ' .. ((okR and hasR and ret) and xy(ret) or 'none')
          end
          p(string.format('leash report: %s %s at %s, %s from home %s, hp %s/%s, untargetable %s, in combat %s, best %s, '
            .. 'list %s%s', name, tostring(id), xy(pos), f1(dist(pos, home)), xy(home), f1(nGameObject.GetHitPoints(id)),
            f1(nGameObject.GetMaxHitPoints(id)), f1(nAttribute.GetAttributeValue(id, nAttributeType.Untargetable)),
            tostring(nGameObject.IsInCombat(id)), tostring(nAgent.GetBestTarget(id)), aggro(id), extra))
        end
      end
    end
  end
  if n == 0 then p('leash report: no living creep or worker within 45 of the human') end
end)
if not ok then p('leash report: ERROR', tostring(err)) end
return table.concat(out, '\n')
