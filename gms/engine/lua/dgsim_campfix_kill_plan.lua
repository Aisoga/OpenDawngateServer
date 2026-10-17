-- CAMPFIX live check, the human's own kills ([host] test_lua=<s>:dgsim_campfix_kill_plan.lua once, before the first
-- dgsim_campicons.lua call; a practice match with bots, the human on team 1; its basic attacks on the nearest Fish
-- creature and its move away come from a commands file): sets dgsim_campicons_steps to
--   1 tp player 0 next to the small CreepsFish camp (65.7, -132.2); the human kills it by basic attacks and walks away
--     (its own icon would cover the camp's on the minimap)
--   2 tp player 0 next to the Boar camp (176.1, -147.5)
--   3 kill the Boar camp with player 0's snapshot (the human is the killer and stands beside it: a buff-class clear)
--   4 tp player 0 home (-200, -200), away from both camps
dgsim_campicons_steps = {
  {'tp', 56.0, -126.0},
  {'tp', 166.0, -140.0},
  {'kill', 'Creep_Jungle_Boar_', 176.1, -147.5},
  {'tp', -200.0, -200.0},
}
return 'campfix kill plan: ' .. #dgsim_campicons_steps .. ' steps for dgsim_campicons.lua'
