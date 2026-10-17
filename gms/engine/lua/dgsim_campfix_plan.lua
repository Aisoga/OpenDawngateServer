-- CAMPFIX live check ([host] test_lua=<s>:dgsim_campfix_plan.lua once, before the first dgsim_campicons.lua call; a
-- practice match with bots, the human on team 1, its attack-moves from a commands file): sets dgsim_campicons_steps to
--   1 tp player 0 next to the small CreepsFish camp (65.7, -132.2), which the human then kills by attack-moves
--   2 kill the 180-s CreepsFungus camp at (86.4, 52.5) with player 5's snapshot (a team-2 bot): team 1 doesn't see it,
--     its icon stays for team 1 (stale)
--   3 tp player 0 next to that empty camp: seen empty, team 1's icon off (the respawn time unknown)
--   4 tp player 0 next to the Boar camp (176.1, -147.5), which the human then kills by attack-moves
--   5 report the Boar camp
dgsim_campicons_steps = {
  {'tp', 56.0, -126.0},
  {'kill', 'Creep_Jungle_Fungus_', 86.4, 52.5, 5},
  {'tp', 80.0, 47.0},
  {'tp', 166.0, -140.0},
  {'report', 'Creep_Jungle_Boar_', 176.1, -147.5},
}
return 'campfix plan: ' .. #dgsim_campicons_steps .. ' steps for dgsim_campicons.lua'
