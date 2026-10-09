-- D2g-rest test fixture ([host] test_lua=<s>:dgsim_fog_rest.lua; docs/GMS_PROTOCOL.md *Terrain, grass and stealth
-- against the original (D2g-rest)*): four jungle creatures (Creep_Jungle_Fish_Minor; three on team NPC, fogged for
-- Mortal; aggroRange 0, so they stay where they are unless hit) around the grass patch beside the Mortal top base
-- (grass map value 0x16882698, the client's outline 0x16982688; condition_Bot_Ward's "MortalTopBaseBush"):
--   C1 in the patch at (-114, -98): hidden from a source outside the patch (the target's own patch);
--   C2 behind the patch at (-126, -98), seen from (-100, -98) through the patch: hidden by the patch between (the
--      grass ray, D2g-rest), visible from inside the patch;
--   C3 in the open at (-104, -102), stealthed (nGameObject.SetStealthType FullyInvisible): hidden from Mortal while
--      fog_stealth is on, whoever stands next to it (no StealthDetection in this build's data);
--   S, the same creature on team Spirit, in the open at (-96, -100): an enemy source (vision 30) 16 units east of the
--      patch's middle, so a Mortal shaper in the patch is concealed from Spirit, and an ability cast there reveals it
--      for grassRevealDuration (AgentBlackboard_RevealInGrass, from LabsAbilities_CreateAbilityObject).
-- The globals dgsim_fogrest_c1/c2/c3/s = {x, y} override the positions. Returns a report; "fogrest ids:" lists the
-- four server ids (tostring).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

local ok, err = pcall(function()
  local noun = nUtil.GetAsset(dgsim_fogrest_noun or 'Creep_Jungle_Fish_Minor.Noun')
  local spots = {
    {'C1 in the patch', dgsim_fogrest_c1 or {-114.0, -98.0}, false},
    {'C2 behind the patch', dgsim_fogrest_c2 or {-126.0, -98.0}, false},
    {'C3 stealthed', dgsim_fogrest_c3 or {-104.0, -102.0}, true},
    {'S Spirit sentinel', dgsim_fogrest_s or {-96.0, -100.0}, false, nTeamID.Spirit},
  }
  local ids = {}
  for _, s in ipairs(spots) do
    local id = nObjectManager.CreateObject(noun, s[4] or nTeamID.NPC, nVector.create(s[2][1], s[2][2], 0), kZeroVector, 1)
    if id and id ~= kObjIDNone then
      if s[3] then nGameObject.SetStealthType(id, nStealthTypes.FullyInvisible) end
      ids[#ids + 1] = tostring(id)
      p('fogrest:', s[1], tostring(id), 'at', vec(nGameObject.GetPosition(id)), 'hp', nGameObject.GetHitPoints(id),
        'stealthed', tostring(nGameObject.GetIsStealthed and nGameObject.GetIsStealthed(id)), 'in grass',
        tostring(nGameObject.IsInGrass(id)))
    else
      p('fogrest:', s[1], 'NOT created')
    end
  end
  dgsim_fogrest_ids = ids
  p('fogrest ids: ' .. table.concat(ids, ' '))
end)
if not ok then p('fogrest: ERROR', err) end
return table.concat(out, '\n')
