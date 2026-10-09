-- D6a test probe ([host] test_lua=<s>:dgsim_bots_kills.lua, listed five times): D1h's two-player checks with bot
-- players (DAMAGE.md *D1h*, *What G3 has to check*). Each run does the next step (the global dgsim_bots_kills_step);
-- every hit is nGameObject.TakeDamage with a snapshot of the attacker's creature (its player index credits the hit, as
-- dgsim_damage_lasthit.lua does), true damage, so the host's damage core, death processing and kill bookkeeping run:
--   1. ally bot 1 hits enemy bot 5 for 30 (an assist), then the human (player 0) kills bot 5: first blood, a kill for
--      the human, an assist for bot 1, a death for bot 5;
--   2. enemy bot 6 kills the human: the human's death recap names bot 6 (a player source);
--   3. bot 6 kills ally bot 1 (a double kill within 10 s of step 2 when run 5 s later);
--   4. bot 6 kills ally bot 2 (a triple kill, kill streak 3);
--   5. the human kills bot 6: the end of a streak of 3 (TargetKillStreakEnd), the streak bounty.
-- The global dgsim_bots_kills_victim_hp (5000) is the killing hit. Returns a report: each player's gold before/after.
dgsim_bots_kills_step = (dgsim_bots_kills_step or 0) + 1
local step = dgsim_bots_kills_step
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end
local function creature(index)
  local c = nPlayer.GetPlayerControlledObjectID(index)
  if c == nil or c == kObjIDNone then return nil end
  return c
end
local function gold(index) return f2(nPlayer.GetPlayerAvailableGold(index)) end
local function hit(attackerIndex, victimIndex, amount)
  local a, v = creature(attackerIndex), creature(victimIndex)
  if a == nil or v == nil then
    p(string.format('  hit %d -> %d: no creature (%s, %s)', attackerIndex, victimIndex, tostring(a), tostring(v)))
    return
  end
  local hp = nGameObject.GetHitPoints(v)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(a, kObjIDNone)
  local s, d = nGameObject.TakeDamage(snap, v, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  p(string.format('  hit player %d (%s) -> player %d (%s): %s, dealt %s, hp %s -> %s, alive %s', attackerIndex,
    tostring(nGameObject.GetAssetName(a)), victimIndex, tostring(nGameObject.GetAssetName(v)), tostring(s), f2(d),
    f2(hp), f2(nGameObject.GetHitPoints(v)), tostring(nGameObject.IsAlive(v))))
end
local function golds()
  local t = {}
  for i = 0, 9 do t[#t + 1] = i .. ':' .. gold(i) end
  return table.concat(t, ' ')
end
local kill = dgsim_bots_kills_victim_hp or 5000

local ok, err = pcall(function()
  p('bots kills step ' .. step .. ' at match ' .. f2(nGameSimulator.GetMatchTime()) .. ' s; gold before: ' .. golds())
  if step == 1 then
    hit(1, 5, 30)
    hit(0, 5, kill)
  elseif step == 2 then
    hit(6, 0, kill)
  elseif step == 3 then
    hit(6, 1, kill)
  elseif step == 4 then
    hit(6, 2, kill)
  elseif step == 5 then
    hit(0, 6, kill)
  else
    p('  no step ' .. step)
  end
  p('gold after: ' .. golds())
end)
if not ok then p('bots kills: ERROR', err) end
return table.concat(out, '\n')
