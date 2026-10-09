-- D14 live probe: structures (D14b "Structures", MATCH_FLOW.md *D14*). [host] test_lua with the eleven calls
--   T, T+1, T+1.8, T+4.5, T+6.5, T+8, T+10, T+12, T+14, T+16, T+21 (each <s>:dgsim_d14_structures.lua; T = 25)
-- (d14b_struct2 ran T+5.5 for part 4 and killed the bot there; the Binding had killed it already at T+5.3, and the two
-- minions had walked off towards the Mortal base: since d14b_struct3 the minions are immobilized, part 4 runs inside the
-- bot's life and moves it out of range, and part 5 kills the Binding)
-- in a practice game with bot_brain=0 (the bots stand still, so the teleported Spirit bot stays put), director_waves=0
-- (empty lanes), the human on team 1 (Mortal, the default). Each call runs the next part:
--   part 1 (T): the Binding's switch to an enemy shaper that hurts an allied shaper near it (locale 0x11c4cfe9: "They will
--     also defend their Shapers and will target you if you deal damage to a Shaper within their range"). The human (the
--     victim) to (-100, 35) and a Spirit bot (the attacker) to (-94, 41), both inside the top Binding_Mortal_Outer's 28
--     (-116.3, 28.9); two Spirit melee minions beside the Binding at (-106.4, 28.7) and (-104, 31).
--   part 2 (T+1): the Binding's best target before the hit: expected a Spirit minion (towers: non-players first,
--     gms/core/agents/aggro.h); then 20 basic physical damage from the bot's snapshot onto the human (the host's damage
--     hook calls Agent_CallForHelp 0x008AB860 with the human as the victim: the Binding is in the human's perception list).
--   part 3 (T+1.8): expected the bot as best target (rule 2: reported by a player-controlled ally within 3 s) and as the
--     Binding's target (nGameObject.GetTargetID, set by modifier_Binding_Passive's HandleBindingAttack).
--   part 4 (T+4.5): the 3-s window is over: expected the bot still (sticky) with the minions still on the list; then the
--     bot is teleported to (-80, 55), 44 units from the Binding (out of its range: the beam must leave it).
--   part 5 (T+6.5): expected the bot gone from the list and the target back on a minion (the beam follows:
--     OnBestTargetChanged destroys and recreates it; screenshot); then the Binding is killed (KillObject by the bot):
--     its beam must go (HandleBindingDeath; screenshot).
--   part 6 (T+8): the Guardian shield (locale 0x11a95cbb: "The Guardian ... is invincible as long as any Cores remain
--     intact"; modifier_Tower_Immunity, which no script applies; the host applies it with [host] guardian_shield=1):
--     expected the Spirit Guardian with modifier_Tower_Immunity, Immune 1, Unattackable 1; then four Spirit cores (nubs
--     A-D) are killed (KillObject by a Mortal minion spawned for it).
--   part 7 (T+10): one core left (E): expected the shield still on; then core E is killed.
--   part 8 (T+12): no core left: expected the shield gone (no modifier_Tower_Immunity, Immune 0, Unattackable 0; the host
--     checks every tick, match.cpp UpdateGuardianShields); then core E is resurrected (nGameObject.ResurrectObject, as ability_BaseNub_Death does
--     after 480 s).
--   part 9 (T+14): a core is back: expected the shield on again [assumed: "as long as any Cores remain intact"].
--   part 10 (T+16): the base's own defense (the Locus, DEST_prefab_red_tower_v5 at (242, 242): aggro 38.5,
--     ability_Tower_MegaZap 2000 + stacks to shapers, 40 % of max HP to a melee minion and 60 % to a ranged one, one
--     zap a second; locale "The Locus annihilates any shapers not attuned to it"): a Mortal melee
--     minion spawned at (232, 228), 17 units from it.
--   part 11 (T+21 since d14b_struct1, which ran it at T+18.5, 0.2 s before the third zap): expected the minion dead
--     (killed by DEST_prefab_red_tower_v5: the host's death: line) and the Locus's best target gone again; the leftover
--     helpers are marked for delete.
-- Returns a report (the host logs it after "host: test_lua dgsim_d14_structures.lua at ...").
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end
local function pos(o)
  local v = o and nGameObject.GetPosition(o)
  if not v then return '(?)' end
  return string.format('(%.1f, %.1f, %.1f)', v.x, v.y, v.z)
end

local S = dgsim_d14_structures or {}
dgsim_d14_structures = S

local function liveObjects()
  local live, seen = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if not seen[tostring(id)] then
      seen[tostring(id)] = true
      live[#live + 1] = id
    end
  end
  return live
end

local function findByName(name, test)
  for _, id in ipairs(liveObjects()) do
    if nGameObject.GetAssetName(id) == name and (not test or test(nGameObject.GetPosition(id))) then return id end
  end
end

local function humanAndBot(botTeam)
  local human, bot
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone then
      if not nPlayer.IsBot(c) then human = human or c  -- nPlayer.IsBot takes an object (0x00947660), not a player id
      elseif nGameObject.GetTeam(c) == botTeam and nGameObject.IsAlive(c) then bot = bot or c end
    end
  end
  return human, bot
end

local function teleport(o, x, y)
  nLocomotion.TeleportToPositionAndOrientation(o, nVector.create(x, y, 0), nGameObject.GetOrientation(o))
end

local function spawn(noun, team, x, y)
  return nObjectManager.CreateObject(nUtil.GetAsset(noun), team, nVector.create(x, y, 0), kZeroVector, 1)
end

local function list(o)
  local okList, ids = pcall(nAgent.GetTargetsOnAggroList, o)
  if not okList or type(ids) ~= 'table' then return '[?]' end
  local t = {}
  for _, id in ipairs(ids) do t[#t + 1] = string.format('%s %s', tostring(id), nGameObject.GetAssetName(id) or '?') end
  return '[' .. table.concat(t, ', ') .. ']'
end

local function shield(g)
  local mod = nModifier.GetFirstModifierByGUID(g, nUtil.SPID('modifier_Tower_Immunity'))
  local on = mod ~= nil and mod ~= kObjIDNone
  return on, string.format('modifier_Tower_Immunity %s, Immune %s, Unattackable %s', on and tostring(mod) or 'none',
    f2(nAttribute.GetAttributeValue(g, nAttributeType.Immune)), f2(nAttribute.GetAttributeValue(g, nAttributeType.Unattackable)))
end

local parts = {}

parts[1] = function()
  local human, bot = humanAndBot(nTeamID.Spirit)
  S.human, S.bot = human, bot
  S.binding = findByName('Binding_Mortal_Outer', function(v) return v and v.y > v.x end)
  if not human or not bot or not S.binding then
    p('d14 structures part 1: missing human', tostring(human), 'Spirit bot', tostring(bot), 'or top Binding_Mortal_Outer',
      tostring(S.binding))
    return
  end
  teleport(human, -100, 35)
  teleport(bot, -94, 41)
  S.m1 = spawn('Minion_Spirit_Melee.Noun', nTeamID.Spirit, -106.4, 28.7)
  S.m2 = spawn('Minion_Spirit_Melee.Noun', nTeamID.Spirit, -104, 31)
  for _, m in ipairs({S.m1, S.m2}) do  -- they stay in the Binding's range instead of walking to the Mortal base
    if m then nAttribute.AddAttributeModifier(m, nAttributeType.Immobilized, 1, nAttributeOp.kAdd) end
  end
  p('d14 structures part 1: human', tostring(human), 'to', pos(human), '; Spirit bot', tostring(bot), 'to', pos(bot),
    '; Binding', tostring(S.binding), pos(S.binding), '; Spirit minions', tostring(S.m1), tostring(S.m2))
end

parts[2] = function()
  local b = S.binding
  p('d14 structures part 2: Binding best target', tostring(nAgent.GetBestTarget(b)), 'list', list(b), 'target id',
    tostring(nGameObject.GetTargetID(b)), '(expected a Spirit minion first: non-players before players)')
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(S.bot, kObjIDNone)
  local s, dealt = nGameObject.TakeDamage(snap, S.human, 20, 0, nBit.Or(nDescriptors.IsBasic, nDescriptors.IsPhysicalDamage),
    kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  p('d14 structures part 2: 20 basic physical from the bot onto the human -> dealt', f2(dealt), 'human hp',
    f2(nGameObject.GetHitPoints(S.human)), '(expected dealt > 0: only a hit that takes HP calls for help, pipeline.h DrawsAggro)')
end

parts[3] = function()
  local b = S.binding
  local best = nAgent.GetBestTarget(b)
  p('d14 structures part 3: Binding best target', tostring(best), 'list', list(b), 'target id',
    tostring(nGameObject.GetTargetID(b)), '; expected the bot', tostring(S.bot), '->', tostring(best == S.bot),
    '; bot hp', f2(nGameObject.GetHitPoints(S.bot)))
end

parts[4] = function()
  local b = S.binding
  local best = nAgent.GetBestTarget(b)
  p('d14 structures part 4: 3.5 s after the hit, best target', tostring(best), '(expected the bot still: sticky) ->',
    tostring(best == S.bot), 'list', list(b), '; bot hp', f2(nGameObject.GetHitPoints(S.bot)))
  teleport(S.bot, -80, 55)
  p('d14 structures part 4: the bot moved out of range to', pos(S.bot))
end

parts[5] = function()
  local b = S.binding
  local best = nAgent.GetBestTarget(b)
  p('d14 structures part 5: after the bot left: best target', tostring(best), 'target id', tostring(nGameObject.GetTargetID(b)),
    'list', list(b), '(expected a Spirit minion; the bot not on the list)')
  nGameObject.KillObject(b, S.bot, false)
  p('d14 structures part 5: the Binding killed -> hp', f2(nGameObject.GetHitPoints(b)), '(its beam must go)')
end

parts[6] = function()
  local g = nBattlegrounds.GetGuardianOnTeam(nTeamID.Spirit)
  S.guardian = g
  local on, line = shield(g)
  p('d14 structures part 6: Spirit Guardian', tostring(g), line, '-> shielded', tostring(on), '(expected true)')
  S.killer = spawn('Minion_Mortal_Melee.Noun', nTeamID.Mortal, 120, 120)
  if S.killer then nAttribute.AddAttributeModifier(S.killer, nAttributeType.Immobilized, 1, nAttributeOp.kAdd) end
  S.cores = {}
  for _, l in ipairs({'A', 'B', 'C', 'D', 'E'}) do S.cores[l] = findByName('nub_spirit_v5_' .. l) end
  for _, l in ipairs({'A', 'B', 'C', 'D'}) do
    if S.cores[l] then nGameObject.KillObject(S.cores[l], S.killer, false) end
    p('d14 structures part 6: core', l, tostring(S.cores[l]), 'killed -> hp', f2(S.cores[l] and nGameObject.GetHitPoints(S.cores[l])))
  end
end

parts[7] = function()
  local on, line = shield(S.guardian)
  p('d14 structures part 7: one core left: ', line, '-> shielded', tostring(on), '(expected true)')
  local killer = (S.killer and nGameObject.IsAlive(S.killer)) and S.killer or kObjIDNone
  if S.cores.E then nGameObject.KillObject(S.cores.E, killer, false) end
  p('d14 structures part 7: core E killed -> hp', f2(S.cores.E and nGameObject.GetHitPoints(S.cores.E)))
end

parts[8] = function()
  local on, line = shield(S.guardian)
  p('d14 structures part 8: no core left:', line, '-> shielded', tostring(on), '(expected false)')
  if S.cores.E then nGameObject.ResurrectObject(S.cores.E) end
  p('d14 structures part 8: core E resurrected -> hp', f2(S.cores.E and nGameObject.GetHitPoints(S.cores.E)))
end

parts[9] = function()
  local on, line = shield(S.guardian)
  p('d14 structures part 9: core E back:', line, '-> shielded', tostring(on), '(expected true [assumed])')
end

parts[10] = function()
  S.locus = findByName('DEST_prefab_red_tower_v5')
  S.intruder = spawn('Minion_Mortal_Melee.Noun', nTeamID.Mortal, 232, 228)
  p('d14 structures part 10: Locus', tostring(S.locus), pos(S.locus), 'hp', f2(S.locus and nGameObject.GetHitPoints(S.locus)),
    '; intruder', tostring(S.intruder), pos(S.intruder), 'hp', f2(nGameObject.GetHitPoints(S.intruder)))
end

parts[11] = function()
  local alive = S.intruder and nGameObject.IsAlive(S.intruder)
  p('d14 structures part 11: intruder alive', tostring(alive), 'hp', f2(S.intruder and nGameObject.GetHitPoints(S.intruder)),
    '(expected false: ability_Tower_MegaZap deals 40 % of a melee minion\'s max HP a zap (60 % ranged; 2000 + stacks only to shapers), so 3 zaps, 1 s apart); Locus best target',
    tostring(S.locus and nAgent.GetBestTarget(S.locus)), 'list', S.locus and list(S.locus) or '-')
  for _, o in ipairs({S.m1, S.m2, S.killer, S.intruder}) do
    if o and nGameObject.IsAlive(o) then nGameObject.MarkForDelete(o) end
  end
end

local ok, err = pcall(function()
  S.part = (S.part or 0) + 1
  local f = parts[S.part]
  p('d14 structures: part', S.part, 'at match time', f2(nGameSimulator.GetMatchTime()))
  if f then f() else p('d14 structures: no part', S.part) end
end)
if not ok then p('d14 structures: ERROR', err) end
return table.concat(out, '\n')
