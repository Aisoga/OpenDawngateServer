-- D8-live test probe (MATCH_FLOW.md *D8-live*, T4/T9): run before dgsim_damage_kill_guardian.lua in the same
-- [host] test_lua list (the probes share the server's lua_State), so that probe kills the Spirit Guardian with a real
-- lethal hit instead of the Mortal one: team 1 (the player's) wins, reason 1.
--   test_lua=29:dgsim_d8_win.lua,30:dgsim_damage_kill_guardian.lua
dgsim_kill_team = nTeamID.Spirit
return 'dgsim_kill_team = nTeamID.Spirit (' .. tostring(dgsim_kill_team) ..
  '): the next dgsim_damage_kill_guardian.lua kills the Spirit Guardian (a win for team 1)'
