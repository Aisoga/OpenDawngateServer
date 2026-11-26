-- MAPLUA: dgsim_mapfix.lua over the game's own MM_Map_v3 scripts, run in a stub world. The scripts are game content,
-- read from the gitignored re/out/serverdata_lua/lua at run time and never committed; this file is our own code.
--   java -cp luaj-jse-2.0.3.jar lua test_dgsim_mapfix.lua <scripts dir> <dgsim_mapfix.lua>
-- (re/scripts/bots_lua_check.py --callers runs it). Checks:
--   - before the fix-up the Flocklord's Blue and Orange buffs request the missing MM sub-modifiers;
--   - after it they request modifier_Jungle_BlueBuff_BonusHaste / _OrangeBuff_DamageReduction (a non-basic ability
--     used; damage dealt by a non-item), and still nothing for a basic attack or an item;
--   - the fix-up waits while a table is missing, keeps a missing name that a script defines after all, and is off with
--     dgsim_mapfix.wanted = false;
--   - modifier_MM_regenSpeed removes the handle its Activate got (never "modifier 0"), once;
--   - modifier_returntobase lets ward casts return true while other abilities still cancel recall.
-- Prints "N checks, M failed".

local scriptsDir, fixFile = arg[1], arg[2]
local checks, failed = 0, 0
local function check(ok, msg)
  checks = checks + 1
  if not ok then
    failed = failed + 1
    print('FAIL ' .. msg)
  end
end

-- ---- the stub world ------------------------------------------------------------------------------------------------
local world = { requests = {}, removed = {}, nextMod = 6 }
kObjIDNone = 0
kZeroVector = { x = 0, y = 0, z = 0 }
nAbilityFns = { TranslateToken = 1, Activate = 2, Tick = 3, Deactivate = 4, OnAbilityUsed = 5, OnDealtDamage = 6,
                OnTookDamage = 7, OnStackModifier = 8, AbsorbDamage = 9, OnUtilityUsed = 10 }
nActivationType = { Unique = 1, Stacks = 2 }
nDeactivationType = { OnDuration = 1, OnAgentDeath = 2 }
nDescriptors = { IsBuff = 1, IsBasic = 2, IsItem = 4, DoesNotGenerateAbilityUsedCallback = 8 }
nAttributeType = { Haste = 1, PhysicalDamageReduction = 2, MovementSpeedPercentIncrease = 3, MovementSpeed = 4,
                   DamageReduction = 5 }
nAttributeOp = { kAdd = 1, kPercentChangeBest = 2 }
nBit = { Or = function(a, b) return a + b end }
nUtil = { SPID = function(s) return 'spid:' .. s end, GetAsset = function(s) return s end,
          ToGUID = function(s) return s end }
nAbility = {
  create = function() return {} end,
  RegisterAbility = function() end,
  GetRankedValue = function(v) return v[1] end,
  CheckDescriptors_AnyMatch = function(want, have) return have % (want * 2) >= want end,
}
nModifier = {
  RequestModifier = function(_, _, guid) world.requests[#world.requests + 1] = guid end,
}
nAttribute = {
  AddAttributeModifier = function() world.nextMod = world.nextMod + 1 return world.nextMod end,
  RemoveAttributeModifier = function(obj, h) world.removed[#world.removed + 1] = { obj = obj, h = h } end,
  AttributeInterpolate = function() return 'interp' end,
}
nEffect = { CreateEffect = function() return 'fx' end, DestroyEffect = function() world.fxDestroyed = true end }
nThread = { WaitForever = function() end, WaitForXSeconds = function() end }

local function load(name)
  local f = assert(loadfile(scriptsDir .. '/Modifiers/' .. name .. '.lua'))
  f()
end
local function reset() world.requests = {} world.removed = {} end

local function blueUse(descriptors)
  reset()
  modifier_MM_BlueBuff[nAbilityFns.OnAbilityUsed]({ myObjectID = 77, snapshot = 1, rank = 1,
                                                    abilityUsedDescriptors = descriptors })
  return world.requests[1]
end
local function orangeHit(descriptors)
  reset()
  modifier_MM_OrangeBuff[nAbilityFns.OnDealtDamage]({ myObjectID = 77, snapshot = 1, rank = 1,
                                                      dealtDamageDescriptors = descriptors })
  return world.requests[1]
end
local function recallUse(ability)
  return modifier_returntobase[nAbilityFns.OnAbilityUsed]({
    abilityUsedAbility = ability and nUtil.SPID(ability) or nil,
  })
end

-- ---- 1. a missing sibling: the fix-up waits ------------------------------------------------------------------------
load('modifier_MM_BlueBuff')
load('modifier_MM_OrangeBuff')
load('modifier_MM_regenSpeed')
load('modifier_returntobase')
check(blueUse(0) == 'spid:modifier_MM_BlueBuff_BonusHaste', 'the script as shipped requests the missing name')
check(orangeHit(0) == 'spid:modifier_MM_OrangeBuff_DamageReduction', 'the Orange script as shipped')
check(recallUse('ability_Item_Ward') == false, 'recall as shipped cancels on ward')
local s = dofile(fixFile)
check(s:find('pending 2', 1, true) ~= nil, 'waits for both siblings: ' .. s)
check(modifier_MM_BlueBuff.bonusHasteMod == 'spid:modifier_MM_BlueBuff_BonusHaste', 'nothing changed while waiting')

-- ---- 2. the siblings loaded: applied ---------------------------------------------------------------------------------
load('modifier_Jungle_BlueBuff_BonusHaste')
load('modifier_Jungle_OrangeBuff_DamageReduction')
s = dgsim_mapfix.Apply()
check(s:find('pending', 1, true) == nil, 'nothing pending: ' .. s)
check(s:find('bonusHasteMod -> modifier_Jungle_BlueBuff_BonusHaste: applied', 1, true) ~= nil, 'Blue applied: ' .. s)
check(s:find('damageReductionMod -> modifier_Jungle_OrangeBuff_DamageReduction: applied', 1, true) ~= nil,
      'Orange applied: ' .. s)
check(s:find('modifier_MM_regenSpeed handle: applied', 1, true) ~= nil, 'regenSpeed applied: ' .. s)
check(s:find('modifier_returntobase ward: applied', 1, true) ~= nil, 'recallWard applied: ' .. s)
check(blueUse(0) == 'spid:modifier_Jungle_BlueBuff_BonusHaste', 'an ability use requests the SM sibling')
check(blueUse(nDescriptors.IsBasic) == nil, 'a basic attack requests nothing')
check(blueUse(nDescriptors.IsItem) == nil, 'an item requests nothing')
check(orangeHit(0) == 'spid:modifier_Jungle_OrangeBuff_DamageReduction', 'damage dealt requests the SM sibling')
check(orangeHit(nDescriptors.IsItem) == nil, "an item's damage requests nothing")
-- the siblings read only their own tables: 3 s, +20 haste; 3 s, 1 % a stack, at most 10
check(modifier_Jungle_BlueBuff_BonusHaste.duration[1] == 3 and modifier_Jungle_BlueBuff_BonusHaste.bonusHaste == 20,
      'BonusHaste: 3 s, +20')
check(modifier_Jungle_OrangeBuff_DamageReduction.duration[1] == 3 and
      modifier_Jungle_OrangeBuff_DamageReduction.maxStackCount == 10, 'DamageReduction: 3 s, 10 stacks')
-- applying again changes nothing
local before = modifier_MM_BlueBuff.bonusHasteMod
local beforeRecall = modifier_returntobase[nAbilityFns.OnAbilityUsed]
dgsim_mapfix.Apply()
check(modifier_MM_BlueBuff.bonusHasteMod == before, 'idempotent')
check(modifier_returntobase[nAbilityFns.OnAbilityUsed] == beforeRecall, 'recallWard idempotent')

-- ---- 3. modifier_MM_regenSpeed's handle ------------------------------------------------------------------------------
local R = modifier_MM_regenSpeed
local p = { myObjectID = 55, myTeam = 1, snapshot = 1 }
reset()
R[nAbilityFns.Activate](p)
local h = p.dgsimSpeedMod
check(h ~= nil and h ~= 0, 'Activate keeps a handle')
R[nAbilityFns.Tick](p)
check(#world.removed == 1 and world.removed[1].obj == 55 and world.removed[1].h == h, 'Tick removes that handle')
R[nAbilityFns.OnDealtDamage](p)
R[nAbilityFns.OnTookDamage](p)
world.fxDestroyed = false
R[nAbilityFns.Deactivate](p)
check(#world.removed == 1, 'removed once only')
check(world.fxDestroyed, 'Deactivate still destroys the effect')
local q = { myObjectID = 56, myTeam = 1, snapshot = 1 }
reset()
R[nAbilityFns.Activate](q)
local hq = q.dgsimSpeedMod
R[nAbilityFns.OnTookDamage](q)
check(#world.removed == 1 and world.removed[1].obj == 56 and world.removed[1].h == hq,
      'taking damage removes the haste on the agent (not kObjIDNone)')
for _, r in ipairs(world.removed) do check(r.h ~= 0, 'never modifier 0') end

-- ---- 4. modifier_returntobase ward casts -----------------------------------------------------------------------------
check(recallUse('ability_Item_Ward') == true, 'standard ward keeps recall running')
check(recallUse('ability_Outlaw_Passive_Ward') == true, 'Outlaw ward keeps recall running')
check(recallUse('ability_WarlockKnight_E_Scream') == false, 'other abilities still cancel recall')
check(recallUse(nil) == false, 'missing ability still cancels recall')

-- ---- 5. a missing name defined after all; the switch off -------------------------------------------------------------
dgsim_mapfix = nil
load('modifier_MM_BlueBuff')
load('modifier_returntobase')
modifier_MM_BlueBuff_BonusHaste = {}
s = dofile(fixFile)
check(s:find('kept (modifier_MM_BlueBuff_BonusHaste is defined)', 1, true) ~= nil, 'kept when defined: ' .. s)
check(modifier_MM_BlueBuff.bonusHasteMod == 'spid:modifier_MM_BlueBuff_BonusHaste', 'the field stays')
modifier_MM_BlueBuff_BonusHaste = nil
dgsim_mapfix = nil
modifier_returntobase = nil
s = dofile(fixFile)
check(s:find('modifier_returntobase ward: waiting', 1, true) ~= nil and s:find('pending 1', 1, true) ~= nil,
      'waits when modifier_returntobase is missing: ' .. s)
load('modifier_returntobase')
s = dgsim_mapfix.Apply()
check(s:find('modifier_returntobase ward: applied', 1, true) ~= nil and s:find('pending', 1, true) == nil,
      'applies once modifier_returntobase loads: ' .. s)
dgsim_mapfix = { wanted = false }
load('modifier_MM_BlueBuff')
load('modifier_returntobase')
s = dofile(fixFile)
check(s:find('fix-ups off', 1, true) ~= nil, 'off: ' .. s)
check(modifier_MM_BlueBuff.bonusHasteMod == 'spid:modifier_MM_BlueBuff_BonusHaste', 'off leaves the field')
check(recallUse('ability_Item_Ward') == false, 'off leaves modifier_returntobase alone')

print(checks .. ' checks, ' .. failed .. ' failed')
