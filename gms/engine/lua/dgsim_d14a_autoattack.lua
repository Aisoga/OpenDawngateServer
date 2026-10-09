-- D14a-live probe: the server's auto-acquire and a recall cancelled by an attack out of range (SERVER_MODE.md *Battle
-- spells, recall, auto-attack ... (D14)*; d14a_recall1c's auto-attack steps tested nothing: their move target was off
-- the navmesh and the probe minion walked away). Each call reports the human (position, recall running) and the probe
-- minion (HP, position); some calls replace the minion. Every probe minion is an enemy melee minion ROOTED
-- (Immobilized +1) on the walked path from the human toward its start point, so it stays where it was put (a rooted
-- melee minion reaches nobody beyond 2 units). Fenmore's basic range is 16.18 (ability_Alpha_Basic).
-- Schedule (test_lua seconds) with re\out\d14\commands_autoattack.txt (1 move to (-120, -190), 24 stop, 31 move to
-- (-122, -191), 36 move to (-121, -190) + 36.2 stop, 44 attack the nearest minion (keep attacking), 50 recall,
-- 52 attack the nearest minion):
--   20 call 1: minion A 12 units toward the base; the human idle after its move: expected the auto-acquire to start
--      (A's HP falls by the next call);
--   23 call 2: A's HP below its max;
--   25 call 3 (after the stop at 24): A deleted, B at the same spot;
--   30 call 4: B at full HP (no auto-attack after a stop);
--   35 call 5 (after the move at 31): B's HP below max (the auto-acquire after a move);
--   37 call 6 (after the move + stop at 36/36.2, the client's "Enable Auto Attacks" off): B deleted, C at the spot;
--   43 call 7: C at full HP (move + stop: no auto-attack);
--   47 call 8 (after the attack at 44): C's HP below max; C deleted, E 25 units toward the base (out of range);
--   51 call 9 (recall at 50): the recall running;
--   53 call 10 (attack on E at 52, out of range): expected the recall ended [inferred: the request path ends a
--      selfInterruptible holder, or OnAbilityUsed when the attack fires], the human walking toward E;
--   56 call 11: the human attacking E (E's HP falling), no recall.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c, pid end
  end
end

local function minionText(m)
  if not m then return 'none' end
  if not nGameObject.Exists(m) then return tostring(m) .. ' gone' end
  local v = nGameObject.GetPosition(m)
  return string.format('%s hp %s/%s alive %s at (%.2f, %.2f)', tostring(m), f2(nGameObject.GetHitPoints(m)),
    f2(nGameObject.GetMaxHitPoints(m)), tostring(nGameObject.IsAlive(m)), v.x, v.y)
end

local function spawn(h, pid, dist, tag)
  local v = nGameObject.GetPosition(h)
  local s = nGameSimulator.GetPlayerStartingPoint(pid)
  local dx, dy = s.x - v.x, s.y - v.y
  local d = math.sqrt(dx * dx + dy * dy)
  if d < 1 then d, dx, dy = 1, -1, 0 end
  local at = nVector.create(v.x + dx / d * dist, v.y + dy / d * dist, 0)
  local team = nGameObject.GetTeam(h)
  local noun, enemy = 'Minion_Spirit_Melee.Noun', nTeamID.Spirit
  if team == nTeamID.Spirit then noun, enemy = 'Minion_Mortal_Melee.Noun', nTeamID.Mortal end
  local m = nObjectManager.CreateObject(nUtil.GetAsset(noun), enemy, at, kZeroVector, 1)
  if m then nAttribute.AddAttributeModifier(m, nAttributeType.Immobilized, 1, nAttributeOp.kAdd) end
  p(string.format('d14 autoattack: minion %s %s spawned rooted %.0f units toward the base at (%.2f, %.2f)', tag, tostring(m),
    dist, at.x, at.y))
  return m
end

local function remove(m)
  if m and nGameObject.Exists(m) then nGameObject.MarkForDelete(m) end
end

local ok, err = pcall(function()
  local h, pid = human()
  if not h then
    p('d14 autoattack: no human player creature')
    return
  end
  dgsim_d14_aa_call = (dgsim_d14_aa_call or 0) + 1
  local n = dgsim_d14_aa_call
  local v = nGameObject.GetPosition(h)
  local expect = {
    'spawn A; the auto-acquire starts by the next call',
    'A below max (the auto-acquire on an idle shaper after a move)',
    'after the stop: B replaces A',
    'B at full HP (no auto-attack after a stop)',
    'B below max (the auto-acquire after the move at 31)',
    'after move + stop: C replaces B',
    'C at full HP (move + stop: no auto-attack)',
    'C below max (the attack at 44); E replaces C, 25 units away',
    'the recall running',
    'the recall ended by the attack on E (out of range), the human walking toward E',
    'E below max, no recall',
  }
  p(string.format('d14 autoattack call %d: match %.1f s, human at (%.2f, %.2f) hp %s, recall running %s, AutoAttackDisabled %s; '
    .. 'minion %s; expected: %s', n, nGameSimulator.GetMatchTime(), v.x, v.y, f2(nGameObject.GetHitPoints(h)),
    tostring(nModifier.AgentHasModifierMatchingGUID(h, nUtil.SPID('modifier_returntobase'))),
    f2(nAttribute.GetAttributeValue(h, nAttributeType.AutoAttackDisabled)), minionText(dgsim_d14_aa_minion),
    tostring(expect[n] or 'report')))
  if n == 1 then
    dgsim_d14_aa_minion = spawn(h, pid, 12, 'A')
  elseif n == 3 or n == 6 then
    remove(dgsim_d14_aa_minion)
    dgsim_d14_aa_minion = spawn(h, pid, 12, n == 3 and 'B' or 'C')
  elseif n == 8 then
    remove(dgsim_d14_aa_minion)
    dgsim_d14_aa_minion = spawn(h, pid, 25, 'E')
  end
end)
if not ok then p('d14 autoattack: ERROR', err) end
return table.concat(out, '\n')
