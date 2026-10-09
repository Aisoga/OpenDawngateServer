-- D14 live probe, fog details (docs/GMS_PROTOCOL.md *Fog details and presentation (D14)*; [host] test_lua with this
-- file at eight times, e.g. 115, 119, 123, 127, 131, 134, 140, 146 s after EnterGame; run with bots=0;director_waves=0
-- so no allied source but the human's shaper and the Mortal Bindings sees the test spots). Each call runs the next part
-- (global dgsim_d14_fog_part):
--   part 1: the jungle creep nearest P (default (67.6, -3.2), the small Fungus camp east of the river; the global
--     dgsim_d14_fog_p = {x, y} overrides it; no Mortal source reaches it) loses its regen modifier and takes 40 % of
--     its maximum HP as a self-inflicted true hit (no aggro) while fogged for Mortal (expected visible false); then the
--     human's shaper is teleported onto P: the next tick's fog step should exit the creep for player 0 (host: "fog:
--     <creep> ... exits fog for player 0 ... 94, N full-state message(s)"), the client showing 60 %;
--   part 2: the creep's HP (expected 60 %) and visibility (expected true); the shaper back to its start point;
--   part 3: another 20 % while fogged (expected 40 %, visible false; the host builds the A0 delta and holds it back:
--     the per-second fog line's "deltas held back" rises);
--   part 4: the shaper onto P again: the exit's full state must carry the 40 % (game_peek.py: the creep's hp);
--   part 5: the shaper home; at Q (default (66.0, -69.4), the bottom lane's meeting point; dgsim_d14_fog_q) a new
--     Fish minor (team NPC, no camp, so no camp pull) and 9 units up the lane (direction dgsim_d14_fog_dir, default
--     (0.7071, 0.7071)) a Spirit ranged minion; both immobilized; one 1-point true hit each way with the other's
--     snapshot (the damage hook puts each on the other's aggro list), so the minion shoots the creep with homing
--     projectiles that start in Mortal's fog;
--   part 6: the shaper 29.5 units down the lane from Q (the creep inside its 35.3 vision, the minion outside at 38.5;
--     the bottom Binding_Mortal_Outer sees neither): every projectile is created fogged and must exit fog mid-flight
--     (host: "fog: <id> Ability_... exits fog for player 0"); the live projectiles near Q are listed with their
--     Mortal visibility;
--   part 7: the list again, then the cleanup (both test creatures deleted, the shaper home);
--   part 8: the healing number of a dead healer: the shaper hurt by 100 (an enemy snapshot), an allied minion's
--     snapshot taken, the minion killed, then a 50-point heal with that snapshot (nGameObject.HealDamage): expected
--     +50 HP and the client's heal number over the shaper (CombatEvent flag 2, source the dead minion).
-- Returns a report; every line starts with "d14 fog".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'd14 fog ' .. table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f, %.1f)', v.x, v.y) end

local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    -- nPlayer.IsBot takes an object (0x00947660), not a player id
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return pid, c end
  end
end

local function each(fn)
  local seen = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      fn(id)
    end
  end
end

local function dist(a, b) return math.sqrt((a.x - b.x) ^ 2 + (a.y - b.y) ^ 2) end

local function hit(from, to, amount)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(from, kObjIDNone)
  local ok2, dealt = nGameObject.TakeDamage(snap, to, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  return ok2, dealt
end

local function teleport(shaper, pos)
  nLocomotion.TeleportToPositionAndFacing(shaper, pos, nVector.create(1, 0, 0))
end

local function home(pid, shaper)
  local pos, orient = nGameSimulator.GetPlayerStartingPoint(nPlayer.GetPlayerIdForObject(shaper) or pid)
  nLocomotion.TeleportToPositionAndOrientation(shaper, pos, orient)
end

local function state(id, what)
  if id == nil or not nGameObject.IsAlive(id) then
    p(what, tostring(id), 'not alive')
    return
  end
  p(what, tostring(id), nGameObject.GetAssetName(id), 'at', vec(nGameObject.GetPosition(id)), 'hp',
    f1(nGameObject.GetHitPoints(id)) .. '/' .. f1(nGameObject.GetMaxHitPoints(id)), 'visible to Mortal',
    tostring(nGameObject.IsVisibleToTeam(id, nTeamID.Mortal)))
end

local function projectilesNear(Q, r)
  local n = 0
  each(function(id)
    local name = nGameObject.GetAssetName(id) or ''
    if string.find(name, '^Ability_') then
      local pos = nGameObject.GetPosition(id)
      if dist(pos, Q) <= r then
        n = n + 1
        p('  projectile', tostring(id), name, 'at', vec(pos), 'visible to Mortal',
          tostring(nGameObject.IsVisibleToTeam(id, nTeamID.Mortal)))
      end
    end
  end)
  p('  projectiles within', r, 'of Q:', n)
end

local ok, err = pcall(function()
  local pid, shaper = human()
  if not shaper then
    p('no human player creature')
    return
  end
  local pp = dgsim_d14_fog_p or {67.6, -3.2}
  local qq = dgsim_d14_fog_q or {66.0, -69.4}
  local dd = dgsim_d14_fog_dir or {0.7071, 0.7071}
  local P = nVector.create(pp[1], pp[2], 0)
  local Q = nVector.create(qq[1], qq[2], 0)
  local D = nVector.create(dd[1], dd[2], 0)
  dgsim_d14_fog_part = (dgsim_d14_fog_part or 0) + 1
  local part = dgsim_d14_fog_part
  p('part', part, 'match time', f1(nGameSimulator.GetMatchTime()), 'shaper', tostring(shaper), 'at',
    vec(nGameObject.GetPosition(shaper)))
  if part == 1 then
    local best, bestD = nil, 15
    each(function(id)
      local name = nGameObject.GetAssetName(id) or ''
      if string.find(name, '^Creep_Jungle_') and nGameObject.IsAlive(id) then
        local d = dist(nGameObject.GetPosition(id), P)
        if d < bestD then best, bestD = id, d end
      end
    end)
    if not best then
      p('no living jungle creep within 15 of P', vec(P), '(the camp not spawned yet?)')
      return
    end
    dgsim_d14_fog_creep = best
    local removed = 0
    for _, n in ipairs({'modifier_Jungle_Creep_Regen', 'modifier_Jungle_Buff_Regen_Fish', 'modifier_Jungle_Buff_Regen_Fungus',
                        'modifier_Jungle_Buff_Regen_Ugger', 'modifier_Jungle_Buff_Regen_Boar', 'modifier_Jungle_Buff_Regen'}) do
      local g = nUtil.SPID(n)
      if nModifier.AgentHasModifierMatchingGUID(best, g) then
        nModifier.MarkForDelete(best, nModifier.GetFirstModifierByGUID(best, g))
        removed = removed + 1
      end
    end
    p('creep', tostring(best), nGameObject.GetAssetName(best), f1(bestD), 'from P: regen modifiers removed', removed)
    state(best, 'before:')
    local a, d = hit(best, best, nGameObject.GetMaxHitPoints(best) * 0.4)
    p('self-inflicted true hit of 40 % ->', tostring(a), f1(d), '(no aggro: same team)')
    state(best, 'after (expected 60 %, visible false):')
    teleport(shaper, P)
    p('shaper teleported onto P: expected the creep exits fog for player 0 next tick, client hp 60 %')
  elseif part == 2 then
    state(dgsim_d14_fog_creep, 'creep (expected 60 %, visible true):')
    home(pid, shaper)
    p('shaper home: expected the creep enters fog for player 0 (93)')
  elseif part == 3 then
    local c = dgsim_d14_fog_creep
    if c and nGameObject.IsAlive(c) then
      local a, d = hit(c, c, nGameObject.GetMaxHitPoints(c) * 0.2)
      p('self-inflicted true hit of 20 % while fogged ->', tostring(a), f1(d))
    end
    state(c, 'creep (expected 40 %, visible false; its A0 delta held back):')
  elseif part == 4 then
    state(dgsim_d14_fog_creep, 'creep before the exit (expected 40 %):')
    teleport(shaper, P)
    p('shaper teleported onto P: expected 94 + full state (A0 with 40 %) + 95; game_peek hp 40 %')
  elseif part == 5 then
    state(dgsim_d14_fog_creep, 'creep after the exit (expected 40 %, visible true):')
    home(pid, shaper)
    local c = nObjectManager.CreateObject(nUtil.GetAsset('Creep_Jungle_Fish_Minor.Noun'), nTeamID.NPC, Q, kZeroVector, 1)
    local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Spirit_Ranged.Noun'), nTeamID.Spirit,
      nVector.create(Q.x + 9 * D.x, Q.y + 9 * D.y, 0), kZeroVector, 1)
    if not c or c == kObjIDNone or not m or m == kObjIDNone then
      p('test creatures not created:', tostring(c), tostring(m))
      return
    end
    dgsim_d14_fog_c2, dgsim_d14_fog_minion = c, m
    nAttribute.AddAttributeModifier(c, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
    nAttribute.AddAttributeModifier(m, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
    local a1, d1 = hit(m, c, 1)
    local a2, d2 = hit(c, m, 1)
    state(c, 'test creep at Q:')
    state(m, 'Spirit ranged minion:')
    p('minion -> creep', tostring(a1), f1(d1), '; creep -> minion', tostring(a2), f1(d2), '; minion best target',
      tostring(nAgent.GetBestTarget(m)), '(expected the creep)')
  elseif part == 6 then
    teleport(shaper, nVector.create(Q.x - 29.5 * D.x, Q.y - 29.5 * D.y, 0))
    state(dgsim_d14_fog_c2, 'test creep (29.5 from the shaper: visible once the next fog step ran):')
    state(dgsim_d14_fog_minion, 'minion (38.5 from the shaper: expected visible false):')
    projectilesNear(Q, 20)
    p('expected in the host log from now: "fog: <id> Ability_... exits fog for player 0" for projectiles in flight')
  elseif part == 8 then
    -- the healing number of a healer that died before its heal landed: an allied minion's snapshot taken, the minion
    -- killed, then a 50-point heal with that snapshot on the human's shaper (hurt first by an enemy snapshot): the heal
    -- core's CombatEvent (flag 2, source = the dead minion, kind 2 on the shaper: its seers) must still show "+50"
    local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Spirit_Melee.Noun'), nTeamID.Spirit, nVector.create(150, 150, 0),
      kZeroVector, 1)
    if m and m ~= kObjIDNone then
      local a, d = hit(m, shaper, 100)
      p('shaper hurt by an enemy snapshot ->', tostring(a), f1(d))
      nGameObject.MarkForDelete(m)
    end
    local sp = nGameObject.GetPosition(shaper)
    local healer = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Mortal_Melee.Noun'), nTeamID.Mortal,
      nVector.create(sp.x + 5, sp.y, 0), kZeroVector, 1)
    if not healer or healer == kObjIDNone then
      p('healer not created')
      return
    end
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(healer, kObjIDNone)
    nGameObject.KillObject(healer, kObjIDNone, false)
    local before = nGameObject.GetHitPoints(shaper)
    local r1, r2 = nGameObject.HealDamage(snap, shaper, 50, 0, 0, false, false, false, false)
    nAttribute.RemoveAttributeSnapshot(snap)
    p('heal of 50 from the dead healer', tostring(healer), '(alive', tostring(nGameObject.IsAlive(healer)) .. ') ->',
      tostring(r1), tostring(r2), '; shaper hp', f1(before), '->', f1(nGameObject.GetHitPoints(shaper)),
      '(expected +50 and the client\'s green "+50" over the shaper)')
  else
    state(dgsim_d14_fog_c2, 'test creep:')
    state(dgsim_d14_fog_minion, 'minion (expected visible false):')
    projectilesNear(Q, 20)
    for _, id in ipairs({dgsim_d14_fog_minion, dgsim_d14_fog_c2}) do
      if id and id ~= kObjIDNone and nGameObject.IsAlive(id) then nGameObject.MarkForDelete(id) end
    end
    home(pid, shaper)
    p('cleanup: the test creep and minion deleted, the shaper home')
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
