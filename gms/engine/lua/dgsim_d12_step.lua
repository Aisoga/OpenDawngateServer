-- D12: the next step of the session's ability-check plan (dgsim_d12.lua; [host] test_lua=<s>:dgsim_d12_step.lua at
-- every step's time, in the plan's order). Returns the step's report.
if dgsim_d12 == nil or dgsim_d12.step == nil then return 'd12 step: dgsim_d12.lua not loaded' end
return dgsim_d12.step()
