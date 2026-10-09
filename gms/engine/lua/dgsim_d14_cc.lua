-- D14 live probe: crowd control, movement after a respawn, Fluidity's UnitPassThrough (D14b "Crowd control",
-- "after a death the shaper must move again at once", "Fluidity's UnitPassThrough"; MATCH_FLOW.md *D14*). [host] test_lua
-- with 31 calls at match seconds
--   20, 20.3, 21.2, 22, 23, 23.5, 24, 25, 27, 31, 34, 37, 40, 40.75, 41, 41.25, 41.5, 42, 42.5, 43, 43.5, 44, 44.75, 45,
--   45.25, 45.5, 46, 46.5, 47, 47.5, 48
-- and [host] respawn_s=6, director_waves=0, bot_brain=0, plus a commands_file (gitignored) with the human's test-side
-- commands ({caster} = the human's creature):
--   23.6 a601e10000{caster}000000003333e4c2cdcccc40000000000000000000000000   move to (-114.1, 6.4) during the stun
--   29 c301b15827b20b00000000ff                                               buy Basic_Life while dead
--   34.5 a601e20000{caster}00000000000048c300004dc3000000000000000000000000   move to (-200, -205) after the respawn
--   40.5 a601e30000{caster}000000003333e4c2cdcccc40000000000000000000000000   walk A (no UnitPassThrough)
--   44.5 a601e40000{caster}000000003333e4c2cdcccc40000000000000000000000000   walk B (with UnitPassThrough)
-- Parts (the human's creature H is the caster of every modifier; the spot (-119, -7.75) is on blue_top between knots 3
-- and 4, 37 units from both top Mortal Bindings):
--   1 (20): H to (-123.9, -21.9) (in view of the spot); a Spirit melee minion M1 there; modifier_Desecrator_Q_Knockup on it (nLocomotion.KnockUp 0.75 s, height 5.5,
--     holds the agent): the server's own jump (Locomotion_StartJump 0x00890C40) and ObjectJump (96) to the clients.
--   2 (20.3): expected M1 in the air (z above the ground) and the modifier on.
--   3 (21.2): expected the modifier gone, M1 on the ground and alive.
--   4 (22): expected M1 walking again (its behavior back after the hold); M2 spawned, modifier_Artificer_R_KnockAside
--     (Petrus R's knock-aside: JumpInDirection 4 units) with the request parameter directionToKnock (0.945, -0.327, 0).
--   5 (23): expected M2 about 4 units along that direction, the modifier gone; H to (-123.9, -21.9).
--   6 (23.5): modifier_Artificer_W_Stun (0.75 s, Immobilized) on H: expected Immobilized 1.
--   7 (24): expected H not moved by the 23.6 move (Immobilized; GameObject_IsMovementImpaired 0x008804B0 in
--     Locomotion_TickMovers) and the stun still on.
--   8 (25): expected the stun ended on time (Immobilized 0); H's position says whether the move given during the stun was
--     kept (walked after it) or dropped [unknown].
--   9 (27): H stunned again and killed (KillObject by an immobilized Spirit minion M3): a death while CC'd.
--   10 (31): dead: expected the stun ended (OnAgentDeath), MaxHealth 550 if the 29 s purchase while dead went through
--     (LabsPlayer_CanUseItemShop: dead or near the shop).
--   11 (34): respawned (respawn_s=6): expected alive at the start point, Immobilized/ImmobilizedNoRotate/Suppressed 0,
--     MaxHealth 550 (the item bought while dead), HP full.
--   12 (37): expected H at least 15 units from the start point toward (-200, -205) (the 34.5 s move): moving at once.
--   13 (40): Fluidity: five Mortal melee minions, immobilized, in a row across H's path (spacing 2.5, centre
--     (-119, -7.75)); H to (-123.9, -21.9).
--   14-21 (40.75 ... 43.5): samples of walk A (no UnitPassThrough): H's position, its lateral offset from the straight
--     line, the nearest minion's edge distance, the minions' displacement.
--   22 (44): the row reset, H back, modifier_Item_Legendary_UnitPassThrough (Fluidity's passive, attribute 45) on H.
--   23-30 (44.75 ... 47.5): samples of walk B.
--   31 (48): summary: expected walk B straighter (lateral offset smaller, nearest distance smaller) than walk A, since
--     UnitPassThrough clears H's repulsor blockage flags (Pathfinder_UpdateMoverRepulsorFlags 0x0089D1B0, every frame
--     through Locomotion_UpdateMoverTune 0x0089D890) while the minions still give way to H [code]; the modifier removed,
--     the helpers deleted. If the immobilized minions push nothing in either walk, the row isn't a valid obstacle: run
--     it again through a real wave (D1k's follow_top recipe) instead.
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end
local function v2s(v) if not v then return '(?)' end return string.format('(%.2f, %.2f, %.2f)', v.x, v.y, v.z) end

local S = dgsim_d14_cc or {}
dgsim_d14_cc = S

local SPOT = nVector.create(-119.0, -7.75, 0)
local START = nVector.create(-123.9, -21.9, 0)
local GOAL = nVector.create(-114.1, 6.4, 0)
local DIR = nVector.create(0.327, 0.945, 0)   -- START -> GOAL, normalized
local PERP = nVector.create(0.945, -0.327, 0)

local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    -- nPlayer.IsBot takes an object (0x00947660), not a player id
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c end
  end
end

local function attr(o, name) return nAttribute.GetAttributeValue(o, nAttributeType[name]) end
local function cc(o)
  return string.format('Immobilized %s, ImmobilizedNoRotate %s, Suppressed %s', f2(attr(o, 'Immobilized')),
    f2(attr(o, 'ImmobilizedNoRotate')), f2(attr(o, 'Suppressed')))
end
local function has(o, name)
  local id = nModifier.GetFirstModifierByGUID(o, nUtil.SPID(name))
  if id == nil or id == kObjIDNone then return nil end
  return id
end
local function request(target, name, params)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(S.h, kObjIDNone)
  local id = nModifier.RequestModifier(target, S.h, nUtil.SPID(name), snap, 1, kZeroVector, params)
  nAttribute.RemoveAttributeSnapshot(snap)
  return id
end
local function spawn(noun, team, v)
  return nObjectManager.CreateObject(nUtil.GetAsset(noun), team, v, kZeroVector, 1)
end
local function teleport(o, v)
  nLocomotion.TeleportToPositionAndOrientation(o, v, nGameObject.GetOrientation(o))
end
local function enemyTeam()
  if nGameObject.GetTeam(S.h) == nTeamID.Spirit then return nTeamID.Mortal, 'Minion_Mortal_Melee.Noun' end
  return nTeamID.Spirit, 'Minion_Spirit_Melee.Noun'
end
local function ownMinion()
  if nGameObject.GetTeam(S.h) == nTeamID.Spirit then return 'Minion_Spirit_Melee.Noun' end
  return 'Minion_Mortal_Melee.Noun'
end

local function rowPoint(k) return SPOT + PERP * (2.5 * k) end
local function resetRow()
  for k = -2, 2 do
    local m = S.row and S.row[k + 3]
    if m and nGameObject.IsAlive(m) then teleport(m, rowPoint(k)) end
  end
end
local function sample(tag)
  local h = nGameObject.GetPosition(S.h)
  local rel = h - START
  local lateral = math.abs(rel.x * DIR.y - rel.y * DIR.x)
  local along = rel.x * DIR.x + rel.y * DIR.y
  local nearest, moved = 1e9, 0
  for k = -2, 2 do
    local m = S.row[k + 3]
    if m and nGameObject.IsAlive(m) then
      local mp = nGameObject.GetPosition(m)
      local d = nVector.distance(mp, h) - nGameObject.GetFootprintRadius(m) - nGameObject.GetFootprintRadius(S.h)
      if d < nearest then nearest = d end
      local dm = nVector.distance(mp, rowPoint(k))
      if dm > moved then moved = dm end
    end
  end
  local w = S.walk[tag]
  if lateral > w.maxLateral then w.maxLateral = lateral end
  if nearest < w.minNearest then w.minNearest = nearest end
  if moved > w.maxMoved then w.maxMoved = moved end
  if along >= 27.0 and not w.arrived then w.arrived = nGameSimulator.GetMatchTime() end
  p(string.format('d14 cc walk %s: H %s along %.2f lateral %.2f, nearest minion edge %.2f, row displaced up to %.2f, '
    .. 'UnitPassThrough %s', tag, v2s(h), along, lateral, nearest, moved, f2(attr(S.h, 'UnitPassThrough'))))
end

local parts = {}
parts[1] = function()
  local team, noun = enemyTeam()
  teleport(S.h, START)  -- 14 units from the spot: the camera (on H) shows the knock-up and the knock-aside
  S.m1 = spawn(noun, team, SPOT)
  S.k1 = request(S.m1, 'modifier_Desecrator_Q_Knockup')
  S.ground = nGameObject.GetGroundPosition(S.m1)
  p('d14 cc part 1: M1', tostring(S.m1), v2s(nGameObject.GetPosition(S.m1)), 'knock-up modifier', tostring(S.k1))
end
parts[2] = function()
  local v, g = nGameObject.GetPosition(S.m1), nGameObject.GetGroundPosition(S.m1)
  p('d14 cc part 2: M1', v2s(v), 'ground', v2s(g), 'height', f2(v.z - g.z), 'modifier', tostring(has(S.m1, 'modifier_Desecrator_Q_Knockup')),
    '(expected height > 0.5 and the modifier on)')
end
parts[3] = function()
  local v, g = nGameObject.GetPosition(S.m1), nGameObject.GetGroundPosition(S.m1)
  S.m1At = v
  p('d14 cc part 3: M1', v2s(v), 'height', f2(v.z - g.z), 'modifier', tostring(has(S.m1, 'modifier_Desecrator_Q_Knockup')),
    'alive', tostring(nGameObject.IsAlive(S.m1)), '(expected height < 0.3, the modifier gone, alive)')
end
parts[4] = function()
  local v = nGameObject.GetPosition(S.m1)
  p('d14 cc part 4: M1 moved', f2(nVector.distance(v, S.m1At)), 'since part 3 (expected > 1: walking again)')
  local team, noun = enemyTeam()
  S.m2 = spawn(noun, team, SPOT + nVector.create(0, 6, 0))
  S.m2At = nGameObject.GetPosition(S.m2)
  local params = nTable.CreateTable()
  nTable.SetTableValue(params, 'directionToKnock', PERP)
  S.k2 = request(S.m2, 'modifier_Artificer_R_KnockAside', params)
  p('d14 cc part 4: M2', tostring(S.m2), v2s(S.m2At), 'knock-aside modifier', tostring(S.k2))
end
parts[5] = function()
  local v = nGameObject.GetPosition(S.m2)
  local d = v - S.m2At
  p('d14 cc part 5: M2 moved', f2(nVector.distance(v, S.m2At)), 'of which along the direction', f2(d.x * PERP.x + d.y * PERP.y),
    '(expected about 4); modifier', tostring(has(S.m2, 'modifier_Artificer_R_KnockAside')), '(expected gone)')
  for _, o in ipairs({S.m1, S.m2}) do if o and nGameObject.IsAlive(o) then nGameObject.MarkForDelete(o) end end
  teleport(S.h, START)
end
parts[6] = function()
  S.stun = request(S.h, 'modifier_Artificer_W_Stun')
  S.hAt = nGameObject.GetPosition(S.h)
  p('d14 cc part 6: H', v2s(S.hAt), 'stun', tostring(S.stun), cc(S.h), '(expected Immobilized 1)')
end
parts[7] = function()
  local v = nGameObject.GetPosition(S.h)
  p('d14 cc part 7: H moved', f2(nVector.distance(v, S.hAt)), '(expected < 0.5: immobilized), stun', tostring(has(S.h, 'modifier_Artificer_W_Stun')),
    cc(S.h))
end
parts[8] = function()
  local v = nGameObject.GetPosition(S.h)
  p('d14 cc part 8: stun', tostring(has(S.h, 'modifier_Artificer_W_Stun')), cc(S.h), '(expected gone, Immobilized 0); H moved',
    f2(nVector.distance(v, S.hAt)), 'since the stun (> 3: the move given during the stun was kept; < 0.5: dropped)')
end
parts[9] = function()
  local team, noun = enemyTeam()
  S.m3 = spawn(noun, team, nGameObject.GetPosition(S.h) + nVector.create(3, 0, 0))
  if S.m3 then nAttribute.AddAttributeModifier(S.m3, nAttributeType.Immobilized, 1, nAttributeOp.kAdd) end
  request(S.h, 'modifier_Artificer_W_Stun')
  nGameObject.KillObject(S.h, S.m3 or kObjIDNone, false)
  p('d14 cc part 9: H stunned and killed by', tostring(S.m3), '-> hp', f2(nGameObject.GetHitPoints(S.h)), cc(S.h),
    'MaxHealth', f2(nGameObject.GetMaxHitPoints(S.h)))
end
parts[10] = function()
  p('d14 cc part 10: dead: alive', tostring(nGameObject.IsAlive(S.h)), 'stun', tostring(has(S.h, 'modifier_Artificer_W_Stun')),
    '(expected gone: OnAgentDeath)', cc(S.h), 'MaxHealth', f2(nGameObject.GetMaxHitPoints(S.h)),
    '(expected 550 after the purchase while dead)')
end
parts[11] = function()
  S.startAt = nGameObject.GetPosition(S.h)
  p('d14 cc part 11: after the respawn: alive', tostring(nGameObject.IsAlive(S.h)), 'at', v2s(S.startAt), cc(S.h),
    '(expected all 0) hp', f2(nGameObject.GetHitPoints(S.h)), '/', f2(nGameObject.GetMaxHitPoints(S.h)), '(expected 550 / 550)')
  if S.m3 and nGameObject.IsAlive(S.m3) then nGameObject.MarkForDelete(S.m3) end
end
parts[12] = function()
  local v = nGameObject.GetPosition(S.h)
  p('d14 cc part 12: H moved', f2(nVector.distance(v, S.startAt)), 'from the start point toward (-200, -205): now', v2s(v),
    '(expected >= 15: it moves at once after the respawn)', cc(S.h))
end
parts[13] = function()
  S.row = {}
  for k = -2, 2 do
    local m = spawn(ownMinion(), nGameObject.GetTeam(S.h), rowPoint(k))
    if m then nAttribute.AddAttributeModifier(m, nAttributeType.Immobilized, 1, nAttributeOp.kAdd) end
    S.row[k + 3] = m
  end
  S.walk = {A = {maxLateral = 0, minNearest = 1e9, maxMoved = 0}, B = {maxLateral = 0, minNearest = 1e9, maxMoved = 0}}
  teleport(S.h, START)
  p('d14 cc part 13: row of five immobilized minions across the path at', v2s(SPOT), '; H to', v2s(START),
    'UnitPassThrough', f2(attr(S.h, 'UnitPassThrough')), '(expected 0)')
end
for i = 14, 21 do parts[i] = function() sample('A') end end
parts[22] = function()
  resetRow()
  teleport(S.h, START)
  S.fluidity = request(S.h, 'modifier_Item_Legendary_UnitPassThrough')
  p('d14 cc part 22: walk A done (max lateral', f2(S.walk.A.maxLateral), ', nearest', f2(S.walk.A.minNearest), ', arrived',
    tostring(S.walk.A.arrived), '); Fluidity passive', tostring(S.fluidity), 'UnitPassThrough', f2(attr(S.h, 'UnitPassThrough')),
    '(expected 1)')
end
for i = 23, 30 do parts[i] = function() sample('B') end end
parts[31] = function()
  local a, b = S.walk.A, S.walk.B
  p(string.format('d14 cc part 31: walk A max lateral %.2f, nearest %.2f, row displaced %.2f, arrived %s; walk B max lateral '
    .. '%.2f, nearest %.2f, row displaced %.2f, arrived %s (expected B straighter and closer than A)', a.maxLateral,
    a.minNearest, a.maxMoved, tostring(a.arrived), b.maxLateral, b.minNearest, b.maxMoved, tostring(b.arrived)))
  if S.fluidity then nModifier.MarkForDelete(S.h, S.fluidity) end
  for _, m in ipairs(S.row or {}) do if m and nGameObject.IsAlive(m) then nGameObject.MarkForDelete(m) end end
end

local ok, err = pcall(function()
  S.h = S.h or human()
  if not S.h then
    p('d14 cc: no human player creature')
    return
  end
  S.part = (S.part or 0) + 1
  local f = parts[S.part]
  p('d14 cc: part', S.part, 'at match time', f2(nGameSimulator.GetMatchTime()))
  if f then f() else p('d14 cc: no part', S.part) end
end)
if not ok then p('d14 cc: ERROR', err) end
return table.concat(out, '\n')
