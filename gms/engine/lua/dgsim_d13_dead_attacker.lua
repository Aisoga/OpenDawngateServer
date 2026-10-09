-- D13 live probe (docs/DAMAGE.md *Combat edge cases (D13)*, "a Tactician's attack landing just after the attacker
-- died"; [host] test_lua=<T>:dgsim_d13_dead_attacker.lua,<T+1>:dgsim_d13_dead_attacker.lua; a practice game with bots,
-- so an enemy player's creature exists). Each call runs the next part:
--   part 1: the human's shaper gets LifeDrain + 0.5 and modifier_Role_Tactician (also active by itself with [host]
--     role_passives=1 and the Tactician role), its snapshot is taken (as a projectile's own copy would be), then it is
--     killed (KillObject by a Binding) and, in the same Lua call, the snapshot hits an enemy bot's shaper with a 50
--     basic attack. Expected: the bot takes it (mitigated by its armor), the dead shaper stays dead (HP 0, IsAlive false;
--     before D13 the lifedrain healed it through the heal core, which resurrects an object at HP 0), the host's damage line
--     ends "(attacker dead: no lifedrain, no aggro)", the death recap/kill credit still name the dead shaper, and the
--     Tactician's vim (+20 gold, nGoldSourceType.Role) still comes: its modifier ends only 3 scheduler runs after the
--     death [assumed rule for the original];
--   part 2 (1 s later): the same snapshot hits again: the shaper still dead, the role modifier gone (ended at the death:
--     OnAgentDeath), no second vim. The snapshot is removed. The shaper respawns as usual (D1g).
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local function find(name)
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if nGameObject.GetAssetName(id) == name then return id end
  end
end

local ok, err = pcall(function()
  local human, pid0
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)  -- nPlayer.IsBot takes the creature, not the player id (D13-live)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then
      human, pid0 = c, pid
      break
    end
  end
  if not human then
    p('d13 dead attacker: no human player creature')
    return
  end
  local team = nGameObject.GetTeam(human)
  local bot
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and nGameObject.GetTeam(c) ~= team and nGameObject.IsAlive(c) then
      bot = c
      break
    end
  end
  if not bot then
    p('d13 dead attacker: no living enemy player creature (bots off?)')
    return
  end
  dgsim_d13_dead_part = (dgsim_d13_dead_part or 0) + 1
  local part = dgsim_d13_dead_part
  local tactician = nUtil.SPID('modifier_Role_Tactician')
  local basic = nBit.Or(nBit.Or(nDescriptors.IsBasic, nDescriptors.IsPhysicalDamage), nDescriptors.IsProjectile)
  if part == 1 then
    dgsim_d13_dead_ld = nAttribute.AddAttributeModifier(human, nAttributeType.LifeDrain, 0.5, nAttributeOp.kAdd)
    local had = nModifier.GetFirstModifierByGUID(human, tactician)
    if had == nil or had == kObjIDNone then
      local self = nAttribute.CreateAttributeSnapshot_FromObject(human, kObjIDNone)
      nModifier.RequestModifier(human, human, tactician, self, 1, kZeroVector, nil)
      nAttribute.RemoveAttributeSnapshot(self)
    end
    dgsim_d13_dead_snap = nAttribute.CreateAttributeSnapshot_FromObject(human, kObjIDNone)
    local killer = find(team == nTeamID.Spirit and 'Binding_Mortal_Inner' or 'Binding_Spirit_Inner')
    nGameObject.KillObject(human, killer or kObjIDNone, false)
    p('d13 dead attacker part 1: human', tostring(human), 'role', tostring(nPlayer.GetPlayerRole(pid0)), 'LifeDrain',
      f2(nAttribute.GetAttributeValue(human, nAttributeType.LifeDrain)), 'killed by', tostring(killer), '-> hp',
      f2(nGameObject.GetHitPoints(human)), 'alive', tostring(nGameObject.IsAlive(human)))
  end
  local snap = dgsim_d13_dead_snap
  if not snap then
    p('d13 dead attacker: no snapshot (part 1 first)')
    return
  end
  local gold = nPlayer.GetPlayerAvailableGold(pid0)
  local bh = nGameObject.GetHitPoints(bot)
  local s, dealt = nGameObject.TakeDamage(snap, bot, 50, 0, basic, kZeroVector)
  local mod = nModifier.GetFirstModifierByGUID(human, tactician)
  p(string.format('d13 dead attacker part %d: 50 basic on the bot %s -> success %s dealt %s, bot hp %s -> %s; the dead '
    .. 'shaper hp %s alive %s (expected 0, false); gold %s -> %s (part 1: +20 Tactician vim expected, part 2: none); '
    .. 'Tactician modifier %s', part, tostring(bot), tostring(s), f2(dealt), f2(bh), f2(nGameObject.GetHitPoints(bot)),
    f2(nGameObject.GetHitPoints(human)), tostring(nGameObject.IsAlive(human)), f2(gold),
    f2(nPlayer.GetPlayerAvailableGold(pid0)), tostring(mod)))
  if part >= 2 then
    nAttribute.RemoveAttributeSnapshot(snap)
    dgsim_d13_dead_snap = nil
    if dgsim_d13_dead_ld then nAttribute.RemoveAttributeModifier(human, dgsim_d13_dead_ld) end
    dgsim_d13_dead_ld = nil
  end
end)
if not ok then p('d13 dead attacker: ERROR', err) end
return table.concat(out, '\n')
