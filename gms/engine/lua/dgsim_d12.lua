-- D12 test library (docs/ABILITIES.md; [host] test_lua=<s>:dgsim_d12.lua first, then the session's plan file from
-- re/scripts/ability_check.py plan, then dgsim_d12_step.lua at each step's time): the arena for one shaper's ability
-- check. Defines the global table dgsim_d12 and returns a short report; the plan file sets dgsim_d12_plan and calls
-- dgsim_d12.init(), each run of dgsim_d12_step.lua calls dgsim_d12.step() for the plan's next step.
--
-- init(): the human player's creature (player 0) and every bot creature are teleported into an arena away from towers
-- and bases (dgsim_d12_plan.at, default (-100, -104), D11's spot; run with [host] fog=0, some cells there are grass):
-- the human at the centre facing +x, the enemy bots (the other team's players) at +4 and +7 units along x (so the
-- nearest enemy player is the first), the allied bots behind it; every bot gets XP to level 20 (nGameObject.GrantXP,
-- nXPSourceType.Cheat: more HP, so a capped ultimate rarely kills it) and its brain stays off ([host] bot_brain=0);
-- dgsim_d12_plan.creeps camp creeps (Creep_Jungle_Fish_Minor, team NPC: a real camp creep, aggro range 0) are created
-- 6-9 units away. Logs "d12 init:" lines with every object's id, noun, team and position.
--
-- step(): the plan's next step (dgsim_d12_plan.steps[k], in the order of the test_lua times):
--   kind 'reset'   before a cast: every participant's HP before ("hp" = what the last cast left), then the human, the
--                  bots and the creeps healed to full (nGameObject.HealDamage with their own snapshot), the human's
--                  resource refilled, everyone teleported back to the spot if moved more than 1.5 units (nothing is
--                  stopped: an ability still running keeps running), dead creeps replaced; with cooldowns = true also
--                  every cooldown of the plan's abilities set to 0 (nAbility.SetCooldownRemaining); reports the human's
--                  remaining cooldown per ability (a cast's cooldown proves it was paid).
--   kind 'passive' whether the human carries its PlayerClass.passiveAbility (nModifier.AgentHasModifierMatchingGUID) and
--                  that modifier's stack count and remaining duration.
--   kind 'minion'  creates dgsim_d12_plan.minion (default Minion_Spirit_Melee.Noun) of the enemy team 3 units in front
--                  of the human (a target for a basic attack).
-- Every line starts with "d12 " and the match time, so re/scripts/ability_check.py report can read them.
dgsim_d12 = dgsim_d12 or {}
local D = dgsim_d12

local function fmt(v)
  if type(v) == 'number' then return string.format('%.1f', v) end
  return tostring(v)
end

local function vec(v)
  if v == nil then return 'nil' end
  return string.format('(%.1f, %.1f, %.1f)', v.x, v.y, v.z)
end

local function hexid(id)  -- object ids are GUID userdata; tostring gives "0x7c78012d"
  if id == nil or id == kObjIDNone then return 'none' end
  local s = tostring(id)
  if string.sub(s, 1, 2) == '0x' then s = string.sub(s, 3) end
  return string.upper(s)
end

local function live(id)
  return id ~= nil and id ~= kObjIDNone and nObjectManager.IsValidObject(id)
end

function D.begin()
  D.out = {}
end

function D.p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = fmt((select(i, ...))) end
  D.out[#D.out + 1] = 'd12 ' .. string.format('%.2f', nGameSimulator.GetMatchTime()) .. ' ' .. table.concat(t, ' ')
end

function D.finish()
  return table.concat(D.out, '\n')
end

-- The arena's spots (offsets from the plan's centre): the human, the enemies, the allies, the creeps.
local kEnemy = {{4, 0}, {7, 3}, {7, -3}, {9, 0}, {9, 4}}
local kAlly = {{-4, 3}, {-4, -3}, {-6, 0}, {-6, 4}}
local kCreep = {{6, -6}, {9, -7}, {8, 7}}

local function spot(off)
  local at = D.plan.at or {-100.0, -104.0}
  return at[1] + off[1], at[2] + off[2]
end

local function place(id, x, y, why)
  local pos = nGameObject.GetPosition(id)
  local dx, dy = pos.x - x, pos.y - y
  if dx * dx + dy * dy <= 1.5 * 1.5 then return false end
  nLocomotion.TeleportToPositionAndFacing(id, nVector.create(x, y, pos.z), nVector.create(1, 0, 0))
  D.p('teleport', hexid(id), nGameObject.GetAssetName(id), why, 'from', vec(pos), 'to',
    vec(nGameObject.GetPosition(id)))
  return true
end

local function heal(id)
  if not nGameObject.IsAlive(id) then return 'dead' end
  local hp, max = nGameObject.GetHitPoints(id), nGameObject.GetMaxHitPoints(id)
  if hp < max then
    nGameObject.HealDamage(nGameObject.GetAttributeSnapshot(id), id, max - hp + 1, 0, 0, false, false)
  end
  return string.format('%.0f/%.0f', hp, max)
end

local function makeCreep(i)
  local x, y = spot(kCreep[i])
  local noun = nUtil.GetAsset(D.plan.creepNoun or 'Creep_Jungle_Fish_Minor.Noun')
  local id = nObjectManager.CreateObject(noun, nTeamID.NPC, nVector.create(x, y, 0), kZeroVector, 1)
  if live(id) then
    D.creeps[i] = id
    D.p('creep', i, hexid(id), nGameObject.GetAssetName(id), 'team', nGameObject.GetTeam(id), 'at',
      vec(nGameObject.GetPosition(id)), 'hp', nGameObject.GetHitPoints(id))
  else
    D.p('creep', i, 'NOT created')
  end
end

function D.init()
  D.begin()
  local ok, err = pcall(function()
    D.plan = dgsim_d12_plan or {}
    D.k = 0
    D.human = nPlayer.GetPlayerControlledObjectID(D.plan.human or 0)
    if not live(D.human) then
      D.p('init: no creature for player', D.plan.human or 0)
      return
    end
    local myTeam = nGameObject.GetTeam(D.human)
    D.enemies, D.allies, D.creeps, D.home = {}, {}, {}, {}
    for i = 0, 9 do
      if i ~= (D.plan.human or 0) then
        local c = nPlayer.GetPlayerControlledObjectID(i)
        if live(c) then
          if nGameObject.GetTeam(c) == myTeam then D.allies[#D.allies + 1] = c else D.enemies[#D.enemies + 1] = c end
          if D.plan.botXp ~= false then nGameObject.GrantXP(i, D.plan.botXpAmount or 20330, false, nXPSourceType.Cheat) end
        end
      end
    end
    local hx, hy = spot({0, 0})
    D.home[D.human] = {hx, hy}
    place(D.human, hx, hy, 'human')
    for i, c in ipairs(D.enemies) do
      local x, y = spot(kEnemy[math.min(i, #kEnemy)])
      D.home[c] = {x, y}
      place(c, x, y, 'enemy ' .. i)
    end
    for i, c in ipairs(D.allies) do
      local x, y = spot(kAlly[math.min(i, #kAlly)])
      D.home[c] = {x, y}
      place(c, x, y, 'ally ' .. i)
    end
    for i = 1, math.min(D.plan.creeps or 2, #kCreep) do makeCreep(i) end
    D.p('init:', D.plan.shaper, 'human', hexid(D.human), nGameObject.GetAssetName(D.human), 'team', myTeam, 'at',
      vec(nGameObject.GetPosition(D.human)), 'level', nGameObject.GetPlayerLevel(D.human), 'resource',
      fmt(nGameObject.GetResourcePoints(D.human)), 'of', fmt(nGameObject.GetMaxResourcePoints(D.human)),
      '; enemies', #D.enemies,
      '; allies', #D.allies, '; steps', #(D.plan.steps or {}))
    for _, c in ipairs(D.enemies) do
      D.p('init: enemy', hexid(c), nGameObject.GetAssetName(c), 'at', vec(nGameObject.GetPosition(c)), 'hp',
        nGameObject.GetHitPoints(c))
    end
    for _, c in ipairs(D.allies) do
      D.p('init: ally', hexid(c), nGameObject.GetAssetName(c), 'at', vec(nGameObject.GetPosition(c)), 'hp',
        nGameObject.GetHitPoints(c))
    end
  end)
  if not ok then D.p('init: ERROR', err) end
  return D.finish()
end

local function cooldowns()
  local parts = {}
  for _, n in ipairs(D.plan.abilities or {}) do
    local cd = nAbility.GetCooldownRemaining(D.human, nUtil.SPID(n))
    if cd ~= nil and cd > 0 then parts[#parts + 1] = string.format('%s %.1f', n, cd) end
  end
  return #parts > 0 and table.concat(parts, ', ') or 'none'
end

local function reset(s)
  local hp = {}
  local function one(id, what)
    if not live(id) then
      hp[#hp + 1] = what .. ' gone'
      return
    end
    hp[#hp + 1] = what .. ' ' .. heal(id)
    local home = D.home[id]
    if home and nGameObject.IsAlive(id) then place(id, home[1], home[2], what) end
  end
  D.p('reset before', s.note or '?', ': cooldowns', cooldowns(), '; resource',
    live(D.human) and fmt(nGameObject.GetResourcePoints(D.human)) or '?', 'of',
    live(D.human) and fmt(nGameObject.GetMaxResourcePoints(D.human)) or '?')
  one(D.human, 'human')
  if live(D.human) and nGameObject.IsAlive(D.human) then
    local max = nGameObject.GetMaxResourcePoints(D.human)
    if max ~= nil and max > 0 then nGameObject.SetResourcePoints(D.human, max) end
  end
  for i, c in ipairs(D.enemies) do one(c, 'enemy' .. i) end
  for i, c in ipairs(D.allies) do one(c, 'ally' .. i) end
  for i = 1, math.min(D.plan.creeps or 2, #kCreep) do
    local c = D.creeps[i]
    if live(c) and nGameObject.IsAlive(c) then
      hp[#hp + 1] = 'creep' .. i .. ' ' .. heal(c)
    else
      hp[#hp + 1] = 'creep' .. i .. ' dead'
      makeCreep(i)
    end
  end
  D.p('hp (what the last step left):', table.concat(hp, ', '))
  if s.cooldowns then
    for _, n in ipairs(D.plan.abilities or {}) do nAbility.SetCooldownRemaining(D.human, 0, nUtil.SPID(n)) end
    D.p('cooldowns reset; now', cooldowns())
  end
end

local function passive()
  local name = D.plan.passive
  if name == nil then
    D.p('passive: the plan names none')
    return
  end
  local guid = nUtil.SPID(name)
  local has = nModifier.AgentHasModifierMatchingGUID(D.human, guid)
  local extra = ''
  if has then
    local m = nModifier.GetFirstModifierByGUID(D.human, guid)
    if m ~= nil and m ~= kObjIDNone then
      extra = string.format(' (modifier %s, stacks %s, remaining %s)', hexid(m), tostring(nModifier.GetStackCount(m)),
        fmt(nModifier.GetRemainingDuration(m)))
    end
  end
  D.p('passive', name, has and 'ACTIVE' or 'MISSING', 'on', hexid(D.human), extra)
end

-- The passive again, the way the scripts request a modifier (D12, Marah's attached trigger volumes): phase 'off' marks
-- the host's instance for delete, phase 'on' requests a new one with the human as caster.
local function repassive(s)
  local name = D.plan.passive
  if name == nil then
    D.p('repassive: the plan names none')
    return
  end
  local guid = nUtil.SPID(name)
  if s.phase == 'off' then
    local m = nModifier.GetFirstModifierByGUID(D.human, guid)
    if m ~= nil and m ~= kObjIDNone then nModifier.MarkForDelete(D.human, m) end
    D.p('repassive off', name, hexid(D.human), 'modifier', tostring(m))
  else
    local snap = nGameObject.GetAttributeSnapshot(D.human)
    local id = nModifier.RequestModifier(D.human, D.human, guid, snap, s.rank or 1, kZeroVector, nil)
    D.p('repassive on', name, hexid(D.human), '->', tostring(id), '; active',
      tostring(nModifier.AgentHasModifierMatchingGUID(D.human, guid)))
  end
end

local function minion()
  local hx, hy = spot({3, 2})
  local team = nGameObject.GetTeam(D.human) == nTeamID.Mortal and nTeamID.Spirit or nTeamID.Mortal
  local noun = nUtil.GetAsset(D.plan.minion or 'Minion_Spirit_Melee.Noun')
  local id = nObjectManager.CreateObject(noun, team, nVector.create(hx, hy, 0), kZeroVector, 1)
  D.p('minion', live(id) and hexid(id) or 'NOT created', live(id) and nGameObject.GetAssetName(id) or '', 'team', team,
    live(id) and vec(nGameObject.GetPosition(id)) or '')
end

-- D1j's lead (d1j_run3: "Invalid modifier ID passed to nModifier.MarkForDelete", AbilitiesLua.cpp 526, after a Tidal
-- Queen bot died to the parasite): the parasite's burn, modifier_Parasite_Fire, requests modifier_StatusEffect_MortalStrike
-- in its Activate and marks it for delete in its Deactivate; both end at the carrier's death (OnAgentDeath). lead_fire puts
-- the burn on the last enemy bot (caster: the human, as the parasite would be), lead_kill kills that bot with one hit, so
-- the death path ends both and the assert line names the script that asserts (the host's Lua context).
local function leadTarget()
  return D.enemies[#D.enemies]
end

local function leadFire()
  local t = leadTarget()
  if not live(t) then
    D.p('lead: no enemy bot')
    return
  end
  local fire = nUtil.SPID('modifier_Parasite_Fire')
  local ms = nUtil.SPID('modifier_StatusEffect_MortalStrike')
  local snap = nGameObject.GetAttributeSnapshot(D.human)
  if (D.plan.lead or 1) >= 2 then
    -- variant 2: the target already carries a mortal strike (as from another source) when the burn arrives
    local first = nModifier.RequestModifier(t, D.human, ms, snap, 1, kZeroVector, nil)
    D.p('lead: modifier_StatusEffect_MortalStrike first on', hexid(t), '->', tostring(first))
  end
  local id = nModifier.RequestModifier(t, D.human, fire, snap, 1, kZeroVector, nil)
  local m = nModifier.GetFirstModifierByGUID(t, fire)
  D.p('lead: modifier_Parasite_Fire on', hexid(t), nGameObject.GetAssetName(t), '->', tostring(id), '; carries burn',
    tostring(nModifier.AgentHasModifierMatchingGUID(t, fire)), ', mortal strike',
    tostring(nModifier.AgentHasModifierMatchingGUID(t, ms)), ', mortal strike instances',
    #(nModifier.GetModifiersMatchingGUID(t, ms) or {}), ', burn', tostring(m))
end

local function leadKill()
  local t = leadTarget()
  if not live(t) or not nGameObject.IsAlive(t) then
    D.p('lead: the bot is gone or dead already')
    return
  end
  local hp = nGameObject.GetHitPoints(t)
  nGameObject.TakeDamage(nGameObject.GetAttributeSnapshot(D.human), t, hp * 10 + 10000, 0, nDescriptors.IsMagicalDamage,
    kZeroVector)
  D.p('lead: killed', hexid(t), nGameObject.GetAssetName(t), 'hp', fmt(hp), '->', fmt(nGameObject.GetHitPoints(t)),
    'alive', tostring(nGameObject.IsAlive(t)))
end

-- ---- the extra cases (pass C, re/scripts/ability_check.py EXTRAS) --------------------------------------------------------

local function teamOf(which)
  local mine = nGameObject.GetTeam(D.human)
  if which == 'ally' then return mine end
  if which == 'npc' then return nTeamID.NPC end
  return mine == nTeamID.Mortal and nTeamID.Spirit or nTeamID.Mortal
end

-- spawn: s.noun (default Minion_Spirit_Melee.Noun) of s.team ('enemy', 'ally', 'npc') at the arena offset s.at (each
-- s.count, 1.5 units apart along y): minions for a knock-up, a well worker (Titan_Node_Follower) as a target
local function spawn(s)
  local noun = nUtil.GetAsset(s.noun or 'Minion_Spirit_Melee.Noun')
  local at = s.at or {3, 0}
  D.spawned = D.spawned or {}
  for i = 1, s.count or 1 do
    local x, y = spot({at[1], at[2] + (i - 1) * 1.5})
    local id = nObjectManager.CreateObject(noun, teamOf(s.team), nVector.create(x, y, 0), kZeroVector, 1)
    if live(id) then D.spawned[#D.spawned + 1] = id end
    D.p('spawn', s.noun or 'Minion_Spirit_Melee.Noun', live(id) and hexid(id) or 'NOT created', 'team', teamOf(s.team),
      live(id) and vec(nGameObject.GetPosition(id)) or '', live(id) and ('hp ' .. fmt(nGameObject.GetHitPoints(id))) or '')
  end
end

-- the object s.who names: 'human', 'enemy1', 'enemy2', 'ally1', 'spawned1', or 'mod:<modifier>' (the first enemy bot,
-- spawned object or creep that carries that modifier)
local function who(s)
  local w = s.who or 'enemy1'
  if w == 'human' then return D.human end
  local k = tonumber(string.match(w, '^enemy(%d)$'))
  if k then return D.enemies[k] end
  k = tonumber(string.match(w, '^ally(%d)$'))
  if k then return D.allies[k] end
  k = tonumber(string.match(w, '^spawned(%d)$'))
  if k then return (D.spawned or {})[k] end
  k = tonumber(string.match(w, '^creep(%d)$'))
  if k then return D.creeps[k] end
  local mod = string.match(w, '^mod:(.+)$')
  if mod then
    local guid = nUtil.SPID(mod)
    local pool = {}
    for _, c in ipairs(D.enemies) do pool[#pool + 1] = c end
    for _, c in ipairs(D.spawned or {}) do pool[#pool + 1] = c end
    for _, c in pairs(D.creeps) do pool[#pool + 1] = c end
    for _, c in ipairs(pool) do
      if live(c) and nGameObject.IsAlive(c) and nModifier.AgentHasModifierMatchingGUID(c, guid) then return c end
    end
  end
  return nil
end

-- kill / hurt: one hit with an enemy's snapshot (the human's for anyone else) of the target's HP (kill) or of
-- s.fraction of its maximum (hurt)
-- teleport `who` to the arena spot `at` (an offset from the arena's centre), as the init does (D12: do trigger volumes
-- attached to the object follow a teleport?)
local function teleport(s)
  local t = who(s)
  if not live(t) then
    D.p('teleport', s.who, ': no live object')
    return
  end
  local x, y = spot(s.at or {0, 0})
  if not place(t, x, y, s.who) then D.p('teleport', s.who, hexid(t), ': already there') end
end

local function hit(s, kill)
  local t = who(s)
  if not live(t) or not nGameObject.IsAlive(t) then
    D.p(kill and 'kill' or 'hurt', s.who, ': no live target')
    return
  end
  local src = (t == D.human) and D.enemies[1] or D.human
  local hp, max = nGameObject.GetHitPoints(t), nGameObject.GetMaxHitPoints(t)
  local amount = kill and (hp * 10 + 10000) or (max * (s.fraction or 0.5))
  nGameObject.TakeDamage(nGameObject.GetAttributeSnapshot(src), t, amount, 0, nDescriptors.IsTrueDamage or 0, kZeroVector)
  D.p(kill and 'kill' or 'hurt', s.who, hexid(t), nGameObject.GetAssetName(t), 'hp', fmt(hp), '->',
    string.format('%.3f', nGameObject.GetHitPoints(t)), 'of', fmt(max), 'alive', tostring(nGameObject.IsAlive(t)))
end

-- state: the human's HP (three decimals: fractional health), resource, the remaining cooldowns, s.mods (presence,
-- stacks, remaining duration on the human, the bots, the spawned objects and creeps) and how many live objects whose
-- noun starts with each of s.nouns exist
local function state(s)
  local h = D.human
  D.p('state', s.note or '', ': human hp', string.format('%.3f', nGameObject.GetHitPoints(h)), 'of',
    fmt(nGameObject.GetMaxHitPoints(h)), 'resource', fmt(nGameObject.GetResourcePoints(h)), 'of',
    fmt(nGameObject.GetMaxResourcePoints(h)), '; cooldowns', cooldowns())
  local pool = {{h, 'human'}}
  for i, c in ipairs(D.enemies) do pool[#pool + 1] = {c, 'enemy' .. i} end
  for i, c in ipairs(D.allies) do pool[#pool + 1] = {c, 'ally' .. i} end
  for i, c in ipairs(D.spawned or {}) do pool[#pool + 1] = {c, 'spawned' .. i} end
  for i, c in pairs(D.creeps) do pool[#pool + 1] = {c, 'creep' .. i} end
  -- the movement speed of everyone (a slow that lingers shows here)
  local speeds = {}
  for _, e in ipairs(pool) do
    if live(e[1]) and nGameObject.IsAlive(e[1]) then
      local ok, v = pcall(nGameObject.GetModifiedMoveSpeed, e[1])
      speeds[#speeds + 1] = e[2] .. ' ' .. (ok and fmt(v) or '?')
    end
  end
  D.p('state speeds:', table.concat(speeds, ', '))
  for _, name in ipairs(s.mods or {}) do
    local guid = nUtil.SPID(name)
    local on = {}
    for _, e in ipairs(pool) do
      if live(e[1]) and nModifier.AgentHasModifierMatchingGUID(e[1], guid) then
        local m = nModifier.GetFirstModifierByGUID(e[1], guid)
        on[#on + 1] = string.format('%s (stacks %s, remaining %s)', e[2], tostring(nModifier.GetStackCount(m)),
          fmt(nModifier.GetRemainingDuration(m)))
      end
    end
    D.p('state', name, ':', #on > 0 and table.concat(on, ', ') or 'on nobody')
  end
  if s.nouns then
    local counts, seen = {}, {}
    for i = 0, 1023 do
      local id = nObjectManager.GetObjectIdByIndex(i)
      if id == nil or id == kObjIDNone then break end
      local key = tostring(id)
      if not seen[key] then
        seen[key] = true
        local name = nGameObject.GetAssetName(id) or ''
        for _, prefix in ipairs(s.nouns) do
          if string.sub(name, 1, #prefix) == prefix then counts[prefix] = (counts[prefix] or 0) + 1 end
        end
      end
    end
    for _, prefix in ipairs(s.nouns) do D.p('state objects', prefix, ':', counts[prefix] or 0) end
  end
end

function D.step()
  D.begin()
  local ok, err = pcall(function()
    if D.plan == nil or D.human == nil then
      D.p('step: no init')
      return
    end
    D.k = (D.k or 0) + 1
    local s = (D.plan.steps or {})[D.k]
    if s == nil then
      D.p('step', D.k, ': none left')
      return
    end
    if s.kind == 'reset' then reset(s)
    elseif s.kind == 'passive' then passive()
    elseif s.kind == 'repassive' then repassive(s)
    elseif s.kind == 'teleport' then teleport(s)
    elseif s.kind == 'minion' then minion()
    elseif s.kind == 'lead_fire' then leadFire()
    elseif s.kind == 'lead_kill' then leadKill()
    elseif s.kind == 'spawn' then spawn(s)
    elseif s.kind == 'kill' then hit(s, true)
    elseif s.kind == 'hurt' then hit(s, false)
    elseif s.kind == 'state' then state(s)
    else D.p('step', D.k, ': unknown kind', s.kind) end
    if s.passive then passive() end
  end)
  if not ok then D.p('step: ERROR', err) end
  return D.finish()
end

return 'd12 library loaded'
