-- D13 live probe (docs/DAMAGE.md *Combat edge cases (D13)*, "damage after an ability's own instance ended"; the human
-- plays Mikella: [host] noun=0x8C671F8D (merchant_princess.Noun); test_lua=<T>:dgsim_d13_mikella.lua,<T+4.8>:
-- dgsim_d13_mikella.lua,<T+6.5>:dgsim_d13_mikella.lua; the shaper moved out of its base first). Parts:
--   part 1: her W (ability_MerchantPrincess_W_TriShot, forced): three coin modifiers (5 s) and the W's
--     ModifyDamageOutgoing (+20/40/60 % on the next basics while a coin is spent), its Tick waits 5 s;
--   part 2 (4.8 s): an enemy minion 10 units from the shaper (only now, so her own basic attacks don't spend the coins
--     before), her basic attack forced on it: its Tick fires a coin's projectile 0.1 s later (about 4.9 s),
--     which lands about 0.13 s after (speed 75), after the W and the coins ended (5.0 s). The basic waits for its
--     projectile (WaitForAllProjectilesToEnd), the projectile's damage uses its own snapshot copy;
--   part 3 (6.5 s): the minion's HP (expected 58 + Power, mitigated by its armor, without the coin's +20 % when the W was
--     gone at the hit [our host]; the original kept the W running then [reported, not checked]), the W not running,
--     no exception in dgsim.log, no Lua error of the basic or the projectile.
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

-- nPlayer.IsBot takes the player's creature (an object id), not the player id (D13-live)
local function humanShaper()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c end
  end
end

local ok, err = pcall(function()
  local shaper = humanShaper()
  if not shaper then
    p('d13 mikella: no human player creature')
    return
  end
  dgsim_d13_mikella_part = (dgsim_d13_mikella_part or 0) + 1
  local part = dgsim_d13_mikella_part
  local H = dgsim_d13_mikella or {}
  dgsim_d13_mikella = H
  p('d13 mikella part', part, ': shaper', tostring(shaper), nGameObject.GetAssetName(shaper), 'match time',
    nGameSimulator.GetMatchTime())
  local copper = nUtil.SPID('modifier_MerchantPrincess_W_CopperEffect')
  if part == 1 then
    local wg = nUtil.SPID('ability_MerchantPrincess_W_TriShot')
    local r1, inst = nAbility.RequestAbility(wg, shaper, kObjIDNone, kZeroVector, 1, 0, nAbilitySlot.None, kObjIDNone, true, false,
      nil)
    -- RequestAbility's second result can be 0 although the instance exists (D13-live, the recall): ask the agent
    if inst == nil or inst == 0 or inst == kObjIDNone then inst = nAbility.GetFirstAbilityByGUID(shaper, wg) end
    H.w = inst
    p('d13 mikella part 1: W ->', tostring(r1), tostring(inst), 'copper coin modifier',
      tostring(nModifier.GetFirstModifierByGUID(shaper, copper)))
  elseif part == 2 then
    -- the target only now (D13-live, d13_run2b: placed in part 1, it drew the shaper's own basic attacks, which spent the
    -- three coins long before part 2)
    local team = nGameObject.GetTeam(shaper)
    local noun, enemy = 'Minion_Spirit_Melee.Noun', nTeamID.Spirit
    if team == nTeamID.Spirit then noun, enemy = 'Minion_Mortal_Melee.Noun', nTeamID.Mortal end
    local sp = nGameObject.GetPosition(shaper)
    H.target = nObjectManager.CreateObject(nUtil.GetAsset(noun), enemy, nVector.create(sp.x + 10, sp.y, 0), kZeroVector, 1)
    H.hp = H.target and nGameObject.GetHitPoints(H.target)
    p('d13 mikella part 2: target', tostring(H.target), 'copper coin modifier', tostring(nModifier.GetFirstModifierByGUID(shaper, copper)),
      '(expected still on: 5 s)')
    local bg = nUtil.SPID('ability_MerchantPrincess_Basic')
    local r1, inst = nAbility.RequestAbility(bg, shaper, H.target, kZeroVector, 1, 0, nAbilitySlot.None, kObjIDNone, true, false, nil)
    if inst == nil or inst == 0 or inst == kObjIDNone then inst = nAbility.GetFirstAbilityByGUID(shaper, bg) end
    H.basic = inst
    p('d13 mikella part 2: W running', tostring(H.w and nAbility.IsAbilityRunning(H.w)), 'basic ->', tostring(r1), tostring(inst),
      'target hp', f2(H.hp))
  else
    p('d13 mikella part 3: W running', tostring(H.w and nAbility.IsAbilityRunning(H.w)), '(expected false), basic running',
      tostring(H.basic and nAbility.IsAbilityRunning(H.basic)), 'target hp', f2(H.hp), '->',
      f2(H.target and nGameObject.GetHitPoints(H.target)), '(expected 58 + Power, mitigated; see the host\'s damage line)')
    if H.target then nGameObject.MarkForDelete(H.target) end
    dgsim_d13_mikella = nil
  end
end)
if not ok then p('d13 mikella: ERROR', err) end
return table.concat(out, '\n')
