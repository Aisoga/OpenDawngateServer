-- D15b test fixture ([host] test_lua=<s>:dgsim_d15b_gather.lua): the aura items with an owner
-- that walked away from where it bought them. Brings every allied player's creature (the practice game's bots, standing
-- still with bot_brain=0) to a ring of dgsim_d15b_ring (6) units around player 0's shaper wherever it is now, and,
-- unless dgsim_d15b_enemy == false, the first live enemy player's creature dgsim_d15b_enemy_at (10) units east of it
-- (kept in dgsim_d15_bot for dgsim_d15b_state.lua; Glory's and Oppression's enemy auras). The commands file's
-- `report ... near 30` lines then show which aura buffs the allies and the enemy carry at the new place.
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end

local ok, err = pcall(function()
  local me = nPlayer.GetPlayerControlledObjectID(0)
  if me == nil or me == kObjIDNone then
    p('d15b gather: no shaper for player 0')
    return
  end
  local team = nGameObject.GetTeam(me)
  local mp = nGameObject.GetPosition(me)
  local ring = dgsim_d15b_ring or 6.0
  local allies = {}
  local enemy
  for i = 1, 9 do
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if id ~= nil and id ~= kObjIDNone and nGameObject.IsAlive(id) then
      if nGameObject.GetTeam(id) == team then
        allies[#allies + 1] = id
      elseif enemy == nil then
        enemy = id
      end
    end
  end
  for k, id in ipairs(allies) do
    local a = (k - 1) * 2 * math.pi / math.max(#allies, 1) + math.pi / 2
    local to = nVector.create(mp.x + ring * math.cos(a), mp.y + ring * math.sin(a), 0)
    nLocomotion.TeleportToPositionAndFacing(id, to, nVector.create(0, -1, 0))
    local at = nGameObject.GetPosition(id)
    p('d15b gather: ally', tostring(id), nGameObject.GetAssetName(id), 'to', f1(at.x), f1(at.y), 'distance',
      f1(nVector.distance(at, mp)))
  end
  if dgsim_d15b_enemy ~= false then
    if enemy ~= nil then
      local d = dgsim_d15b_enemy_at or 10.0
      nLocomotion.TeleportToPositionAndFacing(enemy, nVector.create(mp.x + d, mp.y, 0), nVector.create(-1, 0, 0))
      dgsim_d15_bot = enemy
      local at = nGameObject.GetPosition(enemy)
      p('d15b gather: enemy', tostring(enemy), nGameObject.GetAssetName(enemy), 'to', f1(at.x), f1(at.y), 'distance',
        f1(nVector.distance(at, mp)))
    else
      p('d15b gather: no live enemy player')
    end
  end
  p('d15b gather:', #allies, 'allies around player 0 at', f1(mp.x), f1(mp.y), 'match time',
    string.format('%.2f', nGameSimulator.GetMatchTime()))
end)
if not ok then p('d15b gather: ERROR', err) end
return table.concat(out, '\n')
