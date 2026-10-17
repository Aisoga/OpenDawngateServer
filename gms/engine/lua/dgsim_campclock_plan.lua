-- CAMPICONS step 2 live check ([host] test_lua=<s>:dgsim_campclock_plan.lua once, before the first
-- dgsim_campicons.lua call): sets dgsim_campicons_steps to the world-clock plan (camp_icons=3, camp_clock=1, fog on;
-- a practice match with bots=0 and director_waves=0, or a custom game). The steps, one per dgsim_campicons.lua call:
--    1 tp next to the Buff_Fish camp (major at 74.0, -162.5)    7 kill that camp: seen by team 2 only (its clock)
--    2 kill it in sight: team 1's clock                         8 tp next to it: team 1 must not see team 2's clock
--    3 tp next to the small Fish camp                           9 tp back to the first Buff_Fish camp
--    4 kill it in sight: a 60-s respawn, no clock              10 tp to the unseen Buff_Fish (its reveal window: the
--    5 kill the other Buff_Fish camp unseen: no clock             time known, no clock)
--    6 a team-2 vision object at the Buff_Fungus camp          11 tp back to the first Buff_Fish
-- dgsim_campicons.lua's step kinds plus {'vision', team, x, y, radius, seconds}. The rejoin check's steps:
-- dgsim_campclock_rejoin.lua.
dgsim_campicons_steps = {
  {'tp', 66.0, -156.0},
  {'kill', 'Creep_Jungle_Fish_', 74.0, -162.5},
  {'tp', 62.0, -138.0},
  {'kill', 'Creep_Jungle_Fish_', 65.7, -132.2},
  {'kill', 'Creep_Jungle_Fish_', -67.6, 165.9},
  {'vision', 2, -52.7, -52.3, 20.0, 30.0},
  {'kill', 'Creep_Jungle_Fungus_', -52.7, -52.3},
  {'tp', -46.0, -46.0},
  {'tp', 66.0, -156.0},
  {'tp', -60.0, 160.0},
  {'tp', 66.0, -156.0},
}
return 'campclock plan: ' .. #dgsim_campicons_steps .. ' steps for dgsim_campicons.lua'
