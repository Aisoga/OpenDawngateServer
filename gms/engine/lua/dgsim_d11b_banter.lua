-- D11b live probe (docs/GMS_PROTOCOL.md *The shapers' banter and kill lines (D11b)*, *The live check (D11b-live)*).
-- [host] test_lua with this file at five times, each call running the next part (global dgsim_d11b_part); the
-- recipe's schedule: 20, 40, 52, 60, 68 s after EnterGame, a practice game with its bots, the human Fenmore (the
-- default noun) and bot_classes with shapers Fenmore has lines about. Parts:
--   1 (20 s)  report: every player's index, bot or human, team, noun, position, distance to the human, alive (the
--             host's own `banter:` lines name the tables it read);
--   2 (40 s)  the meeting: the first living allied bot whose noun starts with one of DGSIM_D11B_ALLY (default
--             desecrator, survivor, shadow_assassin, shadowassassin: Fenmore's friend lines) teleported 8 units west
--             of the human, the first living enemy bot of DGSIM_D11B_ENEMY (default desecrator, shadow_assassin,
--             shadowassassin: its foe lines) 12 units east, both immobilized: expected `banter: friendly meeting`
--             (Fenmore's FriendlyBanterInitiate about the ally, the ally's FriendlyBanterResponse, the same variant)
--             within 1 s, and `banter: enemy meeting` 5 s later (the minimum banter cooldown of the human) once each
--             team sees the other;
--   3 (52 s)  both bots free again (the Immobilized modifiers removed);
--   4 (60 s)  a kill: the ally hits the enemy (60 physical basic), then the human kills it (5,000 true), both with
--             their creatures' snapshots through the host's damage core: expected `banter: kill` KillBanterWithTarget
--             (Fenmore about a Desecrator or Shadow Assassin: Play_PC_Alpha_VO_kill_PC_<...>) and `banter: assist`
--             AssistBanter by the ally;
--   5 (68 s)  a second kill by the human 8 s later (another living enemy bot): a double kill (multi-kill 2 within
--             10 s): expected no kill line (`not said: multi-kill`) while the client's own announcer shows
--             "<name> Double Kill" and plays the killer's Multikill_2_Player after it.
-- Returns a report; every line starts with "d11b".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'd11b ' .. table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f, %.1f)', v.x, v.y) end
local function dist(a, b) return math.sqrt((a.x - b.x) ^ 2 + (a.y - b.y) ^ 2) end

local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    if not nPlayer.IsBot(pid) then
      local c = nPlayer.GetPlayerControlledObjectID(pid)
      if c ~= nil and c ~= kObjIDNone then return pid, c end
    end
  end
end

local function players()
  local list = {}
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone then
      list[#list + 1] = {pid = pid, c = c, bot = nPlayer.IsBot(pid), team = nGameObject.GetTeam(c),
                         name = nGameObject.GetAssetName(c) or '?'}
    end
  end
  return list
end

-- the first living bot of `team` whose noun starts with one of `prefixes`, not `skip`
local function pick(list, team, prefixes, skip)
  for _, prefix in ipairs(prefixes) do
    for _, e in ipairs(list) do
      if e.bot and e.team == team and e.c ~= skip and nGameObject.IsAlive(e.c) and string.find(e.name, prefix, 1, true) == 1 then
        return e
      end
    end
  end
end

local function hit(from, to, amount, desc)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(from, kObjIDNone)
  local ok2, dealt = nGameObject.TakeDamage(snap, to, amount, 0, desc or nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  return ok2, dealt
end

local function teleport(o, pos)
  nLocomotion.TeleportToPositionAndFacing(o, pos, nVector.create(1, 0, 0))
end

local function state(e, what)
  if e == nil then
    p(what, 'none')
    return
  end
  p(what, 'player', e.pid, tostring(e.c), nGameObject.GetAssetName(e.c), 'team', nGameObject.GetTeam(e.c), 'at',
    vec(nGameObject.GetPosition(e.c)), 'hp', f1(nGameObject.GetHitPoints(e.c)), 'alive', tostring(nGameObject.IsAlive(e.c)),
    'visible to Mortal', tostring(nGameObject.IsVisibleToTeam(e.c, nTeamID.Mortal)), 'to Spirit',
    tostring(nGameObject.IsVisibleToTeam(e.c, nTeamID.Spirit)))
end

dgsim_d11b_part = (dgsim_d11b_part or 0) + 1
local part = dgsim_d11b_part
local ok, err = pcall(function()
  local hpid, hc = human()
  if not hc then
    p('part', part, ': no human creature')
    return
  end
  local H = nGameObject.GetPosition(hc)
  local myTeam = nGameObject.GetTeam(hc)
  local enemyTeam = myTeam == nTeamID.Mortal and nTeamID.Spirit or nTeamID.Mortal
  local list = players()
  p('part', part, 'at match time', nGameSimulator.GetMatchTime(), ': the human is player', hpid, tostring(hc),
    nGameObject.GetAssetName(hc), 'team', myTeam, 'at', vec(H))
  if part == 1 then
    for _, e in ipairs(list) do
      local P = nGameObject.GetPosition(e.c)
      p('player', e.pid, e.bot and 'bot' or 'human', 'team', e.team, e.name, 'at', vec(P), 'distance', f1(dist(P, H)),
        'alive', tostring(nGameObject.IsAlive(e.c)))
    end
  elseif part == 2 then
    local ally = pick(list, myTeam, DGSIM_D11B_ALLY or {'desecrator', 'survivor', 'shadow_assassin', 'shadowassassin'})
    local enemy = pick(list, enemyTeam, DGSIM_D11B_ENEMY or {'desecrator', 'shadow_assassin', 'shadowassassin'})
    dgsim_d11b_ally, dgsim_d11b_enemy = ally, enemy
    if ally then
      teleport(ally.c, nVector.create(H.x - 8, H.y, 0))
      dgsim_d11b_ally_mod = nAttribute.AddAttributeModifier(ally.c, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
    end
    if enemy then
      teleport(enemy.c, nVector.create(H.x + 12, H.y, 0))
      dgsim_d11b_enemy_mod = nAttribute.AddAttributeModifier(enemy.c, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
    end
    state(ally, 'ally 8 west (expected a friendly meeting within 1 s):')
    state(enemy, 'enemy 12 east (expected an enemy meeting about 5 s later):')
  elseif part == 3 then
    if dgsim_d11b_ally and dgsim_d11b_ally_mod then nAttribute.RemoveAttributeModifier(dgsim_d11b_ally.c, dgsim_d11b_ally_mod) end
    if dgsim_d11b_enemy and dgsim_d11b_enemy_mod then
      nAttribute.RemoveAttributeModifier(dgsim_d11b_enemy.c, dgsim_d11b_enemy_mod)
    end
    dgsim_d11b_ally_mod, dgsim_d11b_enemy_mod = nil, nil
    state(dgsim_d11b_ally, 'ally free:')
    state(dgsim_d11b_enemy, 'enemy free:')
  elseif part == 4 then
    local enemy = dgsim_d11b_enemy
    if enemy == nil or not nGameObject.IsAlive(enemy.c) then
      enemy = pick(list, enemyTeam, {''})
      dgsim_d11b_enemy = enemy
    end
    local ally = dgsim_d11b_ally or pick(list, myTeam, {''})
    if enemy == nil then
      p('no living enemy bot')
      return
    end
    if ally and nGameObject.IsAlive(ally.c) then
      local basic = nBit.Or(nBit.Or(nDescriptors.IsMelee, nDescriptors.IsBasic), nDescriptors.IsPhysicalDamage)
      local st, d = hit(ally.c, enemy.c, 60, basic)
      p('ally player', ally.pid, nGameObject.GetAssetName(ally.c), 'hits the enemy: 60 basic ->', tostring(st), f1(d))
    end
    local st, d = hit(hc, enemy.c, 5000, nDescriptors.IsTrueDamage)
    p('the human kills enemy player', enemy.pid, nGameObject.GetAssetName(enemy.c), ': 5000 true ->', tostring(st), f1(d),
      'alive', tostring(nGameObject.IsAlive(enemy.c)), '(expected banter: kill KillBanterWithTarget, banter: assist)')
  elseif part == 5 then
    local second = pick(list, enemyTeam, {''}, dgsim_d11b_enemy and dgsim_d11b_enemy.c or nil)
    if second == nil then
      p('no second living enemy bot')
      return
    end
    local st, d = hit(hc, second.c, 5000, nDescriptors.IsTrueDamage)
    p('the human kills enemy player', second.pid, nGameObject.GetAssetName(second.c), ': 5000 true ->', tostring(st), f1(d),
      'alive', tostring(nGameObject.IsAlive(second.c)), '(expected a double kill: no kill line, the client\'s announcer)')
  else
    p('no part', part)
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
