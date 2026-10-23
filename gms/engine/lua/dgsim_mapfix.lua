-- MAPLUA: the host's fix-ups of MM_Map_v3's own scripts, over their tables after the level's scripts are loaded; the
-- game's data stays as it is (gms/core/matchflow/mapfix.h). gms/engine/mapfix.cpp runs this
-- chunk on the server's lua_State at the level start (and again once a second until everything is in), after its
-- switch: dgsim_mapfix.wanted (false: [host] map_script_fixups=0). Returns a one-line state.
--   1. modifier_MM_BlueBuff.bonusHasteMod and modifier_MM_OrangeBuff.damageReductionMod name modifiers no script
--      defines (modifier_MM_BlueBuff_BonusHaste, modifier_MM_OrangeBuff_DamageReduction): each request asserted
--      LabsAbilities.cpp 2346 and gave nothing. They are pointed at the SM siblings' GUIDs
--      (modifier_Jungle_BlueBuff_BonusHaste, modifier_Jungle_OrangeBuff_DamageReduction), which the client knows too.
--      Skipped when a script defines the missing name after all, or the sibling isn't loaded.
--   2. modifier_MM_regenSpeed removes "modifier 0" (its Tick, Deactivate, OnDealtDamage; OnTookDamage on kObjIDNone):
--      the project's own functions (written from the script's evident intent, not taken from the game) keep the
--      handle its Activate returned and remove that, once. OnTookDamage removes it too [assumed: the twin of
--      OnDealtDamage; the original passes kObjIDNone, which removes nothing].
-- Test: gms/engine/lua/tests/test_dgsim_mapfix.lua (re/scripts/bots_lua_check.py --callers).

dgsim_mapfix = dgsim_mapfix or {}
local M = dgsim_mapfix
M.version = '1'
M.done = M.done or {}

local function global(name)
  if type(_G) == 'table' then return rawget(_G, name) end
  return nil
end

M.redirects = {
  { holder = 'modifier_MM_BlueBuff', field = 'bonusHasteMod', missing = 'modifier_MM_BlueBuff_BonusHaste',
    sibling = 'modifier_Jungle_BlueBuff_BonusHaste' },
  { holder = 'modifier_MM_OrangeBuff', field = 'damageReductionMod', missing = 'modifier_MM_OrangeBuff_DamageReduction',
    sibling = 'modifier_Jungle_OrangeBuff_DamageReduction' },
}

-- One redirect: 'applied', 'kept (defined)', 'waiting' (a table not loaded yet) or 'skipped (...)'.
local function redirect(r)
  local h = global(r.holder)
  if type(h) ~= 'table' then return 'waiting' end
  if type(global(r.missing)) == 'table' then return 'kept (' .. r.missing .. ' is defined)' end
  if type(global(r.sibling)) ~= 'table' then return 'waiting' end
  if not (nUtil and nUtil.SPID) then return 'skipped (no nUtil.SPID)' end
  local want = nUtil.SPID(r.sibling)
  local was = h[r.field]
  if was ~= nUtil.SPID(r.missing) and was ~= want then
    return 'skipped (' .. r.holder .. '.' .. r.field .. ' names something else)'
  end
  h[r.field] = want
  return 'applied'
end

-- The haste's handle: the one Activate kept, removed at most once.
local function removeSpeed(p)
  if p.dgsimSpeedMod ~= nil then
    nAttribute.RemoveAttributeModifier(p.myObjectID, p.dgsimSpeedMod)
    p.dgsimSpeedMod = nil
  end
end

local function fixRegenSpeed(t)
  local F = nAbilityFns
  local activate = t[F.Activate]
  if type(activate) ~= 'function' then return false end
  t[F.Activate] = function(p)
    local r = activate(p)  -- the script's own: p.translatedToken = the MovementSpeedPercentIncrease modifier
    p.dgsimSpeedMod = p.translatedToken
    return r
  end
  t[F.Tick] = function(p)
    nThread.WaitForXSeconds(2, p.snapshot)
    removeSpeed(p)
    p.translatedToken = nAttribute.AttributeInterpolate(p.myObjectID, p.snapshot, nAttributeType.MovementSpeed, 3, 0,
                                                        0.2, 20, nAttributeOp.kPercentChangeBest)
  end
  t[F.Deactivate] = function(p)
    removeSpeed(p)
    if p.effectToDestroy ~= nil then
      nEffect.DestroyEffect(p.myObjectID, false, p.effectToDestroy)
    end
  end
  t[F.OnDealtDamage] = function(p) removeSpeed(p) end
  t[F.OnTookDamage] = function(p) removeSpeed(p) end
  return true
end

-- Applies what isn't applied yet; returns the state line.
function M.Apply()
  if M.wanted == false then return 'dgsim_mapfix ' .. M.version .. ': fix-ups off' end
  local parts, pending = {}, 0
  for _, r in ipairs(M.redirects) do
    local s = M.done[r.holder]
    if s == nil or s == 'waiting' then
      s = redirect(r)
      M.done[r.holder] = s
    end
    if s == 'waiting' then pending = pending + 1 end
    parts[#parts + 1] = r.holder .. '.' .. r.field .. ' -> ' .. r.sibling .. ': ' .. s
  end
  if not M.done.regenSpeed then
    local t = global('modifier_MM_regenSpeed')
    if type(t) == 'table' and type(nAbilityFns) == 'table' then
      M.done.regenSpeed = fixRegenSpeed(t) and 'applied' or 'skipped (no Activate)'
    end
  end
  if not M.done.regenSpeed then pending = pending + 1 end
  parts[#parts + 1] = 'modifier_MM_regenSpeed handle: ' .. (M.done.regenSpeed or 'waiting')
  M.pending = pending
  return 'dgsim_mapfix ' .. M.version .. ': ' .. table.concat(parts, '; ') .. (pending > 0 and ' (pending ' .. pending .. ')' or '')
end

return M.Apply()
