-- G3b test probe (DAMAGE.md *What G3 has to check*, D1h between humans): player kills through the host's damage core
-- with chosen attackers, so the kill bookkeeping, assists, multi-kills, streaks and the recap can be watched with two
-- humans. Each step is one attacker's snapshot (nAttribute.CreateAttributeSnapshot_FromObject of a player's creature)
-- hitting a player's creature (nGameObject.TakeDamage). The step list is G3B_STEPS (a global the caller's probe sets, e.g.
-- a steps file run just before), entries {attacker player index, victim player index, damage, kind 'basic' | 'true'}
-- or {caster index, target index, modifier name, 'buff'}; the
-- default is the party match's assist: player 1 hits player 2 (a bot of the other team) with a 60 physical basic,
-- then player 0 kills it with 5,000 true damage (a kill for player 0, an assist for player 1 from the hit). Returns a
-- report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local steps = G3B_STEPS or {{1, 2, 60, 'basic'}, {0, 2, 5000, 'true'}}
G3B_STEPS = nil
local ok, err = pcall(function()
  local basic = nBit.Or(nBit.Or(nDescriptors.IsMelee, nDescriptors.IsBasic), nDescriptors.IsPhysicalDamage)
  for _, s in ipairs(steps) do
    local a = nPlayer.GetPlayerControlledObjectID(s[1])
    local v = nPlayer.GetPlayerControlledObjectID(s[2])
    if a == nil or a == kObjIDNone or v == nil or v == kObjIDNone then
      p('g3b: step', s[1], '->', s[2], ': a creature is missing (attacker', tostring(a), 'victim', tostring(v), ')')
    elseif not nGameObject.IsAlive(v) then
      p('g3b: step', s[1], '->', s[2], ': the victim is dead')
    elseif s[4] == 'buff' then
      -- {caster, target, modifier name, 'buff'}: the caster's modifier on the target (nModifier.RequestModifier, rank
      -- 1), e.g. modifier_Alpha_Q_Shield on a teammate: DamageTracker_RecordModifierEvent ->
      -- AssistTracker_HandleBuffEvent copies the target's recent credits to the caster
      local snap = nAttribute.CreateAttributeSnapshot_FromObject(a, kObjIDNone)
      local r = nModifier.RequestModifier(v, a, nUtil.SPID(s[3]), snap, 1, kZeroVector, nil)
      nAttribute.RemoveAttributeSnapshot(snap)
      p('g3b: player', s[1], 'gives player', s[2], s[3], '->', tostring(r), 'at match time', nGameSimulator.GetMatchTime())
    else
      local snap = nAttribute.CreateAttributeSnapshot_FromObject(a, kObjIDNone)
      local hp0 = nGameObject.GetHitPoints(v)
      local desc = s[4] == 'true' and nDescriptors.IsTrueDamage or basic
      local st, d = nGameObject.TakeDamage(snap, v, s[3], 0, desc, kZeroVector)
      nAttribute.RemoveAttributeSnapshot(snap)
      p('g3b: player', s[1], '(' .. nGameObject.GetAssetName(a) .. ')', 'hits player', s[2],
        '(' .. nGameObject.GetAssetName(v) .. ')', s[4], f2(s[3]), '->', tostring(st), f2(d), 'hp', f2(hp0), '->',
        f2(nGameObject.GetHitPoints(v)), 'alive', tostring(nGameObject.IsAlive(v)), 'at match time',
        nGameSimulator.GetMatchTime())
    end
  end
end)
if not ok then p('g3b: ERROR', err) end
return table.concat(out, '\n')
