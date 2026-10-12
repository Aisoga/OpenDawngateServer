-- D3c test probe ([host] test_lua=<s>:dgsim_d3c.lua,...): a perk
-- page's modifiers on player 0's creature around grass, a death and the respawn. Each run reports the creature
-- (alive, in grass, MovementSpeedPercentIncrease, which of the page's modifiers and their children it holds) and then
-- does the run's step, taken from the global dgsim_d3c_steps (default below) by the run's number:
--   'grass'  teleport into the grass patch beside the Mortal top base (-114, -98) (dgsim_fog_rest.lua's C1 spot),
--   'open'   teleport out of it to (-100, -104) (D11's arena spot, out of every tower's reach),
--   'report' nothing more.
-- Kill the shaper with dgsim_damage_kill_shaper.lua between runs; [host] respawn_s shortens the wait.
dgsim_d3c_run = (dgsim_d3c_run or 0) + 1
local steps = dgsim_d3c_steps or {'grass', 'open', 'report', 'grass', 'open'}
local step = steps[dgsim_d3c_run] or 'report'
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local names = {
  'modifier_MediumPerk_BushSpeed', 'modifier_PerkTriggered_BushSpeed', 'modifier_PerkTriggered_LeavingBushSpeed',
  'modifier_PerkHandler_ProtectDistantAllies',
}

local ok, err = pcall(function()
  local c = nPlayer.GetPlayerControlledObjectID(0)
  if c == nil or c == kObjIDNone then
    p('d3c run', dgsim_d3c_run, ': no creature for player 0')
    return
  end
  local pos = nGameObject.GetPosition(c)
  local okSpeed, speed = pcall(nAttribute.GetAttributeValue, c, nAttributeType.MovementSpeedPercentIncrease)
  local held = {}
  for _, n in ipairs(names) do
    local has = nModifier.AgentHasModifierMatchingGUID(c, nUtil.SPID(n))
    held[#held + 1] = n:gsub('^modifier_', '') .. '=' .. tostring(has)
  end
  p(string.format('d3c run %d (%s) at match %.2f: creature %s alive %s hp %s at (%.1f, %.1f) in grass %s; '
      .. 'MovementSpeedPercentIncrease %s; %s', dgsim_d3c_run, step, nGameSimulator.GetMatchTime(), tostring(c),
    tostring(nGameObject.IsAlive(c)), tostring(nGameObject.GetHitPoints(c)), pos.x, pos.y,
    tostring(nGameObject.IsInGrass(c)), okSpeed and tostring(speed) or ('ERR ' .. tostring(speed)),
    table.concat(held, ' ')))
  local to
  if step == 'grass' then to = dgsim_d3c_grass or {-114.0, -98.0}
  elseif step == 'open' then to = dgsim_d3c_open or {-100.0, -104.0} end
  if to then
    nLocomotion.TeleportToPositionAndFacing(c, nVector.create(to[1], to[2], pos.z), nVector.create(1, 0, 0))
    local q = nGameObject.GetPosition(c)
    p(string.format('d3c run %d: teleported to (%.1f, %.1f); in grass %s', dgsim_d3c_run, q.x, q.y,
      tostring(nGameObject.IsInGrass(c))))
  end
end)
if not ok then p('d3c run', dgsim_d3c_run, ': ERROR', err) end
return table.concat(out, '\n')
