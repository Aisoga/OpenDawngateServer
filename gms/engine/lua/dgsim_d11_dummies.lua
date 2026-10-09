-- D11 test fixture ([host] test_lua=<s>:dgsim_d11_dummies.lua; docs/SERVER_MODE.md *Ability ranks on the host and the
-- client (D11)*): targets for the rank checks. Creates dgsim_d11_count (6) creatures of dgsim_d11_noun
-- (Creep_Jungle_Fish_Minor.Noun: aggroRange 0, so they stay where they are unless hit) on team Spirit (an enemy of the
-- Mortal player, not a neutral: Fenmore's R and other area abilities skip idle neutrals, nGetObjectsFlags.
-- IgnoreNeutralUnits; dgsim_d11_team = 'NPC' for jungle-team ones), in a row around dgsim_d11_at ({-100, -104}: east of
-- the grass patch beside the Mortal top base, D2g-rest's spot, out of every tower's reach; some cells there are grass,
-- so run with [host] fog=0), 3 units apart. Run it again for a fresh batch. The commands file aims at them with
-- {nearest:Creep_Jungle_Fish} / {nearestpos:Creep_Jungle_Fish}. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

local ok, err = pcall(function()
  local noun = nUtil.GetAsset(dgsim_d11_noun or 'Creep_Jungle_Fish_Minor.Noun')
  local at = dgsim_d11_at or {-100.0, -104.0}
  local n = dgsim_d11_count or 6
  dgsim_d11_batch = (dgsim_d11_batch or 0) + 1
  local ids = {}
  for i = 1, n do
    local x = at[1] + ((i - 1) % 3) * 3.0
    local y = at[2] - math.floor((i - 1) / 3) * 3.0
    local id = nObjectManager.CreateObject(noun, nTeamID[dgsim_d11_team or 'Spirit'], nVector.create(x, y, 0),
      kZeroVector, 1)
    if id and id ~= kObjIDNone then
      ids[#ids + 1] = tostring(id)
      p('d11 dummies: batch', dgsim_d11_batch, 'creature', i, tostring(id), nGameObject.GetAssetName(id), 'at',
        vec(nGameObject.GetPosition(id)), 'hp', nGameObject.GetHitPoints(id))
    else
      p('d11 dummies: batch', dgsim_d11_batch, 'creature', i, 'NOT created')
    end
  end
  p('d11 dummies: batch ' .. dgsim_d11_batch .. ' ids ' .. table.concat(ids, ' ') .. ' at match ' ..
    string.format('%.2f', nGameSimulator.GetMatchTime()))
end)
if not ok then p('d11 dummies: ERROR', err) end
return table.concat(out, '\n')
