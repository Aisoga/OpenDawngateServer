-- D14 live probe (docs/MATCH_FLOW.md *D14*, "jungle buffs"; [host] test_lua=<T>:dgsim_d14_jungle.lua,<T+20>:...,
-- <T+22>:...,<T+24>:...,<T+26>:...; T >= 92, the buff camps spawn at match 90 s). The three majors' buffs come from their
-- CharacterRewardData.buffs ("last player to hit"), which the host's death step 4 grants since D14 (nothing in the
-- client reads the list). Each call runs the next part:
--   part 1: the human's shaper kills the Green (Fish) major at (68.0, -165.6), the Blue (Fungus) major at (-59.4, -57.4)
--     and the Orange (Ugger) major at (-169.1, 61.5) with one lethal true hit each from its own snapshot (the credit);
--     a bot kills the other Orange major at (167.5, -62.7) the same way; an enemy minion's snapshot kills the other Blue
--     major at (59.0, 58.7). Expected: the shaper has modifier_Jungle_GreenBuff, _BlueBuff and _OrangeBuff, each with
--     about 150 s left, Power +15 and +10 % (Green), Haste +20 (Blue), armor +20 (Orange); the bot has _OrangeBuff;
--     nobody gets the minion-killed Blue (the host's line "buff modifier_Jungle_BlueBuff ...: nobody"); before each of
--     the shaper's kills, the major's distance from its camp's scale-2.0 marker (the tutorial's buff ping; expected
--     < 0.5 with [host] camp_major_spot=1) and its BodyScale;
--   part 2 (20 s later): the Green buff has about 130 s left; the shaper kills the other Green major at (-67.6, 165.9):
--     expected one Green instance again with about 150 s left (a Unique modifier is renewed: the engine ends the old
--     instance and starts a new one), Power still +15 / +10 % once;
--   part 3: Orange's follow-up: one hit from the shaper's snapshot on a spawned enemy minion: expected
--     modifier_Jungle_OrangeBuff_DamageReduction with 1 stack (OnDealtDamage; 1 % DamageReduction, 3 s);
--     Blue's follow-up needs a non-basic ability: cast one from the commands file (e.g. Fenmore's W) between parts 2
--     and 3 and look for modifier_Jungle_BlueBuff_BonusHaste here (3 s, Haste +20);
--   part 4: the shaper is killed (KillObject): the buffs end OnAgentDeath (3 scheduler runs later);
--   part 5 (2 s later): expected no jungle buff on the dead shaper.
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local GREEN = nUtil.SPID('modifier_Jungle_GreenBuff')
local BLUE = nUtil.SPID('modifier_Jungle_BlueBuff')
local ORANGE = nUtil.SPID('modifier_Jungle_OrangeBuff')
local BLUE2 = nUtil.SPID('modifier_Jungle_BlueBuff_BonusHaste')
local ORANGE2 = nUtil.SPID('modifier_Jungle_OrangeBuff_DamageReduction')

-- nPlayer.IsBot takes an object (the player's creature: nPlayer_IsBot 0x00947660 -> SporeLabsLua_GetObjectArg ->
-- GameObject_IsBot), not a player index; given an index it is false for everyone (D14a-live: the bot part found none).
local function shaperOf(wantBot, team)
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and (nPlayer.IsBot(c) and true or false) == wantBot and nGameObject.IsAlive(c) and
        (team == nil or nGameObject.GetTeam(c) == team) then
      return c, pid
    end
  end
end

local function findNear(prefix, x, y)
  local seen = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      local pos = nGameObject.GetPosition(id)
      if string.find(name, '^' .. prefix) and pos and math.abs(pos.x - x) < 15 and math.abs(pos.y - y) < 15 and
          nGameObject.IsAlive(id) then
        return id
      end
    end
  end
end

-- D14: a buff camp's major stands on the camp's scale-2.0 child marker (the tutorial's buff ping), the host's
-- camp_major_spot rule; the three pings of team 1's side.
local PINGS = {
  Creep_Jungle_Fish_Major = {73.96, -162.46},
  Creep_Jungle_Fungus_Major = {-52.69, -52.33},
  Creep_Jungle_Ugger_Major = {-166.79, 69.06},
}

local function killWith(killer, prefix, x, y, what)
  local target = findNear(prefix, x, y)
  if not target then
    p('d14 jungle:', what, 'no living', prefix, 'near', x, y)
    return
  end
  local ping = PINGS[prefix]
  if ping and what == 'shaper' then
    local tp = nGameObject.GetPosition(target)
    p(string.format('d14 jungle: %s at (%.2f, %.2f), %.2f from the scale-2.0 marker (expected < 0.5: camp_major_spot); '
      .. 'BodyScale %s', prefix, tp.x, tp.y, math.sqrt((tp.x - ping[1]) ^ 2 + (tp.y - ping[2]) ^ 2),
      f2(nAttribute.GetAttributeValue(target, nAttributeType.BodyScale))))
  end
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(killer, kObjIDNone)
  local s, d = nGameObject.TakeDamage(snap, target, 100000, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  p('d14 jungle:', what, 'killed', prefix, tostring(target), '->', tostring(s), f2(d), 'alive',
    tostring(nGameObject.IsAlive(target)))
end

local function buffs(obj)
  local function one(guid, name)
    local mods = nModifier.GetModifiersMatchingGUID(obj, guid) or {}
    local n, left = 0, nil
    for _, m in pairs(mods) do
      n = n + 1
      left = nModifier.GetRemainingDuration(m)
    end
    return string.format('%s x%d%s', name, n, left and string.format(' (%.1f s left)', left) or '')
  end
  return one(GREEN, 'Green') .. ', ' .. one(BLUE, 'Blue') .. ', ' .. one(ORANGE, 'Orange') .. ', ' ..
    one(BLUE2, 'BlueHaste') .. ', ' .. one(ORANGE2, 'OrangeDR')
end

local function stats(obj)
  return string.format('Power %s Haste %s armor %s DR %s', f2(nAttribute.GetAttributeValue(obj, nAttributeType.Power)),
    f2(nAttribute.GetAttributeValue(obj, nAttributeType.Haste)),
    f2(nAttribute.GetAttributeValue(obj, nAttributeType.PhysicalDamageReduction)),
    f2(nAttribute.GetAttributeValue(obj, nAttributeType.DamageReduction)))
end

local ok, err = pcall(function()
  local shaper = dgsim_d14_jungle_shaper or shaperOf(false)
  if not shaper then
    p('d14 jungle: no human player creature')
    return
  end
  dgsim_d14_jungle_shaper = shaper
  dgsim_d14_jungle_part = (dgsim_d14_jungle_part or 0) + 1
  local part = dgsim_d14_jungle_part
  p('d14 jungle part', part, ': shaper', tostring(shaper), 'match time', f2(nGameSimulator.GetMatchTime()), 'before:',
    buffs(shaper), ';', stats(shaper))
  if part == 1 then
    killWith(shaper, 'Creep_Jungle_Fish_Major', 68.0, -165.6, 'shaper')
    killWith(shaper, 'Creep_Jungle_Fungus_Major', -59.4, -57.4, 'shaper')
    killWith(shaper, 'Creep_Jungle_Ugger_Major', -169.1, 61.5, 'shaper')
    local bot = shaperOf(true)
    if bot then
      dgsim_d14_jungle_bot = bot
      killWith(bot, 'Creep_Jungle_Ugger_Major', 167.5, -62.7, 'bot')
    else
      p('d14 jungle: no living bot (bots=0?)')
    end
    local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Spirit_Melee.Noun'), nTeamID.Spirit,
      nVector.create(55.0, 52.0, 0), kZeroVector, 1)
    if m then
      killWith(m, 'Creep_Jungle_Fungus_Major', 59.0, 58.7, 'enemy minion')
      nGameObject.MarkForDelete(m)
    end
    p('d14 jungle part 1: shaper after:', buffs(shaper), ';', stats(shaper),
      '(expected Green, Blue, Orange x1 with about 150 s; Power +15 +10 %, Haste +20, armor +20)')
    if dgsim_d14_jungle_bot then
      p('d14 jungle part 1: bot', tostring(dgsim_d14_jungle_bot), 'after:', buffs(dgsim_d14_jungle_bot),
        '(expected Orange x1)')
    end
  elseif part == 2 then
    killWith(shaper, 'Creep_Jungle_Fish_Major', -67.6, 165.9, 'shaper (second Green)')
    p('d14 jungle part 2: after the second Green kill:', buffs(shaper), ';', stats(shaper),
      '(expected Green x1 renewed to about 150 s, Power unchanged from part 1)')
  elseif part == 3 then
    local team = nGameObject.GetTeam(shaper)
    local noun, enemy = 'Minion_Spirit_Melee.Noun', nTeamID.Spirit
    if team == nTeamID.Spirit then noun, enemy = 'Minion_Mortal_Melee.Noun', nTeamID.Mortal end
    local sp = nGameObject.GetPosition(shaper)
    local m = nObjectManager.CreateObject(nUtil.GetAsset(noun), enemy, nVector.create(sp.x + 4, sp.y + 3, 0), kZeroVector, 1)
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(shaper, kObjIDNone)
    local s, d = nGameObject.TakeDamage(snap, m, 10, 0, nDescriptors.IsPhysicalDamage, kZeroVector)
    nAttribute.RemoveAttributeSnapshot(snap)
    nGameObject.MarkForDelete(m)
    p('d14 jungle part 3: one hit on a minion ->', tostring(s), f2(d), ';', buffs(shaper), ';', stats(shaper),
      '(expected OrangeDR x1 (3 s); BlueHaste x1 only if an ability was cast within 3 s)')
  elseif part == 4 then
    nGameObject.KillObject(shaper)
    p('d14 jungle part 4: shaper killed, alive', tostring(nGameObject.IsAlive(shaper)))
  else
    p('d14 jungle part', part, ': dead shaper:', buffs(shaper), '(expected none: OnAgentDeath)')
  end
end)
if not ok then p('d14 jungle: ERROR', err) end
return table.concat(out, '\n')
