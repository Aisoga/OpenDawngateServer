-- CAMPICONS step 2 rejoin check ([host] test_lua=<s>:dgsim_campclock_rejoin.lua once, before the first
-- dgsim_campicons.lua call; a custom game with a second human client, on either team; team_left_surrender_s high so
-- its absence doesn't end the game): sets dgsim_campicons_steps to
--   1 tp player 0 next to the Boar camp (major at 175.6, -146.0)      3 kill the camp in sight of both: a clock for
--   2 tp the other human next to it too                                  each of their teams (the Boar's 300 s)
--   4-15 (around the second client's rejoin) tp the other human next to the Boar camp again (a leaving player's
--        shaper goes home): its picture shows the clock's phase on a client that got the clock late
-- The second client leaves and rejoins between steps 3 and 4 (run_session.ps1 -SecondRestartAt); schedule several
-- calls around the rejoin, since test_lua's clock may follow the rejoined player's EnterGame.
dgsim_campicons_steps = {
  {'tp', 166.0, -140.0},
  {'tpo', 168.0, -136.0},
  {'kill', 'Creep_Jungle_Boar_', 175.6, -146.0},
}
for _ = 1, 12 do dgsim_campicons_steps[#dgsim_campicons_steps + 1] = {'tpo', 168.0, -140.0} end
return 'campclock rejoin plan: ' .. #dgsim_campicons_steps .. ' steps for dgsim_campicons.lua'
