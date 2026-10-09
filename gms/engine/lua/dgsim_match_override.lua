-- D1c-live test probe (MATCH_FLOW.md T10; [host] test_lua=<s>:dgsim_match_override.lua): the spawner override as
-- modifier_Parasite_EmpoweredMinions makes it: nDirector.OverrideMinionSpawnerAsset(SpawnPoint_v5_Spirit_Top.Noun,
-- SpawnPoint_Spirit_Empowered3.Noun), so the next Spirit top wave is 1 Strider_Spirit_Large + 3 melee + 3 ranged. With
-- dgsim_override_reset = true (dgsim_match_reset.lua sets it) it calls ResetMinionSpawnerAsset instead. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local spawner = nUtil.GetAsset(dgsim_override_spawner or 'SpawnPoint_v5_Spirit_Top.Noun')
  if dgsim_override_reset then
    nDirector.ResetMinionSpawnerAsset(spawner)
    p('override: ResetMinionSpawnerAsset at match time', nGameSimulator.GetMatchTime())
  else
    local over = nUtil.GetAsset(dgsim_override_with or 'SpawnPoint_Spirit_Empowered3.Noun')
    nDirector.OverrideMinionSpawnerAsset(spawner, over)
    p('override: OverrideMinionSpawnerAsset at match time', nGameSimulator.GetMatchTime(), tostring(spawner), tostring(over))
  end
end)
if not ok then p('override: ERROR', err) end
return table.concat(out, '\n')
