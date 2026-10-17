-- TOWERAGGRO live probe: whom the top Binding_Mortal_Outer shoots ([host] test_lua with thirteen calls,
--   T, T+1.5, T+2.5, T+3.5, T+4.3, T+6.5, T+7.3, T+11.5, T+15.5, T+17, T+18.5, T+20, T+21 (T = 25), each
--   <s>:dgsim_towers.lua), in a practice game with bot_brain=0 (the bots stand still), director_waves=0 (empty lanes),
--   the human on team 1 (Mortal, the default). gms/core/agents/tower_target.h has the rule; compare tower_target=0.
--   Positions are offsets from the Binding B; the spawned Spirit minions are immobilized and healed at every call, as
--   are the bot and the human, so nobody dies in the test.
--   part 1 (T): "a shaper walking in before the wave": a Spirit bot to B + (16, 6), nothing else in range.
--   part 2 (T+1.5): expected the bot as B's best target (the only one); then three Spirit melee minions beside B.
--   part 3 (T+2.5): expected a minion (every non-player before every shaper; the old order kept the sticky bot).
--   part 4 (T+3.5): "a shaper hitting minions under an enemy Binding": the bot hits a Mortal minion v (immobilized at
--     B + (28, 18)) for 20 (v calls for help: B gets a non-player's report on the bot), then B's current best minion is
--     killed (the old order: the reported bot came before the other minions once the sticky target was gone).
--   part 5 (T+4.3): expected a Spirit minion, not the bot.
--   part 6 (T+6.5): expected a minion; then "a shaper hitting an own-team shaper under the Binding": the human to
--     B + (19.3, 9.1) (within B's 28), the bot to B + (22.3, 12.1), the bot hits the human for 20.
--   part 7 (T+7.3): expected the bot (the defend lock).
--   part 8 (T+11.5), part 9 (T+15.5): 4 and 8 s after the hit, past the 3-s call-for-help window: expected the bot still
--     (the user's rule: it stays the target until it leaves the range); then the bot to 30 units from B (edge ~25.8).
--   part 10 (T+17): expected the bot (still in range); then the bot to 36 units (edge ~31.8, out of range).
--   part 11 (T+18.5): expected a minion (the lock released); then the bot back to B + (22.3, 12.1), no hit.
--   part 12 (T+20): expected a minion (no new hit, no lock).
--   part 13 (T+21): the spawned minions removed.
-- Returns a report (the host logs it after "host: test_lua dgsim_towers.lua at ...").
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end
local function pos(o)
  local v = o and nGameObject.GetPosition(o)
  if not v then return '(?)' end
  return string.format('(%.1f, %.1f)', v.x, v.y)
end

local S = dgsim_towers or {}
dgsim_towers = S

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
      if not nPlayer.IsBot(c) then human = human or c  -- nPlayer.IsBot takes an object (0x00947660)
      elseif nGameObject.GetTeam(c) == botTeam and nGameObject.IsAlive(c) then bot = bot or c end
    end
  end
  return human, bot
end

local function at(dx, dy) return S.bx + dx, S.by + dy end
local function teleport(o, x, y)
  nLocomotion.TeleportToPositionAndOrientation(o, nVector.create(x, y, 0), nGameObject.GetOrientation(o))
end
local function spawn(noun, team, x, y)
  local m = nObjectManager.CreateObject(nUtil.GetAsset(noun), team, nVector.create(x, y, 0), kZeroVector, 1)
  if m then
    nAttribute.AddAttributeModifier(m, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
    S.spawned[#S.spawned + 1] = m
  end
  return m
end
local function heal(h)
  if not h or not nGameObject.IsAlive(h) then return end
  local missing = nGameObject.GetMaxHitPoints(h) - nGameObject.GetHitPoints(h)
  if missing < 1 then return end
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(h, kObjIDNone)
  nGameObject.HealDamage(snap, h, missing, 0)
  nAttribute.RemoveAttributeSnapshot(snap)
end
local function healAll()
  heal(S.human)
  heal(S.bot)
  for _, m in ipairs(S.spawned or {}) do heal(m) end
end
local function hit(from, to, amount)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(from, kObjIDNone)
  local _, dealt = nGameObject.TakeDamage(snap, to, amount, 0, nBit.Or(nDescriptors.IsBasic, nDescriptors.IsPhysicalDamage),
    kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  return dealt
end
local function name(id)
  if id == nil or id == kObjIDNone then return 'none' end
  if id == S.bot then return 'the bot ' .. tostring(id) end
  if id == S.human then return 'the human ' .. tostring(id) end
  return tostring(id) .. ' ' .. tostring(nGameObject.GetAssetName(id))
end
local function list(o)
  local okList, ids = pcall(nAgent.GetTargetsOnAggroList, o)
  if not okList or type(ids) ~= 'table' then return '[?]' end
  local t = {}
  for _, id in ipairs(ids) do t[#t + 1] = name(id) end
  return '[' .. table.concat(t, ', ') .. ']'
end
local function check(label, expectBot)
  local best = nAgent.GetBestTarget(S.binding)
  local isBot = best == S.bot
  local good = (expectBot and isBot) or (not expectBot and best ~= nil and best ~= kObjIDNone and not isBot and best ~= S.human)
  p('towers:', label, 'best', name(best), 'expected', expectBot and 'the bot' or 'a minion', '->',
    good and 'PASS' or 'FAIL', '; list', list(S.binding), '; bot at', pos(S.bot), 'hp', f1(nGameObject.GetHitPoints(S.bot)))
  return best
end

local parts = {}

parts[1] = function()
  S.human, S.bot = humanAndBot(nTeamID.Spirit)
  S.binding = findByName('Binding_Mortal_Outer', function(v) return v and v.y > v.x end)
  S.spawned = {}
  if not S.human or not S.bot or not S.binding then
    p('towers part 1: missing human', tostring(S.human), 'Spirit bot', tostring(S.bot), 'or top Binding_Mortal_Outer',
      tostring(S.binding))
    S.part = 100
    return
  end
  local b = nGameObject.GetPosition(S.binding)
  S.bx, S.by = b.x, b.y
  teleport(S.bot, at(16, 6))
  -- the Binding kills a level-1 bot in about 5 s: 20,000 more health for the bot and the human, healed at every call
  for _, o in ipairs({S.bot, S.human}) do
    nAttribute.AddAttributeModifier(o, nAttributeType.MaxHealth, 20000, nAttributeOp.kAdd)
  end
  healAll()
  p('towers part 1: the bot', tostring(S.bot), 'to', pos(S.bot), '; Binding', tostring(S.binding), pos(S.binding),
    '; the human', tostring(S.human), pos(S.human))
end

parts[2] = function()
  check('part 2 (a shaper alone in range)', true)
  local mx, my = at(10, 0)
  S.m = {spawn('Minion_Spirit_Melee.Noun', nTeamID.Spirit, mx, my)}
  mx, my = at(12.3, 2.1)
  S.m[2] = spawn('Minion_Spirit_Melee.Noun', nTeamID.Spirit, mx, my)
  mx, my = at(8, 4)
  S.m[3] = spawn('Minion_Spirit_Melee.Noun', nTeamID.Spirit, mx, my)
  healAll()
  p('towers part 2: three Spirit minions', tostring(S.m[1]), tostring(S.m[2]), tostring(S.m[3]), 'beside the Binding')
end

parts[3] = function()
  check('part 3 (the wave arrived after the shaper)', false)
  healAll()
end

parts[4] = function()
  local vx, vy = at(28, 18)
  S.v = spawn('Minion_Mortal_Melee.Noun', nTeamID.Mortal, vx, vy)
  local dealt = S.v and hit(S.bot, S.v, 20)
  local best = nAgent.GetBestTarget(S.binding)
  local killed = 'none'
  if best ~= nil and best ~= kObjIDNone and best ~= S.bot then
    nGameObject.KillObject(best, S.v or kObjIDNone, false)
    killed = name(best)
  end
  healAll()
  p('towers part 4: the bot hit the Mortal minion', tostring(S.v), 'for', f1(dealt), '; the Binding\'s target', killed,
    'killed')
end

parts[5] = function()
  check('part 5 (a shaper hit a minion under the Binding, its target died)', false)
  healAll()
end

parts[6] = function()
  check('part 6', false)
  teleport(S.human, at(19.3, 9.1))
  teleport(S.bot, at(22.3, 12.1))
  local dealt = hit(S.bot, S.human, 20)
  p('towers part 6: the human to', pos(S.human), ', the bot to', pos(S.bot), '; the bot hit the human for', f1(dealt))
end

parts[7] = function()
  check('part 7 (it hit an own shaper in range)', true)
  healAll()
end

parts[8] = function()
  check('part 8 (4 s after the hit)', true)
  healAll()
end

parts[9] = function()
  check('part 9 (8 s after the hit)', true)
  teleport(S.bot, at(0.879 * 30, 0.477 * 30))
  healAll()
  p('towers part 9: the bot to', pos(S.bot), '(30 from the Binding)')
end

parts[10] = function()
  check('part 10 (30 from the Binding, still in range)', true)
  teleport(S.bot, at(0.879 * 36, 0.477 * 36))
  healAll()
  p('towers part 10: the bot to', pos(S.bot), '(36 from the Binding: out of range)')
end

parts[11] = function()
  check('part 11 (it left the range)', false)
  teleport(S.bot, at(22.3, 12.1))
  healAll()
  p('towers part 11: the bot back to', pos(S.bot), 'without a hit; the human hp', f1(nGameObject.GetHitPoints(S.human)))
end

parts[12] = function()
  check('part 12 (back in range without a new hit)', false)
  p('towers part 12: the human hp', f1(nGameObject.GetHitPoints(S.human)), 'of', f1(nGameObject.GetMaxHitPoints(S.human)))
end

parts[13] = function()
  for _, m in ipairs(S.spawned or {}) do
    if m and nGameObject.IsAlive(m) then nGameObject.MarkForDelete(m) end
  end
  p('towers part 13: the spawned minions removed')
end

local ok, err = pcall(function()
  S.part = (S.part or 0) + 1
  local f = parts[S.part]
  p('towers: part', S.part, 'at match time', string.format('%.2f', nGameSimulator.GetMatchTime()))
  if f then f() end
end)
if not ok then p('towers: ERROR', err) end
return table.concat(out, '\n')
