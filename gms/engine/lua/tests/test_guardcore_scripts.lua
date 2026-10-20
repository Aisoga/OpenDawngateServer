-- GUARDCORE: the game's own scripts that tie each Guardian Core to one special, run in a stub world. The scripts are game content, read from the gitignored re/out/serverdata_lua/lua at run time and never
-- committed; this file is our own code.
--   java -cp luaj-jse-2.0.3.jar lua test_guardcore_scripts.lua <scripts dir>
-- (re/scripts/bots_lua_check.py --callers runs it). Checks, for all 32 sets of living Cores:
--   - each special's condition (condition_Guardian_Use*) passes exactly while its Core's
--     modifier_Guardian_Nub<X>_Destroyed is off (with an enemy player in reach, no special cooldown; HealthPacks also
--     an allied player);
--   - modifier_Guardian_SpecialAbility_Cooldown lasts 6 s + 2 s per destroyed Core;
--   - condition_Titan_UseSpecial (the link into the special phase) doesn't look at the Cores;
-- and that no condition passes during the special cooldown or without an enemy player.
-- Prints "N checks, M failed".

local scriptsDir = arg[1]
local checks, failed = 0, 0
local function check(ok, msg)
  checks = checks + 1
  if not ok then
    failed = failed + 1
    print('FAIL ' .. msg)
  end
end

-- ---- the stub world ------------------------------------------------------------------------------------------------
local world = { mods = {}, enemies = 1, allies = 1, duration = nil }
kObjIDNone = 0
kZeroVector = { x = 0, y = 0, z = 0 }
nCondition = { create = function() return {} end, RegisterCondition = function() end }
nUtil = { SPID = function(s) return s end, GetAsset = function(s) return s end, ToGUID = function(s) return s end }
nModifier = {
  AgentHasModifierMatchingGUID = function(_, guid) return world.mods[guid] == true end,
  SetDuration = function(_, d) world.duration = d end,
}
nGameObject = {
  GetPosition = function() return { x = 0, y = 0, z = 0 } end,
  GetTeam = function() return 1 end,
}
nGetObjectsSortType = { None = 0, Distance = 1, DistancePrioritizePlayers = 2 }
nTargetType = { EnemyPlayers = 1, AllyPlayers = 2, EnemyNPCs = 4, Self = 8 }
nBit = { Or = function(a, b) return a + b end }
local function ids(n, base)
  local t = {}
  for i = 1, n do t[i] = base + i end
  return t
end
nObjectManager = {
  GetObjectsInRadius = function(_, _, _, targetType)
    if targetType == nTargetType.EnemyPlayers then return ids(world.enemies, 100) end
    if targetType == nTargetType.AllyPlayers then return ids(world.allies, 200) end
    return {}
  end,
}
nTable = { GetTableCount = function(t) return #t end }
nAbility = {
  create = function() return {} end,
  RegisterAbility = function() end,
  GetRankedValue = function(v, rank) return v[rank] or v[1] end,
}
nActivationType = { Unique = 1 }
nDeactivationType = { OnDuration = 1, OnAgentDestroyed = 2, OnAgentDeath = 4 }
nAbilityFns = { Activate = 'Activate', Tick = 'Tick', Deactivate = 'Deactivate' }
nThread = { WaitForever = function() end }

local LETTERS = { 'A', 'B', 'C', 'D', 'E' }
local CONDITION = {
  A = 'condition_Guardian_UseHealthPacks', B = 'condition_Guardian_UseOrbLauncher',
  C = 'condition_Guardian_UseFireTrail', D = 'condition_Guardian_UseKnockUpOrbs', E = 'condition_Guardian_UseLaserBlast',
}
for _, l in ipairs(LETTERS) do dofile(scriptsDir .. '/Conditions/' .. CONDITION[l] .. '.lua') end
dofile(scriptsDir .. '/Conditions/condition_Titan_UseSpecial.lua')
dofile(scriptsDir .. '/Modifiers/modifier_Guardian_SpecialAbility_Cooldown.lua')
local COOLDOWN = 'modifier_Guardian_SpecialAbility_Cooldown'

local function setCores(mask)  -- bit i: Core LETTERS[i + 1] alive
  world.mods = {}
  local destroyed = 0
  for i, l in ipairs(LETTERS) do
    local isAlive = math.floor(mask / 2 ^ (i - 1)) % 2 == 1
    if not isAlive then
      world.mods['modifier_Guardian_Nub' .. l .. '_Destroyed'] = true
      destroyed = destroyed + 1
    end
  end
  return destroyed
end

local function pass(name)
  local r = _G[name][1](1)
  return r == true
end

for mask = 0, 31 do
  local destroyed = setCores(mask)
  world.enemies, world.allies = 1, 1
  for i, l in ipairs(LETTERS) do
    local isAlive = math.floor(mask / 2 ^ (i - 1)) % 2 == 1
    check(pass(CONDITION[l]) == isAlive, string.format('mask %d: %s %s with core %s %s', mask, CONDITION[l],
      tostring(not isAlive), l, isAlive and 'alive' or 'destroyed'))
  end
  check(pass('condition_Titan_UseSpecial'), 'mask ' .. mask .. ': condition_Titan_UseSpecial should not look at the cores')
  world.duration = nil
  _G[COOLDOWN][nAbilityFns.Activate]({ myObjectID = 1, rank = 1 })
  check(world.duration == 6 + 2 * destroyed, string.format('mask %d: special cooldown %s, expected %d', mask,
    tostring(world.duration), 6 + 2 * destroyed))
  -- during the special cooldown nothing passes
  world.mods[COOLDOWN] = true
  for _, l in ipairs(LETTERS) do check(not pass(CONDITION[l]), 'mask ' .. mask .. ': ' .. l .. ' passes in cooldown') end
  check(not pass('condition_Titan_UseSpecial'), 'mask ' .. mask .. ': UseSpecial passes in cooldown')
  world.mods[COOLDOWN] = nil
  -- no enemy player in reach: nothing; no allied player: no health packs
  world.enemies = 0
  for _, l in ipairs(LETTERS) do check(not pass(CONDITION[l]), 'mask ' .. mask .. ': ' .. l .. ' passes without enemy') end
  world.enemies, world.allies = 2, 0
  check(not pass(CONDITION.A), 'mask ' .. mask .. ': HealthPacks passes without an allied player')
end

print(string.format('%d checks, %d failed', checks, failed))
