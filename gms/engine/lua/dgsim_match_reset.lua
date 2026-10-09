-- D1c-live test probe (MATCH_FLOW.md T10, second half; [host] test_lua=<s>:dgsim_match_reset.lua): removes the spawner
-- override that dgsim_match_override.lua set (nDirector.ResetMinionSpawnerAsset). Returns a report.
local out = {}
local ok, err = pcall(function()
  nDirector.ResetMinionSpawnerAsset(nUtil.GetAsset(dgsim_override_spawner or 'SpawnPoint_v5_Spirit_Top.Noun'))
  out[#out + 1] = 'reset: ResetMinionSpawnerAsset at match time ' .. tostring(nGameSimulator.GetMatchTime())
end)
if not ok then out[#out + 1] = 'reset: ERROR ' .. tostring(err) end
return table.concat(out, '\n')
